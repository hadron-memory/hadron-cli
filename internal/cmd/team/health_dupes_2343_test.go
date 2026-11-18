//go:build !windows

package team

// hadron-cli#2343 — the #2037 health sweep's failure paths, pinned through the
// same end-to-end world as health_dupes_2027_test.go: stop-failed, close-failed,
// pause-unreadable, a terminal row with no tabId, a symlinked worktree, and a
// notice post that fails. Each test names the branch it covers and the receipt
// row (or stderr note) that branch must leave.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRawList writes the fake orca's terminal listing from raw row JSON, for
// rows the typed writeList cannot express (no tabId key, a symlinked path),
// with a screen file per handle so the sweep can classify each.
func (w *health2027World) writeRawList(t *testing.T, screens map[string][]string, rows ...string) {
	t.Helper()
	list := `{"ok":true,"result":{"terminals":[` + strings.Join(rows, ",") + `]}}`
	if err := os.WriteFile(filepath.Join(w.dir, "orca-list.json"), []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	for handle, screen := range screens {
		tail := `["` + strings.Join(screen, `","`) + `"]`
		if err := os.WriteFile(filepath.Join(w.dir, "screens", handle+".json"), []byte(tail), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func (w *health2027World) rawRow(handle, tab, path string) string {
	if tab == "" {
		return fmt.Sprintf(`{"handle":%q,"worktreePath":%q,"title":"Iris","connected":true,"writable":true}`, handle, path)
	}
	return fmt.Sprintf(`{"handle":%q,"worktreePath":%q,"tabId":%q,"title":"Iris","connected":true,"writable":true}`, handle, path, tab)
}

var (
	health2343Agent = []string{"✻ Compiling…", "  go build ./..."}
	health2343Bare  = []string{"orca@orca1:~$"}
)

// stop-failed: the duplicate the census named is gone by the time the sweep
// signals it (killed inside the pause read, after the census). The failed stop
// is an alert row, never a fix row, never "applied", and nothing is signalled.
func TestHealth2343StopFailedIsAnAlertNotAFix(t *testing.T) {
	w := health2027Setup(t)
	w.spawnAgent(t, "codex", true)
	w.spawnAgent(t, "claude", false)
	stray := w.agents[len(w.agents)-1]
	w.beforeFix = func() {
		_ = stray.Process.Kill()
		_ = stray.Wait() // reaped, so the pid is truly gone and not a zombie
	}
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	alert := health2027Find(dto.Alerts, func(a health2027Alert) bool { return a.Code == "stop-failed" })
	if alert == nil || alert.Worker != "w1" || alert.Count != 1 {
		t.Fatalf("a failed stop left no stop-failed alert: %+v", dto.Alerts)
	}
	if fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "stop-agent" }); fix != nil {
		t.Fatalf("a failed stop was reported as a fix: %+v", dto.Fixes)
	}
	if terms, _ := w.records(); terms != "" {
		t.Fatalf("a SIGTERM was recorded for a process that was already gone: %q", terms)
	}
	if w.chatPosts() != 0 {
		t.Fatal("a sweep that applied nothing posted a notice")
	}
}

// close-failed: orca's `terminal close` errors. The receipt carries a
// close-failed alert and no close-tab fix row, and nothing is counted applied
// (so no notice would be posted for it).
func TestHealth2343CloseFailedIsAnAlertNotAFix(t *testing.T) {
	w := health2027Setup(t)
	w.setTerminals(t, []health2027Term{
		{Handle: "term_agent", Tab: "tab-agent", Screen: health2343Agent},
		{Handle: "term_bare", Tab: "tab-bare", Screen: health2343Bare},
	})
	if err := os.WriteFile(w.closeFail, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	alert := health2027Find(dto.Alerts, func(a health2027Alert) bool { return a.Code == "close-failed" })
	if alert == nil || alert.Worker != "w1" || alert.Count != 1 {
		t.Fatalf("a failed close left no close-failed alert: %+v", dto.Alerts)
	}
	if fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "close-tab" }); fix != nil {
		t.Fatalf("a failed close was reported as a fix: %+v", dto.Fixes)
	}
	if skip := health2027Find(dto.Skips, func(s health2027Skip) bool { return strings.HasPrefix(s.Code, "close-") }); skip != nil {
		t.Fatalf("a failed close (not a declined one) left a decline skip: %+v", dto.Skips)
	}
	if _, closes := w.records(); closes != "" {
		t.Fatalf("a close that failed recorded a close: %q", closes)
	}
}

// pause-unreadable: the pause query fails, so a pause cannot be ruled out. The
// sweep fails closed to report-only: the wildcard skip row, the fix listed and
// not applied, no signal sent, and the sweep itself does not fail.
func TestHealth2343UnreadablePauseFailsClosedToReportOnly(t *testing.T) {
	w := health2027Setup(t)
	w.pauseFails = true
	w.spawnAgent(t, "codex", true)
	w.spawnAgent(t, "claude", false)
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("an unreadable pause must not fail the sweep: %v", err)
	}
	skip := health2027Find(dto.Skips, func(s health2027Skip) bool { return s.Code == "pause-unreadable" })
	if skip == nil || skip.Worker != "*" {
		t.Fatalf("missing the wildcard pause-unreadable skip: %+v", dto.Skips)
	}
	fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "stop-agent" })
	if fix == nil || fix.Applied {
		t.Fatalf("an unreadable pause must report the fix without applying it: %+v", dto.Fixes)
	}
	if terms, _ := w.records(); terms != "" {
		t.Fatalf("a sweep that could not read the pause signalled a process: %q", terms)
	}
}

