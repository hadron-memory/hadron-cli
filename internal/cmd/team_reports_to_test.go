package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func reportingWorker(t *testing.T, base string, target any, retired bool) string {
	t.Helper()
	var row map[string]any
	if err := json.Unmarshal([]byte(base), &row); err != nil {
		t.Fatal(err)
	}
	row["reportsTo"] = target
	row["reportsToRetired"] = retired
	row["hasLiveSession"] = false
	raw, _ := json.Marshal(row)
	return string(raw)
}

var reportsBoss = map[string]any{"id": "boss1", "name": "Marco", "urn": "hrn:worker:acme.com:eng-team:marco"}

func TestWorkerReportsToReadSurfaces(t *testing.T) {
	for _, verb := range []string{"get", "list"} {
		for _, retired := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/retired=%t", verb, retired), func(t *testing.T) {
				row := reportingWorker(t, irisWorkerJSON, reportsBoss, retired)
				gql, _ := captureGraphQL(t, map[string]string{"GetWorker": `{"data":{"worker":` + row + `}}`, "Workers": `{"data":{"workers":{"total":1,"items":[` + row + `]}}}`, "WorkersRoster": `{"data":{"workers":{"total":1,"items":[` + row + `]}}}`, "AuthContext": whoamiAuthJSON, "TeamAppIdentity": `{"data":{"app":` + appJSON + `}}`})
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"team", "worker", verb, "--app", "capp100000000000000000000", "--server", gql.URL}
				if verb == "get" {
					args = append(args, "wkr1")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(strings.ToLower(out.String()), "reports to") || !strings.Contains(out.String(), "Marco") {
					t.Fatal(out.String())
				}
				if strings.Contains(out.String(), "Marco (retired)") != retired {
					t.Fatal(out.String())
				}
			})
		}
	}
}

