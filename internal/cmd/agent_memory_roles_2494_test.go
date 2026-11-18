package cmd

import (
	"strings"
	"testing"
)

// cli#2494: the documented agent-memory access levels are exactly `read` and
// `read-write` (cor:dmo:050:09), and the help must say so where the user types
// --role. The server still validates the value (cli#58: the client forwards
// it as typed), so these pins are about what the help states, not a client
// rejection.

func agentMemoryHelp2494(t *testing.T, verb string) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "memory", verb, "--help"})
	root.SetOut(out)
	root.SetErr(out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestAgentMemoryRoleHelpNamesTheDocumentedSet2494(t *testing.T) {
	for _, verb := range []string{"add", "set-role"} {
		help := agentMemoryHelp2494(t, verb)
		for _, want := range []string{"read-write", "cor:dmo:050:09"} {
			if !strings.Contains(help, want) {
				t.Errorf("%s --help must name %q:\n%s", verb, want, help)
			}
		}
		// The flag line itself lists both values, so a user reading only the
		// flags learns the set.
		var flagLine string
		for _, l := range strings.Split(help, "\n") {
			if strings.Contains(l, "--role string") {
				flagLine = l
			}
		}
		if !strings.Contains(flagLine, "read") || !strings.Contains(flagLine, "read-write") {
			t.Errorf("%s: the --role flag line must list read and read-write: %q", verb, flagLine)
		}
	}
}

func TestAgentMemoryGroupHelpNamesTheDocumentedSet2494(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "memory", "--help"})
	root.SetOut(out)
	root.SetErr(out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"read", "read-write", "cor:dmo:050:09"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("agent memory --help must name %q:\n%s", want, out.String())
		}
	}
}

// The server's answer for a value outside the set is passed through, not
// replaced by a client guess: an unlisted role reaches the server once.
func TestAgentMemoryUnlistedRoleStillReachesTheServer2494(t *testing.T) {
	calls, _, _ := runAgentMemory(t, map[string]string{
		"UpdateAgentMemoryRole": `{"errors":[{"message":"invalid role","extensions":{"code":"BAD_USER_INPUT"}}]}`,
	}, "set-role", "agt1", "mem1", "--role", "admin")
	if len(calls.ops) != 1 || calls.vars["UpdateAgentMemoryRole"]["role"] != "admin" {
		t.Fatalf("the server owns validation: ops=%v vars=%v", calls.ops, calls.vars)
	}
}
