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