func TestWorkerReportsToSetAndClear(t *testing.T) {
	for _, clear := range []bool{false, true} {
		t.Run(fmt.Sprint(clear), func(t *testing.T) {
			row := reportingWorker(t, irisWorkerJSON, reportsBoss, false)
			gql, captured := captureGraphQL(t, map[string]string{"GetWorker": `{"data":{"worker":` + row + `}}`, "SetWorkerReportsTo": `{"data":{"setWorkerReportsTo":` + row + `}}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"team", "worker", "set-reports-to", "wkr1", "--json", "--server", gql.URL}
			if clear {
				args = append(args, "--clear")
			} else {
				args = append(args, "Marco")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var vars map[string]json.RawMessage
			_ = json.Unmarshal(captured["SetWorkerReportsTo"], &vars)
			if string(vars["workerRef"]) != `"wkr1"` {
				t.Fatal(vars)
			}
			want := `"Marco"`
			if clear {
				want = "null"
			}
			if string(vars["reportsTo"]) != want {
				t.Fatalf("vars=%s", captured["SetWorkerReportsTo"])
			}
			if !strings.Contains(out.String(), `"reportsTo"`) {
				t.Fatal(out.String())
			}
		})
	}
}

func TestWorkerReportsToRefusalRendered(t *testing.T) {
	for _, code := range []string{"WORKER_REPORTS_TO_SELF", "WORKER_REPORTS_TO_OTHER_APP", "WORKER_REPORTS_TO_RETIRED", "WORKER_REPORTS_TO_CYCLE", "WORKER_REPORTS_TO_FORBIDDEN", "WORKER_REPORTS_TO_HELD", "WORKER_NOT_FOUND"} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", code, asJSON), func(t *testing.T) {
				row := reportingWorker(t, irisWorkerJSON, nil, false)
				gql, captured := captureGraphQL(t, map[string]string{"GetWorker": `{"data":{"worker":` + row + `}}`, "SetWorkerReportsTo": fmt.Sprintf(`{"errors":[{"message":"server refusal text","extensions":{"code":%q}}]}`, code)})
				f, out := testFactory(t)
				stderr := &strings.Builder{}
				f.IOStreams.ErrOut = stderr
				root := NewRootCmd(f)
				args := []string{"team", "worker", "set-reports-to", "wkr1", "Marco", "--server", gql.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				err := root.Execute()
				if err == nil || out.Len() != 0 || captured["SetWorkerReportsTo"] == nil {
					t.Fatalf("err=%v out=%s", err, out.String())
				}
				renderFailure(f, args, err)
				rendered := stderr.String()
				if asJSON {
					rendered = out.String()
				}
				if !strings.Contains(rendered, "server refusal text") {
					t.Fatal(stderr.String())
				}
				if !strings.Contains(rendered, code) {
					t.Fatal(stderr.String())
				}
			})
		}
	}
}

func TestSessionStartReportsToForwardedOrOmitted(t *testing.T) {
	for _, send := range []bool{false, true} {
		t.Run(fmt.Sprint(send), func(t *testing.T) {
			teamGitDir(t)
			row := withLiveness(irisWorkerJSON, "false")
			gql, captured := captureGraphQL(t, map[string]string{"Workers": `{"data":{"workers":{"total":1,"items":[` + row + `]}}}`, "TeamSessionsPage": `{"data":{"sessionsPage":{"total":0,"hasMore":false,"items":[]}}}`, "TeamMemoryApp": `{"data":{"memory":{"id":"m1","appId":"capp100000000000000000000"}}}`, "StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"team", "session", "start", "--as", "Iris", "--tool", "codex", "-m", "acme.com::eng-team", "--json", "--server", gql.URL}
			if send {
				args = append(args, "--reports-to", "Marco")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var vars struct {
				Input map[string]json.RawMessage `json:"input"`
			}
			_ = json.Unmarshal(captured["StartTeamSession"], &vars)
			v, ok := vars.Input["reportsTo"]
			if ok != send || send && string(v) != `"Marco"` {
				t.Fatal(string(captured["StartTeamSession"]))
			}
		})
	}
}

func TestSessionWhoamiReadsCurrentReportingRelation(t *testing.T) {
	writeTeamBinding(t)
	gql, _ := captureGraphQL(t, map[string]string{"WorkerReporting": `{"data":{"worker":{"id":"wkr1","hasLiveSession":false,"reportsTo":{"id":"boss1","name":"Marco","urn":null},"reportsToRetired":true}}}`})
	setTeamBindingServer(t, gql.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "whoami", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "reports to: Marco (retired)") {
		t.Fatal(out.String())
	}
}

func TestWorkerRosterGroupsUnboundByReportingIdentity(t *testing.T) {
	var rows []string
	for i, boss := range []string{"b2", "b1", "b2"} {
		var w map[string]any
		_ = json.Unmarshal([]byte(irisWorkerJSON), &w)
		w["id"] = fmt.Sprintf("w%d", i)
		w["name"] = fmt.Sprintf("Person%d", i)
		base, _ := json.Marshal(w)
		rows = append(rows, reportingWorker(t, string(base), map[string]any{"id": boss, "name": "Same name", "urn": nil}, false))
	}
	gql, _ := captureGraphQL(t, map[string]string{"WorkersRoster": `{"data":{"workers":{"total":3,"items":[` + strings.Join(rows, ",") + `]}}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "list", "--app", "capp100000000000000000000", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Name           string
		ReportsTo      struct{ ID string }
		HasLiveSession bool
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ReportsTo.ID != "b1" || got[1].ReportsTo.ID != "b2" || got[2].ReportsTo.ID != "b2" || got[0].HasLiveSession {
		t.Fatal(out.String())
	}
}

func TestReportingUnsetAndUnavailableRemainDistinct(t *testing.T) {
	for _, known := range []bool{false, true} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("known=%t/json=%t", known, asJSON), func(t *testing.T) {
				writeTeamBinding(t)
				live := "null"
				if known {
					live = "false"
				}
				gql, _ := captureGraphQL(t, map[string]string{"WorkerReporting": `{"data":{"worker":{"id":"wkr1","hasLiveSession":` + live + `,"reportsTo":null,"reportsToRetired":false}}}`})
				setTeamBindingServer(t, gql.URL)
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"team", "session", "whoami", "--server", gql.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if asJSON {
					var dto map[string]any
					_ = json.Unmarshal([]byte(out.String()), &dto)
					if dto["reportingKnown"] != known || dto["reportsTo"] != nil {
						t.Fatal(out.String())
					}
				} else {
					want := "reports to: ? (unavailable)"
					if known {
						want = "reports to: -"
					}
					if !strings.Contains(out.String(), want) {
						t.Fatal(out.String())
					}
				}
			})
		}
	}
}

func TestReportsToSetterCarriesTheBoundActor(t *testing.T) {
	writeTeamBinding(t)
	row := reportingWorker(t, irisWorkerJSON, reportsBoss, false)
	srv, calls := attnServer(t, map[string]string{"GetWorker": `{"data":{"worker":` + row + `}}`, "SetWorkerReportsTo": `{"data":{"setWorkerReportsTo":` + row + `}}`})
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "set-reports-to", "hrn:worker:acme.com:eng-team:iris", "Marco", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	writes := 0
	for _, call := range *calls {
		if call.Op == "SetWorkerReportsTo" {
			writes++
			if call.Session != "s-new" {
				t.Fatalf("wrong actor: %+v", call)
			}
		}
	}
	if writes != 1 {
		t.Fatalf("writes=%d", writes)
	}
}

func TestReportsToInvalidInputsRefuseBeforeRequests(t *testing.T) {
	for _, args := range [][]string{{"team", "worker", "set-reports-to", "wkr1"}, {"team", "worker", "set-reports-to", "wkr1", "Marco", "--clear"}, {"team", "worker", "set-reports-to", "wkr1", ""}, {"team", "session", "start", "--as", "Iris", "--reports-to", ""}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			gql, calls := attnServer(t, map[string]string{})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(args, "--server", gql.URL))
			if err := root.Execute(); exitCodeFor(err) != 2 || len(*calls) != 0 {
				t.Fatalf("err=%v calls=%+v", err, *calls)
			}
		})
	}
}

func TestWhoamiFallbackCarriesReportingRelation(t *testing.T) {
	teamGitDir(t)
	var session map[string]any
	_ = json.Unmarshal([]byte(whoamiSession("s-mine", "wkr1", "u-me", "Iris", false)), &session)
	session["worker"] = map[string]any{"id": "wkr1", "name": "Iris", "role": "engineer", "hasLiveSession": false, "reportsTo": reportsBoss, "reportsToRetired": false}
	row, _ := json.Marshal(session)
	gql, _ := captureGraphQL(t, map[string]string{"AuthContext": whoamiAuthJSON, "TeamSessionsPage": `{"data":{"sessionsPage":{"total":1,"hasMore":false,"items":[` + string(row) + `]}}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "whoami", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var dto struct {
		ReportsTo      struct{ ID string }
		ReportingKnown bool
		Candidates     []struct{ ReportsTo struct{ ID string } }
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if !dto.ReportingKnown || dto.ReportsTo.ID != "boss1" || len(dto.Candidates) != 1 || dto.Candidates[0].ReportsTo.ID != "boss1" {
		t.Fatal(out.String())
	}
}
