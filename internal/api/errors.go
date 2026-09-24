package api

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// serverError renders a GraphQL failure as the server's own message and
// nothing else, while keeping the original error reachable through Unwrap.
//
// It exists because `gqlerror.Error.Error()` is not a message, it is a
// DEVELOPER rendering of one (#566):
//
//	<file>:<line>: <path> <message>
//
// and every part of that prefix is meaningless to the person reading it.
// `input` is gqlparser's FALLBACK filename — what it prints when the error
// carries no `file` extension, which ours never do — the line number is a
// position in a document the user never wrote, and the path is the GraphQL
// field an argument landed in, a name they never typed, which reads as though
// some unrelated argument is at fault:
//
//	hadron: input:3: workers workers URN "hadron-dev-team" is not fully qualified.
//
// Two things about that are worse than they look, both measured rather than
// reasoned. The field name appears TWICE when the server's own message opens
// with it — as the `workers` and `memory` refusals in #566 do; how common that
// is across every resolver was not measured and is not claimed here. And the
// prefix is NOT conditional on the server sending locations: `Error()` writes
// the filename and `": "` unconditionally, so an error with no locations and no
// path still renders as `input: <message>`. There is no shape of gqlerror that
// escapes it.
//
// One path leaks differently and is worth knowing about, because a fix aimed at
// the prefix alone would miss it: a non-200 arrives as `graphql.HTTPError`,
// whose own rendering is `returned error <status>: <the entire raw JSON body>`.
// Cleaning keys on the ENVELOPE rather than on the prefix, so it reaches both.
//
// Unwrap is the load-bearing half. HasErrorCode, WorkerTakenDetail,
// DescendantCount and every other extensions reader runs `errors.As` over this
// chain, and several of them are documented as "call it BEFORE MapError wraps"
// — which stays true only because wrapping preserves the chain. Replacing the
// error instead of wrapping it would turn each of those into a silent
// always-false.
type serverError struct {
	msg string
	err error
}

func (e *serverError) Error() string { return e.msg }
func (e *serverError) Unwrap() error { return e.err }

// ServerMessages returns the server's own message for each GraphQL error in
// err, with genqlient's location/path rendering left off. Empty when err
// carries no GraphQL errors at all — a transport failure or a raw HTTP body —
// which is the caller's signal to fall back to err.Error().
func ServerMessages(err error) []string {
	var msgs []string
	for _, e := range graphQLErrors(err) {
		if e != nil && e.Message != "" {
			msgs = append(msgs, strings.TrimSpace(e.Message))
		}
	}
	return msgs
}

// ServerMessage returns the FIRST server message in err, or "" when there is
// none. For a caller rendering one line about one failure; MapError joins the
// whole list instead, because dropping the others would hide them.
func ServerMessage(err error) string {
	if msgs := ServerMessages(err); len(msgs) > 0 {
		return msgs[0]
	}
	return ""
}

// hasStructuredEnvelope reports whether a GraphQL error list looks like one the
// SERVER composed, rather than one genqlient synthesised from a body it could
// not parse.
//
// The distinction is load-bearing on the HTTPError path (PR #581 review, Codex
// P2). genqlient parses the body itself and, when it is not JSON, builds a
// one-entry list holding the WHOLE BODY as the message — `internal/api/client.go`
// documents this, because classifyTransport was caught by the same thing:
//
//	Errors: gqlerror.List{&gqlerror.Error{Message: string(respBody)}}
//
// `bearerDoer` only intercepts responses at or above 500, so a proxy's HTML
// 401/403/404 reaches here intact. Cleaning that entry would emit the raw HTML
// and DROP the status — measured before fixing:
//
//	raw:     returned error 404: {"data":null,"errors":[{"message":"<html>…"}]}
//	cleaned: <html><head><title>404 Not Found</title></head><body>nginx</body></html>
//
// which is worse than the leak #566 exists to fix: the reader loses the one
// fact that would have told them a proxy answered rather than the API.
//
// The test is structural, and deliberately asks whether ANY entry carries
// extensions, a location or a path — the three things a real GraphQL response
// supplies and a synthesised one cannot have. A genuine envelope with none of
// them keeps the HTTP rendering, which is the safe direction to be wrong in:
// it costs a tidier message, where the other way costs the status.
func hasStructuredEnvelope(list gqlerror.List) bool {
	for _, e := range list {
		if e == nil {
			continue
		}
		if len(e.Extensions) > 0 || len(e.Locations) > 0 || len(e.Path) > 0 {
			return true
		}
	}
	return false
}

