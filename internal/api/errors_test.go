package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func gqlErr(code string) error {
	return gqlerror.List{{Message: "boom", Extensions: map[string]any{"code": code}}}
}

func TestMapError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, exitcode.OK},
		{"unauthenticated", gqlErr("UNAUTHENTICATED"), exitcode.AuthRequired},
		{"not found", gqlErr("NOT_FOUND"), exitcode.NotFound},
		{"node not found", gqlErr("NODE_NOT_FOUND"), exitcode.NotFound},
		{"bad input", gqlErr("BAD_USER_INPUT"), exitcode.Usage},
		{"urn not qualified", gqlErr("URN_NOT_QUALIFIED"), exitcode.Usage}, // spec 022, #540
		{"validation", gqlErr("GRAPHQL_VALIDATION_FAILED"), exitcode.Usage},
		{"duplicate", gqlErr("DUPLICATE_APP_AGENT"), exitcode.Conflict},
		// TEAM_ROLE_EXISTS is spelled without the _ALREADY_ the suffix rule
		// matches, so it needs the explicit case. Its register-invariant
		// siblings (TEAM_ROLE_IN_USE, _NAME_MINTED, _NAME_DUPLICATE,
		// _NAME_OUT_OF_RANGE, _STALE) went with the register itself
		// (hadron-server#1050) — pinning an exit code for a refusal the server
		// cannot produce documents a contract nobody can exercise.
		{"role exists", gqlErr("TEAM_ROLE_EXISTS"), exitcode.Conflict},
		// hadron-server#1050: a nameless cast. `worker cast` refuses this
		// locally with the remedy, so the mapping covers the paths that do not
		// — exit 1 for a plainly-fixable input would read as a server fault.
		{"name required", gqlErr("WORKER_NAME_REQUIRED"), exitcode.Usage},
		// hadron-cli#487 / cor:agt:020:09: a name held by another person is a
		// state conflict like its WORKER_ neighbours — retrying cannot change
		// it, with or without --force. It needs the explicit case: the suffix
		// rules above match _TAKEN, and HELD is precisely the thing that is
		// not taken.
		{"held", gqlErr("WORKER_HELD"), exitcode.Conflict},
		{"taken", gqlErr("WORKER_TAKEN"), exitcode.Conflict},
		// hadron-cli#522 / hadron-server#1084: the hold is not what the caller
		// asserted, so the release they described is not the one that would
		// happen. A state conflict like its neighbours, and it needs the
		// EXPLICIT case — there is no _STALE suffix rule, deliberately, since
		// inventing that family would map codes nobody has defined.
		//
		// Pinned HERE rather than through a command, because `worker release`
		// intercepts this code before MapError ever sees it — it turns the
		// refusal into an informed retry. A mutation run showed the mapping
		// survives being deleted with every command-level test still green, so
		// without this row it is a line of setup rather than a guard. It earns
		// its place as the general contract: exit codes are documented, and a
		// future caller that does NOT intercept must still get 5 rather than 1.
		{"hold stale", gqlErr("WORKER_HOLD_STALE"), exitcode.Conflict},
		{"forbidden", gqlErr("FORBIDDEN"), exitcode.Error},
		{"no extension", gqlerror.List{{Message: "boom"}}, exitcode.Error},
		{"plain", errors.New("network down"), exitcode.Error},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exitcode.FromError(MapError(tt.err))
			if got != tt.want {
				t.Errorf("MapError() exit code = %d, want %d", got, tt.want)
			}
		})
	}
}

// #239: DescendantCount extracts extensions.count from a NODE_HAS_DESCENDANTS
// error (JSON numbers arrive as float64), and returns -1 for anything else.
func TestDescendantCount(t *testing.T) {
	withCount := gqlerror.List{{
		Message:    "has descendants",
		Extensions: map[string]any{"code": "NODE_HAS_DESCENDANTS", "count": float64(7)},
	}}
	if got := DescendantCount(withCount); got != 7 {
		t.Errorf("count = %d, want 7", got)
	}
	noCount := gqlerror.List{{Message: "x", Extensions: map[string]any{"code": "NODE_HAS_DESCENDANTS"}}}
	if got := DescendantCount(noCount); got != -1 {
		t.Errorf("missing count should be -1, got %d", got)
	}
	negCount := gqlerror.List{{Message: "x", Extensions: map[string]any{"code": "NODE_HAS_DESCENDANTS", "count": float64(-5)}}}
	if got := DescendantCount(negCount); got != -1 {
		t.Errorf("negative count should be treated as -1, got %d", got)
	}
	if got := DescendantCount(gqlErr("BAD_USER_INPUT")); got != -1 {
		t.Errorf("wrong code should be -1, got %d", got)
	}
	if got := DescendantCount(errors.New("plain")); got != -1 {
		t.Errorf("plain error should be -1, got %d", got)
	}
}

