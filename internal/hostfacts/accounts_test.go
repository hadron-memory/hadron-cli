package hostfacts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#3236: the accounts file (hadron-server#5210's note): ~/.config/hadron/accounts.yaml, keys
// family, program, configDir; accounts only (no workers, worktrees or host settings); `tool` and
// `version` are RESERVED names a newer hadron will use; read at the moment of use.

func writeAccounts(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "accounts.yaml")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAccountsPathIsInTheCLIConfigDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if p, err := AccountsPath(); err != nil || p != "/xdg/hadron/accounts.yaml" {
		t.Fatalf("AccountsPath() = %q, %v", p, err)
	}
}

func TestAbsentFileIsAnEmptySetNotAnError(t *testing.T) {
	a, err := LoadAccounts(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil || a.Present || len(a.Entries) != 0 || len(a.Names) != 0 {
		t.Fatalf("an absent file is no accounts, no complaint: %+v, %v", a, err)
	}
}

func TestAValidFileLoadsAndTheToolIsTheFamilys(t *testing.T) {
	p := writeAccounts(t, `accounts:
  work:
    family: claude
    configDir: /home/u/.claude-work
  review:
    family: glm
    program: /home/u/.local/bin/claude-review
  codex-two:
    family: codex
    configDir: /home/u/.codex-two
`, 0o600)
	a, err := LoadAccounts(p)
	if err != nil || !a.Present {
		t.Fatalf("LoadAccounts: %+v, %v", a, err)
	}
	if got := strings.Join(a.Names, ","); got != "codex-two,review,work" {
		t.Errorf("Names must be sorted: %s", got)
	}
	w := a.Entries["work"]
	if w.Family != "claude" || w.ConfigDir != "/home/u/.claude-work" || w.Program != "" {
		t.Errorf("work = %+v", w)
	}
	for name, tool := range map[string]string{"work": "claude", "review": "claude", "codex-two": "codex"} {
		if got := a.Entries[name].Tool(); got != tool {
			t.Errorf("%s.Tool() = %q, want %q (an absent tool means the family's tool)", name, got, tool)
		}
	}
}

func TestEveryRefusalNamesTheFileEntryAndKey(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		mode  os.FileMode
		wants []string
		newer bool
	}{
		{"workers key", "accounts: {}\nworkers:\n  iris: {}\n", 0o600, []string{"workers", "accounts only"}, false},
		{"worktrees key", "worktrees_root: /x\naccounts: {}\n", 0o600, []string{"worktrees_root", "accounts only"}, false},
		{"host settings key", "repos: {}\n", 0o600, []string{"repos", "accounts only"}, false},
		{"unknown entry key", "accounts:\n  w:\n    family: claude\n    color: red\n", 0o600, []string{"w", "color"}, false},
		{"tool is reserved", "accounts:\n  w:\n    family: claude\n    tool: claude-code\n", 0o600, []string{"w", "tool", "needs a newer hadron"}, true},
		{"version is reserved (top)", "version: 2\naccounts: {}\n", 0o600, []string{"version", "needs a newer hadron"}, true},
		{"version is reserved (entry)", "accounts:\n  w:\n    family: claude\n    version: 2\n", 0o600, []string{"w", "version", "needs a newer hadron"}, true},
		{"name must be lowercase token", "accounts:\n  Work:\n    family: claude\n", 0o600, []string{"Work"}, false},
		{"name may not start with a dash", "accounts:\n  -x:\n    family: claude\n", 0o600, []string{"-x"}, false},
		{"name at most 64 characters", "accounts:\n  " + strings.Repeat("a", 65) + ":\n    family: claude\n", 0o600, []string{strings.Repeat("a", 65)}, false},
		{"family is required", "accounts:\n  w:\n    configDir: /x\n", 0o600, []string{"w", "family"}, false},
		{"family is one of four", "accounts:\n  w:\n    family: llama\n", 0o600, []string{"w", "family", "llama", "claude, codex, glm or kimi"}, false},
		{"configDir must be absolute", "accounts:\n  w:\n    family: claude\n    configDir: .claude-w\n", 0o600, []string{"w", "configDir", "absolute"}, false},
		{"program must be absolute", "accounts:\n  w:\n    family: claude\n    program: claude-w\n", 0o600, []string{"w", "program", "absolute"}, false},
		{"program has no shell characters", "accounts:\n  w:\n    family: claude\n    program: '/bin/x; rm'\n", 0o600, []string{"w", "program"}, false},
		{"duplicate entry", "accounts:\n  w:\n    family: claude\n  w:\n    family: codex\n", 0o600, []string{"w", "twice"}, false},
		{"duplicate key", "accounts:\n  w:\n    family: claude\n    family: codex\n", 0o600, []string{"w", "family", "twice"}, false},
		{"a scalar is not an entry", "accounts:\n  w: claude\n", 0o600, []string{"w"}, false},
		{"not yaml", "accounts: [unclosed\n", 0o600, []string{"accounts.yaml"}, false},
		{"group-readable", "accounts: {}\n", 0o640, []string{"readable by group or others", "chmod 600"}, false},
		{"world-readable", "accounts: {}\n", 0o644, []string{"readable by group or others", "chmod 600"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeAccounts(t, tc.body, tc.mode)
			_, err := LoadAccounts(p)
			if err == nil {
				t.Fatal("must be refused")
			}
			for _, w := range tc.wants {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("%q must name %q", err.Error(), w)
				}
			}
			if !strings.Contains(err.Error(), "accounts.yaml") {
				t.Errorf("the file is named: %q", err.Error())
			}
			var ae *AccountsError
			if !errors.As(err, &ae) || ae.NeedsNewer != tc.newer {
				t.Errorf("NeedsNewer = %v, want %v (%v)", ae != nil && ae.NeedsNewer, tc.newer, err)
			}
			if tc.newer && strings.Contains(strings.ToLower(err.Error()), "unknown key") {
				t.Errorf("a reserved key is answered 'needs a newer hadron', never 'unknown key': %q", err)
			}
		})
	}
}

// The file is read at the moment of use: an edit needs no restart, and nothing is cached.
func TestAnEditBetweenTwoCallsIsSeen(t *testing.T) {
	p := writeAccounts(t, "accounts:\n  a:\n    family: claude\n", 0o600)
	first, err := LoadAccounts(p)
	if err != nil || len(first.Names) != 1 {
		t.Fatalf("first: %+v, %v", first, err)
	}
	if err := os.WriteFile(p, []byte("accounts:\n  a:\n    family: claude\n  b:\n    family: codex\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := LoadAccounts(p)
	if err != nil || len(second.Names) != 2 {
		t.Fatalf("an edit must be seen at once: %+v, %v", second, err)
	}
}

// Old files stay valid when `tool` exists later: an entry with no tool means the family's tool.
func TestAnEntryWithoutToolStaysValidAndMeansTheFamilysTool(t *testing.T) {
	a, err := LoadAccounts(writeAccounts(t, "accounts:\n  work:\n    family: kimi\n", 0o600))
	if err != nil || a.Entries["work"].Tool() != "claude" {
		t.Fatalf("%+v, %v", a, err)
	}
}