// cleaned wraps err so it renders as the server's message(s) and nothing else.
// An error with no GraphQL messages is returned unchanged rather than
// flattened: a transport failure's own text is the only text there is.
func cleaned(err error) error {
	// A non-200 is the one shape whose "envelope" may be genqlient's own
	// invention; see hasStructuredEnvelope. A bare gqlerror.List got there by
	// genqlient parsing real JSON, so it needs no such guard.
	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) && !hasStructuredEnvelope(graphQLErrors(err)) {
		return err
	}
	msgs := ServerMessages(err)
	if len(msgs) == 0 {
		return err
	}
	// Joined, not first-wins. A multi-error response means several things were
	// refused, and showing one silently discards the rest — the caller then
	// fixes what they were told about and fails again on what they were not.
	return &serverError{msg: strings.Join(msgs, "; "), err: err}
}

// MapError converts transport and GraphQL errors into CodedErrors so
// the root command can derive the documented exit code. Codes come
// from hadron-server's Apollo resolvers (extensions.code).
//
// It is also the ONE place the server's refusal is rendered for a human
// (#566). Every `exitcode.New` below wraps through `cleaned`, so the message
// that reaches `hadron: %s` — and the `--json` error envelope, which renders
// the same string — is what the server said, with none of genqlient's
// document-location scaffolding around it.
func MapError(err error) error {
	if err == nil {
		return nil
	}

	// A curated command sends a query baked into this binary, so a GraphQL
	// *validation* failure means the CLI and server disagree on the schema —
	// version skew, not a user mistake. Turn the raw 400/envelope into one
	// actionable line (#136). (`hadron api` runs user-authored queries and
	// doesn't go through MapError, so its validation errors surface verbatim.)
	if isSchemaSkew(err) {
		// Direction-neutral: skew can be a stale CLI against a newer server OR a
		// newer CLI against an older self-hosted server — recommending only a CLI
		// upgrade would misdirect the latter.
		msg := "the server rejected a query this `hadron` build sends — the CLI and server schema versions are out of sync. " +
			"Update whichever is behind: upgrade the CLI (e.g. `brew upgrade hadron`), or the server if it is self-hosted. " +
			"`hadron version` shows the CLI build."
		if detail := firstGraphQLMessage(err); detail != "" {
			msg += " (server said: " + detail + ")"
		}
		return exitcode.Newf(exitcode.Usage, "%s", msg)
	}

	// #394: a request that never got an answer is not a refusal. Classified
	// before the status switch, because it is the ONLY class that is safe to
	// retry blind — and, after a write, the only one whose outcome is unknown.
	// Curated commands don't tell MapError whether they sent a mutation, so
	// the write caveat is stated conditionally rather than omitted; `hadron
	// api` knows and says it definitely (see RawGraphQL).
	if f, ok := classifyTransport(err); ok {
		return exitcode.Newf(exitcode.Unavailable,
			"%s (%s). If this command performs a write, verify the current state before retrying — it may have been applied",
			f.what, f.retryHint)
	}

	// #681: a key whose OAuth grant is `mcp` alone is refused on every
	// non-MCP surface with a generic FORBIDDEN. Left to the extension-code
	// mapping below it would exit 8, whose documented remedy — "ask someone
	// for access" — is false here: the fix is a different credential, which
	// is exactly what 3 means. And the server's sentence names no way out,
	// while the obvious one (`auth token create`) is refused for the same key.
	if kind := OAuthScopeRefusal(err); kind != "" {
		// Apollo's "Context creation failed: " is transport plumbing, not the
		// server's sentence, and it means nothing to the person reading it, so
		// it is dropped the way #566 drops genqlient's decoration. The chain
		// still unwraps to the original error.
		// Per message, before joining (PR #690 review, @copilot): trimming the
		// joined string would strip only the first message's prefix. The list
		// is never empty here, because OAuthScopeRefusal matched an error.
		msgs := ServerMessages(err)
		for i, m := range msgs {
			msgs[i] = strings.TrimPrefix(m, apolloContextFailurePrefix)
		}
		msg := strings.Join(msgs, "; ")
		return exitcode.New(exitcode.AuthRequired, &serverError{msg: msg + " " + ScopeRefusalRemedy(kind), err: err})
	}

	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) {
		// #563: a non-200 that STILL carries a typed GraphQL envelope is
		// classified by its extensions.code, not by the HTTP status — otherwise
		// a 403/404 (or a 400 that is not schema skew) with a typed code
		// silently downgrades to the generic status mapping on the curated
		// path, while `hadron api`'s raw path already asks the envelope first
		// regardless of status. The status switch is the fallback for a body
		// with no code (a proxy's HTML 404, an unparsed envelope). Schema-skew
		// and transport (5xx) are handled above, so they never reach here.
		for _, e := range graphQLErrors(err) {
			if code := extensionCode(e); code != "" {
				return exitcode.New(codeForExtension(code), cleaned(err))
			}
		}
		switch httpErr.StatusCode {
		case 401:
			return exitcode.New(exitcode.AuthRequired, cleaned(err))
		case 403:
			return exitcode.New(exitcode.Error, cleaned(err))
		case 404:
			return exitcode.New(exitcode.NotFound, cleaned(err))
		}
		return exitcode.New(exitcode.Error, cleaned(err))
	}

	var list gqlerror.List
	if errors.As(err, &list) && len(list) > 0 {
		return exitcode.New(codeForExtension(extensionCode(list[0])), cleaned(err))
	}
	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) {
		return exitcode.New(codeForExtension(extensionCode(gqlErr)), cleaned(err))
	}

	return exitcode.New(exitcode.Error, cleaned(err))
}

