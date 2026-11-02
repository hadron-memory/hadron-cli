package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// agentResetJSON is agentJSON with resetContextAfterTask set, so a read-back
// that drops or defaults the field is visible.
func agentResetJSON(reset bool) string {
	v := "false"
	if reset {
		v = "true"
	}
	return strings.Replace(agentJSON, `"usesTasks":true,`, `"usesTasks":true,"resetContextAfterTask":`+v+`,`, 1)
}

func runAgentUpdateCapture(t *testing.T, argv ...string) map[string]any {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{"UpdateAgent": `{"data":{"updateAgent":` + agentResetJSON(true) + `}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"agent", "update", "agt1"}, argv...), "--server", gql.URL))
	if err := root.Execute(); err != nil {
		t.Fatalf("%v: %v", argv, err)
	}
	var vars map[string]any
	if err := json.Unmarshal(captured["UpdateAgent"], &vars); err != nil {
		t.Fatal(err)
	}
	return vars
}

// Acceptance 1 and 2: the flag alone passes the nothing-to-change guard and
// reaches the write; true and =false are sent as given, and nothing else is.
func TestAgentUpdateResetContextAfterTaskSendsExplicitValue(t *testing.T) {
	for flag, want := range map[string]bool{
		"--reset-context-after-task":       true,
		"--reset-context-after-task=true":  true,
		"--reset-context-after-task=false": false,
	} {
		t.Run(flag, func(t *testing.T) {
			vars := runAgentUpdateCapture(t, flag)
			v, ok := vars["resetContextAfterTask"]
			if !ok || v != want {
				t.Fatalf("resetContextAfterTask presence=%v value=%v want=%v", ok, v, want)
			}
			if len(vars) != 2 {
				t.Fatalf("flag-only update sent unrelated fields: %v", vars)
			}
		})
	}
}

// Acceptance 1 and 6: omitting the flag sends no key, so the server preserves
// the stored value (an omitted flag sent as false would silently clear it).
func TestAgentUpdateOmittedResetContextAfterTaskSendsNoKey(t *testing.T) {
	for _, argv := range [][]string{{"--name", "Renamed"}, {"--uses-tasks=false"}} {
		vars := runAgentUpdateCapture(t, argv...)
		if _, ok := vars["resetContextAfterTask"]; ok {
			t.Fatalf("%v sent resetContextAfterTask: %v", argv, vars)
		}
	}
}

// Gate: a non-manager is refused exactly as for --uses-tasks — the server's
// FORBIDDEN maps to the forbidden exit, with no output and no follow-up.
func TestAgentUpdateResetContextAfterTaskRefusedForNonManager(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{
		"UpdateAgent": `{"errors":[{"message":"refused","extensions":{"code":"FORBIDDEN"}}]}`,
	}, "agent", "update", "agt1", "--reset-context-after-task")
	if err == nil || exitCodeFor(err) != exitcode.Forbidden {
		t.Fatalf("err=%v", err)
	}
	if len(callsFor(calls, "UpdateAgent")) != 1 || len(*calls) != 1 || out != "" {
		t.Fatalf("refusal retried or printed: calls=%d out=%q", len(*calls), out)
	}
}

// Acceptance 3: agent get shows the stored value in text and --json, and the
// update read-back carries it too.
func TestAgentGetShowsResetContextAfterTask(t *testing.T) {
	for _, reset := range []bool{true, false} {
		resp := map[string]string{"GetAgent": `{"data":{"agent":` + agentResetJSON(reset) + `}}`}
		_, human, err := runTeamTask(t, resp, "agent", "get", "agt1")
		if err != nil {
			t.Fatal(err)
		}
		wantLine := "reset context after task: false"
		if reset {
			wantLine = "reset context after task: true"
		}
		if !strings.Contains(human, wantLine) {
			t.Fatalf("text missing %q:\n%s", wantLine, human)
		}
		_, out, err := runTeamTask(t, resp, "agent", "get", "agt1", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(out), &dto); err != nil {
			t.Fatal(err)
		}
		if v, ok := dto["resetContextAfterTask"]; !ok || v != reset {
			t.Fatalf("json resetContextAfterTask=%v present=%v want %v", v, ok, reset)
		}
	}
	_, out, err := runTeamTask(t, map[string]string{"UpdateAgent": `{"data":{"updateAgent":` + agentResetJSON(true) + `}}`},
		"agent", "update", "agt1", "--reset-context-after-task", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"resetContextAfterTask": true`) {
		t.Fatalf("update read-back lost the field: %s", out)
	}
}

// Acceptance 5: help says the flag only records the setting.
func TestAgentUpdateResetContextAfterTaskHelpSaysInert(t *testing.T) {
	f, _ := testFactory(t)
	cmd, _, err := NewRootCmd(f).Find([]string{"agent", "update"})
	if err != nil {
		t.Fatal(err)
	}
	fl := cmd.Flags().Lookup("reset-context-after-task")
	if fl == nil {
		t.Fatal("flag missing")
	}
	if !strings.Contains(fl.Usage, "only records the setting") || !strings.Contains(fl.Usage, "omitted preserves") {
		t.Fatalf("usage: %q", fl.Usage)
	}
	if !strings.Contains(cmd.Long, "--reset-context-after-task only records") {
		t.Fatalf("Long: %s", cmd.Long)
	}
}
