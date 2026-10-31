package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"os"
	"strings"
	"testing"
)

// These runtime pins deliberately compile on the unmodified base: each new
// surface must reach GraphQL, not merely appear in Cobra's flag table.
func TestInputs1337ResumeWire(t *testing.T) {
	teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "")
	srv, calls := captureGraphQL(t, resumeStubs("CHOOSE"))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--server", srv.URL, "--description", "next stint", "--family", "codex"})
	_ = root.Execute() // CHOOSE is intentionally exit 2 and binds nothing.
	var v struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(calls["ResumeTeamSession"], &v); err != nil {
		t.Fatal(err)
	}
	if v.Input["description"] != "next stint" || v.Input["family"] != "codex" {
		t.Fatalf("resume input = %#v", v.Input)
	}
}

func TestInputs1337SessionFilters(t *testing.T) {
	srv, calls := captureGraphQL(t, map[string]string{"TeamSessionsPageFiltered": `{"data":{"sessionsPage":{"total":0,"hasMore":false,"items":[]}}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--server", srv.URL, "--customer", "customer-7", "--type", "coding", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(calls["TeamSessionsPageFiltered"], &v); err != nil {
		t.Fatal(err)
	}
	if v["customerId"] != "customer-7" || v["sessionType"] != "coding" {
		t.Fatalf("filters = %#v", v)
	}
}

func TestInputs1337WorkerHistory(t *testing.T) {
	srv, calls := captureGraphQL(t, map[string]string{"TeamWorkItemsByWorker": `{"data":{"teamWorkItems":{"total":0,"items":[]}}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "work-items", "--server", srv.URL, "--app", "hrn:app:acme.com:eng-team", "--worker", "Retired Uma", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(calls["TeamWorkItemsByWorker"], &v); err != nil {
		t.Fatal(err)
	}
	if v["workerRef"] != "Retired Uma" {
		t.Fatalf("server must resolve even retired names: %#v", v)
	}
	if !strings.Contains(out.String(), `"total": 0`) || !strings.Contains(out.String(), `"items": []`) {
		t.Fatal(out.String())
	}
	if len(calls) != 1 {
		t.Fatalf("unexpected lookup/fanout: %#v", calls)
	}
}

func TestInputs1337EndCounters(t *testing.T) {
	teamGitDir(t)
	srv, calls := captureGraphQL(t, map[string]string{"EndTeamSessionWithStats": `{"errors":[{"message":"not yours","extensions":{"code":"FORBIDDEN"}}]}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end", "--session", "synthetic-session", "--server", srv.URL, "--input-tokens", "0", "--output-tokens", "3", "--turns", "4", "--errors", "2"})
	_ = root.Execute()
	var v map[string]any
	if err := json.Unmarshal(calls["EndTeamSessionWithStats"], &v); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]float64{"inputTokens": 0, "outputTokens": 3, "turnCount": 4, "errorCount": 2} {
		if v[k] != want {
			t.Errorf("%s = %v, want %v", k, v[k], want)
		}
	}
}

func TestInputs1337StartWire(t *testing.T) {
	_, _, calls := runStartAs(t, startedWith(preBindWorkerJSON), "--agent", "agt1", "--customer", "cust1", "--family", "codex", "--language", "go", "--parent-session", "parent1", "--plan", "investigate", "--pr-number", "0", "--previous-session", "prev1", "--type", "coding")
	xs := callsFor(calls, "StartTeamSession")
	if len(xs) != 1 {
		t.Fatal(xs)
	}
	var v struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(mustJSON1337(t, xs[0].Vars), &v); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{"agentRef": "agt1", "customerId": "cust1", "family": "codex", "language": "go", "parentSessionId": "parent1", "plan": "investigate", "prNumber": float64(0), "prevSessionId": "prev1", "type": "coding"} {
		if v.Input[k] != want {
			t.Errorf("%s = %v, want %v", k, v.Input[k], want)
		}
	}
}

func TestInputs1337HostCapabilityWire(t *testing.T) {
	cfg := writeHostConfig(t, "capabilities: [worker.reset-context]\n")
	srv, calls := hostServerFake(t, map[string]string{"RegisterHost": hostRegistrationAnswer, "CollectHostOperations": `{"data":{"collectHostOperations":[]}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var v struct {
		Input struct {
			Capabilities []string `json:"capabilities"`
		} `json:"input"`
	}
	if err := json.Unmarshal(calls()[0].vars, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Input.Capabilities) != 1 || v.Input.Capabilities[0] != "worker.reset-context" {
		t.Fatal(v)
	}
}

func TestInputs1337HostAvailabilityRefusal(t *testing.T) {
	cfg := writeHostConfig(t, "")
	srv, calls := hostServerFake(t, map[string]string{"SetHostWorkerAvailability": `{"errors":[{"message":"another setter owns this mark","extensions":{"code":"AVAILABILITY_NOT_PERMITTED"}}]}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "availability", "Iris", "--reason", "HOST_DOWN", "--config", cfg, "--server", srv.URL, "--json"})
	err := root.Execute()
	renderError(f, err)
	if exitCodeFor(err) != 8 {
		t.Fatalf("exit=%d: %v", exitCodeFor(err), err)
	}
	xs := calls()
	if len(xs) != 1 || xs[0].op != "SetHostWorkerAvailability" || xs[0].auth != "Bearer hk_host_key" || xs[0].session != "" {
		t.Fatalf("host authority: %#v", xs)
	}
	var v struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(xs[0].vars, &v); err != nil {
		t.Fatal(err)
	}
	if v.Input["worker"] != "Iris" || v.Input["reason"] != "HOST_DOWN" || v.Input["state"] != "UNAVAILABLE" {
		t.Fatal(v)
	}
	if !strings.Contains(out.String(), "AVAILABILITY_NOT_PERMITTED") {
		t.Fatal(out.String())
	}
}

func mustJSON1337(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInputs1337OmittedFieldsAndExplicitCounterZero(t *testing.T) {
	t.Run("start omission", func(t *testing.T) {
		_, _, calls := runStartAs(t, startedWith(preBindWorkerJSON))
		xs := callsFor(calls, "StartTeamSession")
		in := xs[0].Vars["input"].(map[string]any)
		for _, k := range []string{"agentRef", "customerId", "family", "language", "parentSessionId", "plan", "prNumber", "prevSessionId", "type"} {
			if _, ok := in[k]; ok {
				t.Errorf("unset %s was sent: %#v", k, in)
			}
		}
	})
	t.Run("end one explicit zero", func(t *testing.T) {
		teamGitDir(t)
		srv, calls := captureGraphQL(t, map[string]string{"EndTeamSessionWithStats": `{"errors":[{"message":"no","extensions":{"code":"FORBIDDEN"}}]}`})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "end", "--server", srv.URL, "--session", "fake", "--turns", "0", "--resume-claim"})
		err := root.Execute()
		if exitCodeFor(err) != 8 {
			t.Fatal(err)
		}
		var v map[string]any
		if err := json.Unmarshal(calls["EndTeamSessionWithStats"], &v); err != nil {
			t.Fatal(err)
		}
		if v["turnCount"] != float64(0) || v["resumeClaim"] != true {
			t.Fatal(v)
		}
		for _, k := range []string{"inputTokens", "outputTokens", "errorCount"} {
			if _, ok := v[k]; ok {
				t.Fatalf("unset counter %s sent: %#v", k, v)
			}
		}
	})
}

func TestInputs1337RefuseBeforeMutation(t *testing.T) {
	cases := [][]string{
		{"session", "start", "--as", "Iris", "--force", "--customer="},
		{"session", "start", "--as", "Iris", "--force", "--description", "dropped"},
		{"session", "start", "--customer", "cust"},
		{"session", "start", "--pr-number", "0"},
		{"session", "start", "--family="},
		{"session", "list", "--customer="},
		{"session", "list", "--type="},
		{"session", "list", "--pr", "acme/widgets#7", "--customer", "cust"},
		{"session", "work-items", "--worker="},
		{"session", "work-items", "--worker", "Iris", "--limit", "-1"},
		{"host", "availability", "Iris", "--clear", "--until", "2030-01-01T00:00:00Z"},
		{"host", "availability", "Iris", "--clear", "--reason="},
		{"host", "availability", "Iris", "--reason", "STALLED"},
		{"host", "availability", "Iris", "--clear=false"},
		{"host", "availability", "Iris", "--reason", "HOST_DOWN", "--until="},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			teamGitDir(t)
			srv, calls := captureGraphQL(t, map[string]string{})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"--server", srv.URL, "team"}, args...))
			err := root.Execute()
			if exitCodeFor(err) != 2 {
				t.Fatalf("want usage: %v", err)
			}
			if len(calls) != 0 {
				t.Fatalf("refused after request: %#v", calls)
			}
		})
	}
}

func TestInputs1337HostAvailabilityClear(t *testing.T) {
	cfg := writeHostConfig(t, "")
	answer := `{"data":{"setHostWorkerAvailability":{"worker":{"id":"w1","kind":"WORKER","name":"Iris","urn":null},"state":"AVAILABLE","reason":null,"until":null,"setAt":"2026-10-31T00:00:00Z","setBy":"HOST","setByHostId":"h1","setByParty":null,"inEffect":true}}}`
	srv, calls := hostServerFake(t, map[string]string{"SetHostWorkerAvailability": answer})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "availability", "Iris", "--config", cfg, "--clear", "--app", "hrn:app:other.com:ignored", "--server", srv.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	xs := calls()
	if len(xs) != 1 {
		t.Fatal(xs)
	}
	var v struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(xs[0].vars, &v); err != nil {
		t.Fatal(err)
	}
	if v.Input["state"] != "AVAILABLE" || v.Input["appRef"] != "hrn:app:acme.com:eng-team" {
		t.Fatal(v)
	}
	for _, k := range []string{"reason", "until"} {
		if _, ok := v.Input[k]; ok {
			t.Fatalf("clear sent %s: %#v", k, v)
		}
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if dto["setByHostId"] != "h1" || dto["state"] != "AVAILABLE" || dto["inEffect"] != true {
		t.Fatal(dto)
	}
	if strings.Contains(out.String(), "hk_host_key") {
		t.Fatal("key echoed")
	}
}

func TestInputs1337NoFilterFallback(t *testing.T) {
	for _, args := range [][]string{{"session", "list", "--customer", "cust"}, {"session", "work-items", "--worker", "Retired Uma", "--app", "hrn:app:acme.com:eng-team"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			teamGitDir(t)
			srv, calls := captureGraphQL(t, map[string]string{"TeamSessionsPageFiltered": `{"errors":[{"message":"unknown filter","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, "TeamWorkItemsByWorker": `{"errors":[{"message":"no access","extensions":{"code":"FORBIDDEN"}}]}`})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"--server", srv.URL, "team"}, args...))
			if err := root.Execute(); err == nil {
				t.Fatal("refusal lost")
			}
			if len(calls) != 1 {
				t.Fatalf("unfiltered retry or pre-read: %#v", calls)
			}
		})
	}
}

func TestInputs1337FilteredPaging(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			teamGitDir(t)
			src := &pagedSource{rows: sessionRows(45), serve: 20, wrapAt: sessionsPageAt}
			srv, rec := pagedGraphQL(t, map[string]*pagedSource{"TeamSessionsPageFiltered": src}, sessionRenderStubs(map[string]string{}))
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"team", "session", "list", "--customer", "cust-1", "--type", "DEVELOPER", "--server", srv.URL, "--json"}
			if active {
				args = append(args, "--active")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var rows []map[string]any
			if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
				t.Fatal(err)
			}
			want := 45
			if active {
				want = 30
			}
			if len(rows) != want {
				t.Fatalf("rows=%d want %d", len(rows), want)
			}
			calls := rec["TeamSessionsPageFiltered"].Calls
			if len(calls) != 3 {
				t.Fatal(calls)
			}
			for i, v := range calls {
				if v["customerId"] != "cust-1" || v["sessionType"] != "DEVELOPER" || v["offset"] != float64(i*20) {
					t.Fatalf("page %d: %#v", i, v)
				}
			}
		})
	}
}

func TestInputs1337FilteredExplicitPage(t *testing.T) {
	teamGitDir(t)
	srv, calls := captureGraphQL(t, map[string]string{"TeamSessionsPageFiltered": `{"data":{"sessionsPage":{"total":9,"hasMore":true,"items":[]}}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--type", "coding", "--limit", "0", "--offset", "3", "--server", srv.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(calls["TeamSessionsPageFiltered"], &v); err != nil {
		t.Fatal(err)
	}
	if v["limit"] != float64(0) || v["offset"] != float64(3) || strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("%#v %s", v, out.String())
	}
	if _, ok := v["customerId"]; ok {
		t.Fatal("omitted customer sent")
	}
}

func TestInputs1337HostMissingKeyNeverFallsBack(t *testing.T) {
	cfg := writeHostConfig(t, "")
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Delete the actual configured key, while testFactory supplies a user token.
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "key_file: ") {
			if err := os.Remove(strings.TrimPrefix(line, "key_file: ")); err != nil {
				t.Fatal(err)
			}
		}
	}
	srv, calls := hostServerFake(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "availability", "Iris", "--clear", "--config", cfg, "--server", srv.URL})
	if err := root.Execute(); exitCodeFor(err) != 3 {
		t.Fatalf("missing key must refuse: %v", err)
	}
	if len(calls()) != 0 {
		t.Fatal("ambient credential fallback")
	}
}

func TestInputs1337CountersDoNotRequireResumeClaim(t *testing.T) {
	doc, err := parser.ParseQuery(&ast.Source{Input: gen.EndTeamSessionWithStats_Operation})
	if err != nil {
		t.Fatal(err)
	}
	op := doc.Operations[0]
	if op.VariableDefinitions.ForName("resumeClaim") != nil {
		t.Fatal("stats-only document declares resumeClaim")
	}
	field := op.SelectionSet[0].(*ast.Field)
	if field.Arguments.ForName("resumeClaim") != nil {
		t.Fatal("stats-only document passes resumeClaim to an older server")
	}
}

func TestInputs1337CapabilityDeclarationWarnsWithoutAHandler(t *testing.T) {
	cfg := writeHostConfig(t, "capabilities: [worker.reset-context]\n")
	srv, calls := hostServerFake(t, map[string]string{"RegisterHost": hostRegistrationAnswer, "CollectHostOperations": `{"data":{"collectHostOperations":[]}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	warning := f.IOStreams.ErrOut.(*strings.Builder).String()
	if !strings.Contains(warning, "worker.reset-context") || !strings.Contains(warning, "no handler") || !strings.Contains(warning, "FAILED") {
		t.Fatalf("unsupported declaration was silent: %s", warning)
	}
	if len(calls()) != 2 {
		t.Fatalf("declaration must still be sent explicitly: %#v", calls())
	}
}