// mcpOnlyRefusal is the sentence hadron-server uses to refuse a user key whose
// OAuth grant is `mcp` alone (hadron-server#1270, `authContextAllowsOAuthSurface`),
// on /graphql and on createUserApiKey ("… and cannot create API keys.").
//
// It is matched as CONTAINED, never as a prefix. On /graphql the live message
// is "Context creation failed: This OAuth credential …": Apollo rebuilds the
// context error with that prefix, because graphql's ESM and CJS builds make
// `instanceof GraphQLError` false. #683 matched a prefix and was inert in
// production (#681 reopened). The resolver-level refusal arrives unprefixed.
// Both shapes are pinned in the tests.
//
// The sentence is now only the FALLBACK. Since server#1306 every scope refusal
// carries extensions.reason, which OAuthScopeRefusal reads first; the prose is
// matched only when no reason is sent (a server that predates #1306). Even
// then both halves must match — FORBIDDEN and the sentence — so a stray
// sentence in some other error cannot qualify, and a reworded sentence fails
// safe: the plain FORBIDDEN mapping, with no false remedy.
const mcpOnlyRefusal = "This OAuth credential is limited to the MCP surface"

// apolloContextFailurePrefix is what Apollo 4 prepends to an error thrown from
// the GraphQL context function when it does not recognise it as a GraphQLError.
const apolloContextFailurePrefix = "Context creation failed: "

// MCPOnlyRemedy is appended wherever the CLI meets an MCP-only key. It names
// both ways back, because either may be the one that works: the browser login
// only helps from a CLI that requests `account` (v0.15.0+), and a portal key
// is the one route that needs no CLI upgrade.
const MCPOnlyRemedy = "This key was issued for MCP clients only, and the CLI needs one with the `account` scope. " +
	"Sign in again with `hadron auth logout && hadron auth login` (hadron v0.15.0 or later requests `account`), " +
	"or create a key on the portal's API keys page (/app/account/api-keys) and run `hadron auth login --with-token` with it. " +
	"If the key comes from HADRON_TOKEN, replace that variable instead."

// UnsupportedScopeRemedy is appended for a key whose OAuth grant carries a
// scope this server does not support: it is refused everywhere, so the only
// fix is a new credential (server#1306).
const UnsupportedScopeRemedy = "Sign in again with `hadron auth logout && hadron auth login`, " +
	"or create a key on the portal's API keys page (/app/account/api-keys) and run `hadron auth login --with-token` with it. " +
	"If the key comes from HADRON_TOKEN, replace that variable instead."

