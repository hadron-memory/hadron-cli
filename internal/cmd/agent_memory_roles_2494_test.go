package cmd

import (
	"regexp"
	"strings"
	"testing"
)

// cli#2494: the documented agent-memory roles are exactly `read` and
// `read-write`, and the help must say so where the user types
// --role. The server still validates the value (cli#58: the client forwards
// it as typed), so these pins are about what the help states, not a client
// rejection. The cor:dmo:050:09 citation left the help with the #2508 scrub.

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

// hasWord2494 reports whether w appears as a whole token: not inside a longer one, where a
// token continues over letters, digits, underscores and hyphens, so "read" is not found in
// "read-write", "reader" or "read2", and "read-write" is not found in "read-writer".
func hasWord2494(s, w string) bool {
	return regexp.MustCompile(`(^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(w) + `([^A-Za-z0-9_-]|$)`).MatchString(s)
}

func TestHasWord2494(t *testing.T) {
	for _, tc := range []struct {
		s, w string
		want bool
	}{
		{"read or read-write", "read", true}, {"read or read-write", "read-write", true},
		{"read-write only", "read", false}, {"the reader role", "read", false}, {"read2", "read", false},
		{"read_only", "read", false}, {"read-writer", "read-write", false}, {"readwrite", "read-write", false},
		{"role: read.", "read", true}, {"(read)", "read", true},
	} {
		if got := hasWord2494(tc.s, tc.w); got != tc.want {
			t.Errorf("hasWord2494(%q, %q) = %v, want %v", tc.s, tc.w, got, tc.want)
		}
	}
}

func TestAgentMemoryRoleHelpNamesBothRoles2494(t *testing.T) {
	for _, tc := range []struct{ verb, flagLine string }{
		{"add", "the Memory's role on the Agent: read or read-write"},
		{"set-role", "the new role: read or read-write"},
	} {
		verb := tc.verb
		help := agentMemoryHelp2494(t, verb)
		for _, want := range []string{"read-write", tc.flagLine} {
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
		if !hasWord2494(flagLine, "read") || !hasWord2494(flagLine, "read-write") {
			t.Errorf("%s: the --role flag line must list read and read-write: %q", verb, flagLine)
		}
	}
}

func TestAgentMemoryGroupHelpNamesBothRoles2494(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "memory", "--help"})
	root.SetOut(out)
	root.SetErr(out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	// Scoped to the role explanation, so an unrelated mention elsewhere on the page cannot
	// satisfy it: the paragraph that starts with the prose fragment, up to the next blank line.
	help := out.String()
	const lead = "Memory's role on an Agent is"
	at := strings.Index(help, lead)
	if at < 0 {
		t.Fatalf("agent memory --help must carry the role explanation (%q):\n%s", lead, help)
	}
	para := help[at:]
	if end := strings.Index(para, "\n\n"); end >= 0 {
		para = para[:end]
	}
	for _, role := range []string{"read", "read-write"} {
		if !hasWord2494(para, role) {
			t.Errorf("the role explanation must name %q as a whole token:\n%s", role, para)
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
