package hostpoller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigWorkerHomesMustBeAbsolute(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ extra, bad string }{
		{"    codex_home: " + dir + "\n    claude_config_dir: " + dir + "\n", ""},
		{"    codex_home: rel/home\n", "codex_home must be an absolute path"},
		{"    claude_config_dir: rel\n", "claude_config_dir must be an absolute path"},
	} {
		p := filepath.Join(dir, "h.yaml")
		cfg := "server: http://x\napp: hrn:app:t:t\nkey_file: " + filepath.Join(dir, "k") + "\nstate_file: " + filepath.Join(dir, "s.json") +
			"\nworktrees_root: " + dir + "\nworkers:\n  w1:\n    family: codex\n    launch: [fake]\n" + tc.extra
		if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, "k"), []byte("key"), 0o600)
		_, err := LoadConfig(p)
		switch {
		case tc.bad == "" && err != nil:
			t.Errorf("valid homes refused: %v", err)
		case tc.bad != "" && (err == nil || !strings.Contains(err.Error(), tc.bad)):
			t.Errorf("want %q, got %v", tc.bad, err)
		}
	}
}
