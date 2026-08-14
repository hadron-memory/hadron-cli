package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"syscall"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #394: a gateway/transport failure and a GraphQL error used to print in the
// same register (`hadron: HTTP <n>: <body>`), so "your query is wrong, fix it"
// and "your query never reached the server" were indistinguishable — and only
// one of those is safe to retry blind.
//
// The distinction that matters is not the status number, it is whether the
// SERVER FORMED AN OPINION. A 400 with GRAPHQL_VALIDATION_FAILED is an
// opinion. A 502 from an edge, a reset connection, a timeout — those mean the
// request may never have arrived, or arrived and committed while the answer
// was lost. After a mutation those two possibilities are not equivalent, which
// is why this classification exists at all.

// transportFailure describes a request that never got an answer from the API.
type transportFailure struct {
	// what returns the operative phrasing: what is known to have happened.
	what string
	// retryHint is the follow-up ("the server may be restarting").
	retryHint string
}

// classifyTransport reports whether err is a transport/gateway failure rather
// than a server-formed refusal, and how to describe it.
//
// The rule for an HTTP error is "5xx WITHOUT a GraphQL errors body". Apollo can
// itself answer 500 with a well-formed `errors[]` — that IS an opinion, and
// stays a plain error. A 502/503/504 whose body is not a GraphQL envelope is
// almost always the edge, not the API.
func classifyTransport(err error) (transportFailure, bool) {
	if err == nil {
		return transportFailure{}, false
	}

	// OUR OWN refusal about a redirect is not a transport failure, even though
	// net/http wraps it in *url.Error (which satisfies net.Error). The server
	// answered; retrying cannot help. Checked first, since the shapes below
	// would otherwise swallow it.
	if errors.Is(err, ErrRedirectPolicy) {
		return transportFailure{}, false
	}

	// A response whose BODY was cut short is a lost answer in the strictest
	// sense: headers arrived, so the request certainly reached the server, and
	// a mutation may already have committed. genqlient surfaces this as a bare
	// io.ErrUnexpectedEOF (or a JSON decode error wrapping it) — none of the
	// shapes below match it, so without this it fell through to exit 1 with no
	// idempotency warning: the one documented reset case conflated with a
	// refusal (PR #415 review).
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) {
		return transportFailure{
			what:      "the connection dropped while reading the server's answer",
			retryHint: "retry shortly",
		}, true
	}

	// No HTTP response at all: refused, reset, DNS, TLS, timeout.
	if errors.Is(err, context.DeadlineExceeded) {
		return transportFailure{
			what:      "the request timed out before the server answered",
			retryHint: "it may still be running server-side",
		}, true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return transportFailure{
				what:      "the request timed out before the server answered",
				retryHint: "it may still be running server-side",
			}, true
		}
		return transportFailure{
			what:      "could not reach the server",
			retryHint: "check the network and `hadron config get server`",
		}, true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return transportFailure{
			what:      "could not reach the server",
			retryHint: "check the network and `hadron config get server`",
		}, true
	}

	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode < 500 {
			return transportFailure{}, false
		}
		// A 5xx carrying real GraphQL errors is the API refusing, not the edge.
		// (graphql.Response is a struct, not a pointer — an absent body is the
		// zero value, i.e. no errors.)
		if len(httpErr.Response.Errors) > 0 {
			return transportFailure{}, false
		}
		return transportFailure{
			what:      fmt.Sprintf("HTTP %d from a gateway, not a GraphQL error — the request may not have reached the server, or its answer was lost", httpErr.StatusCode),
			retryHint: "the server may be restarting; retry shortly",
		}, true
	}
	return transportFailure{}, false
}

