package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestInputs1346(t *testing.T) {
	for _, tt := range []struct {
		name, op, response string
		args               []string
		want               map[string]string
	}{
		{"schedule one shot", "CreateAgentSchedule", `{"data":{"createAgentSchedule":` + scheduleJSON + `}}`, []string{"schedule", "create", "--app", "capp100000000000000000000", "--name", "once", "--entry", "hrn:node:acme.com:ops:task", "--run-at", "2027-01-01T00:00:00Z", "--scope", "work"}, map[string]string{"runAt": `"2027-01-01T00:00:00Z"`, "scopeRef": `"work"`}},
		{"schedule update", "UpdateAgentSchedule", `{"data":{"updateAgentSchedule":` + scheduleJSON + `}}`, []string{"schedule", "update", "sch1", "--run-at", "2027-01-01T00:00:00Z", "--scope", "", "--agent", "hrn:agent:acme.com:bot"}, map[string]string{"runAt": `"2027-01-01T00:00:00Z"`, "scopeRef": `""`, "agentRef": `"hrn:agent:acme.com:bot"`}},
		{"webhook scope", "CreateAgentWebhook", `{"data":{"createAgentWebhook":` + webhookCredsJSON + `}}`, []string{"webhook", "create", "--app", "capp100000000000000000000", "--name", "hook", "--entry", "hrn:node:acme.com:ops:task", "--scope", "work"}, map[string]string{"scopeRef": `"work"`}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g, c := captureGraphQL(t, map[string]string{tt.op: tt.response})
			f, _ := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs(append(tt.args, "--server", g.URL))
			if err := r.Execute(); err != nil {
				t.Fatal(err)
			}
			var v struct {
				Input map[string]json.RawMessage `json:"input"`
			}
			if err := json.Unmarshal(c[tt.op], &v); err != nil {
				t.Fatal(err)
			}
			for k, w := range tt.want {
				if string(v.Input[k]) != w {
					t.Errorf("%s=%s want%s", k, v.Input[k], w)
				}
			}
			if tt.name == "schedule one shot" {
				if _, ok := v.Input["cron"]; ok {
					t.Fatal("one-shot sent cron")
				}
			}
		})
	}
}
func TestRunFilters1346(t *testing.T) {
	g, c := captureGraphQL(t, map[string]string{"AppRuns": `{"data":{"appRuns":{"total":0,"items":[]}}}`})
	f, _ := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"run", "list", "--org", "acme.com", "--statuses", "FAILED,CANCELLED", "--parent", "parent1", "--sort", "startedAt", "--dir", "asc", "--server", g.URL})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	var v map[string]json.RawMessage
	if err := json.Unmarshal(c["AppRuns"], &v); err != nil {
		t.Fatal(err)
	}
	for k, w := range map[string]string{"statuses": `["FAILED","CANCELLED"]`, "parentRunId": `"parent1"`, "sortBy": `"startedAt"`, "sortDir": `"asc"`} {
		if string(v[k]) != w {
			t.Errorf("%s=%s want%s", k, v[k], w)
		}
	}
}
func TestRawAutomation1346(t *testing.T) {
	for _, tt := range []struct {
		cmd, op, body string
		extra         []string
	}{{"preview", "PreviewAutomation", `{"data":{"previewAutomation":{"hash":"digest-a","nodes":{}}}}`, nil}, {"re-adopt", "ReAdoptAutomation", `{"data":{"reAdoptAutomation":true}}`, []string{"--expected-hash", "digest-a", "--yes"}}} {
		t.Run(tt.cmd, func(t *testing.T) {
			g, c := captureGraphQL(t, map[string]string{tt.op: tt.body})
			f, out := testFactory(t)
			r := NewRootCmd(f)
			a := []string{"automation", tt.cmd, "MANUAL", "run1", "--json", "--server", g.URL}
			r.SetArgs(append(a, tt.extra...))
			if err := r.Execute(); err != nil {
				t.Fatal(err)
			}
			if !json.Valid([]byte(out.String())) {
				t.Fatal(out.String())
			}
			var v map[string]json.RawMessage
			if err := json.Unmarshal(c[tt.op], &v); err != nil {
				t.Fatal(err)
			}
			if string(v["triggerKind"]) != `"MANUAL"` || string(v["triggerRef"]) != `"run1"` {
				t.Fatal(string(c[tt.op]))
			}
			if tt.cmd == "re-adopt" && string(v["expectedHash"]) != `"digest-a"` {
				t.Fatal(string(c[tt.op]))
			}
		})
	}
}
func TestRefusals1346(t *testing.T) {
	for _, a := range [][]string{{"schedule", "create", "--cron", "* * * * *", "--run-at", "2027-01-01T00:00:00Z", "--name", "n", "--entry", "hrn:node:acme.com:ops:task"}, {"schedule", "update", "sch1", "--cron", "* * * * *", "--run-at", "2027-01-01T00:00:00Z"}, {"automation", "re-adopt", "MANUAL", "run1", "--expected-hash", "digest-a"}} {
		g, c := captureGraphQL(t, nil)
		f, _ := testFactory(t)
		r := NewRootCmd(f)
		r.SetArgs(append(a, "--server", g.URL))
		wantExit(t, r.Execute(), exitcode.Usage)
		if len(c) != 0 {
			t.Fatal(c)
		}
	}
}

