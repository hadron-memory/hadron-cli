package cmd

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// These snapshots are captured from main 5face16e before the rendering change.
// JSON hashes pin the exact bytes, including indentation, escaping and nulls;
// clean text hashes pin spacing as well as values. Host/router local-only rows
// and numeric-only settings tables are inventoried separately in the PR.
func TestTeamSafeCells1101(t *testing.T) {
	type commandCase struct {
		name      string
		args      []string
		responses map[string]string
		op, old   string
	}
	base := taskResponses()
	base["TeamAttentionPage"] = attentionJSON
	base["TeamAttentionPreviewPage"] = switchoverPreviewJSON
	base["TeamComposition"] = composition1085
	base["TeamRoles"] = teamRolesJSON
	base["AppAgentRoster"] = engTeamRosterJSON
	base["WorkersRoster"] = staffJSON
	base["AuthContext"] = whoamiAuthJSON
	base["TeamSessions"] = `{"data":{"sessions":[` + activeSessionJSON + `]}}`
	base["TeamWorkItems"] = provenanceWorklog
	base["GetTeamSession"] = `{"data":{"session":` + activeSessionJSON + `}}`
	base["TeamMemoryApp"] = `{"data":{"memory":{"id":"m1","appId":"capp100000000000000000000"}}}`
	base = sessionRenderStubs(base)
	unbound := make(map[string]string)
	for op, body := range base {
		unbound[op] = body
	}
	unbound["AuthContext"] = strings.ReplaceAll(whoamiAuthJSON, "u-me", "u-holger")
	unbound["TeamSessions"] = `{"data":{"sessions":[` + activeSessionJSON + `,` + strings.ReplaceAll(activeSessionJSON, "s-old", "s-another") + `]}}`
	cases := []commandCase{
		{"session-whoami-multiple", []string{"session", "whoami"}, unbound, "TeamSessions", "Iris"},
		{"attention", []string{"attention"}, base, "TeamAttentionPage", "Iris"},
		{"switchover-preview", []string{"attention", "switchover", "preview"}, base, "TeamAttentionPreviewPage", "Iris"},
		{"availability", []string{"availability", "get"}, base, "TeamAvailability", "Rufus"},
		{"availability-family", []string{"availability", "get"}, base, "TeamAvailability", "codex"},
		{"composition", []string{"composition"}, base, "TeamComposition", "Jack"},
		{"composition-counts", []string{"composition"}, base, "TeamComposition", "engineer"},
		{"events", []string{"events"}, base, "AgentTeamEvents", "holger"},
		{"role-list", []string{"role", "list"}, base, "TeamRoles", "Backend role"},
		{"roster", []string{"roster"}, base, "TeamRoster", "Rufus"},
		{"host-list", []string{"host", "list"}, hostResponses(), "HostRegistrations", "Orca box 1"},
		{"host-ops", []string{"host", "ops"}, hostResponses(), "HostOperations", "Jonas"},
		{"worker-list", []string{"worker", "list"}, base, "WorkersRoster", "Iris"},
		{"session-provenance", []string{"session", "list", "--pr", "hadron-memory/hadron-cli#371", "-m", "hrn:mem:acme.com:eng-team"}, base, "GetTeamSession", "Iris"},
		{"session-worklog", []string{"session", "list", "--pr", "hadron-memory/hadron-cli#371", "-m", "hrn:mem:acme.com:eng-team"}, base, "TeamWorkItems", "opened"},
		{"session-list", []string{"session", "list"}, base, "TeamSessions", "Iris"},
		{"task-list", []string{"task", "list"}, base, "AgentTaskList", "Fix the flaky CI build"},
		{"task-mine", []string{"task", "mine"}, base, "AgentTaskList", "Fix the flaky CI build"},
		{"stop-list", []string{"stop", "list"}, stopResponses(), "TeamStopSignals", "wkr1"},
		{"stop-set", []string{"stop", "set", "--scope", "team", "--level", "pause-dispatch", "--worker", "Iris"}, stopResponses(), "TeamStopSignalSet", "wkr1"},
		{"stop-lift", []string{"stop", "lift", "sig2"}, stopResponses(), "TeamStopSignalLift", "host1"},
	}
	const prefix = "a\nb\x1b[31mc"
	attack := prefix + strings.Repeat("界", 1000)
	// Independent expected contract: each control becomes ONE space, and the
	// complete cell is at most 256 runes, including its terminal ellipsis.
	sanitized := []rune("a b [31mc" + strings.Repeat("界", 1000))
	want := string(sanitized[:255]) + "…"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.responses[tc.op], tc.old) {
				t.Fatalf("fixture does not contain %q", tc.old)
			}
			args := append([]string{"team"}, tc.args...)
			_, clean, err := runSafeCells1101(t, tc.name, tc.responses, args...)
			if err != nil {
				t.Fatal(err)
			}
			responses := make(map[string]string, len(tc.responses))
			for op, body := range tc.responses {
				responses[op] = body
			}
			encoded, _ := json.Marshal(attack)
			needle, _ := json.Marshal(tc.old)
			responses[tc.op] = strings.ReplaceAll(responses[tc.op], string(needle), string(encoded))
			if responses[tc.op] == tc.responses[tc.op] {
				t.Fatal("attack was not installed")
			}
			_, rawJSON, err := runSafeCells1101(t, tc.name, responses, append(args, "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(rawJSON, string(encoded)) {
				t.Fatalf("JSON lost the original server string: %s", rawJSON)
			}
			snapshot := fmt.Sprintf("clean-text %x\nattacked-json %x\n", sha256.Sum256([]byte(clean)), sha256.Sum256([]byte(rawJSON)))
			path := filepath.Join("testdata", "safe-cells-1101", tc.name+".sha256")
			if os.Getenv("UPDATE_SAFE_CELLS_1101") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(snapshot), 0644); err != nil {
					t.Fatal(err)
				}
			}
			saved, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(saved) != snapshot {
				t.Errorf("clean text or exact JSON bytes changed:\nwant %s got %s", saved, snapshot)
			}
			_, out, err := runSafeCells1101(t, tc.name, responses, args...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, want) {
				t.Errorf("missing bounded sanitized cell (256 runes ending in ellipsis)")
			}
			for _, r := range out {
				if r != '\n' && (unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)) {
					t.Errorf("terminal control U+%04X survived", r)
					break
				}
			}
			if strings.Count(out, "\n") != strings.Count(clean, "\n") {
				t.Errorf("record split across lines: clean %d, attacked %d", strings.Count(clean, "\n"), strings.Count(out, "\n"))
			}
		})
	}
}

func runSafeCells1101(t *testing.T, name string, responses map[string]string, args ...string) (*[]attnCall, string, error) {
	t.Helper()
	if name != "session-whoami-multiple" {
		return runTeamTask(t, responses, args...)
	}
	teamGitDir(t)
	// Server recovery path: deliberately no local binding, two open sessions.
	_ = os.Remove(filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json"))
	srv, calls := attnServer(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, args...))
	err := root.Execute()
	return calls, out.String(), err
}
