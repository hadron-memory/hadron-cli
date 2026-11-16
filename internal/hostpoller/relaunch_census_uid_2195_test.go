//go:build linux

package hostpoller

import (
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The reporter's raw proc entries were not retained. These synthetic entries
// reproduce the reported unreadable-cwd/shifted-UID boundary; live acceptance
// remains a separate gate.
func TestCensusUnreadableCwdUIDBoundary2195(t *testing.T) {
	foreign := os.Geteuid() + 1000000
	for _, tc := range []struct {
		name, status string
		skip         bool
	}{
		{"foreign", fmt.Sprintf("Uid:\t%d\t%d\t%d\t%d\n", foreign, foreign, foreign, foreign), true},
		{"missing status", "", false},
		{"malformed", "Uid:\t1000000\tbroken\t1000000\t1000000\n", false},
		{"missing field", "Uid:\t1000000\t1000000\t1000000\n", false},
		{"duplicate", "Uid:\t1000000\t1000000\t1000000\t1000000\nUid:\t1000000\t1000000\t1000000\t1000000\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc, work := t.TempDir(), t.TempDir()
			dir := censusFixture2060(t, proc, work, 41)
			censusFixture2060(t, proc, work, 42)
			if err := os.Remove(filepath.Join(dir, "cwd")); err != nil {
				t.Fatal(err)
			}
			if tc.status != "" {
				if err := os.WriteFile(filepath.Join(dir, "status"), []byte(tc.status), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := strictRelaunchCensus(context.Background(), proc, work, func(context.Context) error { return nil })
			if tc.skip {
				if err != nil || len(got) != 1 || got[0].PID != 42 {
					t.Fatalf("foreign exclusion: agents=%+v err=%v", got, err)
				}
			} else if err == nil {
				t.Fatal("unknown ownership proved absence")
			}
		})
	}
	for _, uid := range []int{0, os.Geteuid()} {
		for field := 0; field < 4; field++ {
			t.Run(fmt.Sprintf("protected-%d-field-%d", uid, field), func(t *testing.T) {
				proc, work := t.TempDir(), t.TempDir()
				dir := censusFixture2060(t, proc, work, 41)
				if err := os.Remove(filepath.Join(dir, "cwd")); err != nil {
					t.Fatal(err)
				}
				fields := []string{fmt.Sprint(foreign), fmt.Sprint(foreign), fmt.Sprint(foreign), fmt.Sprint(foreign)}
				fields[field] = fmt.Sprint(uid)
				if err := os.WriteFile(filepath.Join(dir, "status"), []byte("Uid:\t"+strings.Join(fields, "\t")+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := strictRelaunchCensus(context.Background(), proc, work, func(context.Context) error { return nil }); err == nil {
					t.Fatal("same-uid/root unreadable cwd accepted")
				}
			})
		}
	}
}

func TestCensusForeignSkipIdentityAndDisappearance2195(t *testing.T) {
	for _, mode := range []string{"stable", "reused", "gone"} {
		t.Run(mode, func(t *testing.T) {
			proc, work := t.TempDir(), t.TempDir()
			dir := censusFixture2060(t, proc, work, 41)
			censusFixture2060(t, proc, work, 42)
			if err := os.Remove(filepath.Join(dir, "cwd")); err != nil {
				t.Fatal(err)
			}
			status := filepath.Join(dir, "status")
			if err := unix.Mkfifo(status, 0600); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				// Opening the writer proves the census reached status after its first stat.
				f, err := os.OpenFile(status, os.O_WRONLY, 0)
				if err != nil {
					done <- err
					return
				}
				defer func() { _ = f.Close() }()
				if mode == "reused" {
					err = os.WriteFile(filepath.Join(dir, "stat"), []byte("41 (claude) S 2 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 456"), 0600)
				}
				if mode == "gone" {
					err = os.RemoveAll(dir)
				}
				if err == nil {
					u := os.Geteuid() + 1000000
					_, err = fmt.Fprintf(f, "Uid:\t%d\t%d\t%d\t%d\n", u, u, u, u)
				}
				done <- err
			}()
			skipped := 0
			got, err := strictRelaunchCensusCounted(context.Background(), proc, work, func(context.Context) error { return nil }, &skipped)
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			if mode == "reused" {
				if err == nil {
					t.Fatal("foreign skip bypassed stat-twice identity check")
				}
				if skipped != 0 {
					t.Fatal("partial census count escaped")
				}
				return
			}
			want := 0
			if mode == "stable" {
				want = 1
			}
			if err != nil || len(got) != 1 || got[0].PID != 42 || skipped != want {
				t.Fatalf("agents=%+v skipped=%d err=%v", got, skipped, err)
			}
		})
	}
}
