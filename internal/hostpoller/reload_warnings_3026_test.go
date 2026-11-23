package hostpoller

import (
	"strings"
	"testing"
)

// cli#3026 (BLIND question on #3043): Warnings are derived from the file at each load, not a setting.
// fixedChanges compares every exported non-reloadable field, so without a skip, deleting a stale `launch`
// line from a running host's file would log config-restart-needed naming "-" (the yaml tag).
func TestFixedChangesIgnoresLoadWarnings(t *testing.T) {
	a := &Config{App: "x", Warnings: []string{`worker "iris": launch is ignored by worker.start`}}
	b := &Config{App: "x"}
	if got := fixedChanges(a, b); len(got) != 0 {
		t.Fatalf("a changed warning set is not a restart-needed setting: %q", got)
	}
	// A real fixed setting still counts beside a warning difference.
	c := &Config{App: "y"}
	if got := fixedChanges(a, c); len(got) != 1 || got[0] != "app" {
		t.Fatalf("want only app, got %q", got)
	}
}

// A worker the file gains while the poller runs, with a stale `launch` beside its launcher_profile.
const reloadStaleLaunchWorker = `  stale:
    family: claude
    launch: [claude-worker]
    launcher_profile: {family: claude, mode: short, model: opus, account: holger}
    disk_tmp: /tmp/stale
account_profiles:
  holger: {bin: claude-holger}
orca_launch:
  orca_bin: /usr/bin/orca
`

// cli#3045 item 3: `host run` says a load warning once at start. A worker a hot reload ADDS gets the same
// word, as one config-warning event, once: the reload that applies it, not each poll after it.
func TestReloadSaysTheLoadWarningOfAnAddedWorkerOnce3045(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	fx.edit(reloadBase + reloadStaleLaunchWorker)
	ev := fx.kinds("config-warning")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, `worker "stale"`) || !strings.Contains(ev[0].Detail, "worker.start") {
		t.Fatalf("want one config-warning naming the added worker, got %+v", ev)
	}
	fx.step()
	fx.step()
	if n := len(fx.kinds("config-warning")); n != 1 {
		t.Fatalf("the warning is said once, not each poll: %d events", n)
	}
	// An edit that leaves the already-warned worker alone says nothing new of it.
	fx.edit("# touched\n" + reloadBase + billie + reloadStaleLaunchWorker)
	if n := len(fx.kinds("config-warning")); n != 1 {
		t.Fatalf("a later reload must not repeat a warning it already said: %d events", n)
	}
}
