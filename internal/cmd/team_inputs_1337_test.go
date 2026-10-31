package cmd

import (
	"encoding/json"
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
	if v["customerId"] != "customer-7" || v["type"] != "coding" {
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
