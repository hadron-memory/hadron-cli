package spec

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func locSet(locs ...string) (map[string]bool, []string) {
	m := map[string]bool{}
	for _, l := range locs {
		m[l] = true
	}
	return m, locs
}

func TestPlanExtract(t *testing.T) {
	// A product-rooted subtree with feature 020 holding rules 00..03.
	locs, all := locSet("cor", "cor:dmo", "cor:dmo:020", "cor:dmo:020:00",
		"cor:dmo:020:01", "cor:dmo:020:02", "cor:dmo:020:03", "cor:dmo:060", "cor:dmo:060:02")
	source := Citation{Product: "cor", Module: "dmo", Feature: "060", Rule: "02"}

	t.Run("allocate next rule under to-feature", func(t *testing.T) {
		target, parent, inherit, err := planExtract(source, "020", "", locs, all)
		if err != nil {
			t.Fatalf("planExtract: %v", err)
		}
		if got := target.Format(); got != "cor:dmo:020:04" {
			t.Errorf("target = %q, want cor:dmo:020:04", got)
		}
		if parent != "cor:dmo:020" {
			t.Errorf("parent = %q, want cor:dmo:020", parent)
		}
		if inherit != "cor:dmo:020:00" {
			t.Errorf("inherit = %q, want cor:dmo:020:00", inherit)
		}
	})

	t.Run("explicit rule", func(t *testing.T) {
		target, _, _, err := planExtract(source, "020", "07", locs, all)
		if err != nil {
			t.Fatalf("planExtract: %v", err)
		}
		if got := target.Format(); got != "cor:dmo:020:07" {
			t.Errorf("target = %q, want cor:dmo:020:07", got)
		}
	})

	t.Run("malformed to-feature is Usage", func(t *testing.T) {
		_, _, _, err := planExtract(source, "20", "", locs, all)
		if exitcode.FromError(err) != exitcode.Usage {
			t.Errorf("want Usage for bad to-feature, got %v", err)
		}
	})

	t.Run("missing to-feature is NotFound", func(t *testing.T) {
		_, _, _, err := planExtract(source, "990", "", locs, all)
		if exitcode.FromError(err) != exitcode.NotFound {
			t.Errorf("want NotFound for absent feature, got %v", err)
		}
	})

	t.Run("flat-corpus source", func(t *testing.T) {
		flatLocs, flatAll := locSet("msg", "msg:010", "msg:010:02", "msg:020")
		flatSource := Citation{Module: "msg", Feature: "010", Rule: "02"}
		target, parent, _, err := planExtract(flatSource, "020", "", flatLocs, flatAll)
		if err != nil {
			t.Fatalf("planExtract: %v", err)
		}
		if got := target.Format(); got != "msg:020:01" {
			t.Errorf("target = %q, want msg:020:01", got)
		}
		if parent != "msg:020" {
			t.Errorf("parent = %q, want msg:020", parent)
		}
	})
}

func TestStripChunk(t *testing.T) {
	body := "# Node\n\nIntro para.\n\n## Node type\n\nThe nodeType chunk.\n\n## Tail\n\nend.\n"

	t.Run("found once trims and tidies the seam", func(t *testing.T) {
		out, ok := stripChunk(body, "## Node type\n\nThe nodeType chunk.\n")
		if !ok {
			t.Fatal("expected a match")
		}
		want := "# Node\n\nIntro para.\n\n## Tail\n\nend.\n"
		if out != want {
			t.Errorf("trimmed body =\n%q\nwant\n%q", out, want)
		}
		if strings.Contains(out, "nodeType chunk") {
			t.Error("chunk not removed")
		}
	})

	t.Run("not found leaves source untouched", func(t *testing.T) {
		if _, ok := stripChunk(body, "## Absent\n\nnope"); ok {
			t.Error("expected no match")
		}
	})

	t.Run("ambiguous double match refuses", func(t *testing.T) {
		dup := "alpha\n\nrepeat\n\nbeta\n\nrepeat\n\ngamma\n"
		if _, ok := stripChunk(dup, "repeat"); ok {
			t.Error("expected refusal on duplicate match")
		}
	})

	t.Run("whitespace-only chunk refuses", func(t *testing.T) {
		if _, ok := stripChunk(body, "   \n\t"); ok {
			t.Error("expected refusal on empty chunk")
		}
	})

	t.Run("removing the trailing chunk keeps one final newline", func(t *testing.T) {
		out, ok := stripChunk("keep me\n\n## Tail\n\ngone.\n", "## Tail\n\ngone.")
		if !ok {
			t.Fatal("expected a match")
		}
		if out != "keep me\n" {
			t.Errorf("got %q, want %q", out, "keep me\n")
		}
	})
}

func TestTitleFromName(t *testing.T) {
	if got := titleFromName("cor:dmo:060:02 — Node"); got != "Node" {
		t.Errorf("got %q, want Node", got)
	}
	if got := titleFromName("  PlainName  "); got != "PlainName" {
		t.Errorf("no-separator fallback got %q, want PlainName", got)
	}
}

func TestDefaultRefLabel(t *testing.T) {
	if got := defaultRefLabel("Node type", "cor:dmo:060:02 — Node"); got != "documents Node type on the Node entity" {
		t.Errorf("got %q", got)
	}
	// No citation separator — fall back to the whole name.
	if got := defaultRefLabel("X", "PlainName"); got != "documents X on the PlainName entity" {
		t.Errorf("fallback got %q", got)
	}
}