// transportStatus classifies a raw status code + body from the `hadron api`
// path, which reads the response itself rather than going through genqlient.
// A body that parses as a GraphQL envelope with errors is the API's opinion.
func transportStatus(status int, body []byte) (transportFailure, bool) {
	if status < 500 {
		return transportFailure{}, false
	}
	if hasGraphQLErrorsEnvelope(body) {
		return transportFailure{}, false
	}
	return transportFailure{
		what:      fmt.Sprintf("HTTP %d from a gateway, not a GraphQL error — the request may not have reached the server, or its answer was lost", status),
		retryHint: "the server may be restarting; retry shortly",
	}, true
}

// TransportError renders a transport failure as a CodedError with the
// Unavailable exit code. isMutation drives the idempotency warning: after a
// lost answer, a write's outcome is genuinely unknown, and a caller who
// assumes "error ⇒ nothing happened" can double-apply a non-idempotent
// mutation.
func transportError(f transportFailure, isMutation bool) error {
	msg := f.what
	if f.retryHint != "" {
		msg += " (" + f.retryHint + ")"
	}
	if isMutation {
		msg += ". This was a MUTATION: it may or may not have been applied — verify the current state before retrying"
	}
	return exitcode.Newf(exitcode.Unavailable, "%s", msg)
}

// DocumentHasMutation reports whether a GraphQL document contains a mutation
// operation, so `hadron api` can warn about idempotency only when a write was
// actually at stake.
//
// String literals are skipped so a query selecting a field whose *value*
// mentions mutations is not misread. Beyond that the check is deliberately
// generous — a false positive prints a warning that is merely unnecessary,
// while a false negative withholds the one sentence that prevents a
// double-applied write. It errs toward warning.
func DocumentHasMutation(doc string) bool {
	inString := false
	var quote byte
	for i := 0; i < len(doc); i++ {
		c := doc[i]
		if inString {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				inString = false
			}
			continue
		}
		// Block strings ("""…""") may legally contain unescaped quotes, so they
		// need their own scan: treating them as three single-quoted strings can
		// leave the scanner mid-string and hide a later `mutation` token —
		// a FALSE NEGATIVE, the direction this function must not fail in
		// (PR #415 review).
		if c == '"' && strings.HasPrefix(doc[i:], `"""`) {
			if end := strings.Index(doc[i+3:], `"""`); end >= 0 {
				i += 3 + end + 2
			} else {
				i = len(doc) // unterminated block string: nothing left to scan
			}
			continue
		}
		if c == '"' || c == '\'' {
			inString = true
			quote = c
			continue
		}
		if c == '#' { // comment to end of line
			for i < len(doc) && doc[i] != '\n' {
				i++
			}
			continue
		}
		if (c == 'm' || c == 'M') && hasTokenAt(doc, i, "mutation") {
			return true
		}
	}
	return false
}

// hasTokenAt reports whether tok sits at position i on GraphQL name
// boundaries (a GraphQL name is [_A-Za-z][_0-9A-Za-z]*).
func hasTokenAt(s string, i int, tok string) bool {
	if i+len(tok) > len(s) || !strings.EqualFold(s[i:i+len(tok)], tok) {
		return false
	}
	if i > 0 && isNameByte(s[i-1]) {
		return false
	}
	if j := i + len(tok); j < len(s) && isNameByte(s[j]) {
		return false
	}
	return true
}

func isNameByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// hasGraphQLErrorsEnvelope reports whether body is a JSON object carrying a
// non-empty `errors` array — i.e. the API answered with its own opinion,
// however bad the status code. Anything unparseable is NOT an envelope, which
// is the whole point: an edge's HTML error page is not a refusal.
func hasGraphQLErrorsEnvelope(body []byte) bool {
	var envelope struct {
		Errors []struct {
			Message *string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	// At least one entry must be an OBJECT carrying `message` — the field the
	// GraphQL spec requires on every error. A JSON-producing gateway can emit
	// `{"errors":["upstream unavailable"]}` or `{"errors":[null]}`, and taking
	// those as the API's opinion would suppress exit 7 and the mutation
	// warning on exactly the failure they describe (PR #415 review).
	for _, e := range envelope.Errors {
		if e.Message != nil {
			return true
		}
	}
	return false
}
