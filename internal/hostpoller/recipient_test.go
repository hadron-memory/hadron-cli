package hostpoller

import (
	"path/filepath"
	"slices"
	"testing"
)

// C2 (cli#908) delivers on the same host without a live registration, so the
// worker-to-config match and the recipient are exported: one rule for the
// poller and for C2.
func TestRefForMatchesByIDURNOrName(t *testing.T) {
	c := &Config{Workers: map[string]WorkerConfig{
		"iris":                              {Family: "claude"},
		"hrn:worker:acme.com:eng-team:dara": {Family: "codex"},
		"wkr-gil":                           {Family: "glm"},
	}}
	for _, tc := range []struct{ id, urn, name, want string }{
		{"wkr-iris", "hrn:worker:acme.com:eng-team:iris", "Iris", "iris"},
		{"wkr-dara", "hrn:worker:acme.com:eng-team:dara", "Dara", "hrn:worker:acme.com:eng-team:dara"},
		{"wkr-gil", "", "Gil", "wkr-gil"},
		{"wkr-x", "hrn:worker:acme.com:eng-team:x", "Xan", ""},
	} {
		if got := c.RefFor(tc.id, tc.urn, tc.name); got != tc.want {
			t.Errorf("RefFor(%q, %q, %q) = %q, want %q", tc.id, tc.urn, tc.name, got, tc.want)
		}
	}
}

func TestRecipientIsThePollersRecipient(t *testing.T) {
	c := &Config{WorktreesRoot: "/w", Workers: map[string]WorkerConfig{
		"iris": {Family: "glm", TUI: "claude", Worktrees: []string{"repo/a"}},
	}}
	s := emptyState()
	s.Created["iris"] = []string{"repo/b"}
	r := c.Recipient(s, "iris", "wkr-iris")
	want := []string{filepath.Join("/w", "repo", "a"), filepath.Join("/w", "repo", "b")}
	if r.WorkerID != "wkr-iris" || r.Name != "iris" || r.TUI != "claude" || !slices.Equal(r.Worktrees, want) {
		t.Fatalf("%+v", r)
	}
}