// ScopeRefusal names why the server refused a credential by its OAuth scope.
// Its values are also `auth status` / `auth token validate`'s rejectedReason.
type ScopeRefusal string

const (
	// ScopeMCPOnly: the grant is `mcp` alone, valid for MCP clients only.
	ScopeMCPOnly ScopeRefusal = "mcp-only-scope"
	// ScopeUnsupported: the grant carries a scope this server does not
	// support, so it is refused everywhere.
	ScopeUnsupported ScopeRefusal = "unsupported-scope"
)

// Server reasons for a scope refusal (server#1306, extensions.reason).
const (
	reasonScopeInsufficient = "OAUTH_SCOPE_INSUFFICIENT"
	reasonScopeUnsupported  = "OAUTH_SCOPE_UNSUPPORTED"
)

// OAuthScopeRefusal classifies a FORBIDDEN that refuses the CREDENTIAL, not
// the caller's permission. extensions.reason decides whenever the server sends
// it (server#1306), and an unknown reason is NOT a scope refusal. Only a
// server that sends no reason at all (one that predates #1306) is recognised
// by the MCP-only sentence, so the CLI works in either deploy order. Anything else returns "",
// and the error keeps its ordinary FORBIDDEN mapping. Call it on the RAW error.
func OAuthScopeRefusal(err error) ScopeRefusal {
	for _, e := range graphQLErrors(err) {
		if e == nil || extensionCode(e) != "FORBIDDEN" {
			continue
		}
		if raw, present := e.Extensions["reason"]; present {
			// A server that sends reason has decided: an UNKNOWN reason is not
			// a scope refusal this CLI understands, whatever the prose says
			// (#698 review, Codex and Copilot).
			reason, _ := raw.(string)
			switch reason {
			case reasonScopeInsufficient:
				return ScopeMCPOnly
			case reasonScopeUnsupported:
				return ScopeUnsupported
			}
			continue
		}
		// No reason at all: a server that predates server#1306.
		if strings.Contains(e.Message, mcpOnlyRefusal) {
			return ScopeMCPOnly
		}
	}
	return ""
}

// ScopeRefusalRemedy is the recovery the CLI appends for a scope refusal.
func ScopeRefusalRemedy(kind ScopeRefusal) string {
	if kind == ScopeUnsupported {
		return UnsupportedScopeRemedy
	}
	return MCPOnlyRemedy
}

// IsMCPOnlyCredential reports whether err is the server refusing an MCP-only
// key (#681). Call it on the RAW error, before MapError wraps it.
func IsMCPOnlyCredential(err error) bool {
	return OAuthScopeRefusal(err) == ScopeMCPOnly
}

// HasErrorCode reports whether err carries a GraphQL error whose
// extensions.code equals code. It inspects the raw genqlient error (call it
// BEFORE MapError wraps the error into a CodedError) so callers can branch on
// a specific server error — e.g. `node import` falling back from updateNode's
// NODE_NOT_FOUND to createNode.
func HasErrorCode(err error, code string) bool {
	var list gqlerror.List
	if errors.As(err, &list) {
		for _, e := range list {
			if extensionCode(e) == code {
				return true
			}
		}
		return false
	}
	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) {
		return extensionCode(gqlErr) == code
	}
	return false
}

// graphQLErrors extracts the GraphQL error list from any of the shapes an
// operation can fail as: a bare list, a single error, or a non-200 HTTPError
// whose parsed body carries them.
func graphQLErrors(err error) gqlerror.List {
	var list gqlerror.List
	if errors.As(err, &list) {
		return list
	}
	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) {
		return gqlerror.List{gqlErr}
	}
	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Response.Errors
	}
	return nil
}

// isSchemaSkew reports whether err is a GraphQL validation failure — the server
// rejecting a query that references a field/operation it doesn't have.
func isSchemaSkew(err error) bool {
	for _, e := range graphQLErrors(err) {
		if e != nil && (extensionCode(e) == "GRAPHQL_VALIDATION_FAILED" || strings.Contains(e.Message, "Cannot query field")) {
			return true
		}
	}
	// Fallback for a 400 whose body genqlient didn't parse into Response.Errors.
	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == 400 {
		s := err.Error()
		return strings.Contains(s, "GRAPHQL_VALIDATION_FAILED") || strings.Contains(s, "Cannot query field")
	}
	return false
}

