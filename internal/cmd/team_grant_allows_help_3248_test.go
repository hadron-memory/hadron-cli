package cmd

import (
	"strings"
	"testing"
)

// cli#3248: `allows` has no issuer term (hadron-server grantLiveSql); the
// issuer of a secret.reveal, secret.use or boot-prompt grant is judged again at
// each use (cor:acl:140:04 R4).

var allowsFacts3248 = []string{
	"signed, not revoked, its holder has not gone, its App is not deleted, it has started, it has not expired or been used up and it is inside its action's limits",
	"allows does not judge the issuer",
	"for secret.reveal, secret.use and boot-prompt grants the issuer's authority is judged again at each use, so an ACTIVE grant with allows yes can still be refused",
	"an ACTIVE grant shows allows no before its start, or once its App is deleted",
}

var allowsStale3248 = []string{
	"stops allowing without changing",
	"can be used right now",
	"can be used now",
}

func checkAllows3248(t *testing.T, text string, facts []string) {
	t.Helper()
	flat := strings.Join(strings.Fields(text), " ")
	for _, s := range facts {
		if !strings.Contains(flat, s) {
			t.Errorf("lacks %q", s)
		}
	}
	for _, s := range allowsStale3248 {
		if strings.Contains(flat, s) {
			t.Errorf("still says %q", s)
		}
	}
}

func TestGrantAllowsHelp3248(t *testing.T) {
	for _, verb := range []string{"list", "get", "create", "app-list", "revoke", "withdraw"} {
		t.Run(verb, func(t *testing.T) {
			f, _ := testFactory(t)
			c, _, err := NewRootCmd(f).Find([]string{"team", "grant", verb})
			if err != nil || c.Name() != verb {
				t.Fatalf("find %s: %v", verb, err)
			}
			checkAllows3248(t, c.Long, allowsFacts3248)
		})
	}
	t.Run("group", func(t *testing.T) {
		f, _ := testFactory(t)
		c, _, err := NewRootCmd(f).Find([]string{"team", "grant"})
		if err != nil || c.Name() != "grant" {
			t.Fatalf("find grant: %v", err)
		}
		checkAllows3248(t, c.Long, []string{"a check of the issuer at use can still refuse"})
	})
}

func TestGrantAllowsAgenticUsage3248(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	checkAllows3248(t, out.String(), allowsFacts3248)
}
