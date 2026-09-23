package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #648 — agent prompts and an ai-config file are DOCUMENTS: `agent create|update
// --system-prompt - / --persona-prompt -` and `ai-config create --file -` refuse
// an interactive terminal with exit 2, before any request, naming the remedy.
// Piped forms stay covered by TestAgentCreatePersonaPromptFromStdin and
// TestAiConfigCreateFileStdin, which run non-TTY.
//
// Each case also runs SIGNED OUT (PR #675 review, Copilot + Codex): the test
// factory is always signed in, which hid a guard sitting after
// f.GraphQLClient(). Signed out, that ordering answers exit 3 instead of the
// refusal — so the local refusal must come before credentials are resolved.
func TestAgentAndAIConfigDocumentStdinRefusesATerminal(t *testing.T) {
	for _, signedIn := range []bool{true, false} {
		for _, tc := range agentAIConfigStdinCases {
			name := tc.name
			if !signedIn {
				name += " (signed out)"
			}
			t.Run(name, func(t *testing.T) { runDocumentStdinRefusal(t, tc.args, tc.remedy, signedIn) })
		}
	}
}

var agentAIConfigStdinCases = []struct {
	name   string
	args   []string
	remedy string
}{
	{"agent create --system-prompt -", []string{"agent", "create", "--org", "acme.com", "--name", "Bot", "--system-prompt", "-"}, "--system-prompt-file <path>"},
	{"agent create --persona-prompt -", []string{"agent", "create", "--org", "acme.com", "--name", "Bot", "--persona-prompt", "-"}, "--persona-prompt-file <path>"},
	{"agent update --system-prompt -", []string{"agent", "update", "agt1", "--system-prompt", "-"}, "--system-prompt-file <path>"},
	{"agent update --persona-prompt -", []string{"agent", "update", "agt1", "--persona-prompt", "-"}, "--persona-prompt-file <path>"},
	{"ai-config create --file -", []string{"ai-config", "create", "--file", "-"}, "--file <path>"},
}

func runDocumentStdinRefusal(t *testing.T, args []string, remedy string, signedIn bool) {
	t.Helper()
	requests := 0
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	t.Cleanup(gql.Close)
	f, _, errOut := testFactoryTTY(t, "typed at a terminal\n")
	if !signedIn {
		t.Setenv("HADRON_TOKEN", "") // testFactory's token store is empty
	}
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", gql.URL))
	err := root.Execute()
	if err == nil {
		t.Fatal("a document read from a terminal must be refused")
	}
	if got := renderError(f, err); got != exitcode.Usage {
		t.Errorf("want exit %d (usage), got %d", exitcode.Usage, got)
	}
	if requests != 0 {
		t.Errorf("a refusal on argument grounds must make no requests, got %d", requests)
	}
	msg := errOut.String()
	for _, want := range []string{remedy, "interactive terminal"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal must mention %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "%!") {
		t.Errorf("the refusal has a formatting fault:\n%s", msg)
	}
}

// NARROWNESS, the aiconfig half of TestDataKeyStdinIsNotGuardedByTheDocumentRule.
// `--api-key -` reads a SECRET from a terminal deliberately — it is how a key
// is handed over without entering argv or shell history — so the document
// guard must not touch it. Asserting SUCCESS, and that the key reached the
// wire, is what makes this a real control: the read has to have happened.
func TestAIConfigAPIKeyStdinIsNotGuardedByTheDocumentRule(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAiServiceConfig": `{"data":{"createAiServiceConfig":` + aiCfgJSON + `}}`,
	})
	f, _, errOut := testFactoryTTY(t, "sk-typed-at-a-terminal\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"ai-config", "create", "--app", "acme.com:juno-app",
		"--name", "default", "--provider", "anthropic", "--model", "claude-opus-4-8",
		"--api-key", "-", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		_ = renderError(f, err)
		t.Fatalf("a secret read from a terminal must SUCCEED: %v\n%s", err, errOut.String())
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateAiServiceConfig"], &vars)
	if vars["apiKey"] != "sk-typed-at-a-terminal" {
		t.Errorf("the key typed at the terminal must reach the wire, got %v", vars["apiKey"])
	}
	if strings.Contains(errOut.String(), "interactive terminal") {
		t.Errorf("a SECRET read from a terminal must not hit the document guard:\n%s", errOut.String())
	}
}
