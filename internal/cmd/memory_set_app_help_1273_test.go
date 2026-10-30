package cmd

import (
	"strings"
	"testing"
)

// cli#1273: createAppMemory admits the owner of a user-owned App (server#2843),
// so the help must not keep the organization-only rule or the retired refusal.
func TestMemorySetAppHelpUserOwnedApp(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(out.String()), " ")
	for _, gone := range []string{"USER_OWNED_APP_UNSUPPORTED", "must belong to an organization"} {
		if strings.Contains(text, gone) {
			t.Errorf("help still contains %q", gone)
		}
	}
	for _, want := range []string{"user-owned App", "its owner", "admin or owner of the App's organization", "have an Agent installed"} {
		if !strings.Contains(text, want) {
			t.Errorf("help is missing %q", want)
		}
	}
}
