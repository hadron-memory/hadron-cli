package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#1837 over hadron-server#3820 (c18816bb): `team usage project` is a
// view of accountUsageProjection. Through the real command with a stub server.

const projWinProjected = `{"window":"SEVEN_DAY","lengthMinutes":10080,"status":"PROJECTED","reason":null,"shape":"WEEKLY","confidence":"HIGH","usedPct":42.5,"lowPctAtReset":88.2,"highPctAtReset":131.0,"ceilingPct":85,"resetsAt":"2026-11-14T00:00:00Z","runsOutAt":"2026-11-13T06:00:00Z","observedAt":"2026-11-10T13:00:00Z","burn":{"lowPctPerHour":0.4,"highPctPerHour":0.7},"perWorkerBurn":{"lowPctPerHour":0.1,"highPctPerHour":0.2}}`
const projWinNoRunOut = `{"window":"FIVE_HOUR","lengthMinutes":300,"status":"PROJECTED","reason":null,"shape":"FLAT","confidence":"LOW","usedPct":10,"lowPctAtReset":20.5,"highPctAtReset":33.3,"ceilingPct":85,"resetsAt":"2026-11-10T18:00:00Z","runsOutAt":null,"observedAt":"2026-11-10T13:00:00Z","burn":{"lowPctPerHour":1,"highPctPerHour":2},"perWorkerBurn":null}`
const projWinInsufficient = `{"window":"FIVE_HOUR","lengthMinutes":300,"status":"INSUFFICIENT_DATA","reason":"LOW_USE","shape":null,"confidence":null,"usedPct":3,"lowPctAtReset":null,"highPctAtReset":null,"ceilingPct":85,"resetsAt":"2026-11-10T18:00:00Z","runsOutAt":null,"observedAt":"2026-11-10T13:00:00Z","burn":null,"perWorkerBurn":null}`
const projWinUnknown = `{"window":"SEVEN_DAY","lengthMinutes":10080,"status":"UNKNOWN","reason":"STALE","shape":null,"confidence":null,"usedPct":null,"lowPctAtReset":null,"highPctAtReset":null,"ceilingPct":85,"resetsAt":null,"runsOutAt":null,"observedAt":null,"burn":null,"perWorkerBurn":null}`

func projResponse(label, windows, whatIf string) string {
	if whatIf == "" {
		whatIf = "null"
	}
	return `{"data":{"accountUsageProjection":{"account":"` + label + `","asOf":"2026-11-10T13:01:00Z","family":"claude","host":"orca1","newestSnapshotAt":"2026-11-10T13:00:00Z","workers":["Ruth"],"workersConfigured":true,"windows":[` + windows + `],"whatIf":` + whatIf + `}}}`
}

func runProject(t *testing.T, resp map[string]string, argv ...string) (*[]attnCall, string, error) {
	t.Helper()
	return runTeamTask(t, resp, append([]string{"team", "usage", "project"}, argv...)...)
}

// projRow is the first table line for this account and window.
func projRow(t *testing.T, out, account, window string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 2 && f[0] == account && f[1] == window {
			return l
		}
	}
	t.Fatalf("no %s %s row in:\n%s", account, window, out)
	return ""
}

func squash1837(s string) string { return strings.Join(strings.Fields(s), "") }

func projCalls(calls *[]attnCall) []attnCall {
	var got []attnCall
	for _, c := range *calls {
		if c.Op == "AccountUsageProjection" {
			got = append(got, c)
		}
	}
	return got
}

func TestUsageProjectRangeAndRunsOutOnlyWhenReturned(t *testing.T) {
	resp := map[string]string{"AccountUsageProjection": projResponse("team-rupali", projWinProjected+","+projWinNoRunOut, "")}
	calls, out, err := runProject(t, resp, "--account", "team-rupali")
	if err != nil {
		t.Fatal(err)
	}
	seven, five := projRow(t, out, "team-rupali", "7d"), projRow(t, out, "team-rupali", "5h")
	if !strings.Contains(seven, "88.2%–131%") || !strings.Contains(seven, "HIGH") || !strings.Contains(seven, "2026-11-13T06:00:00Z") {
		t.Fatalf("7d row %q", seven)
	}
	if !strings.Contains(five, "20.5%–33.3%") || strings.Contains(five, "2026-") || !strings.Contains(five, "—") {
		t.Fatalf("5h row %q: a run-out time must appear only when the server sent one", five)
	}
	got := projCalls(calls)
	if len(got) != 1 || got[0].Vars["account"] != "team-rupali" || got[0].Vars["appRef"] == "" {
		t.Fatalf("calls %+v", got)
	}
	if _, has := got[0].Vars["whatIf"]; has {
		t.Fatalf("no what-if asked, yet whatIf was sent: %v", got[0].Vars)
	}
}