// A curated command hitting a schema-validation failure (the CLI and server
// disagree on the schema) gets one actionable, direction-NEUTRAL line — not the
// raw envelope — and includes the server's message (#136).
func TestMapErrorSchemaSkewMessage(t *testing.T) {
	err := MapError(gqlErr("GRAPHQL_VALIDATION_FAILED"))
	if err == nil || !strings.Contains(err.Error(), "out of sync") {
		t.Fatalf("validation code should map to a version-skew hint, got %v", err)
	}
	// The hint must not push a CLI upgrade as the only remedy — a newer CLI vs an
	// older self-hosted server needs the server updated instead.
	if !strings.Contains(err.Error(), "self-hosted") {
		t.Errorf("skew hint should be direction-neutral (mention the server side), got %v", err)
	}
	if got := exitcode.FromError(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want Usage", got)
	}

	// Older servers may omit the code and only send the message.
	listErr := gqlerror.List{{Message: `Cannot query field "myMemories" on type "Query"`}}
	e := MapError(listErr)
	if e == nil || !strings.Contains(e.Error(), "out of sync") {
		t.Errorf(`"Cannot query field" should map to a version-skew hint, got %v`, e)
	}
	if !strings.Contains(e.Error(), "myMemories") {
		t.Errorf("skew hint should surface the server message, got %v", e)
	}
}

// A 400 HTTPError carrying a validation error is treated as skew too.
func TestMapErrorSchemaSkewFromHTTP400(t *testing.T) {
	he := &graphql.HTTPError{
		StatusCode: 400,
		Response:   graphql.Response{Errors: gqlerror.List{{Message: `Cannot query field "x"`}}},
	}
	e := MapError(he)
	if e == nil || !strings.Contains(e.Error(), "out of sync") {
		t.Fatalf("a 400 validation error should map to a version-skew hint, got %v", e)
	}
}

// A normal BAD_USER_INPUT must NOT be reframed as a version-skew error.
func TestMapErrorNonSkewUnchanged(t *testing.T) {
	e := MapError(gqlErr("BAD_USER_INPUT"))
	if strings.Contains(e.Error(), "out of sync") {
		t.Errorf("BAD_USER_INPUT should not be reframed as schema skew, got %v", e)
	}
	if got := exitcode.FromError(e); got != exitcode.Usage {
		t.Errorf("BAD_USER_INPUT should stay Usage, got %d", got)
	}
}

// The raw-body message fallback must honor backslash escapes: an embedded \"
// must not truncate the extracted message (Gemini #146 review).
func TestFirstGraphQLMessageUnescapes(t *testing.T) {
	raw := errors.New(`returned error 400: {"errors":[{"message":"Cannot query field \"myMemories\" on type \"Query\""}]}`)
	got := firstGraphQLMessage(raw)
	want := `Cannot query field "myMemories" on type "Query"`
	if got != want {
		t.Errorf("firstGraphQLMessage() = %q, want %q", got, want)
	}
}