// firstGraphQLMessage returns a concise server-side message for the skew hint,
// preferring the structured error, else the first "message" in the raw body.
func firstGraphQLMessage(err error) string {
	for _, e := range graphQLErrors(err) {
		if e != nil && e.Message != "" {
			return e.Message
		}
	}
	// Fallback: pull the first "message" from a raw JSON body, honoring
	// backslash escapes so an embedded \" (e.g. Cannot query field \"x\") ends
	// the value at the real closing quote instead of truncating at the first \".
	s := err.Error()
	const key = `"message":"`
	i := strings.Index(s, key)
	if i < 0 {
		return ""
	}
	rest := s[i+len(key):]
	var b strings.Builder
	for j := 0; j < len(rest); j++ {
		switch c := rest[j]; {
		case c == '\\' && j+1 < len(rest):
			j++
			b.WriteByte(rest[j]) // emit the escaped char literally
		case c == '"':
			return b.String() // unescaped closing quote
		default:
			b.WriteByte(c)
		}
	}
	return ""
}

func extensionCode(e *gqlerror.Error) string {
	if e == nil || e.Extensions == nil {
		return ""
	}
	if code, ok := e.Extensions["code"].(string); ok {
		return code
	}
	return ""
}

// TakenDetail is the informed-takeover payload a WORKER_TAKEN error carries
// (hadron-server#940): everything the takeover prompt needs, in one round
// trip. Fields are empty when the server omitted them (lastDriver is null
// for an unattributed session).
type TakenDetail struct {
	WorkerID   string
	SessionID  string
	LastDriver string
	LastSeenAt string
}

// WorkerTakenDetail extracts the WORKER_TAKEN payload from err's
// extensions; ok is false when err is not that error. The MESSAGE also
// narrates the payload today, but the extensions are the documented
// contract (cor:agt:020:03) — render from these, not from message wording.
// Call it BEFORE MapError wraps the error.
func WorkerTakenDetail(err error) (TakenDetail, bool) {
	for _, e := range graphQLErrors(err) {
		if e == nil || extensionCode(e) != "WORKER_TAKEN" {
			continue
		}
		d := TakenDetail{}
		if e.Extensions != nil {
			d.WorkerID, _ = e.Extensions["workerId"].(string)
			d.SessionID, _ = e.Extensions["sessionId"].(string)
			d.LastDriver, _ = e.Extensions["lastDriver"].(string)
			d.LastSeenAt, _ = e.Extensions["lastSeenAt"].(string)
		}
		return d, true
	}
	return TakenDetail{}, false
}

// HeldDetail is the payload a WORKER_HELD error carries
// (hadron-server#1050): whose name it is, and since when.
//
// TWO server paths raise this code and they do NOT carry the same fields.
// The pre-transaction check resolves the holder and sends workerId, heldBy,
// heldByName and heldAt; the compare-and-set inside the session-creating
// transaction — the one that refuses the loser of a race — sends only
// workerId and heldBy, because it has no holder read to spare. So HolderName
// and HeldAt are absent on a perfectly ordinary refusal, and a caller that
// renders them unconditionally prints a half-empty sentence on the path a
// concurrent bind takes. Every field is best-effort; only ok is a promise.
type HeldDetail struct {
	WorkerID   string
	HolderID   string
	HolderName string
	HeldAt     string
}

// WorkerHeldDetail extracts the WORKER_HELD payload from err's extensions;
// ok is false when err is not that error. Rendered from the extensions, not
// the message wording (cor:agt:020:09 is the contract). Call it BEFORE
// MapError wraps the error.
//
// HELD is not TAKEN and the difference is the whole point: a held name is
// somebody's until they release it, so this refusal has no --force. Never
// pair it with a takeover suggestion.
func WorkerHeldDetail(err error) (HeldDetail, bool) {
	for _, e := range graphQLErrors(err) {
		if e == nil || extensionCode(e) != "WORKER_HELD" {
			continue
		}
		d := HeldDetail{}
		if e.Extensions != nil {
			d.WorkerID, _ = e.Extensions["workerId"].(string)
			d.HolderID, _ = e.Extensions["heldBy"].(string)
			d.HolderName, _ = e.Extensions["heldByName"].(string)
			d.HeldAt, _ = e.Extensions["heldAt"].(string)
		}
		return d, true
	}
	return HeldDetail{}, false
}

