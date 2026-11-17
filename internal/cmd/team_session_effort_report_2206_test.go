package cmd

import (
	"encoding/json"
	"testing"
)

// hadron-cli#2206: `session set-model --effort` and a resume's --effort report
// the effort a session runs at (cor:agt:060:04): a report, never a control,
// omitted unless given and never null.
func TestSetModelEffortOnTheWire2206(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		want map[string]any
	}{
		"effort alone":     {[]string{"--effort", "HIGH"}, map[string]any{"effort": "HIGH"}},
		"model and effort": {[]string{"gpt-x", "--effort", "UNSET"}, map[string]any{"llmModel": "gpt-x", "effort": "UNSET"}},
		"not applicable":   {[]string{"--effort", "NOT_APPLICABLE"}, map[string]any{"effort": "NOT_APPLICABLE"}},
		"all three":        {[]string{"gpt-x", "--family", "codex", "--effort", "XHIGH"}, map[string]any{"llmModel": "gpt-x", "family": "codex", "effort": "XHIGH"}},
	} {
		t.Run(name, func(t *testing.T) {
			v, _, err := setModel887(t, c.args...)
			if err != nil {
				t.Fatal(err)
			}
			for k, want := range c.want {
				if v[k] != want {
					t.Errorf("%s = %#v, want %#v (vars %v)", k, v[k], want, v)
				}
			}
		})
	}
}

// Without --effort the variable is absent, never null: a null never clears one
// and the server's "omitted leaves it" is all the CLI relies on.
func TestSetModelWithoutEffortOmitsIt2206(t *testing.T) {
	v, _, err := setModel887(t, "gpt-x")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := v["effort"]; present {
		t.Fatalf("effort must be omitted, got %#v", v["effort"])
	}
}

func TestSetModelRefusesABadEffort2206(t *testing.T) {
	_, _, err := setModel887(t, "--effort", "ULTRA")
	if !usageErr(err) {
		t.Fatalf("want a usage error, got %v", err)
	}
}

// #2199 refused --effort in resume mode; the server now takes it on a resume.
func TestResumeSendsEffortWhenGiven2206(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want any
	}{{[]string{"--effort", "XHIGH"}, "XHIGH"}, {nil, nil}} {
		teamGitDir(t)
		t.Setenv("HADRON_LAUNCH_GRANT", "")
		srv, calls := captureGraphQL(t, resumeStubs("RESUMED"))
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"team", "session", "start", "--server", srv.URL}, tc.args...))
		_ = root.Execute()
		var v struct {
			Input map[string]any `json:"input"`
		}
		if err := json.Unmarshal(calls["ResumeTeamSession"], &v); err != nil {
			t.Fatalf("no resume sent for %v: %v", tc.args, err)
		}
		got, present := v.Input["effort"]
		if tc.want == nil && present {
			t.Errorf("no --effort: effort must be omitted, got %#v", got)
		}
		if tc.want != nil && got != tc.want {
			t.Errorf("--effort XHIGH: effort = %#v", got)
		}
	}
}