func TestApprovalPageSize1346(t *testing.T) {
	s := newAutoServer(t, map[string]func(map[string]any) autoReply{"AutomationApprovals": fixed(autoPage([]string{autoRow("SCHEDULE", "sch1", "nightly", "ADOPTED", "h")}, "", true)), "AutomationApproval": fixed(autoDetail("ADOPTED"))})
	r := runAuto(t, s, false, "", "approval", "nightly", "--limit", "7")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if s.lastVars("AutomationApprovals")["limit"] != float64(7) {
		t.Fatal(s.lastVars("AutomationApprovals"))
	}
}

func TestAdoptionChanged1346(t *testing.T) {
	g, c := captureGraphQL(t, map[string]string{"ReAdoptAutomation": `{"errors":[{"message":"graph changed","extensions":{"code":"AUTOMATION_CHANGED"}}]}`})
	f, _ := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"automation", "re-adopt", "SCHEDULE", "sch1", "--expected-hash", "reviewed-digest", "--yes", "--server", g.URL})
	wantExit(t, r.Execute(), exitcode.Conflict)
	var v map[string]json.RawMessage
	if err := json.Unmarshal(c["ReAdoptAutomation"], &v); err != nil {
		t.Fatal(err)
	}
	if string(v["expectedHash"]) != `"reviewed-digest"` {
		t.Fatal(v)
	}
	if len(c) != 1 {
		t.Fatal("refusal fetched a new preview", c)
	}
}
func TestUpdatePreserves1346(t *testing.T) {
	g, c := captureGraphQL(t, map[string]string{"UpdateAgentSchedule": `{"data":{"updateAgentSchedule":` + scheduleJSON + `}}`})
	f, _ := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"schedule", "update", "sch1", "--name", "renamed", "--server", g.URL})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	var v struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(c["UpdateAgentSchedule"], &v); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"agentRef", "runAt", "scopeRef", "cron"} {
		if _, ok := v.Input[k]; ok {
			t.Errorf("unrelated update sent %s: %s", k, c["UpdateAgentSchedule"])
		}
	}
}

func TestEmptyInputs1346(t *testing.T) {
	for _, a := range [][]string{
		{"schedule", "create", "--app", "capp100000000000000000000", "--name", "n", "--entry", "hrn:node:acme.com:ops:task", "--run-at", ""},
		{"schedule", "create", "--app", "capp100000000000000000000", "--name", "n", "--entry", "hrn:node:acme.com:ops:task", "--cron", "* * * * *", "--scope", ""},
		{"schedule", "update", "sch1", "--agent", ""},
		{"schedule", "update", "sch1", "--run-at", ""},
		{"run", "list", "--org", "acme.com", "--statuses", ""},
		{"run", "list", "--org", "acme.com", "--status", "FAILED", "--statuses", "CANCELLED"},
		{"webhook", "create", "--app", "capp100000000000000000000", "--name", "n", "--entry", "hrn:node:acme.com:ops:task", "--scope", ""},
		{"run", "list", "--org", "acme.com", "--parent", ""},
		{"run", "list", "--org", "acme.com", "--sort", ""},
		{"run", "list", "--org", "acme.com", "--dir", ""},
		{"automation", "re-adopt", "SCHEDULE", "sch1", "--expected-hash", "", "--yes"},
		{"automation", "approval", "nightly", "--limit", "0"},
	} {
		g, c := captureGraphQL(t, nil)
		f, _ := testFactory(t)
		r := NewRootCmd(f)
		r.SetArgs(append(a, "--server", g.URL))
		wantExit(t, r.Execute(), exitcode.Usage)
		if len(c) != 0 {
			t.Fatal(a, c)
		}
	}
}

