package cmd

import (
	"strings"
	"testing"
)

// #1077: a "y" given to a confirmation nobody saw is not consent. With only the
// prompt write failing and "y" on stdin, the command exits non-zero and sends no
// mutation. The helper-level proof is internal/cmdutil/confirm_test.go; these pin
// that real commands of each caller class reach it.
func TestFailedPromptSendsNoMutation(t *testing.T) {
	const grantee = `{"data":{"user":{"id":"usr1","name":"Alice","email":"alice@acme.com","handle":"alice","githubUsername":null,"roles":[]}}}`
	cases := []struct {
		name     string
		args     []string
		replies  map[string]string
		mutation string
	}{
		{"deletion (ConfirmDeletion): agent rm", []string{"agent", "rm", "agt1"},
			map[string]string{"DeleteAgent": `{"data":{"deleteAgent":true}}`}, "DeleteAgent"},
		{"revocation (Confirm): memory share rm", []string{"memory", "share", "rm", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--grantee", "usr1"},
			map[string]string{"GetUser": grantee, "DeleteMemoryShare": `{"data":{"deleteMemoryShare":{"memoryId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","granteeId":"usr1"}}}`}, "DeleteMemoryShare"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, c.replies)
			f, out, eo := testFactoryTTY(t, "y\n")
			f.IOStreams.ErrOut = failsOnPrompt{w: eo}
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{}, c.args...), "--server", gql.URL))
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), "could not show the confirmation prompt") || !strings.Contains(err.Error(), "nothing was sent") {
				t.Fatalf("want a could-not-show-prompt refusal, got %v", err)
			}
			if exitCodeFor(err) != 1 {
				t.Errorf("exit code = %d, want 1", exitCodeFor(err))
			}
			if _, sent := captured[c.mutation]; sent {
				t.Errorf("%s was sent after a failed prompt write", c.mutation)
			}
			if out.String() != "" {
				t.Errorf("stdout must stay empty, got %q", out.String())
			}
		})
	}
}

// Controls: a shown prompt answered "y" still proceeds, and --yes never prompts.
func TestShownPromptStillConfirms(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"DeleteAgent": `{"data":{"deleteAgent":true}}`})
	f, _, eo := testFactoryTTY(t, "y\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "rm", "agt1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, sent := captured["DeleteAgent"]; !sent || !strings.Contains(eo.String(), "(y/N)") {
		t.Errorf("a shown, answered prompt must proceed (sent=%v, stderr=%q)", sent, eo.String())
	}
}

func TestYesNeverPromptsEvenWithBrokenStderr(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"DeleteAgent": `{"data":{"deleteAgent":true}}`})
	f, _, eo := testFactoryTTY(t, "")
	f.IOStreams.ErrOut = failsOnPrompt{w: eo}
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "rm", "agt1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("--yes must proceed: %v", err)
	}
	if _, sent := captured["DeleteAgent"]; !sent {
		t.Error("--yes did not send the mutation")
	}
}
