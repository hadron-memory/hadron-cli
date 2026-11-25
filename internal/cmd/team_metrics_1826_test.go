package cmd

// hadron-cli#1826: `team metrics` renders the server's teamMetrics reports as
// returned. The CLI never regroups, re-sums, filters or labels: every count,
// grouping and bucket name is the server's. These pins run against the fake
// GraphQL server, red first.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// metricsFull is a full answer: every report present, a No project bucket whose
// taskId and title are null, two owners, two statuses under a project. Every
// count carries named (server#4992): one entry per id of ids, same order, null
// when no name is readable (t2's title is unreadable on purpose).
const metricsFull = `{
 "appId":"capp100000000000000000000",
 "asOf":"2026-11-22T08:00:00Z",
 "window":{"since":"2026-11-15T08:00:00Z","until":"2026-11-22T08:00:00Z"},
 "startedOwned":{
  "all":{"total":3,"ids":["t1","t2","t3"],"named":[{"id":"t1","name":"Ship the metrics command"},{"id":"t2","name":null},{"id":"t3","name":"Roster polish"}],"truncated":false},
  "byOwner":[
   {"owner":{"id":"w1","kind":"WORKER","name":"Eden","urn":"hrn:worker:hadronmemory.com:hadron-dev-team:eden"},
    "tasks":{"total":2,"ids":["t1","t2"],"named":[{"id":"t1","name":"Ship the metrics command"},{"id":"t2","name":null}],"truncated":false},
    "byProject":[
     {"project":{"handle":"AT12","noProject":false,"taskId":"p12","title":"Metrics epic"},"tasks":{"total":1,"ids":["t1"],"named":[{"id":"t1","name":"Ship the metrics command"}],"truncated":false}},
     {"project":{"handle":"AT0","noProject":true,"taskId":null,"title":null},"tasks":{"total":1,"ids":["t2"],"named":[{"id":"t2","name":null}],"truncated":false}}],
    "projectsTruncated":false},
   {"owner":{"id":"u1","kind":"USER","name":"holger","urn":null},
    "tasks":{"total":1,"ids":["t3"],"named":[{"id":"t3","name":"Roster polish"}],"truncated":false},
    "byProject":[{"project":{"handle":"AT12","noProject":false,"taskId":"p12","title":"Metrics epic"},"tasks":{"total":1,"ids":["t3"],"named":[{"id":"t3","name":"Roster polish"}],"truncated":false}}],
    "projectsTruncated":false}],
  "ownersTruncated":false},
 "projects":{"items":[
   {"project":{"handle":"AT12","noProject":false,"taskId":"p12","title":"Metrics epic"},
    "children":[{"status":"STARTED","tasks":{"total":2,"ids":["t1","t3"],"named":[{"id":"t1","name":"Ship the metrics command"},{"id":"t3","name":"Roster polish"}],"truncated":false}},{"status":"FINISHED","tasks":{"total":4,"ids":["t4","t5","t6","t7"],"named":[{"id":"t4","name":"Names in metrics"},{"id":"t5","name":null},{"id":"t6","name":null},{"id":"t7","name":"Window doc"}],"truncated":false}}]}],
  "truncated":false},
 "workers":{
  "active":{"total":2,"ids":["w1","w2"],"named":[{"id":"w1","name":"Eden"},{"id":"w2","name":"Jonas"}],"truncated":false},
  "idle":{"total":1,"ids":["w3"],"named":[{"id":"w3","name":"Petra"}],"truncated":false},
  "overdue":{"total":1,"ids":["w2"],"named":[{"id":"w2","name":"Jonas"}],"truncated":false},
  "inop":{"total":1,"ids":["w2"],"named":[{"id":"w2","name":"Jonas"}],"truncated":false}},
 "flow":{"completed":{"total":5,"ids":["t4","t5","t6","t7","t8"],"named":[{"id":"t4","name":"Names in metrics"},{"id":"t5","name":null},{"id":"t6","name":null},{"id":"t7","name":"Window doc"},{"id":"t8","name":"Cold cache sweep"}],"truncated":false},"started":{"total":3,"ids":["t1","t2","t3"],"named":[{"id":"t1","name":"Ship the metrics command"},{"id":"t2","name":null},{"id":"t3","name":"Roster polish"}],"truncated":false}}
}`

