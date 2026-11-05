package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestAppAgentInstallMethod1646(t *testing.T) {
	for _, withheld := range []bool{false, true} {
		t.Run(map[bool]string{false: "served", true: "withheld"}[withheld], func(t *testing.T) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(appRosterResp), &payload); err != nil {
				t.Fatal(err)
			}
			app := payload["data"].(map[string]any)["app"].(map[string]any)
			agents := app["agents"].([]any)
			second := map[string]any{}
			for k, v := range agents[0].(map[string]any) {
				second[k] = v
			}
			second["id"] = "agt10"
			second["urn"] = "hrn:agent:acme.com:derived"
			second["name"] = "Derived Bot"
			app["agents"] = append(agents, second)
			importer := map[string]any{"id": "agt9", "urn": "hrn:agent:acme.com:support-bot", "name": "Support Bot"}
			rows := []any{}
			for i, id := range []string{"agt9", "agt10"} {
				row := map[string]any{"agent": map[string]any{"id": id}, "installMethod": []string{"ROW", "DERIVED"}[i], "importer": nil, "bootPromptRef": nil, "bootPromptTargetDeleted": nil}
				if i == 1 {
					row["importer"] = importer
				}
				if withheld {
					row["installMethod"] = nil
					row["importer"] = nil
				}
				rows = append(rows, row)
			}
			app["appAgents"] = rows
			wire, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			responses := map[string]string{"AppAgentRoster": string(wire), "AppAgentInstallationRoster": string(wire)}
			_, out, err := runTeamTask(t, responses, "app", "agent", "list", "hrn:app:acme.com:support", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var got []map[string]any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 {
				t.Fatalf("rows: %s", out)
			}
			for i, m := range got {
				expected := map[string]any{}
				for k, v := range app["agents"].([]any)[i].(map[string]any) {
					if k != "visibility" && k != "name" {
						expected[k] = v
					}
				}
				expected["agentName"] = app["agents"].([]any)[i].(map[string]any)["name"]
				expected["bootPromptRef"] = nil
				expected["bootPromptTargetDeleted"] = nil
				expected["installMethod"] = rows[i].(map[string]any)["installMethod"]
				expected["importer"] = rows[i].(map[string]any)["importer"]
				if !reflect.DeepEqual(m, expected) {
					t.Errorf("row %d exact JSON = %#v; want %#v", i, m, expected)
				}
			}
			_, human, err := runTeamTask(t, responses, "app", "agent", "list", "hrn:app:acme.com:support")
			if err != nil {
				t.Fatal(err)
			}
			if withheld {
				if strings.Contains(human, "derived") || strings.Contains(human, "installed") {
					t.Fatalf("withheld marks guessed: %s", human)
				}
			} else if !strings.Contains(human, "installed") || !strings.Contains(human, "derived") || strings.Count(human, "Support Bot") != 2 {
				t.Fatalf("missing method/importer: %s", human)
			}
		})
	}
}
