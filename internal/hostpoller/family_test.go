package hostpoller

import (
	"strings"
	"testing"
)

// cli#971 / hadron-server#2217: the server hands out ONE canonical identity
// for an explicit family (trim, lowercase, anthropic->claude, openai->codex;
// any other valid token is itself). A host configured with a provider alias
// must still accept the canonical operation, and a genuine mismatch must still
// launch nothing.

func TestCanonicalFamily(t *testing.T) {
	for in, want := range map[string]string{
		"claude": "claude", "codex": "codex", "anthropic": "claude", "openai": "codex",
		" Anthropic ": "claude", "OPENAI": "codex", "\tCodex\n": "codex",
		"glm": "glm", "claude-opus": "claude-opus", "Private.Model-7": "private.model-7",
		"": "", "   ": "",
	} {
		if got := CanonicalFamily(in); got != want {
			t.Errorf("CanonicalFamily(%q) = %q, want %q", in, got, want)
		}
	}
}

// startWith runs one worker.start for a host configured as cfgFamily against
// an operation naming opFamily, and returns the report and the launch count.
func startWith(t *testing.T, cfgFamily, opFamily string) (report, int) {
	t.Helper()
	p, srv, host := testPoller(t)
	w := p.cfg.Workers["iris"]
	w.Family = cfgFamily
	p.cfg.Workers["iris"] = w
	srv.notDone["s1"] = 3
	op := startOp()
	op.Args["family"] = opFamily
	srv.batches = [][]Operation{{op}}
	step(t, p)
	return only(t, srv), len(host.launches)
}

func TestWorkerStartAcceptsAliasAndCanonicalFamiliesInBothDirections(t *testing.T) {
	for _, c := range []struct{ cfg, op string }{
		{"openai", "codex"}, {"codex", "openai"}, {"anthropic", "claude"}, {"claude", "anthropic"},
		{"openai", "openai"}, {"anthropic", "anthropic"}, {"codex", "codex"}, {"claude", "claude"},
		{"openai", " Codex "}, {"anthropic", "CLAUDE"}, {"claude", " Anthropic "},
		{"private.model-7", "private.model-7"}, {"glm", "glm"}, {"claude-opus", "claude-opus"},
	} {
		t.Run(c.cfg+"/"+c.op, func(t *testing.T) {
			r, launches := startWith(t, c.cfg, c.op)
			if r.outcome != Succeeded || launches != 1 {
				t.Errorf("config %q op %q: %+v launches=%d", c.cfg, c.op, r, launches)
			}
		})
	}
}

func TestWorkerStartGenuineFamilyMismatchLaunchesNothing(t *testing.T) {
	for _, c := range []struct{ cfg, op string }{
		{"claude", "codex"}, {"codex", "claude"}, {"openai", "claude"}, {"anthropic", "codex"},
		{"glm", "claude"}, {"claude", "glm"}, {"claude-opus", "claude"}, {"claude", "claude-opus"},
		{"private.a", "private.b"}, {"private.a", "codex"}, {"codex", "private.a"},
	} {
		t.Run(c.cfg+"/"+c.op, func(t *testing.T) {
			r, launches := startWith(t, c.cfg, c.op)
			if r.outcome != Failed || launches != 0 || !strings.Contains(r.reason, "runs the worker as") {
				t.Errorf("config %q op %q must be refused with nothing launched: %+v launches=%d", c.cfg, c.op, r, launches)
			}
		})
	}
}

func TestWorkerStartRefusesAnEmptyOrMalformedFamilyEvenWhenBothSidesAgree(t *testing.T) {
	for _, c := range []struct{ cfg, op string }{
		{"claude", ""}, {"claude", "   "}, {"claude", "Claude Code"}, {"claude", "-bad"},
		// a configuration that bypassed load validation must not make "" or a
		// malformed token a launch identity by equalling itself
		{"", ""}, {"Claude Code", "Claude Code"}, {"  ", "  "},
	} {
		t.Run(c.cfg+"/"+c.op, func(t *testing.T) {
			r, launches := startWith(t, c.cfg, c.op)
			if r.outcome != Failed || launches != 0 {
				t.Errorf("config %q op %q: %+v launches=%d", c.cfg, c.op, r, launches)
			}
		})
	}
}

func TestWorkerStartKeepsItsOtherGatesForAnAliasFamily(t *testing.T) {
	for name, mut := range map[string]func(*Operation){
		"worktree": func(o *Operation) { o.Args["worktree"] = "repo/other" },
		"grant":    func(o *Operation) { o.LaunchGrant = "" },
	} {
		t.Run(name, func(t *testing.T) {
			p, srv, host := testPoller(t)
			w := p.cfg.Workers["iris"]
			w.Family = "openai"
			p.cfg.Workers["iris"] = w
			srv.notDone["s1"] = 1
			op := startOp()
			op.Args["family"] = "codex"
			mut(&op)
			srv.batches = [][]Operation{{op}}
			step(t, p)
			if r := only(t, srv); r.outcome != Failed {
				t.Errorf("%+v", r)
			}
			if len(host.launches) != 0 {
				t.Error("nothing may launch")
			}
		})
	}
}

func TestRegistrationSendsTheCanonicalFamily(t *testing.T) {
	for cfg, want := range map[string]string{"openai": "codex", "anthropic": "claude", "claude": "claude", "codex": "codex", "glm": "glm", "private.model-7": "private.model-7"} {
		p, srv, _ := testPoller(t)
		w := p.cfg.Workers["iris"]
		w.Family = cfg
		p.cfg.Workers["iris"] = w
		step(t, p)
		if len(srv.regs) != 1 || len(srv.regs[0].Workers) != 1 || srv.regs[0].Workers[0].Family != want {
			t.Errorf("config %q must register as %q: %+v", cfg, want, srv.regs)
		}
	}
}

func TestFamilyDerivedTUIFollowsTheCanonicalFamily(t *testing.T) {
	for _, c := range []struct{ family, tui, want string }{
		{"openai", "", "codex"}, {"anthropic", "", "claude"}, {"claude", "", "claude"}, {"codex", "", "codex"},
		{"glm", "", ""}, {"private.model-7", "", ""}, {"claude-opus", "", ""},
		{"openai", "claude", "claude"}, {"anthropic", "codex", "codex"}, {"glm", "claude", "claude"},
	} {
		if got := (WorkerConfig{Family: c.family, TUI: c.tui}).tui(); got != c.want {
			t.Errorf("family %q tui %q = %q, want %q", c.family, c.tui, got, c.want)
		}
	}
}

func TestRecipientForAnAliasConfiguredWorkerExpectsItsInterface(t *testing.T) {
	// delivery holds a terminal whose screen disagrees with Recipient.TUI
	// (internal/delivery: "the screen shows a different interface than
	// registered"), so an alias-configured worker must carry a real interface.
	p, _, _ := testPoller(t)
	for family, want := range map[string]string{"openai": "codex", "anthropic": "claude", "glm": ""} {
		w := p.cfg.Workers["iris"]
		w.Family = family
		p.cfg.Workers["iris"] = w
		if r := p.cfg.Recipient(p.state, "iris", "id-iris"); r.TUI != want {
			t.Errorf("family %q: recipient TUI %q, want %q", family, r.TUI, want)
		}
	}
}
