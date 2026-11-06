package cmd

import (
	"encoding/json"
	"fmt"
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
			responses := map[string]string{"AppAgentRoster": string(wire), "AppAgentInstallationRoster": string(wire), "AppAgentEffectiveRoster": string(wire)}
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
				for _, key := range []string{"pointerLevel", "compiledNodeId", "servedRevSeq", "bootPromptRefusal"} {
					expected[key] = nil
				}
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
			if err := checkProvenanceTable1682(human, expectedCells); err != nil {
				t.Fatal(err)
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

// Initially extracted without strengthening the existing row matcher. The
// negative controls below expose the duplicate/header holes before the fix.
func checkProvenanceTable1682(human string, want map[string][]string) error {
	remaining := map[string][]string{}
	for name, cells := range want {
		remaining[name] = cells
	}
	separator := regexp.MustCompile(`\s{2,}`)
	for _, line := range strings.Split(human, "\n") {
		cells := separator.Split(strings.TrimSpace(line), -1)
		expected, ok := remaining[cells[0]]
		if !ok {
			continue
		}
		if len(cells) != 7 || !reflect.DeepEqual(cells[5:], expected) {
			return fmt.Errorf("provenance cells: %q", line)
		}
		delete(remaining, cells[0])
	}
	if len(remaining) != 0 {
		return fmt.Errorf("missing roster rows: %v", remaining)
	}
	return nil
}

func TestAppAgentProvenanceTableControls1682(t *testing.T) {
	const header = "AGENT  ROLE  AGENT URN/ID  BOOT PROMPT  INSTALL  IMPORTER"
	// Seven columns use AGENT URN and ID as separate header cells in the real table.
	const realHeader = "AGENT  ROLE  AGENT URN  ID  BOOT PROMPT  INSTALL  IMPORTER"
	const row = "Support Bot  support  hrn:agent:acme.com:support-bot  agt9  —  installed  —"
	want := map[string][]string{"Support Bot": {"installed", "—"}}
	valid := realHeader + "\n" + row + "\n"
	if err := checkProvenanceTable1682(valid, want); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, table string }{
		{"duplicate", valid + row + "\n"},
		{"extra", valid + strings.Replace(row, "Support Bot", "Unexpected Bot", 1) + "\n"},
		{"missing", realHeader + "\n"},
		{"header", strings.Replace(valid, "INSTALL  IMPORTER", "IMPORTER  INSTALL", 1)},
		{"wrong columns", header + "\n" + row + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if checkProvenanceTable1682(tc.table, want) == nil {
				t.Fatal("malformed table accepted")
			}
		})
	}
}