func TestUsageProjectInsufficientAndUnknownPrintAsThemselves(t *testing.T) {
	resp := map[string]string{"AccountUsageProjection": projResponse("a1", projWinInsufficient+","+projWinUnknown, "")}
	_, out, err := runProject(t, resp, "--account", "a1")
	if err != nil {
		t.Fatalf("exit 0 expected: %v", err)
	}
	if l := projRow(t, out, "a1", "5h"); !strings.Contains(l, "INSUFFICIENT_DATA (LOW_USE)") || strings.Contains(l, "%–") {
		t.Fatalf("%q", l)
	}
	if l := projRow(t, out, "a1", "7d"); !strings.Contains(l, "UNKNOWN (STALE)") || strings.Contains(l, "%–") {
		t.Fatalf("%q", l)
	}
	_, js, err := runProject(t, resp, "--account", "a1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Accounts []struct {
			Windows []map[string]any `json:"windows"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal([]byte(js), &dto); err != nil {
		t.Fatal(err)
	}
	ws := dto.Accounts[0].Windows
	if ws[0]["status"] != "INSUFFICIENT_DATA" || ws[0]["lowPctAtReset"] != nil || ws[0]["runsOutAt"] != nil || ws[1]["status"] != "UNKNOWN" || ws[1]["highPctAtReset"] != nil {
		t.Fatalf("windows %v", ws)
	}
}

func TestUsageProjectWhatIfAddWorkersSentAndPrintedUnmodified(t *testing.T) {
	adj := `{"adjusted":[` + projWinProjected + `],"target":null}`
	resp := map[string]string{"AccountUsageProjection": projResponse("team-rupali", projWinNoRunOut, adj)}
	calls, out, err := runProject(t, resp, "--account", "team-rupali", "--add-workers", "1")
	if err != nil {
		t.Fatal(err)
	}
	wi, _ := projCalls(calls)[0].Vars["whatIf"].(map[string]any)
	if wi["addWorkers"] != float64(1) || wi["moveWorker"] != nil {
		t.Fatalf("whatIf %v", wi)
	}
	if l := projRow(t, out, "team-rupali", "7d"); !strings.Contains(l, "88.2%–131%") || !strings.Contains(l, "what-if") {
		t.Fatalf("what-if row %q", l)
	}
}

func TestUsageProjectMoveSendsBothHalvesAndPrintsTarget(t *testing.T) {
	tgt := `{"account":"team-holger","baseline":[` + projWinNoRunOut + `],"adjusted":[` + projWinProjected + `]}`
	resp := map[string]string{"AccountUsageProjection": projResponse("team-rupali", projWinNoRunOut, `{"adjusted":[`+projWinNoRunOut+`],"target":`+tgt+`}`)}
	calls, out, err := runProject(t, resp, "--account", "team-rupali", "--move-worker", "hrn:worker:hadronmemory.com:hadron-dev-team:ruth", "--to-account", "team-holger", "--to-host", "orca2")
	if err != nil {
		t.Fatal(err)
	}
	wi, _ := projCalls(calls)[0].Vars["whatIf"].(map[string]any)
	mv, _ := wi["moveWorker"].(map[string]any)
	if mv["worker"] != "hrn:worker:hadronmemory.com:hadron-dev-team:ruth" || mv["toAccount"] != "team-holger" || mv["toHost"] != "orca2" {
		t.Fatalf("moveWorker %v", wi)
	}
	if _, has := mv["toFamily"]; has {
		t.Fatalf("unset toFamily must be omitted, got %v", mv)
	}
	if _, has := wi["addWorkers"]; has {
		t.Fatalf("a move must not send addWorkers: %v", wi)
	}
	if !strings.Contains(out, "target what-if") || !strings.Contains(out, "team-holger") {
		t.Fatalf("target not printed:\n%s", out)
	}
}

func TestUsageProjectUsageErrorsExitTwoAndCallNothing(t *testing.T) {
	cases := map[string][]string{
		"add-workers 0":         {"--account", "a", "--add-workers", "0"},
		"add-workers negative":  {"--account", "a", "--add-workers", "-2"},
		"move without to":       {"--account", "a", "--move-worker", "Ruth"},
		"to without move":       {"--account", "a", "--to-account", "b"},
		"both what-ifs":         {"--account", "a", "--add-workers", "1", "--move-worker", "Ruth", "--to-account", "b"},
		"to-host alone":         {"--account", "a", "--to-host", "h"},
		"what-if needs account": {"--add-workers", "1"},
		"unknown flag":          {"--bogus"},
	}
	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			calls, _, err := runProject(t, map[string]string{"AccountUsageProjection": projResponse("a", projWinNoRunOut, "")}, argv...)
			if err == nil || exitOf(err) != exitcode.Usage {
				t.Fatalf("want exit 2, got %v (%d)", err, exitOf(err))
			}
			if n := len(projCalls(calls)); n != 0 {
				t.Fatalf("a usage error made %d projection calls", n)
			}
		})
	}
}

func TestUsageProjectServerRefusalMapsByCode(t *testing.T) {
	_, _, err := runProject(t, map[string]string{"AccountUsageProjection": gqlErrorJSON("APP_NOT_FOUND")}, "--account", "a")
	if err == nil || exitOf(err) == exitcode.OK || !strings.Contains(err.Error(), "APP_NOT_FOUND") {
		t.Fatalf("refusal must map through api.MapError, got %v (%d)", err, exitOf(err))
	}
}

func TestUsageProjectOldServerSaysTooOld(t *testing.T) {
	old := `{"errors":[{"message":"Cannot query field \"accountUsageProjection\" on type \"Query\"."}]}`
	_, _, err := runProject(t, map[string]string{"AccountUsageProjection": old}, "--account", "a")
	if err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("got %v", err)
	}
}

func TestUsageProjectJSONShapeEmptyListsAreNotNull(t *testing.T) {
	resp := map[string]string{"AccountUsageProjection": `{"data":{"accountUsageProjection":{"account":"a","asOf":"2026-11-10T13:01:00Z","family":null,"host":null,"newestSnapshotAt":null,"workers":[],"workersConfigured":false,"windows":[],"whatIf":null}}}`}
	_, js, err := runProject(t, resp, "--account", "a", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"workers":[]`, `"windows":[]`, `"whatIf":null`, `"newestSnapshotAt":null`} {
		if !strings.Contains(squash1837(js), want) {
			t.Fatalf("missing %s in %s", want, js)
		}
	}
	if !strings.Contains(squash1837(js), `"accounts":[{`) {
		t.Fatalf("%s", js)
	}
}

