package cmd

import (
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"reflect"
	"regexp"
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
			expectedCells := map[string][]string{
				"Support Bot": {"installed", "—"},
				"Derived Bot": {"derived", "Support Bot (hrn:agent:acme.com:support-bot)"},
			}
			if withheld {
				expectedCells["Support Bot"] = []string{"?", "?"}
				expectedCells["Derived Bot"] = []string{"?", "?"}
			}
			separator := regexp.MustCompile(`\s{2,}`)
			for _, line := range strings.Split(human, "\n") {
				cells := separator.Split(strings.TrimSpace(line), -1)
				if len(cells) == 0 {
					continue
				}
				expected, ok := expectedCells[cells[0]]
				if !ok {
					continue
				}
				if len(cells) != 7 {
					t.Fatalf("roster columns = %d, want 7: %q", len(cells), line)
				}
				if !reflect.DeepEqual(cells[5:], expected) {
					t.Errorf("%s provenance cells = %q, want %q", cells[0], cells[5:], expected)
				}
				delete(expectedCells, cells[0])
			}
			if len(expectedCells) != 0 {
				t.Fatalf("missing roster rows: %v; output: %s", expectedCells, human)
			}

		})
	}
}

func TestAppAgentInstallationOperation1646(t *testing.T) {
	hasField := func(doc, field string) bool {
		for _, line := range strings.Split(doc, "\n") {
			if strings.TrimSuffix(strings.TrimSpace(line), " {") == field {
				return true
			}
		}
		return false
	}
	for _, field := range []string{"installMethod", "importer", "bootPromptRef", "bootPromptTargetDeleted"} {
		if !hasField(gen.AppAgentInstallationRoster_Operation, field) {
			t.Errorf("listing omits %s", field)
		}
	}
	for _, field := range []string{"installMethod", "importer"} {
		if hasField(gen.AppAgentRoster_Operation, field) {
			t.Errorf("role-warning operation gained %s", field)
		}
	}
}
