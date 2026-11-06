package cmd

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestAppAgentInstallMethod1646(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, withheld := range []bool{false, true} {
			t.Run(fmt.Sprintf("legacy%t/%s", legacy, map[bool]string{false: "served", true: "withheld"}[withheld]), func(t *testing.T) {
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
				steps := provenanceSteps1682(string(wire), legacy)
				out, _, _, err := runProvenance1682(t, steps, true)
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
				human, _, _, err := runProvenance1682(t, steps, false)
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
				if err := checkProvenanceTable1682(human, expectedCells, legacy); err != nil {
					t.Fatal(err)
				}

			})
		}
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
		if !hasField(gen.AppAgentInstallationRoster_Operation, field) || !hasField(gen.AppAgentEffectiveRoster_Operation, field) {
			t.Errorf("listing omits %s", field)
		}
	}
	for _, field := range []string{"installMethod", "importer"} {
		if hasField(gen.AppAgentRoster_Operation, field) {
			t.Errorf("role-warning operation gained %s", field)
		}
	}
}

// checkProvenanceTable1682 checks the whole table, including unknown/duplicate
// rows, and binds provenance to a checked header rather than unchecked offsets.
func checkProvenanceTable1682(human string, want map[string][]string, legacy bool) error {
	separator := regexp.MustCompile(`\s{2,}`)
	lines := strings.Split(strings.TrimSpace(human), "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "AGENT  ") {
			start = i
			break
		}
	}
	if start < 0 {
		return fmt.Errorf("missing roster header")
	}
	header := []string{"AGENT", "ROLE", "AGENT URN", "ID", "BOOT PROMPT (NEXT BIND)", "INSTALL", "IMPORTER"}
	if legacy {
		header[4] = "BOOT PROMPT"
	}
	if !reflect.DeepEqual(separator.Split(strings.TrimSpace(lines[start]), -1), header) {
		return fmt.Errorf("wrong roster header: %s", lines[start])
	}
	data := lines[start+1:]
	if len(data) != len(want) {
		return fmt.Errorf("roster cardinality %d want %d", len(data), len(want))
	}
	seen := map[string]bool{}
	for _, line := range data {
		cells := separator.Split(strings.TrimSpace(line), -1)
		if len(cells) != len(header) {
			return fmt.Errorf("roster columns: %q", line)
		}
		expected, ok := want[cells[0]]
		if !ok || seen[cells[0]] {
			return fmt.Errorf("unexpected/duplicate row: %s", cells[0])
		}
		seen[cells[0]] = true
		if !reflect.DeepEqual(cells[len(header)-2:], expected) {
			return fmt.Errorf("provenance cells: %q", line)
		}
	}
	for name := range want {
		if !seen[name] {
			return fmt.Errorf("missing roster row: %s", name)
		}
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
	if err := checkProvenanceTable1682(valid, want, true); err != nil {
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
			if checkProvenanceTable1682(tc.table, want, true) == nil {
				t.Fatal("malformed table accepted")
			}
		})
	}
}
