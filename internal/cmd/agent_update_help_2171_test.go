package cmd

import (
	"strings"
	"testing"
)

// hadron-cli#2171 (Joaquin F1 on #2169): the --context-cap paragraph must not
// split the lifecycle paragraph, or "The flag requires server #4242" reads as if
// it belonged to the cap. Words are joined so a phrase survives a line wrap.
// cli#2508 (Jonas's BLIND F15 on #2499): the order checks alone no longer
// assert the explanation stays in one piece — an unrelated paragraph could sit
// between the lifecycle text and the cap while the order still holds. The
// whole-paragraph pins restore that: the flattened explanation must appear as
// one contiguous run, so anything interleaved breaks the match.
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
			// And the explanation itself is one piece, start to end: the whole
			// flattened paragraph (verb-specific tail included) must match
			// contiguously, with the cap text directly after it.
			whole := whole2171[verb]
			start := strings.Index(h, whole)
			if start < 0 {
				t.Fatalf("%s: the lifecycle explanation is not one piece (or its wording changed on purpose — update whole2171 with it):\nwant: %s\ngot: %s", verb, whole, h)
			}
			if next := strings.TrimPrefix(h[start+len(whole):], " "); !strings.HasPrefix(next, "--context-cap") {
				t.Fatalf("%s: text between the lifecycle explanation and the context cap: %q", verb, next)
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

// whole2171 is each verb's lifecycle explanation, flattened as the test above
// compares it. create and update share the opening sentence; their tails
// differ (create states the create-time default, update the update-time
// semantics).
var whole2171 = map[string]string{
	"create": strings.Join(strings.Fields(`--lifecycle LONG_LIVED keeps workers running; SHORT_LIVED runs them only
while they hold a runnable task. Omitted: LONG_LIVED. A host acts on it when
its sweep is enabled.`), " "),
	"update": strings.Join(strings.Fields(`--lifecycle LONG_LIVED keeps workers running; SHORT_LIVED runs them only
while they hold a runnable task. Omitted keeps the current value (the default
is LONG_LIVED). It records desired behaviour; a host acts on it only when its
sweep is enabled. The server checks the same permission as --uses-tasks.`), " "),
}
