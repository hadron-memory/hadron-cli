package cmd

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// Column padding in the rendered table: two or more spaces. Single spaces are
// INSIDE headers ("HELD BY", "LAST DRIVEN") and inside cells.
var columnGap = regexp.MustCompile(`  +|\t`)

// hadron-cli#487's client half: `worker list` answers WHO HOLDS a name and
// WHETHER ANYONE IS DRIVING IT, against hadron-server#1086's two Worker fields.
//
// The issue is not a missing column, it is a WORD. "Taken" named two unrelated
// facts — a name allocated forever, and a session open this minute — and a
// coordinator dispatched ten issues to a worker whose entire history was two
// sessions under three minutes, because the roster rendered it exactly like one
// worked yesterday.
//
// EVERY fixture here carries the fields EXPLICITLY. The shared irisWorkerJSON
// has no hasLiveSession at all, so a test built on it exercises the MASKED
// branch while appearing to test the columns — the same shape that made six of
// a previous stint's guards unable to fail, and the same trap the held-session
// fixtures document one file over.

// workerWith stamps the working-state quartet onto the shared iris fixture.
// `live` is the raw JSON for hasLiveSession — "true", "false" or "null".
//
// It enforces the server's own invariants rather than taking the arguments at
// face value (PR #523 review, @copilot), because a fixture that describes a row
// the server cannot emit is worse than no fixture: it looks like coverage.
//
//  1. The working-state group is masked TOGETHER. `hasLiveSession: null` means
//     the read gate refused, and it refuses the whole group — so a masked row
//     cannot carry a hold or a timestamp.
//  2. `heldAt` is null EXACTLY when `heldByUserId` is (the schema states this as
//     an API invariant enforced in the resolver).
//
// A caller asking for a combination that violates either gets a FAILURE, not a
// silent correction. Silently fixing it would leave a test author believing
// they had covered "held but masked" while the fixture tested something else —
// which is the exact hiding this review finding is about.
func workerWith(t *testing.T, name, id, held, lastActive string, live string) string {
	t.Helper()
	visible := live != "null"
	if !visible && (held != "" || lastActive != "") {
		t.Fatalf("impossible fixture for %s: hasLiveSession null means the read gate refused, "+
			"so heldByUserId/lastActiveAt are masked too — got held=%q lastActive=%q", name, held, lastActive)
	}
	quoteOrNull := func(s string) string {
		if s == "" {
			return "null"
		}
		return `"` + s + `"`
	}
	// heldAt travels with heldByUserId or not at all.
	heldAt := "null"
	if held != "" {
		heldAt = `"2026-08-20T09:00:00Z"`
	}
	w := strings.Replace(irisWorkerJSON, `"memoryId":"mw1"`,
		`"memoryId":"mw1","heldByUserId":`+quoteOrNull(held)+
			`,"heldAt":`+heldAt+
			`,"hasLiveSession":`+live+
			`,"lastActiveAt":`+quoteOrNull(lastActive), 1)
	w = strings.Replace(w, `"name":"Iris"`, `"name":"`+name+`"`, 1)
	w = strings.Replace(w, `"id":"wkr1"`, `"id":"`+id+`"`, 1)
	return strings.Replace(w, `:iris"`, `:`+strings.ToLower(name)+`"`, 1)
}

// A roster covering every state the columns can be in at once — which is also
// the fixture that proves they are told apart rather than rendered alike.
func activityRosterJSON(t *testing.T) string {
	t.Helper()
	rows := []string{
		// held by me, and a session is open
		workerWith(t, "Iris", "wkr1", "u-holger", "2026-08-25T11:00:00Z", "true"),
		// held by someone else, idle
		workerWith(t, "Dara", "wkr2", "u-dara", "2026-08-22T12:00:00Z", "false"),
		// unheld and NEVER DRIVEN — the state the issue was filed for
		workerWith(t, "Mira", "wkr3", "", "", "false"),
		// masked: the caller may not read this App's working state
		workerWith(t, "Pia", "wkr4", "", "", "null"),
	}
	return `{"data":{"workers":{"total":4,"items":[` + strings.Join(rows, ",") + `]}}}`
}

func activityStubs(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"WorkersRoster":   activityRosterJSON(t),
		"Workers":         activityRosterJSON(t),
		"TeamAppIdentity": teamAppIdentityJSON,
		"AuthContext":     authContextHolgerJSON,
		"GetUser":         heldHolderUserJSON("u-dara", "Dara Holt", "dara"),
	}
}

func runWorkerList(t *testing.T, args ...string) string {
	t.Helper()
	teamGitDir(t)
	gql, _ := captureGraphQL(t, activityStubs(t))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "worker", "list", "--app", "acme.com:eng-team", "--server", gql.URL}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return out.String()
}