// EndRefusedBeforeCommit reports whether an endSession failure PROVABLY changed
// nothing — the only case in which a client may tell its caller that a handoff
// was definitely not recorded.
//
// It is deliberately ONE code, and not a taxonomy. `cor:agt:020:10` guarantees
// that a failed handoff write REFUSES the end, so `HANDOFF_WRITE_FAILED` is a
// SPEC-BACKED statement that nothing committed. Every other outcome is treated
// as unknown, including other GraphQL errors, because there is no way from here
// to tell a pre-commit refusal from a post-commit one:
//
//   - GraphQL returns `data` AND `errors` when a nested resolver fails after
//     the mutation ran, and
//   - null-bubbling from a non-null child NULLS `data.endSession` even though
//     the write happened,
//
// so neither the presence of an error nor the absence of a payload proves a
// refusal (PR #528 review, Codex, twice).
//
// The asymmetry is the whole design. Saying "may not have been recorded" when
// it definitely was not is harmless — the remedy is identical, since one stint
// records one handoff. Saying "was NOT recorded" when it WAS is the worst
// sentence this command can print, and it would make a later retry failure look
// like confirmation of data loss. So the definite wording is earned by a
// guarantee, not inferred from a shape.
//
// Call it BEFORE MapError wraps.
func EndRefusedBeforeCommit(err error) bool {
	list := graphQLErrors(err)
	if len(list) == 0 {
		return false // transport failure: the outcome is unknowable from here
	}
	for _, e := range list {
		if e == nil || extensionCode(e) != "HANDOFF_WRITE_FAILED" {
			return false // anything unrecognized keeps the whole answer ambiguous
		}
	}
	return true
}

// HoldStaleDetail is the payload a WORKER_HOLD_STALE error carries
// (hadron-server#1084): the hold found NOW, at the moment the guarded write
// refused the caller's assertion.
//
// HolderID is EMPTY when the name is currently held by NOBODY — the server
// sends `heldByUserId: null` for that, and it is a real answer rather than a
// missing field: it is what a caller who asserted a specific holder gets when
// the name was released underneath them. `Held` reports which of the two it is,
// because "" alone cannot: a JSON null and an absent key both decode to "".
//
// The server deliberately does NOT say whose hold it is relative to the caller,
// nor whether releasing would be a force-release. It throws before comparing
// the holder to the caller, and the account now holding the name may well BE
// the caller — a caller asserting expectUnheld who turns out to hold it
// themselves is the plain case. The client knows its own id and can decide;
// rendering the server's neutrality as an accusation is the mistake this
// comment exists to prevent.
type HoldStaleDetail struct {
	WorkerID string
	HolderID string
	Held     bool
}

// WorkerHoldStaleDetail extracts the WORKER_HOLD_STALE payload from err's
// extensions; ok is false when err is not that error. Call it BEFORE MapError
// wraps the error, like WorkerHeldDetail.
func WorkerHoldStaleDetail(err error) (HoldStaleDetail, bool) {
	for _, e := range graphQLErrors(err) {
		if e == nil || extensionCode(e) != "WORKER_HOLD_STALE" {
			continue
		}
		d := HoldStaleDetail{}
		d.WorkerID, _ = e.Extensions["workerId"].(string)
		// PRESENCE first, then type (PR #524 review, Copilot). A bare type
		// assertion cannot tell `heldByUserId: null` from an ABSENT key — both
		// fail it — and the two mean opposite things here: null is a definite
		// "held by nobody now", which sends the caller down the
		// nothing-left-to-release path, while absent means the payload cannot
		// be interpreted at all.
		//
		// So an uninterpretable payload returns ok=false rather than a
		// confident Held=false, and the caller falls through to ordinary error
		// handling — where MapError turns the code into a Conflict carrying the
		// server's own message. Refusing to read a payload is always available;
		// guessing at one is what produces a claim nobody can back.
		raw, present := e.Extensions["heldByUserId"]
		if !present {
			return HoldStaleDetail{}, false
		}
		switch v := raw.(type) {
		case nil:
			// Explicit null — the name is held by nobody now.
		case string:
			d.HolderID, d.Held = v, v != ""
		default:
			return HoldStaleDetail{}, false
		}
		return d, true
	}
	return HoldStaleDetail{}, false
}

