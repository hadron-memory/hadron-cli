package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
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