// WorkerHeldDetail reads the hold off the extensions, which is the contract
// (cor:agt:020:09) — the message narration is not. The two server paths that
// raise this code send DIFFERENT field sets, and the thinner one is not an
// error case: the compare-and-set inside the session-creating transaction
// refuses the loser of an ordinary race with workerId and heldBy alone.
func TestWorkerHeldDetail(t *testing.T) {
	full := gqlerror.List{{
		Message: "held",
		Extensions: map[string]any{
			"code": "WORKER_HELD", "workerId": "wkr1", "heldBy": "u-dara",
			"heldByName": "dara", "heldAt": "2026-08-20T09:00:00Z",
		},
	}}
	d, ok := WorkerHeldDetail(full)
	if !ok {
		t.Fatal("WORKER_HELD must be recognized")
	}
	if d.WorkerID != "wkr1" || d.HolderID != "u-dara" || d.HeldAt != "2026-08-20T09:00:00Z" {
		t.Errorf("payload not extracted: %+v", d)
	}
	if got := d.Holder(); got != "dara" {
		t.Errorf("Holder() = %q, want the resolved name", got)
	}

	// The race path: no heldByName, no heldAt. Holder() falls back to the id
	// rather than going empty, which is what keeps the refusal actionable.
	reduced := gqlerror.List{{
		Message:    "held",
		Extensions: map[string]any{"code": "WORKER_HELD", "workerId": "wkr1", "heldBy": "u-dara"},
	}}
	d2, ok := WorkerHeldDetail(reduced)
	if !ok {
		t.Fatal("the reduced payload is still WORKER_HELD")
	}
	if d2.HolderName != "" || d2.HeldAt != "" {
		t.Errorf("absent fields must stay absent, not be invented: %+v", d2)
	}
	if got := d2.Holder(); got != "u-dara" {
		t.Errorf("Holder() = %q, want the heldBy fallback", got)
	}

	// WORKER_TAKEN is the refusal HELD is forever confused with (#487); the
	// two extractors must not answer for each other, or the CLI would offer a
	// takeover for a hold and a cast-your-own for a live session.
	taken := gqlerror.List{{
		Message:    "taken",
		Extensions: map[string]any{"code": "WORKER_TAKEN", "workerId": "wkr1", "sessionId": "s1"},
	}}
	if _, ok := WorkerHeldDetail(taken); ok {
		t.Error("WORKER_TAKEN must not read as WORKER_HELD")
	}
	if _, ok := WorkerTakenDetail(full); ok {
		t.Error("WORKER_HELD must not read as WORKER_TAKEN")
	}
	if _, ok := WorkerHeldDetail(errors.New("network down")); ok {
		t.Error("a plain error is not a hold")
	}
	if d, ok := WorkerHeldDetail(gqlErr("WORKER_HELD")); !ok || d.Holder() != "" {
		t.Errorf("a bare code carries no holder, and must not fabricate one: %+v %v", d, ok)
	}
}

// WORKER_HOLD_STALE's extensions, and the one distinction that decides whether
// the CLI offers a retry or reports the name already free.
//
// `heldByUserId: null` is a REAL ANSWER — the name is held by nobody now —
// and it must not read as a holder whose id happens to be empty. Both decode
// to "" through a bare type assertion, which is why Held is a separate field.
func TestWorkerHoldStaleDetail(t *testing.T) {
	held := gqlerror.List{{
		Message:    "stale",
		Extensions: map[string]any{"code": "WORKER_HOLD_STALE", "workerId": "wkr1", "heldByUserId": "u-gil"},
	}}
	d, ok := WorkerHoldStaleDetail(held)
	if !ok || d.WorkerID != "wkr1" || d.HolderID != "u-gil" || !d.Held {
		t.Errorf("a named holder must come through: %+v ok=%v", d, ok)
	}

	// Present-and-null: unheld NOW. The caller asserted a holder and that hold
	// was released underneath them, so there is nothing left to release.
	unheld := gqlerror.List{{
		Message:    "stale",
		Extensions: map[string]any{"code": "WORKER_HOLD_STALE", "workerId": "wkr1", "heldByUserId": nil},
	}}
	d, ok = WorkerHoldStaleDetail(unheld)
	if !ok {
		t.Fatal("a null holder is still a WORKER_HOLD_STALE")
	}
	if d.Held || d.HolderID != "" {
		t.Errorf("null means unheld now, not a holder: %+v", d)
	}

	// An ABSENT key is not a null one (PR #524 review, Copilot). Both fail a
	// bare type assertion, and they mean opposite things: null is a definite
	// "held by nobody now" that sends the caller down the
	// nothing-left-to-release path, while absent means the payload cannot be
	// read at all. Answering false there would state a fact nobody sent.
	//
	// ok=false drops the caller into ordinary error handling, where MapError
	// turns the code into a Conflict carrying the server's own message — which
	// is the path the WORKER_HOLD_STALE row in TestMapError above pins.
	missing := gqlerror.List{{
		Message:    "stale",
		Extensions: map[string]any{"code": "WORKER_HOLD_STALE", "workerId": "wkr1"},
	}}
	if d, ok := WorkerHoldStaleDetail(missing); ok {
		t.Errorf("an absent heldByUserId is uninterpretable, not 'unheld now': %+v", d)
	}
	// A wrong TYPE is the same class — a number or an object is not an answer.
	wrongType := gqlerror.List{{
		Message:    "stale",
		Extensions: map[string]any{"code": "WORKER_HOLD_STALE", "workerId": "wkr1", "heldByUserId": 42},
	}}
	if d, ok := WorkerHoldStaleDetail(wrongType); ok {
		t.Errorf("a non-string heldByUserId is uninterpretable: %+v", d)
	}

	// A different code must not be read as this one — the mistake that maps a
	// refusal onto the wrong remedy.
	if _, ok := WorkerHoldStaleDetail(gqlErr("WORKER_HELD")); ok {
		t.Error("WORKER_HELD must not read as WORKER_HOLD_STALE — different refusals, different remedies")
	}
	if _, ok := WorkerHoldStaleDetail(errors.New("network down")); ok {
		t.Error("a plain error is not a typed refusal")
	}
}