// Holder names the person holding the name, preferring the handle the server
// resolved and falling back to the raw user id — which is what the race path
// leaves us with. Empty only when the server sent neither.
func (d HeldDetail) Holder() string {
	if d.HolderName != "" {
		return d.HolderName
	}
	return d.HolderID
}

// DescendantCount returns the descendant count carried by a
// NODE_HAS_DESCENDANTS error (server #661: its extensions.count), or -1 when err
// is not that error or carries no non-negative numeric count. JSON numbers decode
// to float64, but a few other numeric shapes are tolerated. A negative or
// non-numeric value is treated as "no count" (-1) so callers never render a
// nonsensical "-N descendant(s)". Call it BEFORE MapError wraps the error.
func DescendantCount(err error) int {
	for _, e := range graphQLErrors(err) {
		if e == nil || extensionCode(e) != "NODE_HAS_DESCENDANTS" || e.Extensions == nil {
			continue
		}
		n := -1
		switch v := e.Extensions["count"].(type) {
		case float64:
			n = int(v)
		case int:
			n = v
		case int64:
			n = int(v)
		case json.Number:
			if i, cerr := v.Int64(); cerr == nil {
				n = int(i)
			}
		}
		if n >= 0 {
			return n
		}
		return -1
	}
	return -1
}

