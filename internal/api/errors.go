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

// ServerAnswered reports whether the server RESPONDED, as opposed to the
// request failing in transit.
//
// AN ANSWER IS NOT A REFUSAL, and conflating the two is a live hazard rather
// than pedantry (PR #528 review, Codex). GraphQL permits PARTIAL SUCCESS: a
// mutation can commit and a later nested field resolver can still error, so the
// envelope carries `data` AND `errors`. A caller that reads any GraphQL error as
// "nothing happened" will state the opposite of the truth on exactly that path.
//
// So this answers only the transport question — did a reply come back at all —
// and a caller deciding whether anything COMMITTED must additionally look at
// whether the response carried a payload. A transport failure (a timeout, a
// dropped connection, a proxy giving up) means the request may have been
// applied and only the answer lost, which nothing on the client can tell apart
// (review:a-claim-must-not-outrun-its-evidence).
//
// Call it BEFORE MapError wraps.
func ServerAnswered(err error) bool { return len(graphQLErrors(err)) > 0 }

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
	default:
		return exitcode.Error
	}
}
