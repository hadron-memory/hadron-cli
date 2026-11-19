//go:build !windows

package hostpoller

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The owner half of the trust check cannot be shown with real files (a test
// does not run as root), so it is pinned through the FileInfo it reads.
type ownedInfo struct {
	uid  uint32
	mode os.FileMode
}

func (o ownedInfo) Name() string       { return "host.yaml" }
func (o ownedInfo) Size() int64        { return 1 }
func (o ownedInfo) Mode() os.FileMode  { return o.mode }
func (o ownedInfo) ModTime() time.Time { return time.Time{} }
func (o ownedInfo) IsDir() bool        { return false }
func (o ownedInfo) Sys() any           { return &syscall.Stat_t{Uid: o.uid} }

func TestTrustJudgesTheOwnerAndTheMode2623(t *testing.T) {
	me := uint32(os.Geteuid())
	for _, tc := range []struct {
		name string
		info ownedInfo
		want string // substring of the refusal, "" for trusted
	}{
		{"own file, 0600", ownedInfo{me, 0o600}, ""},
		{"own file, 0644", ownedInfo{me, 0o644}, ""},
		{"root's file", ownedInfo{0, 0o644}, ""},
		{"another user's file", ownedInfo{me + 1, 0o600}, "is owned by uid"},
		{"group-writable", ownedInfo{me, 0o620}, "writable by group or others"},
		{"world-writable only", ownedInfo{me, 0o602}, "writable by group or others"},
		{"root's but world-writable", ownedInfo{0, 0o666}, "writable by group or others"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := trustedInfo(tc.info, "/etc/host.yaml", "the configuration file")
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("refused a trusted file: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestAChangedRepoDirectoryOthersCanWriteIsRefused2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	loose := filepath.Join(fx.dir, "loose")
	if err := os.Mkdir(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o777); err != nil {
		t.Fatal(err)
	}
	fx.edit(strings.Replace(reloadBase, "acme/repo: %ROOT%/clone", "acme/repo: "+loose, 1))
	if got, _ := fx.p.config().Repo("acme/repo"); got == loose {
		t.Fatal("a world-writable clone directory was applied")
	}
	if ev := fx.kinds("config-reload-refused"); len(ev) != 1 || !strings.Contains(ev[0].Detail, "writable by group or others") || !strings.Contains(ev[0].Detail, loose) {
		t.Fatalf("got %+v", ev)
	}
}

func TestAChangedAccountProgramOthersCanWriteIsRefused2623(t *testing.T) {
	// A reload cannot start applying an Orca-launched worker unless the poller
	// started with one, so start with a worker that launches through Orca and
	// change its account's program.
	prog := func(dir, name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	fx := newReloadFixture(t)
	start := strings.Replace(reloadBase+reloadOrcaWorker, "{bin: claude-holger}", "{bin: "+prog(fx.dir, "good", 0o755)+"}", 1)
	fx = newReloadFixtureFrom(t, strings.ReplaceAll(start, fx.dir, "%ROOT%"))
	fx.step()
	loose := prog(fx.dir, "loose", 0o777)
	fx.edit(strings.Replace(reloadBase+reloadOrcaWorker, "{bin: claude-holger}", "{bin: "+loose+"}", 1))
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "writable by group or others") || !strings.Contains(ev[0].Detail, loose) {
		t.Fatalf("want a refusal naming the account program, got %+v", ev)
	}
}

// ---- each arm of the guard has a test that fails if the arm is removed ----

// looseProgram is an executable anyone can write.
func looseProgram(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o777); err != nil { // beyond the process umask
		t.Fatal(err)
	}
	return p
}

// looseDir is a directory anyone can write; sticky adds the sticky bit.
func looseDir(t *testing.T, parent, name string, sticky bool) string {
	t.Helper()
	d := filepath.Join(parent, name)
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o777)
	if sticky {
		mode |= os.ModeSticky
	}
	if err := os.Chmod(d, mode); err != nil {
		t.Fatal(err)
	}
	return d
}

func (fx *reloadFixture) wantRefusedNaming(prog string) {
	fx.t.Helper()
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, prog) || !strings.Contains(ev[0].Detail, "writable by group or others") {
		fx.t.Fatalf("want one refusal naming %q, got %+v", prog, ev)
	}
}

func TestAModifiedWorkersNewLaunchProgramIsJudged2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	prog := looseProgram(t, fx.dir, "newlauncher")
	fx.edit(strings.Replace(reloadBase, "[claude-worker]", "["+prog+"]", 1))
	fx.wantRefusedNaming(prog)
	if w, _ := fx.p.config().Worker("iris"); strings.Contains(strings.Join(w.Launch, " "), "newlauncher") {
		t.Fatal("the modified worker's loose launch program was applied")
	}
}

func TestAnEndOrRecoverProgramIsJudged2623(t *testing.T) {
	for _, key := range []string{"end", "recover"} {
		t.Run(key, func(t *testing.T) {
			fx := newReloadFixture(t)
			fx.step()
			prog := looseProgram(t, fx.dir, key+"-prog")
			fx.edit(reloadBase + billie + "    " + key + ": [" + prog + "]\n")
			fx.wantRefusedNaming(prog)
		})
	}
}

// A bare program name is resolved through PATH and the program found is the
// one judged.
func TestABareProgramNameIsJudgedWhereItResolves2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	bin := filepath.Join(fx.dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	prog := looseProgram(t, bin, "pathlauncher")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	fx.edit(reloadBase + "  billie:\n    family: claude\n    launch: [pathlauncher]\n")
	fx.wantRefusedNaming(prog)
}

