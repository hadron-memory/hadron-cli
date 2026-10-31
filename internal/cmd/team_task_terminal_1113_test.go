package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode"
)

// Exercise the real command/DTO/output boundary, changing one populated server
// field per probe so an unguarded field cannot hide behind another's failure.
func TestTaskTerminalHTTP1113(t *testing.T) {
	var base map[string]any
	if err := json.Unmarshal([]byte(finishedTaskFieldsJSON), &base); err != nil {
		t.Fatal(err)
	}
	attack := "attack\x1b[31m\x1b]8;;https://evil\x07\u009b\u202e\u200b\u2028\t\r\r\nend"
	fields := []string{"id", "handle", "title", "status", "appId", "description", "parentTaskId", "startBy", "source", "createdAt", "updatedAt", "startedAt", "finishedAt", "cancelledAt", "tags", "owner", "createdBy", "assignee", "assignedBy", "requirements", "result"}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			raw, _ := json.Marshal(base)
			var row map[string]any
			if err := json.Unmarshal(raw, &row); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "tags":
				row[field] = []string{attack}
			case "owner", "createdBy", "assignee", "assignedBy":
				row[field] = map[string]any{"id": attack, "name": attack, "kind": "WORKER"}
			case "requirements":
				row[field] = map[string]any{"role": attack, "familyIn": []string{attack}, "familyNotIn": []string{attack}, "excludeWorkerIds": []string{attack}}
			case "result":
				row[field] = map[string]any{"outcome": attack, "summary": attack, "evidence": attack}
			default:
				row[field] = attack
			}
			encoded, _ := json.Marshal(row)
			r := taskResponses()
			r["AgentTaskGet"] = `{"data":{"agentTask":` + string(encoded) + `}}`
			r["AgentTaskTree"] = `{"data":{"agentTaskTree":[` + string(encoded) + `]}}`
			r["AgentTeamEvents"] = `{"data":{"agentTeamEvents":[]}}`
			for _, verb := range []string{"get", "tree"} {
				// A malformed ID cannot link itself to the requested root in tree; tree's
				// row primitive is separately covered with hostile IDs at depths 0..3.
				if verb == "tree" && (field == "id" || field == "parentTaskId") {
					continue
				}
				_, out, err := runTeamTask(t, r, "team", "task", verb, "task1")
				if err != nil {
					t.Fatal(err)
				}
				for _, v := range out {
					if (unicode.IsControl(v) && v != '\n') || unicode.In(v, unicode.Cf, unicode.Zl, unicode.Zp) {
						t.Fatalf("%s unsafe %U: %q", verb, v, out)
					}
				}
				_, out, err = runTeamTask(t, r, "team", "task", verb, "task1", "--json")
				if err != nil {
					t.Fatal(err)
				}
				var got any
				if err = json.Unmarshal([]byte(out), &got); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out, `\u001b[31m`) {
					t.Fatalf("JSON lost original for %s: %s", verb, out)
				}
			}
		})
	}
}
