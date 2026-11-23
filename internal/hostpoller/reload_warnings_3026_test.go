//go:build !windows

package hostpoller

import (
	"os"
	"path/filepath"
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

// reloadStaleLaunchFile is a whole host file (already in its launcher_profile form, so hostyaml.Supervised
// is not applied) whose worker "stale" keeps a `launch` argv beside its launcher_profile; extra is more
// workers, placed before the account sections.
func reloadStaleLaunchFile(extra string) string {
	return `app: hrn:app:acme.com:team
label: box1
poll_interval_seconds: 30
key_file: %KEY%
worktrees_root: %ROOT%
state_file: %ROOT%/state.json
repos:
  acme/repo: %ROOT%/clone
workers:
  iris:
    family: claude
    launcher_profile: {family: claude, mode: long, account: acct-iris}
    disk_tmp: /tmp/hostyaml-fixture/iris
  stale:
    family: claude
    launch: [claude-worker]
    launcher_profile: {family: claude, mode: long, account: acct-iris}
    disk_tmp: /tmp/hostyaml-fixture/stale
` + extra + `account_profiles:
  acct-iris: {bin: claude-worker}
orca_launch:
  orca_bin: /usr/bin/orca
`
}

// editRaw is edit without hostyaml.Supervised: that shorthand drops a `launch` written beside a
// launcher_profile, which is exactly the stale line these tests need the file to keep.
func (fx *reloadFixture) editRaw(text string) {
	fx.t.Helper()
	text = strings.NewReplacer("%KEY%", filepath.Join(fx.dir, "host.key"), "%ROOT%", fx.dir).Replace(text)
	if err := os.WriteFile(fx.path, []byte(text), 0o600); err != nil {
		fx.t.Fatal(err)
	}
	fx.step()
	fx.step()
}

// cli#3045 item 3: `host run` says a load warning once at start. A worker a hot reload ADDS gets the same
// word, as one config-warning event, once: the reload that applies it, not each poll after it.
func TestReloadSaysTheLoadWarningOfAnAddedWorkerOnce3045(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	fx.editRaw(reloadStaleLaunchFile(""))
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
	fx.editRaw("# touched\n" + reloadStaleLaunchFile("  billie:\n    family: claude\n    launcher_profile: {family: claude, mode: long, account: acct-iris}\n    disk_tmp: /tmp/hostyaml-fixture/billie\n"))
	if n := len(fx.kinds("config-warning")); n != 1 {
		t.Fatalf("a later reload must not repeat a warning it already said: %d events", n)
	}
}
