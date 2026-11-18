package cmd

import (
	"strings"
	"testing"
)

// hadron-cli#2233: set-model's Short and Long predate --effort. They must say
// that any ONE of model, family or effort suffices, and keep "a report, never a
// control". Words are joined so a phrase survives a line wrap.
func TestSetModelHelpSaysAnyOneOfThreeSuffices2233(t *testing.T) {
	_, out, err := runTeamTask(t, map[string]string{}, "team", "session", "set-model", "--help")
	if err != nil {
		t.Fatal(err)
	}
	h := strings.Join(strings.Fields(out), " ")
	for _, stale := range []string{"model and/or family", "At least one is required"} {
		if strings.Contains(h, stale) {
			t.Errorf("help still carries the pre-effort wording %q:\n%s", stale, h)
		}
	}
	for _, want := range []string{
		"Report the model, family or effort",
		"Give at least one of <model>, --family or --effort",
		"a report, never a control",
		"NOT_APPLICABLE",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("help lacks %q:\n%s", want, h)
		}
	}
}

// The Short is what `team session --help` lists.
func TestSetModelShortSaysAnyOneOfThree2233(t *testing.T) {
	_, out, err := runTeamTask(t, map[string]string{}, "team", "session", "--help")
	if err != nil {
		t.Fatal(err)
	}
	h := strings.Join(strings.Fields(out), " ")
	if strings.Contains(h, "model and/or family") {
		t.Errorf("the listing still says model and/or family:\n%s", h)
	}
	if !strings.Contains(h, "Report the model, family or effort this worktree's session is running (manual)") {
		t.Errorf("the listing lacks the new Short:\n%s", h)
	}
}

// The exact key set per case: "effort alone" must fail if it also sends a model
// or family, not only if effort is missing.
func TestSetModelSendsExactlyTheGivenKeys2233(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		keys []string
	}{
		"effort alone":     {[]string{"--effort", "HIGH"}, []string{"effort"}},
		"model alone":      {[]string{"gpt-x"}, []string{"llmModel"}},
		"family alone":     {[]string{"--family", "codex"}, []string{"family"}},
		"model and effort": {[]string{"gpt-x", "--effort", "UNSET"}, []string{"effort", "llmModel"}},
		"all three":        {[]string{"gpt-x", "--family", "codex", "--effort", "XHIGH"}, []string{"effort", "family", "llmModel"}},
	} {
		t.Run(name, func(t *testing.T) {
			v, _, err := setModel887(t, c.args...)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for k := range v {
				switch k {
				case "effort", "llmModel", "family":
					got[k] = true
				}
			}
			if len(got) != len(c.keys) {
				t.Errorf("run-setting keys sent = %v, want exactly %v (vars %v)", got, c.keys, v)
			}
			for _, k := range c.keys {
				if !got[k] {
					t.Errorf("%s missing (vars %v)", k, v)
				}
			}
		})
	}
}