// Without --account the labels come from the recorded account-usage objects:
// one projection call per distinct (label, host, family), host and family
// passed through so an ambiguous label is never guessed.
func TestUsageProjectAllAccountsOneCallEach(t *testing.T) {
	rec := func(label, host, at string) string {
		return `{"label":"` + label + `","family":"claude","host":"` + host + `","observedAt":"` + at + `","fiveHourPct":10,"fiveHourResetsAt":"2026-11-10T18:00:00Z","workers":[],"workersConfigured":false,"schemaVersion":1}`
	}
	objs := rec("beta", "orca1", "2026-11-10T12:00:00Z") + "," + rec("alpha", "orca1", "2026-11-10T12:00:00Z") + "," + rec("alpha", "orca1", "2026-11-10T11:00:00Z")
	resp := map[string]string{
		"FindObjects":            `{"data":{"findObjects":{"objects":[` + objs + `],"total":3}}}`,
		"AccountUsageProjection": projResponse("x", projWinNoRunOut, ""),
	}
	calls, out, err := runProject(t, resp, "-m", "hrn:mem:example.com:team", "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := projCalls(calls)
	if len(got) != 2 || got[0].Vars["account"] != "alpha" || got[1].Vars["account"] != "beta" {
		t.Fatalf("want one call each for alpha then beta, got %+v", got)
	}
	if got[0].Vars["host"] != "orca1" || got[0].Vars["family"] != "claude" {
		t.Fatalf("host/family not passed through: %v", got[0].Vars)
	}
	if strings.Count(out, `"account"`) != 2 {
		t.Fatalf("two accounts expected in %s", out)
	}
}

func TestUsageProjectNoAccountsIsEmptyListNotNull(t *testing.T) {
	resp := map[string]string{"FindObjects": `{"data":{"findObjects":{"objects":[],"total":0}}}`}
	_, out, err := runProject(t, resp, "-m", "hrn:mem:example.com:team", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(squash1837(out), `"accounts":[]`) {
		t.Fatalf("%s", out)
	}
}

// A what-if applies to one account; with none named the refusal must say so
// (not fall through to "no memory to record in").
func TestUsageProjectWhatIfWithoutAccountNamesTheFlag(t *testing.T) {
	_, _, err := runProject(t, map[string]string{}, "--add-workers", "1")
	if err == nil || exitOf(err) != exitcode.Usage || !strings.Contains(err.Error(), "--account") {
		t.Fatalf("got %v", err)
	}
}
