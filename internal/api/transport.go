package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

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
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	return len(envelope.Errors) > 0
}
