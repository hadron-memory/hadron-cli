package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2206: a task's run settings (family, model or tier, effort) at
// creation. They say what to launch, not whom to match; they are create-only,
// so nothing here can clear one, and no flag means no runSettings key at all.
func createInput2206(t *testing.T, args ...string) (map[string]any, error) {
	t.Helper()
	calls, _, err := runTeamTask(t, taskResponses(), append([]string{"team", "task", "create", "example", "--json"}, args...)...)
	if err != nil {
		return nil, err
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskCreate" {
			return c.Vars["input"].(map[string]any), nil
		}
	}
	t.Fatal("no create sent")
	return nil, nil
}

func TestTaskCreateSendsOnlyTheRunSettingsGiven2206(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		want map[string]any
	}{
		"model and effort":  {[]string{"--model", "claude-sonnet-5-5", "--effort", "HIGH"}, map[string]any{"model": "claude-sonnet-5-5", "effort": "HIGH"}},
		"tier":              {[]string{"--tier", "low"}, map[string]any{"tier": "low"}},
		"family and unset":  {[]string{"--family", "codex", "--effort", "UNSET"}, map[string]any{"family": "codex", "effort": "UNSET"}},
		"all but tier":      {[]string{"--family", "claude", "--model", "opus", "--effort", "MAX"}, map[string]any{"family": "claude", "model": "opus", "effort": "MAX"}},
	} {
		t.Run(name, func(t *testing.T) {
			input, err := createInput2206(t, c.args...)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := input["runSettings"].(map[string]any)
			if len(got) != len(c.want) {
				t.Fatalf("runSettings = %#v, want exactly %#v (a flag not given must be absent, never null)", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("runSettings[%s] = %#v, want %#v", k, got[k], v)
				}
			}
		})
	}
}

func TestTaskCreateWithoutRunSettingsSendsNone2206(t *testing.T) {
	input, err := createInput2206(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := input["runSettings"]; present {
		t.Fatalf("no flag, so no runSettings key: %#v", input["runSettings"])
	}
}

// The CLI refuses, before any write, the shapes the server always refuses.
func TestTaskCreateRefusesInvalidRunSettings2206(t *testing.T) {
	for name, args := range map[string][]string{
		"model with tier":   {"--model", "opus", "--tier", "high"},
		"effort NOT_APPLIC": {"--effort", "NOT_APPLICABLE"},
		"tier outside":      {"--tier", "ultra"},
		"effort outside":    {"--effort", "ULTRA"},
		"model is a flag":   {"--model", "--resume"},
		"empty model":       {"--model", " "},
	} {
		t.Run(name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, taskResponses(), append([]string{"team", "task", "create", "example"}, args...)...)
			var ce *exitcode.CodedError
			if !errors.As(err, &ce) || ce.Code != exitcode.Usage {
				t.Fatalf("want a usage error (exit 2), got %v", err)
			}
			for _, c := range *calls {
				if c.Op == "AgentTaskCreate" {
					t.Fatal("a refused shape must send nothing")
				}
			}
		})
	}
}

func TestTaskCreateHelpSaysWhatRunSettingsAreFor2206(t *testing.T) {
	_, out, err := runTeamTask(t, taskResponses(), "team", "task", "create", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--tier", "--effort", "short-lived"} {
		if !strings.Contains(out, want) {
			t.Errorf("help must mention %q", want)
		}
	}
}