// metricsRosterOnly is a reader who may see the roster but not the tasks: the
// three task reports are null, workers are populated.
const metricsRosterOnly = `{
 "appId":"capp100000000000000000000","asOf":"2026-11-22T08:00:00Z",
 "window":{"since":"2026-11-15T08:00:00Z","until":"2026-11-22T08:00:00Z"},
 "startedOwned":null,"projects":null,
 "workers":{"active":{"total":0,"ids":[],"named":[],"truncated":false},"idle":{"total":2,"ids":["w3","w4"],"named":[{"id":"w3","name":"Petra"},{"id":"w4","name":null}],"truncated":false},
  "overdue":{"total":0,"ids":[],"named":[],"truncated":false},"inop":{"total":0,"ids":[],"named":[],"truncated":false}},
 "flow":null}`

const metricsEmpty = `{
 "appId":"capp100000000000000000000","asOf":"2026-11-22T08:00:00Z",
 "window":{"since":"2026-11-15T08:00:00Z","until":"2026-11-22T08:00:00Z"},
 "startedOwned":{"all":{"total":0,"ids":[],"named":[],"truncated":false},"byOwner":[],"ownersTruncated":false},
 "projects":{"items":[],"truncated":false},
 "workers":{"active":{"total":0,"ids":[],"named":[],"truncated":false},"idle":{"total":0,"ids":[],"named":[],"truncated":false},
  "overdue":{"total":0,"ids":[],"named":[],"truncated":false},"inop":{"total":0,"ids":[],"named":[],"truncated":false}},
 "flow":{"completed":{"total":0,"ids":[],"named":[],"truncated":false},"started":{"total":0,"ids":[],"named":[],"truncated":false}}}`

func metricsResponses1826(body string) map[string]string {
	r := taskResponses()
	r["TeamMetrics"] = `{"data":{"teamMetrics":` + body + `}}`
	return r
}

func metricsCalls1826(calls *[]attnCall) []attnCall {
	var got []attnCall
	for _, c := range *calls {
		if c.Op == "TeamMetrics" {
			got = append(got, c)
		}
	}
	return got
}

func sameJSON1826(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		gb, _ := json.MarshalIndent(g, "", " ")
		wb, _ := json.MarshalIndent(w, "", " ")
		t.Fatalf("--json differs from the server's answer, field for field.\ngot:\n%s\nwant:\n%s", gb, wb)
	}
}

// A full answer renders as the server's own shape, field for field: the DTO
// mirrors TeamMetrics, so nothing is regrouped, summed or dropped.
func TestTeamMetricsJSONMirrorsTheFullAnswer1826(t *testing.T) {
	_, out, err := runTeamTask(t, metricsResponses1826(metricsFull), "team", "metrics", "--json")
	if err != nil {
		t.Fatal(err)
	}
	sameJSON1826(t, out, metricsFull)
}

// A reader of the roster only: the three task reports are null, never zeros.
func TestTeamMetricsRosterOnlyReaderKeepsNulls1826(t *testing.T) {
	_, out, err := runTeamTask(t, metricsResponses1826(metricsRosterOnly), "team", "metrics", "--json")
	if err != nil {
		t.Fatal(err)
	}
	sameJSON1826(t, out, metricsRosterOnly)
	var m map[string]any
	_ = json.Unmarshal([]byte(out), &m)
	for _, k := range []string{"startedOwned", "projects", "flow"} {
		if v, ok := m[k]; !ok || v != nil {
			t.Errorf("%s must be present and null for a roster-only reader, got %v (present %v)", k, v, ok)
		}
	}

	_, text, err := runTeamTask(t, metricsResponses1826(metricsRosterOnly), "team", "metrics")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(text, "not visible to you"); n != 3 {
		t.Errorf("each of the three task reports must say it is not visible to you, got %d:\n%s", n, text)
	}
	for _, zero := range []string{"Started owned tasks: 0", "Started projects: 0", "completed 0", "started 0"} {
		if strings.Contains(text, zero) {
			t.Errorf("a hidden report must never read as zero (%q):\n%s", zero, text)
		}
	}
}

