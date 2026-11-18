package hostpoller

import (
	"reflect"
	"testing"
)

// #1381 (#1379 §10 delta P3-2): a fully scrubbed environment must be a NON-NIL
// EMPTY slice. exec.Cmd.Env treats nil as "inherit the parent's environment", so
// a refactor of agentEnv to `var out []string` would leak the host's credentials
// to the launched agent and to the Orca child. This pins the slice; the exec
// pin that proves the child really inherits nothing is in
// scrubbedenv_exec_1381_test.go.
func TestScrubbedEnvFullyScrubbedIsNonNilEmpty1381(t *testing.T) {
	for name, parent := range map[string][]string{
		"only a credential":          {"HADRON_TOKEN=x"},
		"only the host key variable": {HostKeyEnv + "=secret"},
		"a value carrying the key":   {"OTHER=has-k-in-it"},
		"every scrubbed variable":    {HostKeyEnv + "=a", "HADRON_TOKEN=b", "HADRON_LAUNCH_GRANT=c", StartSettingsAppliedEnvVar + "=d"},
		"an empty parent":            {},
		"a nil parent":               nil,
	} {
		t.Run(name, func(t *testing.T) {
			got := ScrubbedEnv(parent, "k")
			if got == nil || len(got) != 0 {
				t.Fatalf("ScrubbedEnv(%q, \"k\") = %#v: want a non-nil empty slice (nil means exec inherits the parent's environment)", parent, got)
			}
		})
	}
	// Not everything is scrubbed: the rest passes through, in order.
	got := ScrubbedEnv([]string{"A=1", "HADRON_TOKEN=x", "B=2"}, "k")
	if !reflect.DeepEqual(got, []string{"A=1", "B=2"}) {
		t.Fatalf("ScrubbedEnv kept %q", got)
	}
	// agentEnv with only additions is non-nil too (the launch path).
	if got := agentEnv([]string{"HADRON_TOKEN=x"}, ""); got == nil || len(got) != 0 {
		t.Fatalf("agentEnv = %#v", got)
	}
}