// EndRefusedBeforeCommit is deliberately ONE code, and the asymmetry is the
// design (PR #528 review, Codex, twice).
//
// Saying "may not have been recorded" when it definitely was not is harmless —
// the remedy is identical, since one stint records one handoff. Saying "was NOT
// recorded" when it WAS is the worst sentence the command can print, and would
// make a later retry failure look like confirmation of data loss.
//
// So: only a spec-backed guarantee earns the definite wording. cor:agt:020:10
// says a failed handoff write refuses the end; nothing else here proves a
// refusal, because GraphQL can return data with errors after a commit, and
// null-bubbling can null the payload after one too.
func TestEndRefusedBeforeCommit(t *testing.T) {
	if !EndRefusedBeforeCommit(gqlErr("HANDOFF_WRITE_FAILED")) {
		t.Error("the one spec-backed refusal must earn the definite wording")
	}
	// A transport failure is the case the ambiguity exists for.
	if EndRefusedBeforeCommit(errors.New("connection reset")) {
		t.Error("a lost reply proves nothing about what committed")
	}
	// Any other code stays ambiguous: it may have been raised AFTER the write.
	for _, code := range []string{"INTERNAL_SERVER_ERROR", "UNAUTHENTICATED", "NOT_FOUND", ""} {
		if EndRefusedBeforeCommit(gqlErr(code)) {
			t.Errorf("%q is not a proof of pre-commit refusal", code)
		}
	}
	// A MIXED envelope keeps the whole answer ambiguous: one unrecognized error
	// beside the known one means something else also went wrong, and the "one
	// recognized code" reasoning no longer covers the response.
	mixed := gqlerror.List{
		{Message: "handoff", Extensions: map[string]any{"code": "HANDOFF_WRITE_FAILED"}},
		{Message: "and something else", Extensions: map[string]any{"code": "INTERNAL_SERVER_ERROR"}},
	}
	if EndRefusedBeforeCommit(mixed) {
		t.Error("an unrecognized error beside the known one must not be read as a clean refusal")
	}
}

// httpErrWithCode builds a non-200 HTTPError whose body still carries a typed
// GraphQL envelope (extensions.code) — the #563 case.
func httpErrWithCode(status int, code string) error {
	he := &graphql.HTTPError{StatusCode: status}
	if code != "" {
		he.Response = graphql.Response{Errors: gqlerror.List{{Message: "boom", Extensions: map[string]any{"code": code}}}}
	}
	return he
}

