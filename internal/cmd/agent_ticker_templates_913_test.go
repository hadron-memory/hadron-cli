package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const template913 = `{"id":"tpl1","revision":3,"shapeVersion":1,"name":"Heartbeat","tickCode":"heartbeat","cadenceSeconds":60,"quietHours":null,"relevance":"ALL","reasons":["EVERY_TICK"],"createdAt":"2026-10-31T00:00:00Z","updatedAt":null,"agent":{"id":"a1","name":"Worker Agent"}}`

func templateResponses913() map[string]string {
	return map[string]string{
		"AgentTickerTemplates":      `{"data":{"agentTickerTemplates":[` + template913 + `]}}`,
		"CreateAgentTickerTemplate": `{"data":{"createAgentTickerTemplate":` + template913 + `}}`,
		"UpdateAgentTickerTemplate": `{"data":{"updateAgentTickerTemplate":` + template913 + `}}`,
		"DeleteAgentTickerTemplate": `{"data":{"deleteAgentTickerTemplate":true}}`,
	}
}
func TestTemplate913ListJSON(t *testing.T) {
	calls, out, err := runTeamTask(t, templateResponses913(), "agent", "ticker-templates", "list", "legacy-agent-ref", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err = json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("%v %s", err, out)
	}
	if len(rows[0]) != 12 || rows[0]["tickCode"] != "heartbeat" || rows[0]["revision"] != float64(3) {
		t.Fatal(rows)
	}
	got := stopCalls(calls, "AgentTickerTemplates")
	if len(got) != 1 || got[0].Vars["agentRef"] != "legacy-agent-ref" {
		t.Fatal(got)
	}
	r := templateResponses913()
	r["AgentTickerTemplates"] = `{"data":{"agentTickerTemplates":[]}}`
	_, out, err = runTeamTask(t, r, "agent", "ticker-template", "list", "a1", "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatal(err, out)
	}
}
func TestTemplate913CreateAndUpdatePresence(t *testing.T) {
	for _, tc := range []struct {
		args []string
		op   string
		want map[string]any
	}{
		{[]string{"create", "a1", "--name", "Heartbeat", "--cadence-seconds", "60"}, "CreateAgentTickerTemplate", map[string]any{"name": "Heartbeat", "cadenceSeconds": float64(60)}},
		{[]string{"update", "tpl1", "--expected-revision", "3", "--name", "New"}, "UpdateAgentTickerTemplate", map[string]any{"name": "New"}},
		{[]string{"update", "tpl1", "--expected-revision", "3", "--clear-quiet-hours"}, "UpdateAgentTickerTemplate", map[string]any{"clearQuietHours": true}},
	} {
		argv := append([]string{"agent", "ticker-templates"}, tc.args...)
		argv = append(argv, "--json")
		calls, _, err := runTeamTask(t, templateResponses913(), argv...)
		if err != nil {
			t.Fatal(err)
		}
		got := stopCalls(calls, tc.op)
		if len(got) != 1 {
			t.Fatal(got)
		}
		in := got[0].Vars["input"].(map[string]any)
		want, _ := json.Marshal(tc.want)
		wire, _ := json.Marshal(in)
		if string(want) != string(wire) {
			t.Fatalf("want%s got%s", want, wire)
		}
		if got[0].Session != "" {
			t.Fatal("template write carried bound session", got[0])
		}
	}
}
func TestTemplate913UsageAndDelete(t *testing.T) {
	for _, args := range [][]string{
		{"update", "tpl1", "--expected-revision", "3"},
		{"update", "tpl1", "--name", "N"},
		{"update", "tpl1", "--expected-revision", "3", "--quiet-hours", "{}", "--clear-quiet-hours"},
		{"create", "a1", "--name", "N", "--cadence-seconds", "60", "--reasons="},
		{"delete", "tpl1", "--expected-revision", "3"},
	} {
		argv := append([]string{"agent", "ticker-templates"}, args...)
		calls, _, err := runTeamTask(t, templateResponses913(), argv...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Fatalf("args%v err%v calls%v", args, err, *calls)
		}
	}
	calls, out, err := runTeamTask(t, templateResponses913(), "agent", "ticker-templates", "delete", "tpl1", "--expected-revision", "3", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := stopCalls(calls, "DeleteAgentTickerTemplate")
	if len(got) != 1 || got[0].Vars["id"] != "tpl1" || got[0].Vars["expectedRevision"] != float64(3) || got[0].Session != "" {
		t.Fatal(got)
	}
	if !strings.Contains(out, `"deleted": true`) {
		t.Fatal(out)
	}
}
func TestTemplate913ConflictNoRetry(t *testing.T) {
	for _, code := range []string{"TICKER_TEMPLATE_REVISION_MISMATCH", "TICKER_TEMPLATE_LIMIT_REACHED"} {
		r := templateResponses913()
		r["UpdateAgentTickerTemplate"] = `{"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}]}`
		calls, _, err := runTeamTask(t, r, "agent", "ticker-templates", "update", "tpl1", "--expected-revision", "2", "--name", "N")
		if exitCodeFor(err) != 5 || len(stopCalls(calls, "UpdateAgentTickerTemplate")) != 1 {
			t.Fatal(code, err, *calls)
		}
	}
}
