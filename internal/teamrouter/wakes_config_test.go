package teamrouter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigSource(t *testing.T) {
	dir := t.TempDir()
	load := func(extra string) (*Config, error) {
		t.Helper()
		p := filepath.Join(dir, "c.json")
		body := `{"app": "a", "channel": "c", "stateDir": "s", "roster": []` + extra + `}`
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return LoadConfig(p)
	}
	c, err := load(``)
	if err != nil || c.Source != SourceAttention {
		t.Fatalf("default source = %+v (%v), want attention", c, err)
	}
	c, err = load(`, "source": "wakes", "appKeyFile": "~/k"`)
	if err != nil || c.Source != SourceWakes || strings.HasPrefix(c.AppKeyFile, "~") {
		t.Fatalf("wakes config = %+v (%v), want the expanded key path", c, err)
	}
	for name, extra := range map[string]string{
		"unknown source":    `, "source": "both"`,
		"wakes without key": `, "source": "wakes"`,
		"empty key path":    `, "source": "wakes", "appKeyFile": " "`,
		"non-string source": `, "source": 2`,
	} {
		if _, err := load(extra); err == nil {
			t.Errorf("%s: must refuse", name)
		}
	}
}
