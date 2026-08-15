package api

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// MapError converts transport and GraphQL errors into CodedErrors so
// the root command can derive the documented exit code. Codes come
// from hadron-server's Apollo resolvers (extensions.code).
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

	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case 401:
			return exitcode.New(exitcode.AuthRequired, err)
		case 403:
			return exitcode.New(exitcode.Error, err)
		case 404:
			return exitcode.New(exitcode.NotFound, err)
		}
		return exitcode.New(exitcode.Error, err)
	}

	var list gqlerror.List
	if errors.As(err, &list) && len(list) > 0 {
		return exitcode.New(codeForExtension(extensionCode(list[0])), err)
	}
	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) {
		return exitcode.New(codeForExtension(extensionCode(gqlErr)), err)
	}

	return exitcode.New(exitcode.Error, err)
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

// TeamRoleStaleNames extracts the current stored register carried by a
// TEAM_ROLE_STALE refusal (hadron-server#987: extensions.storedNames) — what
// lets a CAS caller rebase its edit without a second read. ok is false when
// err is not that error; a stale error without the payload returns ok=true
// with nil names (the caller re-reads instead). Call BEFORE MapError.
func TeamRoleStaleNames(err error) ([]string, bool) {
	for _, e := range graphQLErrors(err) {
		if e == nil || extensionCode(e) != "TEAM_ROLE_STALE" {
			continue
		}
		// Extensions is non-nil whenever the code matched (the code came
		// from it), so ABSENCE of the key — or a malformed value — must be
		// distinguished from a legitimately EMPTY register: fabricating []
		// here would make the CAS caller "rebase" onto a register that was
		// never observed (PR #440 review, P2). nil = no payload, re-read.
		raw, ok := e.Extensions["storedNames"].([]any)
		if !ok {
			return nil, true
		}
		names := make([]string, 0, len(raw))
		for _, v := range raw {
			s, ok := v.(string)
			if !ok {
				return nil, true // malformed entry: treat the whole payload as missing
			}
			names = append(names, s)
		}
		return names, true
	}
	return nil, false
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
	case code == "BAD_USER_INPUT" || code == "GRAPHQL_VALIDATION_FAILED":
		return exitcode.Usage
	case code == "CONFLICT" || strings.HasPrefix(code, "DUPLICATE_") ||
		strings.HasSuffix(code, "_ALREADY_EXISTS") || strings.HasSuffix(code, "_TAKEN") ||
		// A drained resource (PERSONA_REGISTER_EXHAUSTED, #935) is a state
		// conflict: retrying won't help until the state changes.
		strings.HasSuffix(code, "_EXHAUSTED"):
		return exitcode.Conflict
	// An ambiguous or unusable reference the caller can fix by passing a more
	// specific argument (TEAM_AGENT_AMBIGUOUS → --team-agent;
	// TEAM_AGENT_NOT_INSTALLED → an installed ref) is a usage error, and so
	// is an over-limit input the caller can shrink
	// (TEAM_CHAT_BODY_TOO_LARGE, #939).
	case strings.HasSuffix(code, "_AMBIGUOUS") || strings.HasSuffix(code, "_NOT_INSTALLED") ||
		strings.HasSuffix(code, "_TOO_LARGE"):
		return exitcode.Usage
	// #428/#432: a worker with history refuses deletion, a retired worker
	// refuses new sessions/authorship, and a taken worker refuses binding
	// without force (hadron-server#940, the atomic takeover gate) — state
	// conflicts: retrying blind won't help until the state changes (cast a
	// new worker, pick another one, or take over with --force).
	case code == "WORKER_IN_USE" || code == "WORKER_RETIRED" || code == "WORKER_TAKEN":
		return exitcode.Conflict
	// #410: register-invariant refusals (hadron-server#960). A minted name
	// (the register entry records an allocation that exists forever), a
	// cross-register duplicate, and an already-existing role are state
	// conflicts (TEAM_ROLE_EXISTS is spelled without the _ALREADY_ the
	// suffix rule matches); an out-of-range name is an input the caller can
	// fix (or deliberately override with --allow-out-of-range).
	case code == "TEAM_ROLE_NAME_MINTED" || code == "TEAM_ROLE_NAME_DUPLICATE" ||
		code == "TEAM_ROLE_EXISTS" ||
		// #436: a CAS refusal is by definition a state conflict — the sugar
		// rebases and retries; a caller seeing it surfaced ran out of retries.
		code == "TEAM_ROLE_STALE":
		return exitcode.Conflict
	case code == "TEAM_ROLE_NAME_OUT_OF_RANGE":
		return exitcode.Usage
	default:
		return exitcode.Error
	}
}
