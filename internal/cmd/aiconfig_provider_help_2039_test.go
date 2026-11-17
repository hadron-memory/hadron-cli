package cmd

import (
	"strings"
	"testing"
)

// cli#2039: the server accepts provider "kimi" (hadron-server#4170), so the
// create help names it, in the server's KNOWN_AI_PROVIDERS order. The CLI still
// passes --provider through unvalidated; only the help text moves.
const aiConfigProviderUsage2039 = "provider id (anthropic, openai, glm, kimi, bedrock, openai-compatible)"

func TestAiConfigCreateProviderHelpListsKimi2039(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(out)
	root.SetArgs([]string{"ai-config", "create", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	cmd, _, err := root.Find([]string{"ai-config", "create"})
	if err != nil {
		t.Fatal(err)
	}
	flag := cmd.Flags().Lookup("provider")
	if flag == nil || flag.Usage != aiConfigProviderUsage2039 {
		t.Fatalf("--provider usage = %q, want %q", flag.Usage, aiConfigProviderUsage2039)
	}
	if help := strings.Join(strings.Fields(out.String()), " "); !strings.Contains(help, "--provider string "+aiConfigProviderUsage2039) {
		t.Errorf("ai-config create --help must show the --provider line with kimi:\n%s", out.String())
	}
}
