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
			req := strings.Index(h, "requires server #4242")
			capAt := strings.Index(h, "--context-cap <tokens> sets")
			if life < 0 || req < 0 || capAt < 0 {
				t.Fatalf("a paragraph is missing (lifecycle %d, #4242 %d, cap %d):\n%s", life, req, capAt, h)
			}
			if life >= req || req >= capAt {
				t.Fatalf("the lifecycle paragraph (with its #4242 sentence) must come first, then the cap paragraph: lifecycle %d, #4242 %d, cap %d", life, req, capAt)
			}
		})
	}
}