// Every list renders [] when empty, never null.
func TestTeamMetricsEmptyListsRenderAsEmptyArrays1826(t *testing.T) {
	_, out, err := runTeamTask(t, metricsResponses1826(metricsEmpty), "team", "metrics", "--json")
	if err != nil {
		t.Fatal(err)
	}
	sameJSON1826(t, out, metricsEmpty)
	if strings.Contains(out, "null") {
		t.Errorf("an empty answer holds no null:\n%s", out)
	}
}

// A cut-short count keeps its truncated flag, its larger total and its 200 ids.
func TestTeamMetricsTruncatedCountIsKept1826(t *testing.T) {
	ids := make([]string, 200)
	for i := range ids {
		ids[i] = "t" + strings.Repeat("x", 1) + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	idsJSON, _ := json.Marshal(ids)
	body := strings.Replace(metricsEmpty,
		`"startedOwned":{"all":{"total":0,"ids":[],"named":[],"truncated":false}`,
		`"startedOwned":{"all":{"total":350,"ids":`+string(idsJSON)+`,"named":[],"truncated":true}`, 1)
	_, out, err := runTeamTask(t, metricsResponses1826(body), "team", "metrics", "--json")
	if err != nil {
		t.Fatal(err)
	}
	sameJSON1826(t, out, body)

	_, text, err := runTeamTask(t, metricsResponses1826(body), "team", "metrics")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "350") || strings.Contains(text, "Started owned tasks: 200") {
		t.Errorf("the total is the server's total, never the id count:\n%s", text)
	}
	_, withIDs, err := runTeamTask(t, metricsResponses1826(body), "team", "metrics", "--ids")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withIDs, "first 200 of 350") {
		t.Errorf("--ids must say a truncated list is partial (first 200 of 350):\n%s", withIDs)
	}
}

// ownersTruncated, an owner's projectsTruncated and the projects report's truncated
// are stated whenever true.
func TestTeamMetricsStatesEveryTruncationFlag1826(t *testing.T) {
	body := metricsFull
	body = strings.Replace(body, `"ownersTruncated":false`, `"ownersTruncated":true`, 1)
	body = strings.Replace(body, `"projectsTruncated":false`, `"projectsTruncated":true`, 1)
	body = strings.Replace(body, `"truncated":false},
 "workers"`, `"truncated":true},
 "workers"`, 1)
	_, text, err := runTeamTask(t, metricsResponses1826(body), "team", "metrics")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"more owners than shown", "more projects than shown for this owner", "more projects than shown"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	_, quiet, err := runTeamTask(t, metricsResponses1826(metricsFull), "team", "metrics")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(quiet, "than shown") {
		t.Errorf("nothing is truncated here, nothing may say so:\n%s", quiet)
	}
}

// The human blocks: owners with their projects, the No project bucket shown as
// the server names it, projects with child counts by status, the four worker
// numbers (never summed) and the server's window beside the flow figures.
func TestTeamMetricsHumanBlocks1826(t *testing.T) {
	_, text, err := runTeamTask(t, metricsResponses1826(metricsFull), "team", "metrics")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Started owned tasks: 3",
		"Eden", "holger", "AT12", "Metrics epic",
		"No project",
		"Started projects",
		"STARTED 2", "FINISHED 4",
		"Workers", "active 2", "idle 1", "overdue 1", "inop 1",
		"Flow in the window [2026-11-15T08:00:00Z, 2026-11-22T08:00:00Z)",
		"completed 5", "started 3",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("human output lacks %q:\n%s", want, text)
		}
	}
	// A worker can be in several buckets: the CLI never adds them up.
	if strings.Contains(text, "5 workers") || strings.Contains(text, "workers: 5") {
		t.Errorf("worker buckets must not be summed:\n%s", text)
	}
	// Only the flow carries the window; the snapshots do not.
	if i := strings.Index(text, "Flow in the window"); i < 0 || strings.Contains(text[:i], "2026-11-15T08:00:00Z") {
		t.Errorf("the window belongs beside the flow figures only:\n%s", text)
	}
}

