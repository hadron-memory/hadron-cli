//go:build !windows

package hostpoller

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#2668: the trust walk skips only a missing path (item 2), refuses a
// relative launch, end or recover program (item 4), and start and reload use
// the identical un-cleaned config path (item 8).

// ---- item 2: skip only os.IsNotExist ----

// A visited directory whose Stat fails for a reason other than that it does
// not exist is refused, not skipped: skipping it would judge a path part-way.
func TestAVisitedDirectoryWhoseStatFailsIsRefusedNotSkipped2668(t *testing.T) {
	root := trustTempDir(t)
	a := filepath.Join(root, "a")
	if err := os.Mkdir(a, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, "prog"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	prev := statPath
	statPath = func(p string) (os.FileInfo, error) {
		if p == a {
			return nil, fs.ErrPermission
		}
		return prev(p)
	}
	t.Cleanup(func() { statPath = prev })
	if err := trustedAncestors(filepath.Join(a, "prog"), "a program"); err == nil || !strings.Contains(err.Error(), "cannot be judged") {
		t.Fatalf("a directory whose Stat fails must be refused, got %v", err)
	}
}

// The program file itself, when its Stat fails for another reason, is refused
// the same way.
func TestAProgramWhoseStatFailsIsRefusedNotSkipped2668(t *testing.T) {
	root := trustTempDir(t)
	prog := filepath.Join(root, "prog")
	if err := os.WriteFile(prog, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	prev := statPath
	statPath = func(p string) (os.FileInfo, error) {
		if p == prog {
			return nil, fs.ErrPermission
		}
		return prev(p)
	}
	t.Cleanup(func() { statPath = prev })
	if err := trustedPath(prog, "a program"); err == nil || !strings.Contains(err.Error(), "cannot be judged") {
		t.Fatalf("a program whose Stat fails must be refused, got %v", err)
	}
}

// A followed symlink whose Lstat fails for another reason is refused too: its
// owner chose where it leads, and an unreadable one cannot be judged.
func TestAFollowedLinkWhoseLstatFailsIsRefusedNotSkipped2668(t *testing.T) {
	root := trustTempDir(t)
	b := filepath.Join(root, "b")
	if err := os.Mkdir(b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "end"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "a-link")
	if err := os.Symlink(b, link); err != nil {
		t.Fatal(err)
	}
	prev := lstatLink
	lstatLink = func(p string) (os.FileInfo, error) {
		if p == link {
			return nil, fs.ErrPermission
		}
		return prev(p)
	}
	t.Cleanup(func() { lstatLink = prev })
	if err := trustedAncestors(link, "a program"); err == nil || !strings.Contains(err.Error(), "cannot be judged") {
		t.Fatalf("a followed link whose Lstat fails must be refused, got %v", err)
	}
}

// A path that does not exist is still judged as far as it does: a not-exist
// error skips, as before, whatever the walk would say about the entry.
func TestAMissingPathIsStillSkipped2668(t *testing.T) {
	root := trustTempDir(t)
	a := filepath.Join(root, "a")
	if err := os.Mkdir(a, 0o755); err != nil {
		t.Fatal(err)
	}
	prev := statPath
	statPath = func(p string) (os.FileInfo, error) {
		if p == a {
			return nil, fs.ErrNotExist
		}
		return prev(p)
	}
	t.Cleanup(func() { statPath = prev })
	if err := trustedAncestors(filepath.Join(a, "prog"), "a program"); err != nil {
		t.Fatalf("a not-exist Stat error must still be skipped, got %v", err)
	}
	if err := trustedPath(filepath.Join(a, "absent"), "a program"); err != nil {
		t.Fatalf("a missing program must still be skipped, got %v", err)
	}
}

// A path a component of which is a regular file (ENOTDIR) cannot be resolved
// by anyone, so it is judged like a missing path — as far as it exists — and
// not refused as unjudgeable: ENOTDIR is not fs.ErrNotExist in Go's mapping,
// so it is named explicitly (cli#2673 review F2).
func TestAPathThroughARegularFileIsJudgedAsMissing2668(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	// A regular file where a directory would need to be: the changed launch
	// program names a path through it, absolute so the relative refusal
	// (another arm) does not fire first.
	if err := os.WriteFile(filepath.Join(fx.dir, "blocker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocked := fx.dir + "/blocker/prog" // by concatenation: filepath.Join would clean
	fx.edit(strings.Replace(reloadBase, "[claude-worker]", "["+blocked+"]", 1))
	if ev := fx.kinds("config-reload-refused"); len(ev) != 0 {
		t.Fatalf("a path through a regular file must be judged as missing, not refused, got %+v", ev)
	}
	if w, _ := fx.p.config().Worker("iris"); w.Launch[0] != blocked {
		t.Fatalf("the reload must apply the entry (it fails at use, as before), got %v", w.Launch)
	}
}

// ---- item 4: a relative launch, end or recover program refuses the reload ----

// A program the changed entries name as a relative path (a slash, not
// absolute) cannot be judged — it runs from the worker's directory, which the
// walk cannot know — so the reload is refused naming it, whether or not the
// file happens to exist in the poller's directory.
func TestARelativeProgramIsRefusedOnReload2668(t *testing.T) {
	cases := []struct{ key, name, extra string }{
		{"launch", "./prog", ""},
		{"end", "./end-prog", "    launch: [claude-worker]\n"},
		{"recover", "./rec-prog", "    launch: [claude-worker]\n"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			fx := newReloadFixture(t)
			fx.step()
			line := "  billie:\n    family: claude\n" + tc.extra
			if tc.key == "launch" {
				line += "    launch: [" + tc.name + "]\n"
			} else {
				line += "    " + tc.key + ": [" + tc.name + "]\n"
			}
			fx.edit(reloadBase + line)
			ev := fx.kinds("config-reload-refused")
			if len(ev) != 1 || !strings.Contains(ev[0].Detail, tc.name) || !strings.Contains(ev[0].Detail, "relative path") {
				t.Fatalf("want one refusal naming %q as a relative path, got %+v", tc.name, ev)
			}
			if _, ok := fx.p.config().Worker("billie"); ok {
				t.Fatal("a worker naming a relative program was applied")
			}
		})
	}
}

// The refusal does not depend on the file being absent from the poller's
// directory: even with it present and clean there, the poller would run a
// different file (the worker's directory's), so the reload still refuses.
func TestARelativeProgramIsRefusedEvenWhereItResolves2668(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	if err := os.WriteFile(filepath.Join(fx.dir, "prog"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(fx.dir)
	fx.edit(reloadBase + "  billie:\n    family: claude\n    launch: [./prog]\n")
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "./prog") || !strings.Contains(ev[0].Detail, "relative path") {
		t.Fatalf("want one refusal naming ./prog as a relative path, got %+v", ev)
	}
}

// The relative refusal covers the account program too: it is argv[0] of the
// worker's command (LaunchArgv), run from the worker's directory like launch,
// end and recover. An account a worker references has its form checked at
// load (validateOrcaWorker refuses it before the walk runs), but an account
// no worker references is not validated — its program is still named by the
// changed entries, so a relative bin is refused on reload naming it (cli#2673
// review F1).
func TestARelativeAccountProgramIsRefusedOnReload2668(t *testing.T) {
	start := reloadBase + "account_profiles:\n  spare: {bin: /usr/bin/spare-claude}\n"
	fx := newReloadFixtureFrom(t, start)
	fx.step()
	fx.edit(reloadBase + "account_profiles:\n  spare: {bin: ./sub/tool}\n")
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "./sub/tool") || !strings.Contains(ev[0].Detail, "relative path") {
		t.Fatalf("want one refusal naming ./sub/tool as a relative path, got %+v", ev)
	}
	if a, ok := fx.p.config().Accounts["spare"]; !ok || a.Bin != "/usr/bin/spare-claude" {
		t.Fatalf("a reload naming a relative account program was applied: %+v", a)
	}
}

// An absolute program is judged where it points, as before, and a clean one is
// accepted.
func TestACleanAbsoluteProgramIsStillAcceptedOnReload2668(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	prog := filepath.Join(fx.dir, "cleanlauncher")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fx.edit(strings.Replace(reloadBase, "[claude-worker]", "["+prog+"]", 1))
	if ev := fx.kinds("config-reload-refused"); len(ev) != 0 {
		t.Fatalf("a clean absolute program must not be refused, got %+v", ev)
	}
	if w, _ := fx.p.config().Worker("iris"); w.Launch[0] != prog {
		t.Fatalf("the clean absolute program was not applied: %v", w.Launch)
	}
}

// The refusal separates program names from directory names in namedFiles: a
// relative disk_tmp a changed worker names is a directory, judged by the
// directory rules (missing here, so skipped), not refused as a relative
// program — the slash in it is what tells the two apart, so the pin uses one
// (cli#2668 gate point 1; kills the directories-as-programs mutant).
func TestARelativeDiskTmpIsNotRefusedAsAProgram2668(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	// The relative directory resolves against the process's working
	// directory, so pin it to the fixture's own (tight) directory: judged on
	// its merits, not on whatever mode the checkout happens to carry.
	t.Chdir(fx.dir)
	fx.edit(reloadBase + "  billie:\n    family: claude\n    launch: [claude-worker]\n    disk_tmp: sub/tmp\n")
	if ev := fx.kinds("config-reload-refused"); len(ev) != 0 {
		t.Fatalf("a relative disk_tmp must not be refused as a relative program, got %+v", ev)
	}
	if w, _ := fx.p.config().Worker("billie"); w.DiskTmp != "sub/tmp" {
		t.Fatalf("the reload must apply the relative disk_tmp as written, got %q", w.DiskTmp)
	}
}

// A relative program on a worker the edit did not change is not judged again:
// only the changed entries are.
func TestARelativeProgramOnAnUnchangedWorkerIsNotRejudged2668(t *testing.T) {
	start := reloadBase + "  billie:\n    family: claude\n    launch: [./billie-prog]\n"
	fx := newReloadFixtureFrom(t, start)
	fx.step()
	fx.edit(start + "  carl:\n    family: claude\n    launch: [claude-worker]\n")
	if ev := fx.kinds("config-reload-refused"); len(ev) != 0 {
		t.Fatalf("an unchanged worker's program must not be re-judged, got %+v", ev)
	}
	if fx.registered() != "billie,carl,iris" {
		t.Fatalf("the unrelated edit was not applied: %q", fx.registered())
	}
}

// ---- item 8: start and reload use the identical un-cleaned path ----

// linkedConfigSite builds root/real/sub with root/link -> real/sub, a valid
// root/real/host.yaml written through the fixture placeholders, and a
// different valid root/host.yaml, so a start path root/link/../host.yaml
// reads the first at start while a lexical clean would read the second.
func linkedConfigSite(t *testing.T) (root, startPath string) {
	t.Helper()
	root = trustTempDir(t)
	if err := os.MkdirAll(filepath.Join(root, "real", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("real", "sub"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "host.key"), []byte(hostKey), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "clone"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAt := func(path, label string) {
		text := strings.NewReplacer("%KEY%", filepath.Join(root, "host.key"), "%ROOT%", root).Replace(reloadBase)
		if err := os.WriteFile(path, []byte(strings.Replace(text, "label: box1", "label: "+label, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	real := filepath.Join(root, "real", "host.yaml")
	writeAt(real, "box1")
	twin := filepath.Join(root, "host.yaml")
	writeAt(twin, "box2")
	// The start path is built by concat, not filepath.Join: Join would clean
	// the ".." away lexically, which is exactly the behaviour under test.
	return root, root + "/link/../host.yaml"
}

// A start path with ".." after a symlink reads the same file on reload as at
// start: sourcePath keeps the ".." so the kernel, not filepath.Clean, picks
// the file.
func TestAStartPathWithDotDotAfterALinkReadsTheSameFileOnReload2668(t *testing.T) {
	_, startPath := linkedConfigSite(t)
	cfg, err := LoadConfig(startPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := readConfigFile(cfg.sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); sum != cfg.sourceHash {
		t.Fatalf("reload would read a different file than start: %s", cfg.sourcePath)
	}
}

// A poller started through that path, with the file never touched, logs
// nothing on its polls: it keeps reading the file it started from, not the
// lexical twin.
func TestAPollerStartedThroughALinkedPathDoesNotReloadItsTwin2668(t *testing.T) {
	root, startPath := linkedConfigSite(t)
	fx := newReloadFixtureAtPath(t, startPath, root)
	fx.step()
	fx.step()
	if !fx.quiet() {
		t.Fatalf("an untouched config must reload nothing, got %+v", fx.allEvents())
	}
}

// The trust walk judges the directories the kernel reaches through that path:
// with root/real loose, the refusal names root/real, not the lexical parent
// root.
func TestTheTrustWalkJudgesTheKernelDirectoriesOfTheConfigPath2668(t *testing.T) {
	root, startPath := linkedConfigSite(t)
	if err := os.Chmod(filepath.Join(root, "real"), 0o777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "real"), 0o755) })
	fx := newReloadFixtureAtPath(t, startPath, root)
	fx.step()
	fx.edit(reloadBase + billie)
	real := filepath.Join(root, "real")
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, real) || !strings.Contains(ev[0].Detail, "writable by group or others") {
		t.Fatalf("want one refusal naming %q, got %+v", real, ev)
	}
	// The refusal names the kernel-visited directory only: the lexical parent
	// of the written path (root, Dir of root/link/../host.yaml after cleaning)
	// is tight, so it must not appear as the refused directory — if a rewrite
	// ever judges lexically, this is the confusion it would make (cli#2673
	// review F7).
	if strings.Contains(ev[0].Detail, "the directory "+root+" on the path") {
		t.Fatalf("the refusal names the lexical parent %q as the refused directory: %s", root, ev[0].Detail)
	}
	if _, ok := fx.p.config().Worker("billie"); ok {
		t.Fatal("a reload through a loose kernel-visited directory was applied")
	}
}

// A relative start path stays valid after a change of directory, and the
// stored path is the working directory plus the written path, ".." kept.
func TestARelativeConfigPathStaysValidAfterAChdir2668(t *testing.T) {
	root, _ := linkedConfigSite(t)
	t.Chdir(root)
	cfg, err := LoadConfig("real/../host.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// The kernel resolves real/.. back to root, so the file read is the twin
	// root/host.yaml — and the stored path keeps the ".." as written, joined
	// to the working directory by a slash, not cleaned.
	want := root + "/real/../host.yaml"
	if cfg.sourcePath != want {
		t.Fatalf("sourcePath = %q, want the un-cleaned %q", cfg.sourcePath, want)
	}
	t.Chdir(filepath.Join(root, "real", "sub"))
	raw, _, err := readConfigFile(cfg.sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); sum != cfg.sourceHash {
		t.Fatal("a changed working directory made the reload read another file")
	}
}

// newReloadFixtureAtPath is newReloadFixtureFrom for a config at an arbitrary
// path (the fixture writes and reloads through fx.path).
func newReloadFixtureAtPath(t *testing.T, path, root string) *reloadFixture {
	t.Helper()
	fx := &reloadFixture{t: t, path: path, dir: root}
	fx.write(reloadBase)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := LoadState(cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	fx.srv = &fakeServer{notDone: map[string]int{}, endedOnce: map[string]bool{}}
	fx.host = &fakeHost{trees: map[string]WorktreeState{}}
	fx.p = New(cfg, fx.srv, fx.host, st, hostKey, func(e Event) {
		fx.mu.Lock()
		fx.events = append(fx.events, e)
		fx.mu.Unlock()
	})
	return fx
}

func (fx *reloadFixture) allEvents() []Event {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return append([]Event{}, fx.events...)
}
