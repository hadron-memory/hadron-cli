package cmd

import (
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"strings"
	"testing"
)

func TestRetiredPromptFlagsRefuseBeforeAnyRequest(t *testing.T) {
	for _, prefix := range [][]string{
		{"agent", "create", "--name", "Bot"}, {"agent", "update", "agt1"},
		{"app", "update", "acme.com:team"}, {"team", "worker", "cast", "--name", "Iris", "--role", "engineer"},
		{"team", "worker", "update", "wkr1"},
	} {
		flags := []string{"--system-prompt", "--system-prompt-file", "--clear-system-prompt"}
		if prefix[0] == "team" {
			flags = []string{"--prompt-override", "--clear-prompt-override"}
		}
		for _, flag := range flags {
			t.Run(strings.Join(prefix, " ")+flag, func(t *testing.T) {
				srv, calls := captureGraphQL(t, map[string]string{})
				f, _ := testFactory(t)
				t.Setenv("HADRON_TOKEN", "")
				root := NewRootCmd(f)
				args := append(append([]string{}, prefix...), flag)
				if !strings.HasPrefix(flag, "--clear-") {
					args = append(args, "-")
				}
				root.SetArgs(append(args, "--server", srv.URL))
				err := root.Execute()
				if exitCodeFor(err) != exitcode.Usage {
					t.Fatalf("want usage refusal, got %v", err)
				}
				// cli#1175: the refusal names the retired flag and the new
				// route — source node, then the matching compile command, then
				// node approval — never bare "unknown flag".
				msg := err.Error()
				for _, want := range []string{"--" + strings.TrimPrefix(flag, "--"), "was retired", wantCompile(prefix), "hadron node approve", "boot-prompt"} {
					if !strings.Contains(msg, want) {
						t.Fatalf("migration hint missing %q: %s", want, msg)
					}
				}
				if strings.Contains(msg, "unknown flag") {
					t.Fatalf("retired flag fell through to cobra's unknown-flag error: %s", msg)
				}
				if len(calls) != 0 {
					t.Fatalf("retired flag made requests: %v", calls)
				}
			})
		}
	}
}

// wantCompile is the compile command the hint must name for a command prefix.
func wantCompile(prefix []string) string {
	switch prefix[0] {
	case "team":
		return "hadron team worker compile"
	case "app":
		return "hadron app agent compile"
	default:
		return "hadron agent compile"
	}
}

func TestWorkerGetOmitsRetiredOverrideButKeepsServedPrompt(t *testing.T) {
	srv, _ := captureGraphQL(t, map[string]string{"GetWorker": `{"data":{"worker":` + updatedWorkerJSON + `}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "hrn:worker:acme.com:eng-team:iris", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if _, ok := dto["promptOverride"]; ok {
		t.Fatalf("retired output: %s", out.String())
	}
	if dto["prompt"] == nil {
		t.Fatalf("served prompt lost: %s", out.String())
	}
}
