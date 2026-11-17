package cmd

import (
	"testing"
)

// hadron-cli#2199: a host-launched agent binds with the effort the launcher
// applied. It rides only when given, so an ordinary start never sends
// `"effort": null`.
func TestSessionStartReportsEffort2199(t *testing.T) {
	for _, effort := range []string{"HIGH", "NOT_APPLICABLE"} {
		teamGitDir(t)
		t.Setenv("HADRON_LAUNCH_GRANT", "hlg_abc")
		gql, captured := captureGraphQL(t, launchGrantStubs())
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "start", "--tool", "codex", "--family", "codex", "--model", "gpt-6", "--effort", effort, "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		in := startInput(t, captured["StartTeamSession"])
		if in["effort"] != effort || in["family"] != "codex" || in["llmModel"] != "gpt-6" {
			t.Fatalf("input = %#v", in)
		}
	}
}

func TestSessionStartOmitsEffortByDefault2199(t *testing.T) {
	teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "hlg_abc")
	gql, captured := captureGraphQL(t, launchGrantStubs())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--tool", "claude-code", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, sent := startInput(t, captured["StartTeamSession"])["effort"]; sent {
		t.Fatal("effort must be omitted, not null")
	}
}

func TestSessionStartRefusesAnUnknownEffort2199(t *testing.T) {
	teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", "hlg_abc")
	gql, _ := captureGraphQL(t, launchGrantStubs())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--effort", "ULTRA", "--server", gql.URL})
	if exitCodeFor(root.Execute()) != 2 {
		t.Fatal("an effort outside the scale is a usage error")
	}
}
