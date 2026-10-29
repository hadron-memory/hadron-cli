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
				if err := root.Execute(); exitCodeFor(err) != exitcode.Usage {
					t.Fatalf("want usage refusal, got %v", err)
				}
				if len(calls) != 0 {
					t.Fatalf("retired flag made requests: %v", calls)
				}
			})
		}
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
