package spec

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// testFactory builds a Factory backed by a temp config dir (so the real user
// config is never touched) and buffered IO streams so the stderr note is
// assertable.
func testFactory(t *testing.T) (*cmdutil.Factory, *config.Config, func() string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_SPEC_MEMORY", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ios, _, errBuf := output.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		ConfigFn:  func() (*config.Config, error) { return cfg, nil },
	}
	return f, cfg, errBuf.String
}

func TestEffectiveSpecMemory(t *testing.T) {
	t.Run("flag wins, no note", func(t *testing.T) {
		f, cfg, stderr := testFactory(t)
		if err := cfg.Set("spec_memory", "cfg.org::specs"); err != nil {
			t.Fatal(err)
		}
		got, err := effectiveSpecMemory(f, "flag.org::specs")
		if err != nil {
			t.Fatal(err)
		}
		if got != "flag.org::specs" {
			t.Errorf("got %q, want the flag value", got)
		}
		if s := stderr(); s != "" {
			t.Errorf("flag path must not print a note, got %q", s)
		}
	})

	t.Run("spec_memory default with note", func(t *testing.T) {
		f, cfg, stderr := testFactory(t)
		if err := cfg.Set("spec_memory", "cfg.org::specs"); err != nil {
			t.Fatal(err)
		}
		got, err := effectiveSpecMemory(f, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != "cfg.org::specs" {
			t.Errorf("got %q, want the spec_memory default", got)
		}
		if s := stderr(); !strings.Contains(s, "spec memory") {
			t.Errorf("expected a spec-memory note, got %q", s)
		}
	})

	t.Run("falls back to global active memory", func(t *testing.T) {
		f, cfg, stderr := testFactory(t)
		if err := cfg.Set("memory", "work.org::dev"); err != nil {
			t.Fatal(err)
		}
		got, err := effectiveSpecMemory(f, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != "work.org::dev" {
			t.Errorf("got %q, want the global active memory", got)
		}
		if s := stderr(); !strings.Contains(s, "active memory") {
			t.Errorf("expected an active-memory note, got %q", s)
		}
	})

	t.Run("spec_memory beats global memory", func(t *testing.T) {
		f, cfg, _ := testFactory(t)
		if err := cfg.Set("memory", "work.org::dev"); err != nil {
			t.Fatal(err)
		}
		if err := cfg.Set("spec_memory", "cfg.org::specs"); err != nil {
			t.Fatal(err)
		}
		got, err := effectiveSpecMemory(f, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != "cfg.org::specs" {
			t.Errorf("got %q, want spec_memory to win over the global memory", got)
		}
	})

	t.Run("nothing configured is a usage error", func(t *testing.T) {
		f, _, _ := testFactory(t)
		if _, err := effectiveSpecMemory(f, ""); err == nil {
			t.Fatal("expected a usage error when no memory is configured")
		}
	})
}
