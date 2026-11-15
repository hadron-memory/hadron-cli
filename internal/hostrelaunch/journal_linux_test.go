//go:build linux

package hostrelaunch

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJournalPersistsGenerationAttemptsAndExcludesConcurrentRuns2060(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "relaunch")
	s, err := OpenJournal(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Sweep() != 1 {
		t.Fatal(s.Sweep())
	}
	if _, err := OpenJournal(dir, false); err == nil {
		t.Fatal("concurrent writer admitted")
	}
	at := time.Now().UTC()
	rec := Record{AbsentFor: "proof", AbsentSweep: 1, Attempts: []time.Time{at}}
	if err := s.Save("worker", rec); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenJournal(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Load("worker")
	if err != nil || s.Sweep() != 2 || len(got.Attempts) != 1 || !got.Attempts[0].Equal(at) || got.AbsentSweep != 1 {
		t.Fatalf("%+v %v sweep=%d", got, err, s.Sweep())
	}
}

func TestJournalDryRunCreatesAndChangesNothing2060(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	s, err := OpenJournal(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if s.Sweep() != 0 {
		t.Fatal(s.Sweep())
	}
	if err := s.Save("w", Record{}); err == nil {
		t.Fatal("dry write admitted")
	}
	s.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("dry run created directory", err)
	}
	s, err = OpenJournal(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	before, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err = OpenJournal(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	after, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("dry run changed journal", err)
	}
}

func TestJournalRefusesCorruptionAndLinks2060(t *testing.T) {
	for _, kind := range []string{"corrupt", "directory-link", "state-link", "lock-link", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "relaunch")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "corrupt":
				if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"version":1,"sweep":2,"records":null}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory-link":
				if err := os.Remove(dir); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(parent, dir); err != nil {
					t.Fatal(err)
				}
			case "state-link", "lock-link":
				name := "state.json"
				if kind == "lock-link" {
					name = "run.lock"
				}
				if err := os.Symlink(filepath.Join(parent, "outside"), filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				makeJournalFIFO(t, filepath.Join(dir, "state.json"))
			}
			if s, err := OpenJournal(dir, false); err == nil {
				s.Close()
				t.Fatal("unsafe journal accepted")
			}
		})
	}
}

func makeJournalFIFO(t *testing.T, p string) {
	t.Helper()
	if err := unix.Mkfifo(p, 0600); err != nil {
		t.Fatal(err)
	}
}