// #563: a non-200 that carries a typed envelope is classified by the code, not
// the status — matching `hadron api`'s raw path. Without an envelope the status
// still decides.
func TestMapErrorPrefersEnvelopeCodeOverHTTPStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		// code-based answer DIFFERS from the status-based one — the whole point.
		{"403 + WORKER_TAKEN → Conflict, not the 403 generic Error", httpErrWithCode(403, "WORKER_TAKEN"), exitcode.Conflict},
		{"404 + BAD_USER_INPUT → Usage, not the 404 NotFound", httpErrWithCode(404, "BAD_USER_INPUT"), exitcode.Usage},
		{"400 + BAD_USER_INPUT (not skew) → Usage, not the generic Error", httpErrWithCode(400, "BAD_USER_INPUT"), exitcode.Usage},
		{"403 + a _NOT_FOUND code → NotFound", httpErrWithCode(403, "WORKER_NOT_FOUND"), exitcode.NotFound},
		// no envelope: the status still decides (unchanged behaviour).
		{"403 plain → Error", httpErrWithCode(403, ""), exitcode.Error},
		{"401 plain → AuthRequired", httpErrWithCode(401, ""), exitcode.AuthRequired},
		{"404 plain → NotFound", httpErrWithCode(404, ""), exitcode.NotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitcode.FromError(MapError(tc.err)); got != tc.want {
				t.Errorf("MapError → exit %d, want %d", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// #566 — the rendered message carries the SERVER's words and nothing else.
// ---------------------------------------------------------------------------

// wireErr builds a GraphQL error the way a response actually carries one: a
// clean message, with the document location and field path as separate fields.
// genqlient renders those three into `input:<line>: <path> <message>`, which is
// the leak under test — so every case below has a raw rendering that differs
// from the message, and an assertion that the difference is gone.
func wireErr(msg, path string, line int) error {
	e := &gqlerror.Error{Message: msg}
	if line > 0 {
		e.Locations = []gqlerror.Location{{Line: line, Column: 5}}
	}
	if path != "" {
		e.Path = ast.Path{ast.PathName(path)}
	}
	return gqlerror.List{e}
}

func TestMapErrorRendersTheServerMessageOnly(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			// The issue's own example, and the reason it was filed.
			name: "location and path",
			err:  wireErr(`workers URN "hadron-dev-team" is not fully qualified.`, "workers", 3),
			want: `workers URN "hadron-dev-team" is not fully qualified.`,
		},
		{
			// NO locations and NO path still leaked, because Error() writes the
			// filename and ": " unconditionally — it rendered `input: <msg>`.
			// This is the case the issue missed: the leak is universal, not
			// conditional on the server sending a location.
			name: "no location, no path",
			err:  wireErr("memory not found", "", 0),
			want: "memory not found",
		},
		{
			// A message that itself starts with "input:" — indistinguishable
			// from the prefix to anything parsing the rendered string, and
			// trivial for a structured read.
			name: "message that begins with the prefix token",
			err:  wireErr("input: must be an object", "createNode", 2),
			want: "input: must be an object",
		},
		{
			// Several refusals in one response: joined, because showing one
			// hides the rest and the caller fixes half the problem.
			name: "two errors are both shown",
			err: gqlerror.List{
				{Message: "first is wrong", Locations: []gqlerror.Location{{Line: 3}}},
				{Message: "second is wrong", Locations: []gqlerror.Location{{Line: 4}}},
			},
			want: "first is wrong; second is wrong",
		},
		{
			// Carried inside a non-200. This one leaks DIFFERENTLY and worse:
			// `graphql.HTTPError.Error()` renders `returned error 404: <the
			// entire raw JSON body>`, so the user is shown the wire response.
			// Found by the premise guard below rejecting the fixture — the
			// issue assumed every path leaked the same `input:N:` prefix, and
			// this one never did. Same remedy reaches it, because the cleaning
			// keys on the envelope rather than on the prefix.
			name: "envelope inside an HTTPError",
			err: &graphql.HTTPError{
				StatusCode: 404,
				Response: graphql.Response{Errors: gqlerror.List{
					{Message: "no such node", Locations: []gqlerror.Location{{Line: 7}}, Path: ast.Path{ast.PathName("nodeById")}},
				}},
			},
			want: "no such node",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The premise: the RAW error really does render something other
			// than the server's sentence. Without this the test could pass
			// against a fixture that never leaked, which would make it a check
			// that cannot fail — and it earned its keep, rejecting the
			// HTTPError fixture above and exposing that that path leaks the
			// whole JSON body rather than the prefix.
			if raw := tc.err.Error(); raw == tc.want {
				t.Fatalf("fixture does not reproduce any leak — raw rendering is already %q", raw)
			}
			got := MapError(tc.err).Error()
			if got != tc.want {
				t.Errorf("MapError message = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "input:") && !strings.HasPrefix(tc.want, "input:") {
				t.Errorf("genqlient's location prefix survived: %q", got)
			}
		})
	}
}

// Wrapping, not replacing. Every extensions reader documented as "call it
// BEFORE MapError wraps" depends on the chain surviving — if cleaning replaced
// the error, each of them would become a silent always-false rather than a
// visible failure.
func TestMapErrorKeepsTheErrorChainReachable(t *testing.T) {
	orig := gqlerror.List{{
		Message:    "worker is held",
		Locations:  []gqlerror.Location{{Line: 3}},
		Path:       ast.Path{ast.PathName("startSession")},
		Extensions: map[string]any{"code": "WORKER_HELD", "heldBy": "u1", "heldByName": "holger"},
	}}
	mapped := MapError(orig)

	if !strings.Contains(mapped.Error(), "worker is held") || strings.Contains(mapped.Error(), "input:") {
		t.Errorf("message not cleaned: %q", mapped.Error())
	}
	var list gqlerror.List
	if !errors.As(mapped, &list) {
		t.Fatal("the gqlerror.List must still be reachable through the wrapped chain")
	}
	if !HasErrorCode(mapped, "WORKER_HELD") {
		t.Error("HasErrorCode must still see the code after wrapping")
	}
	if d, ok := WorkerHeldDetail(mapped); !ok || d.Holder() != "holger" {
		t.Errorf("WorkerHeldDetail through the chain = %+v, ok=%v", d, ok)
	}
}

// An error with no GraphQL message keeps its own text — there is no server
// sentence to prefer, and blanking it would lose the only diagnosis there is.
func TestMapErrorLeavesNonGraphQLErrorsAlone(t *testing.T) {
	if got := MapError(errors.New("connection reset by peer")).Error(); got != "connection reset by peer" {
		t.Errorf("plain error rewritten: %q", got)
	}
	// A non-200 whose body carried no parsable envelope: the HTTPError's own
	// rendering is all there is.
	httpErr := &graphql.HTTPError{StatusCode: 502}
	if got := MapError(httpErr).Error(); !strings.Contains(got, "502") {
		t.Errorf("HTTPError text should survive when there is no envelope: %q", got)
	}
}

func TestServerMessageHelpers(t *testing.T) {
	err := gqlerror.List{
		{Message: "one", Locations: []gqlerror.Location{{Line: 1}}},
		{Message: "two"},
	}
	if got := ServerMessage(err); got != "one" {
		t.Errorf("ServerMessage = %q, want %q", got, "one")
	}
	if got := ServerMessages(err); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Errorf("ServerMessages = %#v", got)
	}
	// No envelope: empty, which is the caller's signal to fall back.
	if got := ServerMessage(errors.New("nope")); got != "" {
		t.Errorf("ServerMessage on a plain error = %q, want empty", got)
	}
}