// --ids lists the ids behind each count; without it none are printed.
func TestTeamMetricsIDsFlag1826(t *testing.T) {
	_, plain, err := runTeamTask(t, metricsResponses1826(metricsFull), "team", "metrics")
	if err != nil {
		t.Fatal(err)
	}
	if idsLines2972(plain) != "" {
		t.Errorf("no ids lines without --ids:\n%s", plain)
	}
	_, withIDs, err := runTeamTask(t, metricsResponses1826(metricsFull), "team", "metrics", "--ids")
	if err != nil {
		t.Fatal(err)
	}
	listed := " " + idsLines2972(withIDs) + " "
	for _, id := range []string{"t1", "t2", "t3", "t4", "t8", "w1", "w3"} {
		if !strings.Contains(listed, " "+id+" ") {
			t.Errorf("--ids must list %q on an ids line:\n%s", id, withIDs)
		}
	}
	if !strings.Contains(withIDs, "[p12]") {
		t.Errorf("--ids shows the project task id beside the project:\n%s", withIDs)
	}
}

// Server text is terminal-safe in the human output.
func TestTeamMetricsServerTextIsTerminalSafe1826(t *testing.T) {
	body := strings.Replace(metricsFull, `"name":"Eden"`, `"name":"Ed\u001b[31men"`, 1)
	body = strings.Replace(body, `"title":"Metrics epic"`, `"title":"Metrics\u0007 epic"`, 1)
	_, text, err := runTeamTask(t, metricsResponses1826(body), "team", "metrics", "--ids")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(text, "\x1b\x07") {
		t.Errorf("a control character reached the terminal: %q", text)
	}
	if !strings.Contains(text, "Ed") || !strings.Contains(text, "epic") {
		t.Errorf("the text itself must still show:\n%s", text)
	}
}

// Wire: omitted flags send nothing (the server's snapshot and 7-day default
// apply); given ones go out as written; the App comes from the usual resolution.
func TestTeamMetricsWire1826(t *testing.T) {
	calls, _, err := runTeamTask(t, metricsResponses1826(metricsFull), "team", "metrics")
	if err != nil {
		t.Fatal(err)
	}
	got := metricsCalls1826(calls)
	if len(got) != 1 {
		t.Fatalf("want one TeamMetrics call, got %d", len(got))
	}
	if _, has := got[0].Vars["since"]; has {
		t.Errorf("an omitted --since must not be sent: %v", got[0].Vars)
	}
	if _, has := got[0].Vars["until"]; has {
		t.Errorf("an omitted --until must not be sent: %v", got[0].Vars)
	}
	if ref, _ := got[0].Vars["appRef"].(string); ref == "" {
		t.Errorf("appRef must come from the App resolution: %v", got[0].Vars)
	}

	calls, _, err = runTeamTask(t, metricsResponses1826(metricsFull), "team", "metrics",
		"--since", "2026-11-02T00:00:00Z", "--until", "2026-11-09T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	got = metricsCalls1826(calls)
	if len(got) != 1 || got[0].Vars["since"] != "2026-11-02T00:00:00Z" || got[0].Vars["until"] != "2026-11-09T00:00:00Z" {
		t.Fatalf("the window goes out as written: %+v", got)
	}

	// One bound alone is fine: the server fills the other.
	calls, _, err = runTeamTask(t, metricsResponses1826(metricsFull), "team", "metrics", "--since", "2026-11-02T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	got = metricsCalls1826(calls)
	if len(got) != 1 || got[0].Vars["since"] != "2026-11-02T00:00:00Z" {
		t.Fatalf("a lone --since is sent: %+v", got)
	}
	if _, has := got[0].Vars["until"]; has {
		t.Errorf("an omitted --until must not be sent: %v", got[0].Vars)
	}
}

// A malformed bound, or since after until, is refused locally: exit 2, no request.
func TestTeamMetricsRefusesABadWindowBeforeAnyRequest1826(t *testing.T) {
	for name, argv := range map[string][]string{
		"since not RFC 3339": {"--since", "yesterday"},
		"until not RFC 3339": {"--until", "2026-13-40"},
		"date without time":  {"--since", "2026-11-02"},
		"since after until":  {"--since", "2026-11-09T00:00:00Z", "--until", "2026-11-02T00:00:00Z"},
		"empty since":        {"--since", ""},
	} {
		t.Run(name, func(t *testing.T) {
			calls, out, err := runTeamTask(t, metricsResponses1826(metricsFull), append([]string{"team", "metrics"}, argv...)...)
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Usage, err)
			}
			if strings.Contains(err.Error(), "unknown command") || strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("the command and its flags must exist: %v", err)
			}
			if len(metricsCalls1826(calls)) != 0 {
				t.Errorf("refused before any request, got %v", *calls)
			}
			if out != "" {
				t.Errorf("nothing on stdout: %q", out)
			}
		})
	}
}