func codeForExtension(code string) int {
	switch {
	case code == "UNAUTHENTICATED":
		return exitcode.AuthRequired
	case code == "NOT_FOUND" || strings.HasSuffix(code, "_NOT_FOUND"):
		return exitcode.NotFound
	// URN_NOT_QUALIFIED (spec 022) is the server refusing a ref that is not
	// a PK and not a fully-qualified URN — an argument the caller can fix.
	// The CLI pre-checks App refs (cmdutil.CanonicalAppRef, #540), so this
	// is the mapping for every ref it still forwards unchecked.
	case code == "BAD_USER_INPUT" || code == "GRAPHQL_VALIDATION_FAILED" || code == "URN_NOT_QUALIFIED":
		return exitcode.Usage
	case code == "CONFLICT" || strings.HasPrefix(code, "DUPLICATE_") ||
		strings.HasSuffix(code, "_ALREADY_EXISTS") || strings.HasSuffix(code, "_TAKEN") ||
		// A drained resource (PERSONA_REGISTER_EXHAUSTED, #935) is a state
		// conflict: retrying won't help until the state changes.
		strings.HasSuffix(code, "_EXHAUSTED") ||
		// A duplicate node loc (#608). A LITERAL case, because this code is
		// PascalCase where every pattern above is SCREAMING_SNAKE: the server
		// stamps some extensions.code values from an Error CLASS NAME
		// (`NodeLocConflictError` is its own source of truth for the string),
		// so it matches none of them — not the literal, not a prefix, not a
		// suffix — and a duplicate loc fell through to the generic 1.
		//
		// Deliberately not a `*ConflictError` suffix family. The server has 93
		// PascalCase `*Error` class names, of which five are conflict-shaped
		// (IdempotencyConflictError, PositionConflictError, RootNameTakenError,
		// DuplicateAiServiceConfigNameError, and this one) — but a class name
		// only becomes a wire code where a resolver stamps it, and this is the
		// ONE I have observed on the wire. Mapping the other four would
		// document exit codes no caller may ever observe, which is the trap the
		// TEAM_ROLE comment below names. They are candidates, not omissions;
		// each needs its own measurement, and #608 records that.
		code == "NodeLocConflictError":
		return exitcode.Conflict
	// An ambiguous or unusable reference the caller can fix by passing a more
	// specific argument (TEAM_AGENT_AMBIGUOUS → --team-agent;
	// TEAM_AGENT_NOT_INSTALLED → an installed ref) is a usage error, and so
	// is an over-limit input the caller can shrink
	// (TEAM_CHAT_BODY_TOO_LARGE, #939).
	// A missing required argument (WORKER_NAME_REQUIRED, hadron-server#1050)
	// is the same shape: the caller fixes it by passing the flag. `worker
	// cast` refuses this one locally with the remedy, so the mapping is for
	// the paths that do not — an exit 1 for a plainly-fixable input would
	// read as a server fault.
	case strings.HasSuffix(code, "_AMBIGUOUS") || strings.HasSuffix(code, "_NOT_INSTALLED") ||
		strings.HasSuffix(code, "_TOO_LARGE") || strings.HasSuffix(code, "_REQUIRED"):
		return exitcode.Usage
	// #428/#432: a worker with history refuses deletion, a retired worker
	// refuses new sessions/authorship, and a taken worker refuses binding
	// without force (hadron-server#940, the atomic takeover gate) — state
	// conflicts: retrying blind won't help until the state changes (cast a
	// new worker, pick another one, or take over with --force).
	// WORKER_HELD (hadron-server#1050) joins them: a name held by another
	// person refuses every binder but its holder, and retrying — with or
	// without --force — cannot change that. It is a Conflict for the same
	// reason its neighbours are, and it must NOT ride in on the `_TAKEN`
	// suffix rule above, which is the conflation cor:agt:020:09 names: a
	// held name is not a taken one, and only one of the two is forceable.
	// WORKER_HOLD_STALE (hadron-server#1084) joins them: the hold is not what
	// the caller asserted, so the release they described is not the release
	// that would happen. A state conflict, and it maps to the SAME exit code
	// the client-side re-read used to refuse with — deliberately, so the
	// contract `worker release` publishes does not move when the mechanism
	// behind it gets stronger (hadron-cli#522).
	//
	// It must NOT ride the `_STALE` shape into anything else: there is no
	// suffix rule here, because a code ending in _STALE is not automatically a
	// conflict and inventing the family would map codes nobody has defined.
	case code == "WORKER_IN_USE" || code == "WORKER_RETIRED" || code == "WORKER_TAKEN" ||
		code == "WORKER_HELD" || code == "WORKER_HOLD_STALE":
		return exitcode.Conflict
	// An already-existing role is a state conflict (TEAM_ROLE_EXISTS is
	// spelled without the _ALREADY_ the suffix rule matches).
	//
	// The register-invariant codes that used to live here —
	// TEAM_ROLE_NAME_MINTED, TEAM_ROLE_NAME_DUPLICATE, TEAM_ROLE_STALE,
	// TEAM_ROLE_IN_USE, TEAM_ROLE_NAME_OUT_OF_RANGE — went with the register
	// (hadron-server#1050). The server cannot return them, so a case here
	// would document an exit code no caller can ever observe.
	case code == "TEAM_ROLE_EXISTS":
		return exitcode.Conflict
	// #619 — AUTHENTICATED BUT NOT PERMITTED. Both reached scripts as the
	// generic 1 before exitcode.Forbidden existed, indistinguishable from a
	// bug or an outage on the one class a caller can actually act on.
	//
	// FORBIDDEN is the platform's general refusal (23 SDL sites, and
	// hadron-server#1220 migrates ~59 more onto it).
	// CHANNEL_HOST_NOT_WRITABLE is createTeamChatMessage's successor to the
	// retired SESSION_NOT_IN_APP / SESSION_WORKER_NOT_IN_APP: a worker may
	// author only where the on-behalf-of user may write the host memory.
	//
	// LITERAL CASES, NOT A `_NOT_WRITABLE` SUFFIX FAMILY, and the reason is
	// measured rather than cautious: the only other codes with that suffix are
	// HOST_MEMORY_NOT_WRITABLE and HOST_MEMORY_NOT_READABLE, which are members
	// of the RegisterDisclosure ENUM — rendered values explaining why an
	// attendee cannot act on a row, never an extensions.code. A suffix rule
	// would document an exit no caller can ever observe, which is the trap
	// review:map-new-server-error-codes names and which #619's own first draft
	// fell into with CHANNEL_DELETED (also an enum member, also never a code).
	case code == "FORBIDDEN" || code == "CHANNEL_HOST_NOT_WRITABLE":
		return exitcode.Forbidden
	default:
		return exitcode.Error
	}
}