// The column ORDER, asserted by position rather than by presence.
//
// "HELD BY appears somewhere" is satisfied by it sitting last, which is most of
// the defect still present: this table's reader is a coordinator scanning for
// who is on the team and whether anyone is driving them, and a fact parked
// behind the URN and the id is a fact they do not read. A previous PR here
// shipped exactly that assertion — index comparisons that a wrong order still
// satisfied — so this one pins the whole header.
func TestTeamWorkerListColumnOrderPutsTheReadableFactsFirst(t *testing.T) {
	out := runWorkerList(t)
	header := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "WORKER") && strings.Contains(line, "ROLE") {
			header = line
			break
		}
	}
	if header == "" {
		t.Fatalf("no header row in:\n%s", out)
	}
	want := []string{"WORKER", "ROLE", "HELD BY", "LAST DRIVEN", "RETIRED", "URN", "ID"}
	// Split on the table's column padding, not on whitespace: two of these
	// headers contain a space, and strings.Fields would silently turn a
	// seven-column header into a nine-item list that no longer lines up with
	// anything.
	got := []string{}
	for _, c := range columnGap.Split(strings.TrimSpace(header), -1) {
		if c != "" {
			got = append(got, c)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("header columns: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("column %d: got %q, want %q (full header: %v)", i, got[i], want[i], got)
		}
	}
}

// The four states, each asserted on ITS OWN ROW rather than anywhere in the
// output. A whole-output substring check passes when the right word lands on
// the wrong worker, which on this table is the entire failure mode: it is a
// roster, and every cell is a claim about a specific person's name.
func TestTeamWorkerListTellsTheFourStatesApart(t *testing.T) {
	out := runWorkerList(t)
	row := func(name string) string {
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), name+" ") {
				return line
			}
		}
		t.Fatalf("no row for %s in:\n%s", name, out)
		return ""
	}
	for _, tc := range []struct {
		worker string
		want   []string
		banned []string
	}{
		// Mine, with a session open. No age beside "live": an age there reads
		// as presence, and a worker session outlives the chat session that
		// started it.
		{"Iris", []string{"you", "live"}, []string{"ago", "never", "nobody"}},
		// Someone else's, idle. The holder is NAMED, not printed as a uuid —
		// `worker get` resolves the same id to the same name, and one entity
		// answering two surfaces two ways is what an agent has to special-case.
		{"Dara", []string{"Dara Holt", "3d ago"}, []string{"you", "live", "never"}},
		// Cast and never bound by anyone. THE state the issue exists for.
		{"Mira", []string{"nobody", "never"}, []string{"you", "live", "ago"}},
		// Masked. Says only that it cannot say — never "nobody", never "never".
		{"Pia", []string{"?"}, []string{"nobody", "never", "live", "you", "ago"}},
	} {
		line := row(tc.worker)
		// Trim the URN/ID tail: it contains the worker slug and would satisfy a
		// naive substring check for words that must not be in the CELLS.
		cells := line
		if i := strings.Index(line, "hrn:worker:"); i > 0 {
			cells = line[:i]
		}
		for _, w := range tc.want {
			if !strings.Contains(cells, w) {
				t.Errorf("%s must render %q, got: %s", tc.worker, w, cells)
			}
		}
		for _, b := range tc.banned {
			if strings.Contains(cells, b) {
				t.Errorf("%s must NOT render %q, got: %s", tc.worker, b, cells)
			}
		}
	}
}

// `session start --json` must not report the activity pair AT ALL.
//
// PR #523 review, @codex P1. That whole response is built from the PRE-mutation
// read, so `hasLiveSession` off `w` says `false` immediately after the call that
// made it true — the NEGATION of the operation being reported, inside the
// document reporting its success — and `lastActiveAt` predates the stint. The
// helper already stripped the hold pair for exactly this reason (PR #504) and
// the new pair walked straight past it.
//
// Replayed as REPORTED rather than reconstructed (review:mutate-with-the-
// original-input): an idle worker, a successful bind, `--json`.
//
// OMITTED, not nulled, and that distinction is the fiddly half. `null` on these
// two is load-bearing on `worker list` — it is the signal that the read was
// gated — so publishing null here would give a false account of WHY the value
// is missing. The keys are dropped instead, which is why sessionStartWorker
// shadows them with omitempty; this test is what proves the shadowing works,
// since a struct that embedded them plainly would emit `null` and still look
// fixed.
func TestTeamSessionStartOmitsTheActivityPairItCannotKnow(t *testing.T) {
	teamGitDir(t)
	// The worker as it is read BEFORE the bind: idle, and never driven.
	idle := workerWith(t, "Iris", "wkr1", "", "", "false")
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + idle + `}}`,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--tool", "claude-code",
		"--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var result struct {
		Worker map[string]json.RawMessage `json:"worker"`
	}
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out.String())
	}
	for _, k := range []string{"hasLiveSession", "lastActiveAt", "heldByUserId", "heldAt"} {
		if raw, present := result.Worker[k]; present {
			t.Errorf("session start reports %q from a PRE-bind read; it must be omitted, got %s",
				k, string(raw))
		}
	}
	// A positive control: the response is a real worker document, not an empty
	// object that would satisfy every assertion above for the wrong reason.
	if got := string(result.Worker["name"]); got != `"Iris"` {
		t.Fatalf("fixture check: the worker document must still be rendered, got name=%s", got)
	}
}

