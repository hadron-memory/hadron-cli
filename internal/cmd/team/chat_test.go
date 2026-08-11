package team

import "testing"

// A persona display name folds onto the chat-handle charset ([a-z0-9_-],
// the channel's MENTION_RE) so @mentions of the persona actually match.
func TestHandleFromPersona(t *testing.T) {
	cases := map[string]string{
		"Iris":       "iris",
		"Ana María":  "ana-mara", // non-ascii dropped, space dashed
		"dev-Rufus":  "dev-rufus",
		"Q_A Bot 2":  "q_a-bot-2",
		"  Spaced  ": "spaced",
	}
	for in, want := range cases {
		got, err := handleFromPersona(in)
		if err != nil || got != want {
			t.Errorf("handleFromPersona(%q) = (%q, %v), want %q", in, got, err, want)
		}
	}
	if _, err := handleFromPersona("!!!"); err == nil {
		t.Error("an unusable name must error, not post as an empty handle")
	}
}
