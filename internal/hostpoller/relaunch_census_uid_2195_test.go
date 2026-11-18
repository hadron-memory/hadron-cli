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
			// Pin the identity before the writer changes stat/removes the fixture.
			initial, readErr := os.ReadFile(filepath.Join(dir, "stat"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			parent, start, valid := parseStatLine(string(initial))
			if !valid || parent != 2 || start != 123 {
				t.Fatalf("unexpected first-sweep fixture identity: %q", initial)
			}
			opened := make(chan struct{}, 1)
			finished := make(chan struct{})
			censusFinished := make(chan struct{})
			resumeWriter := make(chan struct{})
			done := make(chan error, 1)
			t.Cleanup(func() {
				close(resumeWriter)
				select {
				case <-finished:
				default:
					// Holding both ends open releases a writer blocked before the reader.
					unblock, openErr := os.OpenFile(status, os.O_RDWR|unix.O_NONBLOCK, 0)
					select {
					case <-finished:
					case <-time.After(fifoWait2197):
						t.Error("FIFO cleanup writer did not exit")
					}
					if openErr == nil {
						_ = unblock.Close()
					}
				}
				select {
				case <-censusFinished:
				case <-time.After(fifoWait2197):
					t.Error("FIFO cleanup census did not exit")
				}
			})
			go func() {
				defer close(finished)
				// Opening the writer proves the census reached status after its first stat.
				f, err := os.OpenFile(status, os.O_WRONLY, 0)
				if err != nil {
					done <- err
					return
				}
				defer func() { _ = f.Close() }()
				opened <- struct{}{}
				if fifoSwitch2197("HADRON_TEST_FIFO_STALL_2197") {
					<-resumeWriter
				}
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
			foreignSkipped := 0
			ctx := context.Background()
			if fifoSwitch2197("HADRON_TEST_FIFO_EARLY_REFUSAL_2197") {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			type censusResult struct {
				agents []WorktreeAgent
				err    error
				count  int
			}
			result := make(chan censusResult, 1)
			go func() {
				defer close(censusFinished)
				count := 0
				agents, err := strictRelaunchCensusCounted(ctx, proc, work, func(context.Context) error { return nil }, &count)
				result <- censusResult{agents, err, count}
			}()
			select {
			case <-opened:
			case <-time.After(fifoWait2197):
				t.Fatal("FIFO writer did not open before deadline")
			}
			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(fifoWait2197):
				t.Fatal("FIFO writer did not complete before deadline")
			}
			var observed censusResult
			select {
			case observed = <-result:
			case <-time.After(fifoWait2197):
				t.Fatal("FIFO census did not complete before deadline")
			}
			got, err := observed.agents, observed.err
			foreignSkipped = observed.count
			if mode == "reused" {
				if err == nil {
					t.Fatal("foreign skip bypassed stat-twice identity check")
				}
				if foreignSkipped != 0 {
					t.Fatal("partial census count escaped")
				}
				return
			}
			want := 0
			if mode == "stable" {
				want = 1
			}
			if err != nil || len(got) != 1 || got[0].PID != 42 || foreignSkipped != want {
				t.Fatalf("agents=%+v foreignSkipped=%d err=%v", got, foreignSkipped, err)
			}
		})
	}
}

// The FIFO subprocess budget (cli#2225, acceptance changed explicitly): one
// in-child wait, plus headroom for process start and scheduling on a loaded
// host, under a hard watchdog. The old gate was 2s waits, completion within 3s
// and a 5s watchdog, which left one second for the child to start. The failure
// proof stays bounded: the child must still refuse inside the budget, and the
// watchdog kills a child that does not. TestFIFOBudgetAcceptance2225 pins these.
const (
	fifoWait2197        = 2 * time.Second
	fifoChildBudget2197 = 6 * time.Second
	fifoWatchdog2197    = 10 * time.Second
)

// fifoSwitch2197 reports a scenario switch only inside a child started with the
// child-mode guard, so a stray variable in a parent environment cannot alter
// the shared ordinary fixture test.
func fifoSwitch2197(name string) bool {
	return os.Getenv("HADRON_TEST_FIFO_CHILD_2197") == "1" && os.Getenv(name) == "1"
}

// runFIFOChild2197 runs the stable fixture in a child with the child-mode guard
// and exactly one scenario switch; every inherited scenario switch is scrubbed.
func runFIFOChild2197(t *testing.T, scenario string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fifoWatchdog2197)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCensusForeignSkipIdentityAndDisappearance2195$/^stable$", "-test.count=1")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HADRON_TEST_FIFO_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "HADRON_TEST_FIFO_CHILD_2197=1", scenario)
	began := time.Now()
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("FIFO refusal exceeded subprocess watchdog: %v: %s", ctx.Err(), out)
	}
	if elapsed := time.Since(began); elapsed > fifoChildBudget2197 {
		t.Fatalf("FIFO refusal took %s, want within %s: %s", elapsed, fifoChildBudget2197, out)
	}
	return out, err
}

// A subprocess keeps a broken fixture from consuming the package timeout.
// The child intentionally fails; its bounded refusal and successful cleanup
// are the expected evidence, rather than a successful census.
func TestCensusFIFORefusalIsBounded2197(t *testing.T) {
	for _, scenario := range []struct{ name, env, want string }{
		{"earlier refusal", "HADRON_TEST_FIFO_EARLY_REFUSAL_2197=1", "FIFO writer did not open before deadline"},
		{"stalled completion", "HADRON_TEST_FIFO_STALL_2197=1", "FIFO writer did not complete before deadline"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			out, err := runFIFOChild2197(t, scenario.env)
			if err == nil {
				t.Fatal("refusal child unexpectedly succeeded")
			}
			if !strings.Contains(string(out), scenario.want) {
				t.Fatalf("wrong refusal: %s", out)
			}
			if strings.Contains(string(out), "FIFO cleanup") {
				t.Fatalf("cleanup failed to unblock writer: %s", out)
			}
		})
	}
}

func TestCensusCountWithheldAfterLaterRefusal2197(t *testing.T) {
	proc, work := t.TempDir(), t.TempDir()
	foreignDir := censusFixture2060(t, proc, work, 41)
	unknownDir := censusFixture2060(t, proc, work, 42)
	for _, dir := range []string{foreignDir, unknownDir} {
		if err := os.Remove(filepath.Join(dir, "cwd")); err != nil {
			t.Fatal(err)
		}
	}
	uid := os.Geteuid() + 1000000
	if err := os.WriteFile(filepath.Join(foreignDir, "status"), []byte(fmt.Sprintf("Uid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)), 0600); err != nil {
		t.Fatal(err)
	}
	foreignSkipped := 0
	_, err := strictRelaunchCensusCounted(context.Background(), proc, work, func(context.Context) error { return nil }, &foreignSkipped)
	if err == nil {
		t.Fatal("later unknown ownership must refuse")
	}
	if foreignSkipped != 0 {
		t.Fatalf("earlier exclusion leaked out of incomplete census: %d", foreignSkipped)
	}
}