// The degenerate holder: a user whose display fields are ALL null.
//
// review:entity-fields-not-display-labels asks for this row explicitly, and it
// matters more in this column than in the ones that rule was written for. A
// label helper that falls through to "" leaves a BLANK cell — and blank in the
// HELD BY column does not read as "no label", it reads as the value directly
// above it in the same column: nobody. An unreadable name would render as a
// free name, which is the one thing this table must never say.
func TestTeamWorkerListHolderWithNoDisplayFieldsStillRendersTheID(t *testing.T) {
	teamGitDir(t)
	stubs := activityStubs(t)
	stubs["GetUser"] = `{"data":{"user":{"id":"u-dara","name":null,"email":null,` +
		`"handle":null,"githubUsername":null,"roles":["MEMBER"]}}}`
	gql, _ := captureGraphQL(t, stubs)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "list", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "Dara ") {
			continue
		}
		cells := line
		if i := strings.Index(line, "hrn:worker:"); i > 0 {
			cells = line[:i]
		}
		if !strings.Contains(cells, "u-dara") {
			t.Errorf("an unlabelable holder must still render its id: %s", cells)
		}
		if strings.Contains(cells, "nobody") {
			t.Errorf("an unlabelable holder must NEVER collapse to 'nobody': %s", cells)
		}
		return
	}
	t.Fatalf("no Dara row in:\n%s", out.String())
}

// --json carries all four keys on every row, and hasLiveSession is PRESENT as
// null rather than omitted.
//
// That presence is the contract, not a formatting nicety: hasLiveSession is the
// signal that says whether the other three nulls on the row are masks or real
// absences, and an omitted key cannot carry it — absent is indistinguishable
// from a client that never asked.
func TestTeamWorkerListJSONCarriesTheActivityQuartet(t *testing.T) {
	out := runWorkerList(t, "--json")
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(rows) != 4 {
		t.Fatalf("want 4 rows, got %d", len(rows))
	}
	for i, r := range rows {
		for _, k := range []string{"hasLiveSession", "lastActiveAt"} {
			if _, present := r[k]; !present {
				t.Errorf("row %d: %q must always be present, even as null: %v", i, k, r)
			}
		}
	}
	// Row 3 is the masked one: its discriminator is explicitly null, which is
	// what lets a consumer refuse to read the other three as answers.
	if got := string(rows[3]["hasLiveSession"]); got != "null" {
		t.Errorf("the masked row's hasLiveSession must be null, got %s", got)
	}
	// Row 2 is permitted and never driven: false plus a null timestamp, and the
	// pair is what means "never" rather than "unknown".
	if got := string(rows[2]["hasLiveSession"]); got != "false" {
		t.Errorf("the never-driven row must report hasLiveSession false, got %s", got)
	}
	if got := string(rows[2]["lastActiveAt"]); got != "null" {
		t.Errorf("the never-driven row must report lastActiveAt null, got %s", got)
	}
	// The hold travels on the roster too, matching `worker get`'s spelling.
	if got := string(rows[1]["heldByUserId"]); got != `"u-dara"` {
		t.Errorf("the roster must carry heldByUserId, got %s", got)
	}
}

// `worker get` says "nobody" only when it has established that it MAY know —
// and stays silent when it has not. Two branches, and the silent one is the
// one that must never become a claim.
func TestTeamWorkerGetSaysNobodyOnlyWhenItMayKnow(t *testing.T) {
	get := func(worker string) string {
		teamGitDir(t)
		gql, _ := captureGraphQL(t, map[string]string{
			"Workers":         `{"data":{"workers":{"total":1,"items":[` + worker + `]}}}`,
			"GetWorker":       `{"data":{"worker":` + worker + `}}`,
			"TeamAppIdentity": teamAppIdentityJSON,
			"AuthContext":     authContextHolgerJSON,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "worker", "get", "Mira", "--app", "acme.com:eng-team", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		return out.String()
	}

	permitted := get(workerWith(t, "Mira", "wkr3", "", "", "false"))
	if !strings.Contains(permitted, "held by: nobody") {
		t.Errorf("a permitted read of an unheld worker must say so: %s", permitted)
	}
	if !strings.Contains(permitted, "driven: never") {
		t.Errorf("a permitted read of an undriven worker must say so: %s", permitted)
	}

	masked := get(workerWith(t, "Mira", "wkr3", "", "", "null"))
	if strings.Contains(masked, "held by:") {
		t.Errorf("a MASKED read must not speak about the hold at all: %s", masked)
	}
	if strings.Contains(masked, "driven:") {
		t.Errorf("a MASKED read must not speak about liveness at all: %s", masked)
	}
	// Proven to be the same command doing real work either way, rather than a
	// read that failed and printed nothing: the worker itself still renders.
	if !strings.Contains(masked, "Mira") {
		t.Errorf("the masked read must still render the worker: %s", masked)
	}
}