func TestPreviewReview1346(t *testing.T) {
	graph := `{"nodes":{"n":{"content":` + jq(strings.Repeat("x", 9000)) + `}},"hash":"reviewed-digest"}`
	s := newAutoServer(t, map[string]func(map[string]any) autoReply{"PreviewAutomation": fixed(`{"data":{"previewAutomation":` + graph + `}}`)})
	r := runAuto(t, s, false, "", "preview", "SCHEDULE", "sch1")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !strings.Contains(r.out, "Hash: reviewed-digest") || !strings.Contains(r.out, "DISPLAY INCOMPLETE") || !strings.Contains(r.out, "--json") {
		t.Fatal(r.out)
	}
	r = runAuto(t, s, false, "", "preview", "SCHEDULE", "sch1", "--json")
	var d struct {
		Hash  string          `json:"hash"`
		Graph json.RawMessage `json:"graph"`
	}
	if err := json.Unmarshal([]byte(r.out), &d); err != nil {
		t.Fatal(err)
	}
	if r.err != nil || d.Hash != "reviewed-digest" || !strings.Contains(string(d.Graph), strings.Repeat("x", 9000)) {
		t.Fatal(r.err, r.out)
	}
}
func TestRawFailurePaths1346(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		args       []string
		want       int
	}{
		{"false", `{"data":{"reAdoptAutomation":false}}`, []string{"re-adopt", "MANUAL", "run1", "--expected-hash", "h", "--yes"}, exitcode.Error},
		{"null", `{"data":{"previewAutomation":null}}`, []string{"preview", "MANUAL", "run1"}, exitcode.Unavailable},
		{"missing digest", `{"data":{"previewAutomation":{"nodes":{}}}}`, []string{"preview", "MANUAL", "run1"}, exitcode.Unavailable},
		{"nonobject", `{"data":{"previewAutomation":[]}}`, []string{"preview", "MANUAL", "run1"}, exitcode.Unavailable},
		{"bad kind", `{}`, []string{"preview", "BOGUS", "run1"}, exitcode.Usage},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newAutoServer(t, map[string]func(map[string]any) autoReply{"PreviewAutomation": fixed(tt.body), "ReAdoptAutomation": fixed(tt.body)})
			r := runAuto(t, s, false, "", tt.args...)
			if r.code != tt.want {
				t.Fatal(r.code, r.err, r.out)
			}
			if strings.Contains(r.out, "Re-adopted") {
				t.Fatal(r.out)
			}
			if tt.name == "bad kind" && s.count("PreviewAutomation") != 0 {
				t.Fatal("invalid kind sent")
			}
		})
	}
}
func TestRunPages1346(t *testing.T) {
	s := newAutoServer(t, map[string]func(map[string]any) autoReply{"AppRuns": func(v map[string]any) autoReply {
		for k, w := range map[string]string{"statuses": `["FAILED","CANCELLED"]`, "parentRunId": `"parent1"`, "sortBy": `"startedAt"`, "sortDir": `"asc"`} {
			b, err := json.Marshal(v[k])
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != w {
				t.Errorf("%s=%s", k, b)
			}
		}
		row := appRunJSON
		if v["offset"] == float64(1) {
			row = strings.Replace(row, `"run1"`, `"run2"`, 1)
		} else if v["offset"] != float64(0) {
			t.Errorf("offset=%v", v["offset"])
		}
		return autoReply{body: `{"data":{"appRuns":{"total":2,"items":[` + row + `]}}}`}
	}})
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"run", "list", "--org", "acme.com", "--statuses", "FAILED,CANCELLED", "--parent", "parent1", "--sort", "startedAt", "--dir", "asc", "--json", "--server", s.URL})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || s.count("AppRuns") != 2 {
		t.Fatal(out.String(), s.count("AppRuns"))
	}
}
func TestApprovalPages1346(t *testing.T) {
	s := newAutoServer(t, map[string]func(map[string]any) autoReply{"AutomationApprovals": func(v map[string]any) autoReply {
		if v["limit"] != float64(1) {
			t.Errorf("limit=%v", v["limit"])
		}
		if v["after"] == nil {
			return autoReply{body: autoPage([]string{autoRow("SCHEDULE", "a", "alpha", "ADOPTED", "h")}, "next", true)}
		}
		if v["after"] != "next" {
			t.Errorf("after=%v", v["after"])
		}
		return autoReply{body: autoPage([]string{autoRow("SCHEDULE", "b", "nightly", "ADOPTED", "h")}, "", true)}
	}, "AutomationApproval": fixed(autoDetail("ADOPTED"))})
	r := runAuto(t, s, false, "", "approval", "nightly", "--limit", "1")
	if r.err != nil || s.count("AutomationApprovals") != 2 || s.lastVars("AutomationApproval")["triggerRef"] != "b" {
		t.Fatal(r.err, s.count("AutomationApprovals"))
	}
}
