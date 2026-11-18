package cmd

import (
	"strings"
	"testing"
)

// hadron-cli#2171 (Joaquin F1 on #2169): the --context-cap paragraph must not
// split the lifecycle paragraph, or "The flag requires server #4242" reads as if
// it belonged to the cap. Words are joined so a phrase survives a line wrap.
func TestAgentHelpKeepsTheLifecycleParagraphWhole2171(t *testing.T) {
	for _, verb := range []string{"create", "update"} {
		t.Run(verb, func(t *testing.T) {
			_, out, err := runTeamTask(t, map[string]string{}, "agent", verb, "--help")
			if err != nil {
				t.Fatal(err)
			}
			h := strings.Join(strings.Fields(out), " ")
			life := strings.Index(h, "--lifecycle LONG_LIVED")
			short := strings.Index(h, "SHORT_LIVED")
			capAt := strings.Index(h, "--context-cap")
			compat := strings.Index(h, "Server compatibility")
			if life < 0 || short < 0 || capAt < 0 || compat < 0 {
				t.Fatalf("a part is missing (lifecycle %d, SHORT_LIVED %d, cap %d, compat %d):\n%s", life, short, capAt, compat, h)
			}
			// The lifecycle explanation (both values) comes first, then the context cap, and
			// the server-compatibility note covers both flags once, after them.
			if life >= short || short >= capAt || capAt >= compat {
				t.Fatalf("order must be lifecycle, SHORT_LIVED, context cap, compatibility: %d %d %d %d", life, short, capAt, compat)
			}
			// User help carries no issue numbers or "S3 server" (cli#1053 audit finding C1).
			for _, bad := range []string{"#4242", "#4258", "S3 server", "hadron-cli#2058"} {
				if strings.Contains(h, bad) {
					t.Errorf("user help must not carry %q", bad)
				}
			}
		})
	}
}
