//go:build linux

package hostpoller

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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
		{"no Uid line", "Name:\tclaude\nGid:\t1000000\t1000000\t1000000\t1000000\n", false},
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
			foreignSkipped := 0
			got, err := strictRelaunchCensusCounted(context.Background(), proc, work, func(context.Context) error { return nil }, &foreignSkipped)
			if tc.skip {
				if err != nil || len(got) != 1 || got[0].PID != 42 || foreignSkipped != 1 {
					t.Fatalf("foreign exclusion: agents=%+v err=%v", got, err)
				}
			} else {
				if err == nil {
					t.Fatal("unknown ownership proved absence")
				}
				if foreignSkipped != 0 {
					t.Fatalf("partial foreign count published: %d", foreignSkipped)
				}
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
				foreignSkipped := 0
				if _, err := strictRelaunchCensusCounted(context.Background(), proc, work, func(context.Context) error { return nil }, &foreignSkipped); err == nil {
					t.Fatal("same-uid/root unreadable cwd accepted")
				}
				if foreignSkipped != 0 {
					t.Fatalf("protected uid published count: %d", foreignSkipped)
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
			ctx := context.Background()
			if os.Getenv("HADRON_TEST_FIFO_EARLY_REFUSAL_2197") == "1" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			got, err := strictRelaunchCensusCounted(ctx, proc, work, func(context.Context) error { return nil }, &skipped)
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

// A subprocess keeps a broken fixture from consuming the package timeout.
// The child intentionally fails; its bounded refusal and successful cleanup
// are the expected evidence, rather than a successful census.
func TestCensusFIFORefusalIsBounded2197(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCensusForeignSkipIdentityAndDisappearance2195$/^stable$", "-test.count=1")
	cmd.Env = append(os.Environ(), "HADRON_TEST_FIFO_EARLY_REFUSAL_2197=1")
	began := time.Now()
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("FIFO refusal exceeded subprocess watchdog: %v: %s", ctx.Err(), out)
	}
	if err == nil {
		t.Fatal("early refusal child unexpectedly succeeded")
	}
	if elapsed := time.Since(began); elapsed > 3*time.Second {
		t.Fatalf("early refusal took %s, want within 3s: %s", elapsed, out)
	}
	if !strings.Contains(string(out), "FIFO writer did not open before deadline") {
		t.Fatalf("wrong refusal: %s", out)
	}
	if strings.Contains(string(out), "FIFO cleanup") {
		t.Fatalf("cleanup failed to unblock writer: %s", out)
	}
}