// A terminal row with no tabId cannot be counted as a tab: it is never closed
// and never makes a tab of its own. Alongside a real bare tab only that tab
// closes; alone with an agent tab the worktree is not even a candidate.
func TestHealth2343TerminalWithoutATabIDIsNotATab(t *testing.T) {
	t.Run("only the real bare tab is closed", func(t *testing.T) {
		w := health2027Setup(t)
		w.writeRawList(t, map[string][]string{"term_agent": health2343Agent, "term_bare": health2343Bare, "term_orphan": health2343Bare},
			w.rawRow("term_agent", "tab-agent", w.worktree), w.rawRow("term_bare", "tab-bare", w.worktree), w.rawRow("term_orphan", "", w.worktree))
		dto, _, err := w.run(t, false, "--dry-run=false")
		if err != nil {
			t.Fatalf("sweep failed: %v", err)
		}
		var closeFixes []health2027Fix
		for _, f := range dto.Fixes {
			if f.Action == "close-tab" {
				closeFixes = append(closeFixes, f)
			}
		}
		if len(closeFixes) != 1 || !closeFixes[0].Applied || !strings.Contains(closeFixes[0].Subject, "tab-bare") {
			t.Fatalf("want exactly the one real bare tab closed: %+v", dto.Fixes)
		}
		if !w.settle(t, func() bool { return strings.Contains(w.closes(), "term_bare") }) {
			t.Fatalf("the bare tab was not closed: %q", w.closes())
		}
		if strings.Contains(w.closes(), "term_orphan") {
			t.Fatalf("a row with no tabId was closed: %q", w.closes())
		}
	})
	t.Run("alone it is not a candidate", func(t *testing.T) {
		w := health2027Setup(t)
		w.writeRawList(t, map[string][]string{"term_agent": health2343Agent, "term_orphan": health2343Bare},
			w.rawRow("term_agent", "tab-agent", w.worktree), w.rawRow("term_orphan", "", w.worktree))
		dto, _, err := w.run(t, false, "--dry-run=false")
		if err != nil {
			t.Fatalf("sweep failed: %v", err)
		}
		if len(dto.Fixes) != 0 || len(dto.Alerts) != 0 {
			t.Fatalf("a row with no tabId made a tab: fixes %+v alerts %+v", dto.Fixes, dto.Alerts)
		}
		if _, closes := w.records(); closes != "" {
			t.Fatalf("a row with no tabId was closed: %q", closes)
		}
	})
}

