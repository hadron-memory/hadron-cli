package cmd

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

// These snapshots are captured from main 5face16e before the rendering change.
// JSON hashes pin the exact bytes, including indentation, escaping and nulls;
// clean text hashes pin spacing as well as values. Host/router local-only rows
// and numeric-only settings tables are inventoried separately in the PR.
//
// #1111 parameterized the matrix per server-supplied field: each case lists one
// cell attack per field that reaches a converted cell (or a composition
// headcount line), so removing any single conversion is independently
// observable instead of only the one field each command used to attack. Fields
// whose fixture lacked a value are covered by the *1101 response variants
// below, stamped with checked replacements (the withLiveness rule: a helper
// that silently declines to act proves nothing). The first cell of every
// original case keeps its historical marker, so its snapshot file is unchanged
// from the #1101 matrix.
func TestTeamSafeCells1101(t *testing.T) {
	type cellAttack struct {
		field string // slug: subtest name, snapshot suffix, sweep-report row
		old   string // quoted-marker value the attack replaces
	}
	type responseSet func(*testing.T) map[string]string
	type commandCase struct {
		name      string
		args      []string
		responses responseSet
		op        string
		cells     []cellAttack
	}
	base := taskResponses()
	base["TeamReportingLines"] = base["TeamRoster"]
	base["TeamAttentionPage"] = attentionJSON
	base["TeamAttentionPreviewPage"] = switchoverPreviewJSON
	base["TeamComposition"] = composition1085
	base["TeamRoles"] = teamRolesJSON
	base["AppAgentRoster"] = engTeamRosterJSON
	base["WorkersRoster"] = staffJSON
	base["AuthContext"] = whoamiAuthJSON
	base["TeamSessions"] = `{"data":{"sessions":[` + activeSessionJSON + `]}}`
	base["TeamWorkItems"] = provenanceWorklog
	base["GetTeamSession"] = `{"data":{"session":` + activeSessionJSON + `}}`
	base["TeamMemoryApp"] = `{"data":{"memory":{"id":"m1","appId":"capp100000000000000000000"}}}`
	base = sessionRenderStubs(base)
	unbound := make(map[string]string)
	for op, body := range base {
		unbound[op] = body
	}
	unbound["AuthContext"] = strings.ReplaceAll(whoamiAuthJSON, "u-me", "u-holger")
	unbound["TeamSessions"] = `{"data":{"sessions":[` + activeSessionJSON + `,` + strings.ReplaceAll(activeSessionJSON, "s-old", "s-another") + `]}}`
	stamp := func(t *testing.T, fixture, anchor, addition string) string {
		t.Helper()
		out := strings.Replace(fixture, anchor, anchor+","+addition, 1)
		if out == fixture {
			t.Fatalf("stamp on %q did not take: fixture would prove nothing", anchor)
		}
		return out
	}
	// The roster's model/tier/check/held-by/held-at cells had no fixture values
	// on main: the fields are real (TeamRoster's projection) but rosterJSON
	// leaves them null, so the cells rendered dashes and no attack could load
	// them (#1111: parameterize per server-supplied field).
	rosterStamped := func(t *testing.T) map[string]string {
		t.Helper()
		r := make(map[string]string, len(base))
		for op, body := range base {
			r[op] = body
		}
		row := stamp(t, rosterJSON, `"unavailableUntil":null`,
			`"model":"sonnet-5-5","tier":"t1","checkStatus":{"status":"PENDING","pendingReason":"queued behind host restart","lastCheckedAt":null},
			 "heldBy":{"id":"u1","handle":null,"urn":null,"name":"Ada Hostmark"},"heldAt":"2026-10-12T03:00:00Z"`)
		r["TeamRoster"] = `{"data":{"teamRoster":` + row + `}}`
		return r
	}
	// worker list's activity columns: the staff fixture carries no
	// family/model/tier/reportsTo/liveness at all, so those cells rendered
	// fixed dashes. held-by rides the failed-lookup path deliberately —
	// describeHolder echoes the raw id when the read fails, which is the one
	// string path from the roster row into that cell.
	workerActivity := func(t *testing.T) map[string]string {
		t.Helper()
		r := make(map[string]string, len(base))
		for op, body := range base {
			r[op] = body
		}
		row := stamp(t, irisWorkerJSON, `"memoryId":"mw1"`,
			`"hasLiveSession":false,"heldByUserId":"u-ada","lastActiveAt":"drift-marker-not-a-time",
			 "reportsTo":{"id":"wkr9","name":"Mira Quill","urn":null},"reportsToRetired":false,
			 "family":"glm","model":"glm-5.3","tier":"standard"`)
		r["WorkersRoster"] = `{"data":{"workers":{"total":2,"items":[` + row + `,` + retiredWorkerJSON + `]}}}`
		r["GetUser"] = `{"errors":[{"message":"no such user"}]}`
		return r
	}
	// whoami's reports-to: the unbound render reads s.Worker.ReportsTo, so the
	// stamp must land INSIDE the session's worker object — a session-level
	// sibling is never read and proves nothing. Both open sessions carry it so
	// the render takes the pick-a-session TABLE branch, whose REPORTS TO column
	// is a converted cell (the one-session prose branch prints reportsToLabel
	// unguarded — filed separately, out of this matrix's cell scope).
	whoamiStamped := func(t *testing.T) map[string]string {
		t.Helper()
		r := make(map[string]string, len(unbound))
		for op, body := range unbound {
			r[op] = body
		}
		ref := `"reportsTo":{"id":"wkr2","name":"Rufus Leadmark","urn":null},"reportsToRetired":false`
		worker := `"worker":{"id":"wkr1","name":"Iris","role":"backend-engineer"}`
		session := strings.Replace(activeSessionJSON, worker, strings.TrimSuffix(worker, "}")+","+ref+"}", 1)
		if session == activeSessionJSON {
			t.Fatalf("whoami reports-to stamp did not take: fixture would prove nothing")
		}
		r["TeamSessions"] = `{"data":{"sessions":[` + session + `,` + strings.ReplaceAll(session, "s-old", "s-another") + `]}}`
		return r
	}
	// provenance's model/transcript cells: null on main's fixture.
	provenanceStamped := func(t *testing.T) map[string]string {
		t.Helper()
		r := make(map[string]string, len(base))
		for op, body := range base {
			r[op] = body
		}
		session := stamp(t, activeSessionJSON, `"transcriptPath":null`, `"transcriptPath":"/mark/transcript.jsonl"`)
		session = stamp(t, session, `"llmModel":null`, `"llmModel":"glm-5.3"`)
		r["GetTeamSession"] = `{"data":{"session":` + session + `}}`
		return r
	}
	// the worklog's model label: null on main's fixture (the "worklog"
	// fallback), so the cell had no server string to attack.
	worklogStamped := func(t *testing.T) map[string]string {
		t.Helper()
		r := make(map[string]string, len(base))
		for op, body := range base {
			r[op] = body
		}
		item := stamp(t, provenanceWorklog, `"at":"2026-08-13T10:00:00Z"`, `"model":"glm-5.3"`)
		r["TeamWorkItems"] = item
		return r
	}
	// task list's assignee cell: null on main's task fixture; the started
	// fixture carries it.
	taskStarted := func(t *testing.T) map[string]string {
		t.Helper()
		r := make(map[string]string, len(base))
		for op, body := range base {
			r[op] = body
		}
		r["AgentTaskList"] = `{"data":{"agentTasks":[` + startedTaskFieldsJSON + `]}}`
		return r
	}
	// The availability family row carries a reason but no until on main; the
	// families table's UNTIL cell cannot be loaded without one.
	availabilityFamilyUntil := func(t *testing.T) map[string]string {
		t.Helper()
		r := make(map[string]string, len(base))
		for op, body := range base {
			r[op] = body
		}
		r["TeamAvailability"] = strings.Replace(`{"data":{"teamAvailability":`+availabilityJSON+`}}`,
			`"family":"codex","state":"UNAVAILABLE","reason":"HOST_DOWN",
	 "until":null`,
			`"family":"codex","state":"UNAVAILABLE","reason":"HOST_DOWN",
	 "until":"2026-10-14T00:00:00Z"`, 1)
		return r
	}
	// stop list renders STATE from the signal's end kind, but the fixture's
	// only list row is live; the ended signal from the lift payload loads it.
	stopListEnded := func(t *testing.T) map[string]string {
		t.Helper()
		r := make(map[string]string, len(stopResponses()))
		for op, body := range stopResponses() {
			r[op] = body
		}
		r["TeamStopSignals"] = `{"data":{"teamStopSignals":{"signals":[` + stopSignalJSON + `,` + endedStopSignalJSON + `],"pageInfo":{"endCursor":null,"hasNextPage":false}}}}`
		return r
	}
	// the session list's ENDED cell: the fixture's sessions are all open, so
	// the cell always rendered a dash until an ended session rides along.
	sessionListEnded := func(t *testing.T) map[string]string {
		t.Helper()
		r := make(map[string]string, len(base))
		for op, body := range base {
			r[op] = body
		}
		r["TeamSessions"] = `{"data":{"sessions":[` + activeSessionJSON + `,` + endedSessionJSON + `]}}`
		return r
	}
	// the composition headcount family line: every family in the fixture also
	// appears in a member row, so no marker could reach ONLY the headcount
	// line — an orca-family count with no matching worker pins it alone.
	compositionHeadcount := func(t *testing.T) map[string]string {
		t.Helper()
		r := make(map[string]string, len(base))
		for op, body := range base {
			r[op] = body
		}
		body := stamp(t, composition1085, `{"role":"qa","family":"glm","count":1}`, `{"role":"lead","family":"orca-family-mark","count":1}`)
		r["TeamComposition"] = body
		return r
	}
	fixed := func(m map[string]string) responseSet {
		return func(*testing.T) map[string]string { return m }
	}
	cases := []commandCase{
		{"session-whoami-multiple", []string{"session", "whoami"}, fixed(unbound), "TeamSessions", []cellAttack{
			{"worker", "Iris"},
			{"role", "backend-engineer"},
			{"repo", "hadron-memory/hadron-cli"},
			{"started-at", "2026-08-11T09:00:00Z"},
			{"id", "s-old"},
		}},
		{"session-whoami-reports-to", []string{"session", "whoami"}, whoamiStamped, "TeamSessions", []cellAttack{
			{"reports-to", "Rufus Leadmark"},
		}},
		{"attention", []string{"attention"}, fixed(base), "TeamAttentionPage", []cellAttack{
			{"worker", "Iris"},
			{"channel", "team"},
		}},
		{"switchover-preview", []string{"attention", "switchover", "preview"}, fixed(base), "TeamAttentionPreviewPage", []cellAttack{
			{"worker", "Iris"},
			{"channel", "team"},
		}},
		{"availability", []string{"availability", "get"}, fixed(base), "TeamAvailability", []cellAttack{
			{"worker", "Rufus"},
			{"state", "UNAVAILABLE"},
			{"reason", "OUT_OF_TOKENS"},
			{"until", "2026-10-13T00:00:00Z"},
			{"family", "codex"},
		}},
		{"availability-family", []string{"availability", "get"}, fixed(base), "TeamAvailability", []cellAttack{
			{"family", "codex"},
		}},
		{"availability-family-reason", []string{"availability", "get"}, fixed(base), "TeamAvailability", []cellAttack{
			{"reason", "HOST_DOWN"},
		}},
		{"availability-family-until", []string{"availability", "get"}, availabilityFamilyUntil, "TeamAvailability", []cellAttack{
			{"until", "2026-10-14T00:00:00Z"},
		}},
		{"composition", []string{"composition"}, fixed(base), "TeamComposition", []cellAttack{
			{"worker-party", "Jack"},
			{"party-urn", "hrn:worker:acme.com:eng-team:jack"},
			// party-id reaches a cell only through partyLabel's name-less
			// fallback — w-nil is the fixture's one name-less party.
			{"party-id", "w-nil"},
			{"coordinator", "Eden"},
			{"state", "WORKING"},
			{"role", "engineer"},
			{"family", "claude"},
		}},
		{"composition-headcount-family", []string{"composition"}, compositionHeadcount, "TeamComposition", []cellAttack{
			{"headcount-family", "orca-family-mark"},
		}},
		{"events", []string{"events"}, fixed(base), "AgentTeamEvents", []cellAttack{
			{"actor", "holger"},
			{"id", "evt1"},
			{"at", "2026-10-12T00:00:00Z"},
			{"type", "task.created"},
			{"task", "task1"},
		}},
		{"role-list", []string{"role", "list"}, fixed(base), "TeamRoles", []cellAttack{
			{"description", "Backend role"},
			{"role", "backend-engineer"},
		}},
		{"roster", []string{"roster"}, fixed(base), "TeamRoster", []cellAttack{
			{"worker", "Rufus"},
			{"state", "ASSIGNED_NOT_STARTED"},
			{"role", "backend-engineer"},
			{"family", "claude"},
			{"reports-to", "Iris"},
		}},
		{"roster-extra", []string{"roster"}, rosterStamped, "TeamRoster", []cellAttack{
			{"model", "sonnet-5-5"},
			{"tier", "t1"},
			{"check-status", "PENDING"},
			{"check-reason", "queued behind host restart"},
			{"held-by-name", "Ada Hostmark"},
			{"held-at", "2026-10-12T03:00:00Z"},
		}},
		{"host-list", []string{"host", "list"}, fixed(hostResponses()), "HostRegistrations", []cellAttack{
			{"label", "Orca box 1"},
			{"host", "orca1"},
			{"refreshed-at", "2026-10-14T01:00:00Z"},
			{"worker", "Jonas"},
			{"worker-family", "claude"},
			{"worktree", "jonas-2"},
		}},
		{"host-ops", []string{"host", "ops"}, fixed(hostResponses()), "HostOperations", []cellAttack{
			{"worker", "Jonas"},
			{"id", "hop1"},
			{"issued-at", "2026-10-14T01:00:00Z"},
			{"type", "worktree.create"},
			{"host", "orca1"},
			{"status", "SUCCEEDED"},
			{"issued-by", "WORKER"},
			{"outcome", "jonas-3"},
		}},
		{"worker-list", []string{"worker", "list"}, fixed(base), "WorkersRoster", []cellAttack{
			{"worker", "Iris"},
			{"role", "backend-engineer"},
			{"urn", "hrn:worker:acme.com:eng-team:iris"},
			{"id", "wkr1"},
		}},
		{"worker-list-activity", []string{"worker", "list"}, workerActivity, "WorkersRoster", []cellAttack{
			{"reports-to", "Mira Quill"},
			{"held-by", "u-ada"},
			{"last-activity", "drift-marker-not-a-time"},
			{"family", "glm"},
			{"model", "glm-5.3"},
			{"tier", "standard"},
		}},
		{"worker-list-retired", []string{"worker", "list", "--include-retired"}, fixed(base), "WorkersRoster", []cellAttack{
			{"retired-worker", "Uma"},
			{"retired-at", "2026-08-13T00:00:00Z"},
			{"retired-urn", "hrn:worker:acme.com:eng-team:uma"},
			{"retired-id", "wkr2"},
			{"retired-role", "qa"},
		}},
		{"session-provenance", []string{"session", "list", "--pr", "hadron-memory/hadron-cli#371", "-m", "hrn:mem:acme.com:eng-team"}, fixed(base), "GetTeamSession", []cellAttack{
			{"worker", "Iris"},
			{"role", "backend-engineer"},
			{"user", "hrn:user:holger"},
			{"tool", "claude-code"},
			{"host", "mac1"},
			{"started-at", "2026-08-11T09:00:00Z"},
			{"id", "s-old"},
		}},
		{"session-worklog", []string{"session", "list", "--pr", "hadron-memory/hadron-cli#371", "-m", "hrn:mem:acme.com:eng-team"}, fixed(base), "TeamWorkItems", []cellAttack{
			{"action", "opened"},
			{"at", "2026-08-13T10:00:00Z"},
			{"tool", "github"},
			// no "session" cell here: the milestone table prints the SESSION's
			// s.ID (session-provenance/id already attacks that field), never
			// the item's own sessionId — attacking the item id lands nowhere.
		}},
		{"session-provenance-model", []string{"session", "list", "--pr", "hadron-memory/hadron-cli#371", "-m", "hrn:mem:acme.com:eng-team"}, provenanceStamped, "GetTeamSession", []cellAttack{
			{"model", "glm-5.3"},
			{"transcript", "/mark/transcript.jsonl"},
		}},
		{"session-worklog-model", []string{"session", "list", "--pr", "hadron-memory/hadron-cli#371", "-m", "hrn:mem:acme.com:eng-team"}, worklogStamped, "TeamWorkItems", []cellAttack{
			{"model", "glm-5.3"},
		}},
		{"session-list", []string{"session", "list"}, fixed(base), "TeamSessions", []cellAttack{
			{"worker", "Iris"},
			{"role", "backend-engineer"},
			{"user", "hrn:user:holger"},
			{"repo", "hadron-memory/hadron-cli"},
			{"started-at", "2026-08-11T09:00:00Z"},
			{"tool", "claude-code"},
			{"id", "s-old"},
		}},
		{"task-list-team", []string{"task", "list", "--team=worker:wkr1"}, fixed(base), "AgentTaskList", []cellAttack{
			{"title", "Fix the flaky CI build"},
			{"id", "task1"},
			{"status", "PENDING"},
			{"owner", "Iris"},
		}},
		{"task-list-team-assignee", []string{"task", "list", "--team=worker:wkr1"}, taskStarted, "AgentTaskList", []cellAttack{
			{"assignee", "Rufus"},
		}},
		{"task-list", []string{"task", "list"}, fixed(base), "AgentTaskList", []cellAttack{
			{"title", "Fix the flaky CI build"},
			{"id", "task1"},
			{"status", "PENDING"},
			{"owner", "Iris"},
		}},
		{"task-assignee", []string{"task", "list"}, taskStarted, "AgentTaskList", []cellAttack{
			{"assignee", "Rufus"},
		}},
		{"task-mine", []string{"task", "mine"}, fixed(base), "AgentTaskList", []cellAttack{
			{"title", "Fix the flaky CI build"},
		}},
		{"session-list-ended", []string{"session", "list"}, sessionListEnded, "TeamSessions", []cellAttack{
			{"ended-at", "2026-08-10T18:00:00Z"},
		}},
		{"stop-list-ended", []string{"stop", "list"}, stopListEnded, "TeamStopSignals", []cellAttack{
			{"state-kind", "LIFTED"},
		}},
		{"stop-list", []string{"stop", "list"}, fixed(stopResponses()), "TeamStopSignals", []cellAttack{
			{"id", "sig1"},
			{"level", "PAUSE_DISPATCH"},
			{"scope", "TEAM"},
			{"target", "wkr1"},
			{"set-at", "2026-10-23T10:00:00Z"},
			{"until", "2026-12-01T08:00:00Z"},
		}},
		{"stop-set", []string{"stop", "set", "--scope", "team", "--level", "pause-dispatch", "--worker", "Iris"}, fixed(stopResponses()), "TeamStopSignalSet", []cellAttack{
			{"id", "sig1"},
			{"level", "PAUSE_DISPATCH"},
			{"scope", "TEAM"},
			{"target", "wkr1"},
			{"set-at", "2026-10-23T10:00:00Z"},
			{"set-by", "usr1"},
			{"until", "2026-12-01T08:00:00Z"},
		}},
		{"stop-lift", []string{"stop", "lift", "sig2"}, fixed(stopResponses()), "TeamStopSignalLift", []cellAttack{
			{"ended-by", "usr1"},
			{"id", "sig2"},
			{"level", "HALT"},
			{"scope", "HOST"},
			{"target", "host1"},
			{"set-at", "2026-10-22T10:00:00Z"},
			{"state-kind", "LIFTED"},
			{"ended-at", "2026-10-22T11:00:00Z"},
		}},
	}
	// Per-cell refinements: how the attacked value reaches the converted cell,
	// so the bounded-cell expectation composes the way the renderer does.
	// Keyed by case/field; every cell not listed receives the server value
	// verbatim, which is why the historical first cells never needed one.
	type cellRefine struct {
		prefix    string // prose the renderer prepends to the value (composed cell)
		lower     bool   // the renderer lowercases the value before the cell
		encoded   bool   // the value arrives as its JSON encoding (Result payload)
		op        string // non-empty: attack this operation, not the case's own
		jsonDrops bool   // the --json projection does not carry this field
		prose     string // the value ALSO reaches this unguarded prose site
	}
	refine := map[string]cellRefine{
		// CHECK composes "PENDING (<pendingReason>)".
		"roster-extra/check-reason": {prefix: "PENDING ("},
		// WORKERS composes name/family[worktrees] per host row.
		"host-list/worker-family": {prefix: "Jonas/"},
		"host-list/worktree":      {prefix: "Jonas/claude["},
		// OUTCOME prints the operation's Result payload verbatim, so the attack
		// arrives pre-escaped and the conversion's control pass is UNOBSERVABLE
		// here — only the bound is pinned (a surviving sweep category, reported
		// with the sweep, not hidden).
		"host-ops/outcome": {prefix: `{"worktree":`, encoded: true},
		// The USER column renders describeSessionUser's GetUser URN lookup, not
		// the session's userId; the session --json projections carry userId
		// only, so the JSON-preservation assertion cannot apply to the URN.
		"session-provenance/user": {op: "GetUser", jsonDrops: true},
		"session-list/user":       {op: "GetUser", jsonDrops: true},
		// TARGET composes its scope's noun phrase (stop.go stopTarget).
		"stop-list/target": {prefix: "team headed by "},
		"stop-set/target":  {prefix: "team headed by "},
		"stop-lift/target": {prefix: "host "},
		// STATE composes "ended (<lowercased kind>)" (stop.go stopState) —
		// both the lift detail and the ended list row.
		"stop-lift/state-kind":       {prefix: "ended (", lower: true},
		"stop-list-ended/state-kind": {prefix: "ended (", lower: true},
		// Cells whose value ALSO reaches an unguarded prose echo — production
		// findings filed out of this test-only change. The prose lines print
		// the raw server string, so the output-wide scans below cannot hold
		// until they are guarded; the cell itself stays fully pinned.
		"role-list/role": {prose: "role.go:188/244 warnings echo r.Role raw"},
		"host-ops/id":    {prose: "host.go:304 --after hint echoes EndCursor raw"},
		"stop-lift/id":   {prose: "stop.go:222 headline echoes Signal.ID raw"},
	}
	const prefix = "a\nb\x1b[31mc"
	attack := prefix + strings.Repeat("界", 1000)
	// Independent expected contract: each control becomes ONE space, and the
	// complete cell is at most 256 runes, including its terminal ellipsis.
	sanitized := []rune("a b [31mc" + strings.Repeat("界", 1000))
	for _, tc := range cases {
		for ci, ca := range tc.cells {
			sub := tc.name
			path := tc.name + ".sha256"
			if ci > 0 {
				sub = tc.name + "/" + ca.field
				path = tc.name + "." + ca.field + ".sha256"
			}
			t.Run(sub, func(t *testing.T) {
				responses := tc.responses(t)
				args := tc.args
				op := tc.op
				ref := refine[tc.name+"/"+ca.field]
				if ref.op != "" {
					op = ref.op
				}
				if !strings.Contains(responses[op], ca.old) {
					t.Fatalf("fixture does not contain %q", ca.old)
				}
				argv := append([]string{"team"}, args...)
				_, clean, err := runSafeCells1101(t, tc.name, responses, argv...)
				if err != nil {
					t.Fatal(err)
				}
				attacked := make(map[string]string, len(responses))
				for o, body := range responses {
					attacked[o] = body
				}
				encoded, _ := json.Marshal(attack)
				needle, _ := json.Marshal(ca.old)
				attacked[op] = strings.ReplaceAll(attacked[op], string(needle), string(encoded))
				if attacked[op] == responses[op] {
					t.Fatal("attack was not installed")
				}
				jsonCalls, rawJSON, err := runSafeCells1101(t, tc.name, attacked, append(argv, "--json")...)
				if err != nil {
					t.Fatal(err)
				}
				// The attacked text render, run once and asserted below. GetUser
				// (user-label cells) fires only here: the --json path skips the
				// table closures, so the consumption check spans BOTH runs.
				textCalls, out, err := runSafeCells1101(t, tc.name, attacked, argv...)
				if err != nil {
					t.Fatal(err)
				}
				// Session scans can request another page. Presence is the claim
				// here: rendering must have consumed the attack-bearing response.
				seen := false
				for _, call := range *jsonCalls {
					seen = seen || call.Op == op
				}
				for _, call := range *textCalls {
					seen = seen || call.Op == op
				}
				if !seen {
					t.Fatalf("attack-bearing operation %s did not run", op)
				}
				if !ref.jsonDrops && !strings.Contains(rawJSON, string(encoded)) {
					t.Fatalf("JSON lost the original server string: %s", rawJSON)
				}
				// The team projection computes age at execution. Normalize only its
				// clock-derived age (and the fixture server's ephemeral port),
				// while preserving every other byte for comparison to main.
				cleanSnapshot := regexp.MustCompile(`http://127\.0\.0\.1:[0-9]+`).ReplaceAllString(clean, "http://127.0.0.1:PORT")
				jsonSnapshot := rawJSON
				if tc.name == "task-list-team" || tc.name == "task-list-team-assignee" {
					cleanSnapshot = regexp.MustCompile(`(?m)^(task1 +(?:PENDING|STARTED) +)\S+`).ReplaceAllString(cleanSnapshot, "${1}AGE")
					jsonSnapshot = regexp.MustCompile(`"ageSeconds": -?[0-9]+`).ReplaceAllString(jsonSnapshot, `"ageSeconds": 0`)
				}
				snapshot := fmt.Sprintf("clean-text %x\nattacked-json %x\n", sha256.Sum256([]byte(cleanSnapshot)), sha256.Sum256([]byte(jsonSnapshot)))
				spath := filepath.Join("testdata", "safe-cells-1101", path)
				if os.Getenv("UPDATE_SAFE_CELLS_1101") == "1" {
					if err := os.MkdirAll(filepath.Dir(spath), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(spath, []byte(snapshot), 0644); err != nil {
						t.Fatal(err)
					}
				}
				saved, err := os.ReadFile(spath)
				if err != nil {
					t.Fatal(err)
				}
				if string(saved) != snapshot {
					t.Errorf("clean text or exact JSON bytes changed:\nwant %s got %s", saved, snapshot)
				}
				// The expected cell composes the way the renderer does (refine),
				// then applies the same contract: one space per control, at
				// most 256 runes including the terminal ellipsis.
				cellWant := string(sanitized)
				if ref.encoded {
					cellWant = ref.prefix + string(encoded)
				} else {
					if ref.lower {
						cellWant = strings.ToLower(cellWant)
					}
					cellWant = ref.prefix + cellWant
				}
				if runes := []rune(cellWant); len(runes) > 256 {
					cellWant = string(runes[:255]) + "…"
				}
				if !strings.Contains(out, cellWant) {
					t.Errorf("missing bounded sanitized cell (256 runes ending in ellipsis)")
				}
				// Output-wide scans, skipped only where the raw value also
				// reaches an unguarded PROSE echo (refine.prose names it).
				if ref.prose == "" {
					for _, r := range out {
						if r != '\n' && (unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)) {
							t.Errorf("terminal control U+%04X survived", r)
							break
						}
					}
					if strings.Count(out, "\n") != strings.Count(clean, "\n") {
						t.Errorf("record split across lines: clean %d, attacked %d", strings.Count(clean, "\n"), strings.Count(out, "\n"))
					}
				}
			})
		}
	}
}

func runSafeCells1101(t *testing.T, name string, responses map[string]string, args ...string) (*[]attnCall, string, error) {
	t.Helper()
	if !strings.HasPrefix(name, "session-whoami") {
		return runTeamTask(t, responses, args...)
	}
	teamGitDir(t)
	// Server recovery path: deliberately no local binding, two open sessions.
	_ = os.Remove(filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json"))
	srv, calls := attnServer(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, args...))
	err := root.Execute()
	return calls, out.String(), err
}
