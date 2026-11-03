package cmd

// hadron-cli#1517 — `team task hand-back` (server#2651, cor:agt:050:29).
// Red first: the verb, the handBack DTO key and its sanitised human line.

import (
	"encoding/json"
	"strings"
	"testing"
)

func handBackResponses1517(hb string) map[string]string {
	r := taskResponses()
	body := strings.Replace(startedTaskFieldsJSON, `"tags":[],`, `"tags":[],"handBack":`+hb+`,`, 1)
	r["AgentTaskHandBack"] = `{"data":{"handBackAgentTask":` + body + `}}`
	r["AgentTaskGet"] = `{"data":{"agentTask":` + body + `}}`
	return r
}

func TestTaskHandBackSendsCodeAndText1517(t *testing.T) {
	hb := `{"code":"MISSING_INPUT","text":"no fixture","handedBackAt":"2026-11-01T12:00:00Z"}`
	calls, out, err := runTeamTask(t, handBackResponses1517(hb), "team", "task", "hand-back", "AT0.7", "--code", "missing-input", "--text", "no fixture")
	if err != nil {
		t.Fatal(err)
	}
	c := sentOp1520(t, calls, "AgentTaskHandBack")
	if c.Vars["taskRef"] != "AT0.7" {
		t.Fatalf("taskRef = %v", c.Vars["taskRef"])
	}
	b, _ := json.Marshal(c.Vars["reason"])
	if string(b) != `{"code":"MISSING_INPUT","text":"no fixture"}` {
		t.Fatalf("reason = %s", b)
	}
	if c.Session != "s-new" {
		t.Fatalf("not attributed to the bound session: %q", c.Session)
	}
	if !strings.Contains(out, "Handed back: MISSING_INPUT at 2026-11-01T12:00:00Z: no fixture") {
		t.Fatalf("receipt: %q", out)
	}
}

func TestTaskHandBackCodeSpellings1517(t *testing.T) {
	hb := `{"code":"CANNOT_DO","text":null,"handedBackAt":"2026-11-01T12:00:00Z"}`
	for in, want := range map[string]string{"cannot-do": "CANNOT_DO", "CANNOT_DO": "CANNOT_DO", "Cannot-Do": "CANNOT_DO", "refused": "REFUSED", "out-of-scope": "OUT_OF_SCOPE", "OUT_OF_SCOPE": "OUT_OF_SCOPE", "duplicate": "DUPLICATE", "missing_input": "MISSING_INPUT"} {
		calls, _, err := runTeamTask(t, handBackResponses1517(hb), "team", "task", "hand-back", "AT0.7", "--code", in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		c := sentOp1520(t, calls, "AgentTaskHandBack")
		b, _ := json.Marshal(c.Vars["reason"])
		if string(b) != `{"code":"`+want+`"}` { // no text flag: the key is omitted, never null
			t.Fatalf("%s: reason = %s", in, b)
		}
	}
}

func TestTaskHandBackRefusedBeforeAnyCall1517(t *testing.T) {
	for _, argv := range [][]string{
		{"team", "task", "hand-back", "AT0.7"},
		{"team", "task", "hand-back", "AT0.7", "--code", ""},
		{"team", "task", "hand-back", "AT0.7", "--code", "nope"},
		{"team", "task", "hand-back"},
	} {
		calls, _, err := runTeamTask(t, handBackResponses1517(`null`), argv...)
		if err == nil || exitCodeFor(err) != 2 {
			t.Fatalf("%v: want usage exit, got %v", argv, err)
		}
		for _, c := range *calls {
			if c.Op == "AgentTaskHandBack" {
				t.Fatalf("%v: a request was sent", argv)
			}
		}
		if len(argv) > 4 && !strings.Contains(err.Error(), "missing-input") {
			t.Fatalf("%v: the refusal must list the set: %v", argv, err)
		}
	}
}

func TestTaskHandBackRefusalMatrix1517(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{"TASK_NOT_FOUND", 4}, {"TASK_NOT_ASSIGNEE", 8}, {"TASK_ENDED", 5}, {"TASK_INVALID_TRANSITION", 5},
		{"BAD_USER_INPUT", 2}, {"SESSION_NOT_BOUND", 2}, {"APP_UNINSTALLED", 5},
	}
	for _, tc := range cases {
		for _, asJSON := range []bool{false, true} {
			argv := []string{"team", "task", "hand-back", "AT0.7", "--code", "refused"}
			if asJSON {
				argv = append(argv, "--json")
			}
			_, out, err := runTeamTask(t, map[string]string{
				"AgentTaskHandBack": `{"errors":[{"message":"Refused by server.","extensions":{"code":"` + tc.code + `"}}]}`,
			}, argv...)
			if err == nil || exitCodeFor(err) != tc.want {
				t.Fatalf("%s: exit = %v, want %d", tc.code, err, tc.want)
			}
			if !strings.Contains(err.Error(), "Refused by server") {
				t.Fatalf("%s: message lost: %v", tc.code, err)
			}
			if strings.Contains(out, "Handed back") || strings.Contains(out, `"handBack"`) {
				t.Fatalf("%s: partial task on stdout: %q", tc.code, out)
			}
		}
	}
}

func TestTaskGetHandBackJSONAndNull1517(t *testing.T) {
	hb := `{"code":"DUPLICATE","text":"dup\n\u001b[31mred","handedBackAt":"2026-11-01T12:00:00Z"}`
	_, out, err := runTeamTask(t, handBackResponses1517(hb), "team", "task", "get", "AT0.7", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		HandBack *struct{ Code, Text, HandedBackAt string } `json:"handBack"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.HandBack == nil || dto.HandBack.Code != "DUPLICATE" || dto.HandBack.Text != "dup\n\x1b[31mred" || dto.HandBack.HandedBackAt != "2026-11-01T12:00:00Z" {
		t.Fatalf("handBack = %+v", dto.HandBack)
	}
	_, out, err = runTeamTask(t, taskResponses(), "team", "task", "get", "AT0.7", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"handBack": null`) {
		t.Fatalf("never handed back must render null: %s", out)
	}
}

func TestTaskGetHandBackHumanLine1517(t *testing.T) {
	hb := `{"code":"DUPLICATE","text":"dup\n\u001b[31mred\u0007","handedBackAt":"2026-11-01T12:00:00Z"}`
	_, out, err := runTeamTask(t, handBackResponses1517(hb), "team", "task", "get", "AT0.7")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Fatalf("control characters reached the terminal: %q", out)
	}
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Handed back:") {
			line = l
		}
	}
	if line == "" || !strings.Contains(line, "DUPLICATE at 2026-11-01T12:00:00Z: dup") || !strings.Contains(line, "red") {
		t.Fatalf("human line: %q in %q", line, out)
	}
	for _, text := range []string{`null`, `""`} {
		hb = `{"code":"REFUSED","text":` + text + `,"handedBackAt":"2026-11-01T12:00:00Z"}`
		_, out, err = runTeamTask(t, handBackResponses1517(hb), "team", "task", "get", "AT0.7")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "Handed back: REFUSED at 2026-11-01T12:00:00Z\n") {
			t.Fatalf("text %s: dangling colon or missing line: %q", text, out)
		}
	}
	_, out, _ = runTeamTask(t, taskResponses(), "team", "task", "get", "AT0.7")
	if strings.Contains(out, "Handed back") {
		t.Fatalf("never handed back printed a line: %q", out)
	}
}
