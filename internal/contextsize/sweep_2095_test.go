package contextsize

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCodexAttributionAndUsageShareTheOpenedFile2095(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("controlled replacement of an open file is exercised on Unix")
	}
	wt := t.TempDir()
	root := t.TempDir()
	path := filepath.Join(root, "rollout-a.jsonl")
	write(t, path, codexMeta(wt)+codexTok("2026-11-15T10:00:00Z", 42, 42))
	old := openContextFile
	defer func() { openContextFile = old }()
	replaced := false
	openContextFile = func(p string) (*os.File, int64, error) {
		f, n, err := old(p)
		if err == nil && p == path && !replaced {
			replaced = true
			if err := os.Rename(path, path+".old"); err != nil {
				_ = f.Close()
				t.Fatal(err)
			}
			write(t, path, codexMeta(t.TempDir())+codexTok("2026-11-15T10:01:00Z", 999, 999))
		}
		return f, n, err
	}
	got := Measure("codex", []string{wt}, Roots{CodexSessions: root}, now)
	if !replaced {
		t.Fatal("replacement boundary was never exercised")
	}
	if got == nil || got.Tokens != 42 {
		t.Fatalf("must use metadata and usage from original descriptor, got %+v", got)
	}
}

func TestCodexArchiveDiscoveryIsOncePerRootPerSweep2095(t *testing.T) {
	const files = 1200
	root := t.TempDir()
	roots := Roots{CodexSessions: root}
	trees := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	for i := 0; i < files; i++ {
		wt := "/unrelated"
		if i < len(trees) {
			wt = trees[i]
		}
		p := filepath.Join(root, "2026", "11", "15", fmt.Sprintf("rollout-%04d.jsonl", i))
		write(t, p, codexMeta(wt)+codexTok("2026-11-15T10:00:00Z", i+1, i+1))
		at := now.Add(-time.Duration(i) * time.Second)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	oldWalk := walkCodexSessions
	defer func() { walkCodexSessions = oldWalk }()
	walks, visits := 0, 0
	walkCodexSessions = func(root string, fn fs.WalkDirFunc) error {
		walks++
		return oldWalk(root, func(p string, d fs.DirEntry, err error) error { visits++; return fn(p, d, err) })
	}
	sweep := &Measurer{}
	for i, tree := range trees {
		got := sweep.Measure("codex", []string{tree}, roots, now)
		if got == nil || got.Tokens != i+1 {
			t.Fatalf("worker %d: %+v", i, got)
		}
	}
	if walks != 1 || visits > files+4 {
		t.Fatalf("archive traversed %d times, %d visits; want one traversal of at most %d entries", walks, visits, files+4)
	}
	// Another root is discovered independently, and another sweep sees new files.
	other := t.TempDir()
	p := filepath.Join(other, "rollout-other.jsonl")
	write(t, p, codexMeta(trees[0])+codexTok("2026-11-15T10:01:00Z", 77, 77))
	if got := sweep.Measure("codex", trees[:1], Roots{CodexSessions: other}, now); got == nil || got.Tokens != 77 {
		t.Fatalf("other root: %+v", got)
	}
	fresh := filepath.Join(root, "rollout-new.jsonl")
	write(t, fresh, codexMeta(trees[0])+codexTok("2026-11-15T10:02:00Z", 88, 88))
	at := now.Add(time.Second)
	if err := os.Chtimes(fresh, at, at); err != nil {
		t.Fatal(err)
	}
	if got := (&Measurer{}).Measure("codex", trees[:1], roots, now); got == nil || got.Tokens != 88 {
		t.Fatalf("next sweep did not rediscover newest rollout: %+v", got)
	}
	if walks != 3 {
		t.Fatalf("wanted one discovery per root/sweep, got %d", walks)
	}
}
