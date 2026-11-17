//go:build linux

package orca

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func hostProcessFixture1729(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, "orca"), []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
}

func TestHostReadIntrinsicTimeout1729(t *testing.T) {
	hostProcessFixture1729(t, "exec /bin/sleep 30\n")
	// This outer watchdog is deliberately longer than the intrinsic bound.
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	start := time.Now()
	out, err := runHostRead(ctx, "orca", []string{"terminal", "list", "--json"})
	elapsed := time.Since(start)
	if err == nil || out != nil || elapsed < 14*time.Second || elapsed > 19*time.Second {
		t.Fatalf("intrinsic15s bound: duration=%s out=%q err=%v", elapsed, out, err)
	}
}

func TestHostReadPipeWaitAndDescendantLifetime1729(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel-parent-%t", cancelParent), func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "fixture-child.pid")
			// Only this fixture's own child PID may be killed, never a process group.
			script := "/bin/sleep 10 &\ncat /proc/\"$!\"/stat > '" + strings.ReplaceAll(pidFile+".identity", "'", "'\\''") + "'\nprintf '%s' \"$!\" > '" + strings.ReplaceAll(pidFile, "'", "'\\''") + "'\n"
			if cancelParent {
				script = "/bin/sleep 0.4\n" + script + "wait\n"
			} else {
				script += "exit 0\n"
			}
			hostProcessFixture1729(t, script)
			var child int
			defer func() {
				raw, err := os.ReadFile(pidFile)
				if err != nil {
					t.Errorf("fixture child PID unavailable: %v", err)
					return
				}
				child, err = strconv.Atoi(strings.TrimSpace(string(raw)))
				if err != nil || child <= 1 || child == os.Getpid() {
					t.Errorf("unsafe fixture PID %q", raw)
					return
				}
				// Compare the fixture-recorded Linux start time before signaling:
				// reject observed PID reuse after the child naturally exits. This
				// check and Kill are not an atomic pidfd operation.
				recorded, e := os.ReadFile(pidFile + ".identity")
				if e != nil {
					t.Errorf("child identity unavailable: %v", e)
					return
				}
				current, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", child))
				if os.IsNotExist(e) {
					return
				}
				if e != nil {
					t.Errorf("child identity unreadable: %v", e)
					return
				}
				before, okBefore := hostFixtureStartTime1729(string(recorded))
				now, okNow := hostFixtureStartTime1729(string(current))
				if !okBefore || !okNow || before != now {
					t.Errorf("fixture PID identity changed; no signal sent")
					return
				}
				if err := syscall.Kill(child, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
					t.Errorf("fixture child cleanup: %v", err)
				}
			}()
			watchdog, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			ctx, cancel := context.WithCancel(watchdog)
			defer cancel()
			start := time.Now()
			var cancellation chan time.Time
			if cancelParent {
				cancellation = make(chan time.Time, 1)
				go func() {
					for {
						pidBytes, pidErr := os.ReadFile(pidFile)
						pid, parseErr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
						identity, identityErr := os.ReadFile(pidFile + ".identity")
						_, identityOK := hostFixtureStartTime1729(string(identity))
						if pidErr == nil && parseErr == nil && pid > 1 && identityErr == nil && identityOK {
							cancellation <- time.Now()
							cancel()
							return
						}
						select {
						case <-watchdog.Done():
							cancellation <- time.Time{}
							return
						case <-time.After(10 * time.Millisecond):
						}
					}
				}()
			}
			out, err := runHostRead(ctx, "orca", []string{"terminal", "list", "--json"})
			if cancelParent {
				start = <-cancellation
				if start.IsZero() {
					t.Fatal("fixture did not become ready before watchdog; cancellation leg not measured")
				}
			}
			elapsed := time.Since(start)
			if err == nil || out != nil || elapsed < 750*time.Millisecond || elapsed > 3500*time.Millisecond {
				t.Fatalf("pipe wait bound: duration=%s out=%q err=%v", elapsed, out, err)
			}
			raw, readErr := os.ReadFile(pidFile)
			if readErr != nil {
				t.Fatal(readErr)
			}
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if parseErr != nil || pid <= 1 || pid == os.Getpid() {
				t.Fatalf("invalid fixture child PID %q", raw)
			}
			if err := syscall.Kill(pid, 0); err != nil {
				t.Fatalf("expected descendant still alive before explicit cleanup: %v", err)
			}
		})
	}
}

// /proc stat's comm may contain spaces/parentheses; fields after its final )
// start at field3, and starttime is field22 (offset19).
func hostFixtureStartTime1729(raw string) (string, bool) {
	end := strings.LastIndex(raw, ")")
	if end < 0 {
		return "", false
	}
	fields := strings.Fields(raw[end+1:])
	if len(fields) <= 19 {
		return "", false
	}
	return fields[19], true
}