// A worktree reached through a symlink matches by its resolved path on both
// sides: terminals that report the symlink path still belong to the worktree
// (the bare tab is closed), and a configured worktrees_root that is itself a
// symlink still finds the real agents (the duplicate is stopped).
func TestHealth2343SymlinkedWorktreeMatchesByResolvedPath(t *testing.T) {
	t.Run("terminals report the symlink path", func(t *testing.T) {
		w := health2027Setup(t)
		link := filepath.Join(w.dir, "wt-link")
		if err := os.Symlink(w.worktree, link); err != nil {
			t.Fatal(err)
		}
		w.writeRawList(t, map[string][]string{"term_agent": health2343Agent, "term_bare": health2343Bare},
			w.rawRow("term_agent", "tab-agent", link), w.rawRow("term_bare", "tab-bare", link))
		dto, _, err := w.run(t, false, "--dry-run=false")
		if err != nil {
			t.Fatalf("sweep failed: %v", err)
		}
		fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "close-tab" })
		if fix == nil || !fix.Applied || !strings.Contains(fix.Subject, "tab-bare") {
			t.Fatalf("a terminal under a symlinked path was not matched to the worktree: %+v", dto.Fixes)
		}
		if !w.settle(t, func() bool { return strings.Contains(w.closes(), "term_bare") }) {
			t.Fatalf("the bare tab was not closed: %q", w.closes())
		}
	})
	t.Run("worktrees_root is a symlink", func(t *testing.T) {
		w := health2027Setup(t)
		rootLink := filepath.Join(w.dir, "wt-root-link")
		if err := os.Symlink(filepath.Join(w.dir, "wt-root"), rootLink); err != nil {
			t.Fatal(err)
		}
		cfg, err := os.ReadFile(w.cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		real := fmt.Sprintf("worktrees_root: %q", filepath.Join(w.dir, "wt-root"))
		if !strings.Contains(string(cfg), real) {
			t.Fatalf("config fixture changed: %s", cfg)
		}
		if err := os.WriteFile(w.cfgPath, []byte(strings.Replace(string(cfg), real, fmt.Sprintf("worktrees_root: %q", rootLink), 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		w.spawnAgent(t, "codex", true)
		w.spawnAgent(t, "claude", false)
		dto, _, err := w.run(t, false, "--dry-run=false")
		if err != nil {
			t.Fatalf("sweep failed: %v", err)
		}
		fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "stop-agent" })
		if fix == nil || !fix.Applied {
			t.Fatalf("a worktree reached through a symlinked root was not handled: fixes %+v alerts %+v skips %+v", dto.Fixes, dto.Alerts, dto.Skips)
		}
		if !w.settle(t, func() bool { return strings.Contains(w.terms(), "term claude") }) {
			t.Fatalf("the duplicate was not stopped: %q", w.terms())
		}
	})
}

// A failing notice post: the fixes stand (the stray is stopped and the receipt
// says applied), the sweep still succeeds, and stderr carries the one note.
func TestHealth2343FailingNoticePostKeepsTheFixes(t *testing.T) {
	w := health2027Setup(t)
	w.chatFails = true
	w.spawnAgent(t, "codex", true)
	w.spawnAgent(t, "claude", false)
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(w.worktree); err != nil { // bound: the notice is attempted
		t.Fatal(err)
	}
	dto, _, err := w.run(t, false, "--dry-run=false")
	if err != nil {
		t.Fatalf("a failing notice post must not fail the sweep: %v", err)
	}
	fix := health2027Find(dto.Fixes, func(f health2027Fix) bool { return f.Action == "stop-agent" })
	if fix == nil || !fix.Applied {
		t.Fatalf("the fix did not stand after the post failed: %+v", dto.Fixes)
	}
	if !w.settle(t, func() bool { return strings.Contains(w.terms(), "term claude") }) {
		t.Fatalf("the duplicate was not stopped: %q", w.terms())
	}
	if !strings.Contains(w.lastErr, "note: the health sweep notice could not be posted; the fixes stand") {
		t.Fatalf("the failed post left no stderr note: %q", w.lastErr)
	}
	if w.chatPosts() != 0 {
		t.Fatalf("a refused post was recorded as sent: %d", w.chatPosts())
	}
}
