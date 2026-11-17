package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#1837 over hadron-server#3820 (c18816bb): `team usage project` is a
// view of accountUsageProjection. Through the real command with a stub server.

const projWinProjected = `{"window":"SEVEN_DAY","lengthMinutes":10080,"status":"PROJECTED","reason":null,"shape":"WEEKLY","confidence":"HIGH","usedPct":42.5,"lowPctAtReset":88.2,"highPctAtReset":97.4,"ceilingPct":85,"resetsAt":"2026-11-14T00:00:00Z","runsOutAt":"2026-11-13T06:00:00Z","observedAt":"2026-11-10T13:00:00Z","burn":{"lowPctPerHour":0.4,"highPctPerHour":0.7},"perWorkerBurn":{"lowPctPerHour":0.1,"highPctPerHour":0.2}}`
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
		if f := strings.Fields(l); len(f) > 3 && f[0] == account && f[3] == window {
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
	if !strings.Contains(seven, "88.2%–97.4%") || !strings.Contains(seven, "HIGH") || !strings.Contains(seven, "2026-11-13T06:00:00Z") {
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
	if l := projRow(t, out, "team-rupali", "7d"); !strings.Contains(l, "88.2%–97.4%") || !strings.Contains(l, "what-if") {
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
		"add-workers 0":          {"--account", "a", "--add-workers", "0"},
		"empty move-worker":      {"--account", "a", "--move-worker", "", "--to-account", "b"},
		"empty to-account":       {"--account", "a", "--move-worker", "Ruth", "--to-account", ""},
		"both empty":             {"--account", "a", "--move-worker", "", "--to-account", ""},
		"add-workers 51":         {"--account", "a", "--add-workers", "51"},
		"host without account":   {"--host", "orca1"},
		"family without account": {"--family", "claude"},
		"add-workers negative":   {"--account", "a", "--add-workers", "-2"},
		"move without to":        {"--account", "a", "--move-worker", "Ruth"},
		"to without move":        {"--account", "a", "--to-account", "b"},
		"both what-ifs":          {"--account", "a", "--add-workers", "1", "--move-worker", "Ruth", "--to-account", "b"},
		"to-host alone":          {"--account", "a", "--to-host", "h"},
		"what-if needs account":  {"--add-workers", "1"},
		"unknown flag":           {"--bogus"},
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

// A what-if applies to one account; with none named the refusal must say so
// (not fall through to "no memory to record in").
func TestUsageProjectWhatIfWithoutAccountNamesTheFlag(t *testing.T) {
	_, _, err := runProject(t, map[string]string{}, "--add-workers", "1")
	if err == nil || exitOf(err) != exitcode.Usage || !strings.Contains(err.Error(), "--account") {
		t.Fatalf("got %v", err)
	}
}

// F2 (BLIND, #1849): two identities sharing a label are told apart in the table.
// (Discovery is the server's list since cli#1878; its order, paging and empty
// cases are pinned in team_usage_project_1878_test.go.)
func TestUsageProjectTableShowsHostAndFamilyForSameLabel(t *testing.T) {
	resp := map[string]string{
		"AccountUsageAccounts":   acctsResponse(true, acctRupaliOrca1, acctRupaliOrca2),
		"AccountUsageProjection": `{"data":{"accountUsageProjection":{"account":"shared","asOf":"2026-11-10T13:01:00Z","family":"claude","host":"HOSTX","newestSnapshotAt":null,"workers":[],"workersConfigured":false,"windows":[` + projWinNoRunOut + `],"whatIf":null}}}`,
	}
	_, out, err := runProject(t, resp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "HOST") || !strings.Contains(out, "FAMILY") || !strings.Contains(out, "HOSTX") || !strings.Contains(out, "claude") {
		t.Fatalf("host and family not shown:\n%s", out)
	}
}

// F4 (BLIND, #1849): a qualifier without --account is refused by name, not
// silently ignored (nor refused only for a missing memory).
func TestUsageProjectQualifierWithoutAccountNamesTheFlag(t *testing.T) {
	for _, fl := range []string{"--host", "--family"} {
		_, _, err := runProject(t, map[string]string{}, fl, "x", "-m", "hrn:mem:example.com:team")
		if err == nil || exitOf(err) != exitcode.Usage || !strings.Contains(err.Error(), "--account") {
			t.Fatalf("%s: got %v", fl, err)
		}
	}
}

// LOCAL delta F3 (#1849): server-sent strings are rendered through SafeCell, so
// a hostile host, family, status reason or run-out time cannot forge a row or
// carry terminal escapes. --json keeps the original value.
func TestUsageProjectTableNeutralizesServerStrings(t *testing.T) {
	hostile := `{"window":"FIVE_HOUR","lengthMinutes":300,"status":"UNKNOWN","reason":"STALE\nFORGED  reason  row","shape":null,"confidence":null,"usedPct":null,"lowPctAtReset":null,"highPctAtReset":null,"ceilingPct":85,"resetsAt":null,"runsOutAt":"2026-01-01\nFORGED  row\u001b[31m","observedAt":null,"burn":null,"perWorkerBurn":null}`
	resp := map[string]string{"AccountUsageProjection": `{"data":{"accountUsageProjection":{"account":"a","asOf":"2026-11-10T13:01:00Z","family":"cl\u001b[31maude","host":"orca1\nFORGED  host  row","newestSnapshotAt":null,"workers":[],"workersConfigured":false,"windows":[` + hostile + `],"whatIf":null}}}`}
	_, out, err := runProject(t, resp, "--account", "a")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\x1b") || strings.Count(strings.TrimSpace(out), "\n") != 1 {
		t.Fatalf("a server string forged a row or carried an escape:\n%q", out)
	}
	_, js, err := runProject(t, resp, "--account", "a", "--json")
	if err != nil {
		t.Fatalf("--json: %v\n%s", err, js)
	}
	// cli#1910 (#1849 BLIND delta P3): decode the JSON and compare host and
	// family EXACTLY against what the stub sent, so a partial, truncated or
	// re-escaped value (a SafeCell leaking into --json) fails; a substring
	// check passed all of those.
	var got struct {
		Accounts []struct {
			Host   *string `json:"host"`
			Family *string `json:"family"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal([]byte(js), &got); err != nil {
		t.Fatalf("--json is not valid JSON: %v\n%s", err, js)
	}
	if len(got.Accounts) != 1 {
		t.Fatalf("--json accounts = %d, want 1:\n%s", len(got.Accounts), js)
	}
	const wantHost, wantFamily = "orca1\nFORGED  host  row", "cl\x1b[31maude"
	acct := got.Accounts[0]
	if acct.Host == nil || *acct.Host != wantHost {
		t.Errorf("--json host = %v, want %q", acct.Host, wantHost)
	}
	if acct.Family == nil || *acct.Family != wantFamily {
		t.Errorf("--json family = %v, want %q", acct.Family, wantFamily)
	}
}

// cli#1850 pin 2 (#1849 BLIND delta P3): one label listed from two hosts is
// two separate rows, each carrying ITS host's projection. The stub echoes the
// requested host and answers each host with a different range, so a CLI that
// dropped or rewrote the host (or printed one answer twice) cannot pass.
func TestUsageProject1850DistinctHostsAreSeparateRows(t *testing.T) {
	writeTeamBinding(t)
	inner, _ := attnServer(t, map[string]string{
		"AccountUsageAccounts": acctsResponse(true, acctRupaliOrca1, acctRupaliOrca2),
	})
	low := map[string]string{"orca1": "11.5", "orca2": "62.5"}
	var (
		mu    sync.Mutex // the handler runs on the server's goroutines (#1909 BLIND F1)
		hosts []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(raw, &body)
		if body.OperationName != "AccountUsageProjection" {
			u := inner.URL + r.URL.Path
			if r.URL.RawQuery != "" {
				u += "?" + r.URL.RawQuery
			}
			req, _ := http.NewRequestWithContext(r.Context(), r.Method, u, bytes.NewReader(raw))
			req.Header = r.Header.Clone()
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Errorf("proxy: %v", err)
				return
			}
			defer resp.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, resp.Body)
			return
		}
		host, _ := body.Variables["host"].(string)
		mu.Lock()
		hosts = append(hosts, host)
		mu.Unlock()
		if _, ok := low[host]; !ok {
			t.Errorf("projection requested for unexpected host %q", host)
			http.Error(w, "unexpected host", http.StatusInternalServerError)
			return
		}
		win := strings.Replace(projWinNoRunOut, `"lowPctAtReset":20.5`, `"lowPctAtReset":`+low[host], 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"accountUsageProjection":{"account":"team-rupali","asOf":"2026-11-10T13:01:00Z","family":"claude","host":"`+host+`","newestSnapshotAt":null,"workers":[],"workersConfigured":true,"windows":[`+win+`],"whatIf":null}}}`)
	}))
	defer srv.Close()
	setTeamBindingServer(t, srv.URL)

	run := func(argv ...string) string {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"--server", srv.URL, "team", "usage", "project"}, argv...))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	out := run()
	// Server order is the documented contract ("one call each, in server
	// order", agentic-usage `team usage project`; cli#1878), so the order is
	// asserted, not just the set.
	mu.Lock()
	got := strings.Join(hosts, ",")
	mu.Unlock()
	if got != "orca1,orca2" {
		t.Fatalf("requested hosts %s, want orca1 then orca2", got)
	}
	// Every 5h row of the label, in table order: exactly two, one per host
	// (a duplicated row is caught; #1909 LOCAL F1). Columns as in projRow.
	var rows []string
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 3 && f[0] == "team-rupali" && f[3] == "5h" {
			rows = append(rows, l)
		}
	}
	if len(rows) != 2 || strings.Fields(rows[0])[1] != "orca1" || !strings.Contains(rows[0], "11.5%") ||
		strings.Fields(rows[1])[1] != "orca2" || !strings.Contains(rows[1], "62.5%") {
		t.Fatalf("want exactly one orca1 row at 11.5%% then one orca2 row at 62.5%%:\n%s", out)
	}

	var dto struct {
		Accounts []struct {
			Host    string           `json:"host"`
			Windows []map[string]any `json:"windows"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal([]byte(run("--json")), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Accounts) != 2 || len(dto.Accounts[0].Windows) == 0 || len(dto.Accounts[1].Windows) == 0 ||
		dto.Accounts[0].Host != "orca1" || dto.Accounts[0].Windows[0]["lowPctAtReset"] != 11.5 ||
		dto.Accounts[1].Host != "orca2" || dto.Accounts[1].Windows[0]["lowPctAtReset"] != 62.5 {
		t.Fatalf("--json accounts %+v", dto.Accounts)
	}
}

// cli#1850 pin 3 (#1849 BLIND delta P3): 50 is the inclusive upper bound of
// --add-workers; it succeeds and reaches the server unchanged.
func TestUsageProject1850AddWorkersFiftyReachesServer(t *testing.T) {
	resp := map[string]string{"AccountUsageProjection": projResponse("team-rupali", projWinNoRunOut, `{"adjusted":[`+projWinProjected+`],"target":null}`)}
	calls, _, err := runProject(t, resp, "--account", "team-rupali", "--add-workers", "50")
	if err != nil {
		t.Fatalf("--add-workers 50 must succeed: %v", err)
	}
	pc := projCalls(calls)
	if len(pc) != 1 {
		t.Fatalf("want one projection call, got %d", len(pc))
	}
	if wi, _ := pc[0].Vars["whatIf"].(map[string]any); wi["addWorkers"] != float64(50) {
		t.Fatalf("whatIf %v, want addWorkers 50", pc[0].Vars["whatIf"])
	}
}