// PR #581 review, Codex P2: genqlient SYNTHESISES a one-entry error list
// holding the whole body when it cannot parse one (internal/api/client.go), and
// bearerDoer only intercepts 5xx — so a proxy's HTML 401/403/404 arrives here
// looking like a GraphQL envelope. Cleaning it would emit raw HTML and lose the
// status, which is worse than the prefix #566 removes: the reader loses the one
// fact saying a proxy answered rather than the API.
func TestMapErrorKeepsTheStatusForASynthesisedEnvelope(t *testing.T) {
	html := "<html><head><title>404 Not Found</title></head><body>nginx</body></html>"
	synth := &graphql.HTTPError{
		StatusCode: 404,
		Response:   graphql.Response{Errors: gqlerror.List{{Message: html}}},
	}
	got := MapError(synth).Error()
	if !strings.Contains(got, "404") {
		t.Errorf("the HTTP status must survive a non-JSON body: %q", got)
	}
	if got == html {
		t.Errorf("a synthesised entry must not be rendered as a server sentence: %q", got)
	}
	// The exit code is unchanged by the guard — it is about the MESSAGE.
	if code := exitcode.FromError(MapError(synth)); code != exitcode.NotFound {
		t.Errorf("a 404 still maps to exit 4, got %d", code)
	}
}

// The guard must not catch a REAL envelope inside a non-200 — that is the case
// #566 is about, and the one most refusals actually take. Pinned in both
// directions so the fix for Codex's finding cannot quietly undo the feature.
func TestMapErrorStillCleansAGenuineEnvelopeInsideANon200(t *testing.T) {
	for _, tc := range []struct {
		name string
		errs gqlerror.List
	}{
		{"carries extensions", gqlerror.List{{Message: "no such node", Extensions: map[string]any{"code": "NODE_NOT_FOUND"}}}},
		{"carries a location", gqlerror.List{{Message: "no such node", Locations: []gqlerror.Location{{Line: 3}}}}},
		{"carries a path", gqlerror.List{{Message: "no such node", Path: ast.Path{ast.PathName("nodeById")}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := &graphql.HTTPError{StatusCode: 404, Response: graphql.Response{Errors: tc.errs}}
			if got := MapError(err).Error(); got != "no such node" {
				t.Errorf("a real envelope must still be cleaned, got %q", got)
			}
		})
	}
}