func TestADirectoryAWorkerNamesForItsAgentIsJudged2623(t *testing.T) {
	for _, key := range []string{"codex_home", "claude_config_dir", "disk_tmp"} {
		t.Run(key, func(t *testing.T) {
			fx := newReloadFixture(t)
			fx.step()
			d := looseDir(t, fx.dir, key, false)
			fx.edit(reloadBase + billie + "    " + key + ": " + d + "\n")
			fx.wantRefusedNaming(d)
			if _, ok := fx.p.config().Worker("billie"); ok {
				t.Fatal("a worker naming a loose directory was applied")
			}
		})
	}
}

func TestAChangedAccountsCodexHomeIsJudged2623(t *testing.T) {
	start := reloadBase + reloadOrcaWorker
	fx := newReloadFixtureFrom(t, start)
	fx.step()
	d := looseDir(t, fx.dir, "codex-home", false)
	fx.edit(strings.Replace(start, "{bin: claude-holger}", "{bin: claude-holger, codex_home: "+d+"}", 1))
	fx.wantRefusedNaming(d)
}

// Someone who can write a directory above a program can replace the program
// without touching its mode: the directories above are judged too.
func TestAProgramInADirectoryOthersCanWriteIsRefused2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	d := looseDir(t, fx.dir, "shared", false)
	prog := filepath.Join(d, "launcher")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\n"), 0o755); err != nil { // the program itself is tight
		t.Fatal(err)
	}
	fx.edit(reloadBase + "  billie:\n    family: claude\n    launch: [" + prog + "]\n")
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "not sticky") || !strings.Contains(ev[0].Detail, d) {
		t.Fatalf("want a refusal naming the writable directory, got %+v", ev)
	}
}

func TestAStickyDirectoryAboveAProgramIsAccepted2623(t *testing.T) {
	fx := newReloadFixture(t)
	fx.step()
	d := looseDir(t, fx.dir, "sticky", true) // like /tmp: others may add entries, not replace ours
	prog := filepath.Join(d, "launcher")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fx.edit(reloadBase + "  billie:\n    family: claude\n    launch: [" + prog + "]\n")
	if got := fx.registered(); got != "billie,iris" {
		t.Fatalf("registration = %q: a sticky directory above a tight program is acceptable", got)
	}
}

func TestTheDirectoriesAboveAPathAreJudgedIncludingOnesThatDoNotExistYet2623(t *testing.T) {
	root := trustTempDir(t)
	open := looseDir(t, root, "open", false)
	if err := trustedAncestors(filepath.Join(open, "not", "yet", "there"), "x"); err == nil || !strings.Contains(err.Error(), "not sticky") {
		t.Fatalf("a path that does not exist yet under a writable directory: %v", err)
	}
	sticky := looseDir(t, root, "sticky", true)
	if err := trustedAncestors(filepath.Join(sticky, "child"), "x"); err != nil {
		t.Fatalf("a sticky parent: %v", err)
	}
	// A symlink in the way is resolved: the directories judged are the real ones.
	link := filepath.Join(root, "link")
	if err := os.Symlink(open, link); err != nil {
		t.Fatal(err)
	}
	if err := trustedAncestors(filepath.Join(link, "prog"), "x"); err == nil || !strings.Contains(err.Error(), open) {
		t.Fatalf("a path through a symlink to a writable directory: %v", err)
	}
}

func TestADirectoryOnThePathOwnedByAnotherUserIsRefused2623(t *testing.T) {
	me := uint32(os.Geteuid())
	for _, tc := range []struct {
		name string
		info ownedInfo
		bad  bool
	}{
		{"own, tight", ownedInfo{me, os.ModeDir | 0o755}, false},
		{"root's, tight", ownedInfo{0, os.ModeDir | 0o755}, false},
		{"another user's", ownedInfo{me + 1, os.ModeDir | 0o755}, true},
		{"root's, sticky and world-writable (like /tmp)", ownedInfo{0, os.ModeDir | os.ModeSticky | 0o777}, false},
		{"own, writable and not sticky", ownedInfo{me, os.ModeDir | 0o775}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := trustedDirInfo(tc.info, "/d", "/d/prog", "a program")
			if tc.bad != (err != nil) {
				t.Fatalf("err = %v, bad = %v", err, tc.bad)
			}
		})
	}
}

// The directories above the configuration file are judged like those above a
// program: someone who can write one can replace the file without touching its
// mode.
func TestAConfigFileInADirectoryOthersCanWriteIsRefused2623(t *testing.T) {
	fx := newReloadFixture(t)
	d := looseDir(t, fx.dir, "cfg", false)
	path := filepath.Join(d, "host.yaml")
	text := strings.NewReplacer("%KEY%", filepath.Join(fx.dir, "host.key"), "%ROOT%", fx.dir).Replace(reloadBase)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil { // the file itself is tight
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	fx.path = path
	fx.p = New(cfg, fx.srv, fx.host, fx.p.state, hostKey, func(e Event) {
		fx.mu.Lock()
		fx.events = append(fx.events, e)
		fx.mu.Unlock()
	})
	fx.step()
	fx.edit(text + billie)
	fx.step()
	if got := fx.registered(); got != "iris" {
		t.Fatalf("a config in a world-writable directory was applied: %q", got)
	}
	ev := fx.kinds("config-reload-refused")
	if len(ev) != 1 || !strings.Contains(ev[0].Detail, "not sticky") || !strings.Contains(ev[0].Detail, d) {
		t.Fatalf("want one refusal naming the directory, got %+v", ev)
	}
}