// A window over 31 days is the server's to refuse, not pre-checked here: the
// request goes out and the server's BAD_USER_INPUT exits 2 with its serverCode.
func TestTeamMetricsLeavesTheWindowLimitToTheServer1826(t *testing.T) {
	r := taskResponses()
	r["TeamMetrics"] = gqlErrorJSON("BAD_USER_INPUT")
	calls, _, err := runTeamTask(t, r, "team", "metrics", "--since", "2026-09-01T00:00:00Z", "--until", "2026-11-01T00:00:00Z")
	if len(metricsCalls1826(calls)) != 1 {
		t.Fatalf("a 60-day window must reach the server: %v", *calls)
	}
	if exitCodeFor(err) != exitcode.Usage || serverCode1669(err) != "BAD_USER_INPUT" {
		t.Fatalf("exit %d serverCode %q, want %d BAD_USER_INPUT: %v", exitCodeFor(err), serverCode1669(err), exitcode.Usage, err)
	}
}

// APP_NOT_FOUND is the one answer for an outsider, a missing App and a caller
// with no standing: exit 4, not told apart.
func TestTeamMetricsAppNotFoundExitsFour1826(t *testing.T) {
	r := taskResponses()
	r["TeamMetrics"] = gqlErrorJSON("APP_NOT_FOUND")
	_, _, err := runTeamTask(t, r, "team", "metrics")
	if exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.NotFound, err)
	}
}

// A server from before teamMetrics gets a plain sentence, not a raw GraphQL error.
func TestTeamMetricsOlderServerGetsAPlainMessage1826(t *testing.T) {
	r := taskResponses()
	r["TeamMetrics"] = missingFieldJSON("teamMetrics")
	_, _, err := runTeamTask(t, r, "team", "metrics")
	if err == nil || !strings.Contains(err.Error(), "does not serve teamMetrics") {
		t.Fatalf("want the plain older-server sentence, got %v", err)
	}
	if strings.Contains(err.Error(), "Cannot query field") {
		t.Errorf("the raw GraphQL error must not show: %v", err)
	}
	if exitCodeFor(err) != exitcode.Usage {
		t.Errorf("exit = %d, want %d", exitCodeFor(err), exitcode.Usage)
	}
	// A validation error about some other field is not this answer.
	r["TeamMetrics"] = missingFieldJSON("somethingElse")
	_, _, err = runTeamTask(t, r, "team", "metrics")
	if err == nil {
		t.Fatal("a validation error about another field is still an error")
	}
	if strings.Contains(err.Error(), "does not serve teamMetrics") {
		t.Errorf("only a rejection of teamMetrics itself may say so: %v", err)
	}
}

// The help says what the numbers are and are not, and carries no citation or issue number.
func TestTeamMetricsHelpSaysWhatTheReportsCount1826(t *testing.T) {
	help := strings.Join(strings.Fields(cmdHelpOut(t, "team", "metrics")), " ")
	for _, want := range []string{
		"--since", "--until", "--ids", "--json",
		"only started tasks that track external work",
		"every task",
		"window applies to the flow",
		"does not serve teamMetrics",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "--by") {
		t.Errorf("the server has no grouping argument, so there is no --by:\n%s", help)
	}
	for _, banned := range []string{"cor:agt", "hadron-server#", "hadron-cli#", "owned flow", "flow owned"} {
		if strings.Contains(help, banned) {
			t.Errorf("help must carry no %q:\n%s", banned, help)
		}
	}
}

// idsLines2972 joins what the "ids:" / "ids (first N of M):" lines list, so an id is looked for where the
// command prints ids and not in the app id, the timestamps or a handle.
func idsLines2972(text string) string {
	var got []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "ids") {
			if i := strings.Index(l, ": "); i >= 0 {
				got = append(got, l[i+2:])
			}
		}
	}
	return strings.Join(got, " ")
}
