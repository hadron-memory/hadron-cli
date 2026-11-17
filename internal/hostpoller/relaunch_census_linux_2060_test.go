//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func censusFixture2060(t *testing.T, proc, work string, pid int) string {
	t.Helper()
	dir := filepath.Join(proc, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	fields := []string{"S", "2"}
	for len(fields) < 19 {
		fields = append(fields, "0")
	}
	fields = append(fields, "123")
	for name, data := range map[string]string{"stat": fmt.Sprintf("%d (claude) %s", pid, strings.Join(fields, " ")), "cmdline": "claude\x00--model\x00test\x00"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(work, filepath.Join(dir, "cwd")); err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestRelaunchCensusIncludesCallerAndAncestors2060(t *testing.T) {
	proc, work := t.TempDir(), t.TempDir()
	censusFixture2060(t, proc, work, os.Getpid())
	censusFixture2060(t, proc, work, os.Getppid())
	calls := 0
	agents, err := strictRelaunchCensus(context.Background(), proc, work, func(context.Context) error { calls++; return nil })
	if err != nil || len(agents) != 2 || calls != 2 {
		t.Fatalf("%+v %v witness calls=%d", agents, err, calls)
	}
}
func TestRelaunchCensusUnknownVisibilityNeverProvesAbsence2060(t *testing.T) {
	proc, work := t.TempDir(), t.TempDir()
	for _, witness := range []func(context.Context) error{nil, func(context.Context) error { return errors.New("proc view unproven") }} {
		if _, err := strictRelaunchCensus(context.Background(), proc, work, witness); err == nil {
			t.Fatal("unproven zero accepted")
		}
	}
	if _, err := strictRelaunchCensus(context.Background(), filepath.Join(proc, "missing"), work, func(context.Context) error { return nil }); err == nil {
		t.Fatal("missing proc accepted as zero")
	}
}
func TestRelaunchCensusPartialEntryRefuses2060(t *testing.T) {
	for _, name := range []string{"cmdline", "stat", "cwd"} {
		t.Run(name, func(t *testing.T) {
			proc, work := t.TempDir(), t.TempDir()
			dir := censusFixture2060(t, proc, work, os.Getpid())
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
			if _, err := strictRelaunchCensus(context.Background(), proc, work, func(context.Context) error { return nil }); err == nil {
				t.Fatal("partial live entry accepted")
			}
		})
	}
}
func TestRelaunchCensusWitnessRechecked2060(t *testing.T) {
	proc, work := t.TempDir(), t.TempDir()
	calls := 0
	if _, err := strictRelaunchCensus(context.Background(), proc, work, func(context.Context) error {
		calls++
		if calls == 2 {
			return errors.New("authority changed")
		}
		return nil
	}); err == nil {
		t.Fatal("changed authority accepted")
	}
}

func TestRelaunchCensusOnlyProvenKernelOrExitedEmptyEntriesAreHarmless2060(t *testing.T) {
	for _, kind := range []string{"kernel", "zombie", "unknown user"} {
		t.Run(kind, func(t *testing.T) {
			proc, work := t.TempDir(), t.TempDir()
			dir := censusFixture2060(t, proc, work, 42)
			if err := os.WriteFile(filepath.Join(dir, "cmdline"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "stat"))
			if err != nil {
				t.Fatal(err)
			}
			end := strings.LastIndexByte(string(raw), ')')
			fields := strings.Fields(string(raw)[end+1:])
			if kind == "kernel" {
				fields[6] = "2097152"
			}
			if kind == "zombie" {
				fields[0] = "Z"
			}
			if err := os.WriteFile(filepath.Join(dir, "stat"), []byte("42 (kernel) "+strings.Join(fields, " ")), 0600); err != nil {
				t.Fatal(err)
			}
			found, err := strictRelaunchCensus(context.Background(), proc, work, func(context.Context) error { return nil })
			if kind == "unknown user" {
				if err == nil {
					t.Fatal("unknown empty userspace entry accepted")
				}
				return
			}
			if err != nil || len(found) != 0 {
				t.Fatalf("%s: %+v %v", kind, found, err)
			}
		})
	}
}

func TestRelaunchCensusIncludesSubdirectoryButNotPrefixSibling2120(t *testing.T) {
	proc, work := t.TempDir(), t.TempDir()
	sub := filepath.Join(work, "nested")
	if err := os.MkdirAll(sub, 0700); err != nil {
		t.Fatal(err)
	}
	censusFixture2060(t, proc, sub, 31337)
	censusFixture2060(t, proc, work+"-sibling", 31338)
	agents, err := strictRelaunchCensus(context.Background(), proc, work, func(context.Context) error { return nil })
	if err != nil || len(agents) != 1 || agents[0].PID != 31337 {
		t.Fatalf("subdirectory/sibling census=%+v err=%v", agents, err)
	}
}
