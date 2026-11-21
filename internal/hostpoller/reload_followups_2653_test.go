//go:build !windows

package hostpoller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pins from the #2625 review follow-ups (cli#2653): the profile-account
// program of a changed worker, a Readlink failure on a followed link, and
// noteRemoved's forgetting of a worker that came back.

// A worker whose entry changed names its launcher profile's account program
// even when the account itself did not change: that program is judged, so a
// reload that would run it is refused while it is writable by others. Pins
// existing behaviour (namedFiles, cli#2653 item 5); the changed-account arm
// is TestAChangedAccountProgramOthersCanWriteIsRefused2623.
func TestAChangedWorkersProfileAccountProgramIsJudgedThoughTheAccountDidNotChange2653(t *testing.T) {
	t.Parallel()
	// The account's program is a real file from the start, and the edit keeps
	// the account entry byte-identical: only oscar's own entry changes (its
	// model), so the refusal, if any, can only come from judging the profile
	// account's program.
	orcaWithProg := strings.Replace(reloadBase+reloadOrcaWorker, "bin: claude-holger", "bin: %ROOT%/good", 1)
	fx := newReloadFixtureFrom(t, orcaWithProg)
	loose := filepath.Join(fx.dir, "good")
	if err := os.WriteFile(loose, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fx.step()
	if err := os.Chmod(loose, 0o777); err != nil {
		t.Fatal(err)
	}
	fx.edit(strings.Replace(orcaWithProg, "model: opus", "model: sonnet", 1))
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "writable by group or others") || !strings.Contains(ev[0].Detail, loose) {
		t.Fatalf("want a refusal naming the worker's profile account program, got %+v", ev)
	}
	if _, ok := fx.p.config().Worker("oscar"); !ok {
		t.Fatal("oscar is configured at start")
	}
	if w, ok := fx.p.config().Worker("oscar"); ok && w.Profile != nil && w.Profile.Model == "sonnet" {
		t.Fatal("a reload judged through a loose account program was applied")
	}
}

// A Readlink that fails on a link the walk has already followed makes the
// path "cannot be judged" — a refusal, never a skip (cli#2653 item 7,
// Rook/Juniper). The seam makes the read fail where a test cannot: between
// the walk listing the link and reading its target.
func TestAReadlinkFailureOnAFollowedLinkRefusesTheReload2653(t *testing.T) {
	t.Parallel()
	fx := newReloadFixture(t)
	fx.step()
	sticky := looseDir(t, fx.dir, "sticky", true)
	safe := filepath.Join(fx.dir, "safe")
	if err := os.Mkdir(safe, 0o755); err != nil {
		t.Fatal(err)
	}
	prog := filepath.Join(safe, "launcher")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(sticky, "tool")
	if err := os.Symlink(prog, link); err != nil {
		t.Fatal(err)
	}
	fx.p.trust.readlink = func(name string) (string, error) {
		if name == link {
			return "", os.ErrInvalid // the link was swapped away between the listing and the read
		}
		return os.Readlink(name)
	}
	fx.edit(reloadBase + "  billie:\n    family: claude\n    launch: [" + link + "]\n")
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "cannot be judged") {
		t.Fatalf("want a refusal that cannot be judged, got %+v", ev)
	}
	if _, ok := fx.p.config().Worker("billie"); ok {
		t.Fatal("a reload through an unjudgeable link was applied")
	}
}

// noteRemoved forgets a worker that came back, so a later removal cannot
// carry the stale entry of her earlier absence (cli#2653 item 10, Juniper;
// the end-to-end arm is TestAReAddedWorkerEndsNormally2623). A direct pin.
func TestNoteRemovedForgetsAWorkerThatComesBack2653(t *testing.T) {
	t.Parallel()
	fx := newReloadFixtureFrom(t, reloadBase+billie)
	fx.step()
	p := fx.p
	id := ""
	p.mu.Lock()
	for i, ref := range p.workerRefs {
		if ref == "billie" {
			id = i
		}
	}
	p.mu.Unlock()
	if id == "" {
		t.Fatal("billie was never registered")
	}
	before := p.config()
	p.noteRemoved(before, []string{"billie"}, nil)
	p.mu.Lock()
	rm, remembered := p.removed[id]
	p.mu.Unlock()
	if !remembered || rm.ref != "billie" || rm.cfg.Family != "claude" {
		t.Fatalf("a removed worker must be remembered with her entry: %+v remembered=%v", rm, remembered)
	}
	p.noteRemoved(before, nil, []string{"billie"})
	p.mu.Lock()
	_, remembered = p.removed[id]
	p.mu.Unlock()
	if remembered {
		t.Fatal("a worker that came back is still remembered as removed")
	}
}
