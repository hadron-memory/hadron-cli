//go:build linux

package team

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Exercise the real fixtures, including t.Cleanup after Fatal. The subprocess
// stays alive until its parent has inspected /proc, retaining ownership of the
// fallback group even on the red/main path. No host-wide pattern signal.
func TestFixtureCleanup2078(t *testing.T) {
	fixtures := map[string]func(*testing.T){"fixture2078HealthAgent": fixture2078HealthAgent, "fixture2078HealthChain": fixture2078HealthChain, "fixture2078HealthSleeper": fixture2078HealthSleeper}
	if name := os.Getenv("FIXTURE_2078_NAME"); name != "" {
		ok := t.Run("fixture", func(t *testing.T) {
			fixtures[name](t)
			if os.Getenv("FIXTURE_2078_FAIL") == "1" {
				t.Fatal("intentional failure before fixture cleanup")
			}
		})
		if err := os.WriteFile(os.Getenv("FIXTURE_2078_DONE"), []byte(strconv.FormatBool(ok)), 0600); err != nil {
			t.Fatal(err)
		}
		// The parent owns stdin and closes/stops us only after checking cleanup.
		var b [1]byte
		_, _ = os.Stdin.Read(b[:])
		return
	}
	for name := range fixtures {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fail=%t", name, fail), func(t *testing.T) {
				root := t.TempDir()
				done := filepath.Join(root, "done")
				log, err := os.Create(filepath.Join(root, "child.log"))
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = log.Close() }()
				c := exec.Command(os.Args[0], "-test.run=^TestFixtureCleanup2078$", "-test.timeout=30s")
				c.Env = append(os.Environ(), "TMPDIR="+root, "FIXTURE_2078_NAME="+name, "FIXTURE_2078_FAIL="+map[bool]string{false: "0", true: "1"}[fail], "FIXTURE_2078_DONE="+done)
				c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				c.Stdout = log
				c.Stderr = log
				stdin, err := c.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := c.Start(); err != nil {
					t.Fatal(err)
				}
				// c is still unreaped: its pid cannot be reused for an unrelated group.
				t.Cleanup(func() { _ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL); _ = stdin.Close(); _ = c.Wait() })
				deadline := time.Now().Add(20 * time.Second)
				var result []byte
				for time.Now().Before(deadline) {
					result, err = os.ReadFile(done)
					if err == nil {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if err != nil {
					out, _ := os.ReadFile(log.Name())
					t.Fatalf("fixture did not complete cleanup: %v\n%s", err, out)
				}
				if string(result) != strconv.FormatBool(!fail) {
					t.Fatalf("child result=%q, forced failure=%t", result, fail)
				}
				// Scope the observation to script argv entries beneath this owned TMPDIR.
				// These pids are never used as signal targets: only our retained group is.
				var live []string
				for deadline = time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
					live = nil
					entries, err := os.ReadDir("/proc")
					if err != nil {
						t.Fatal(err)
					}
					for _, e := range entries {
						if _, err := strconv.Atoi(e.Name()); err != nil {
							continue
						}
						stat, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
						if err != nil {
							continue
						}
						fields := strings.Fields(string(stat)[strings.LastIndexByte(string(stat), ')')+1:])
						if len(fields) == 0 || fields[0] == "Z" {
							continue
						}
						// Include script-less sleepers in our retained subprocess group.
						if len(fields) > 2 && fields[2] == strconv.Itoa(c.Process.Pid) && e.Name() != strconv.Itoa(c.Process.Pid) {
							live = append(live, e.Name()+":owned-group")
							continue
						}
						argv, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
						for _, arg := range strings.Split(string(argv), "\x00") {
							if strings.HasPrefix(arg, root+string(os.PathSeparator)) && !strings.HasSuffix(arg, ".log") {
								live = append(live, e.Name()+":"+arg)
								break
							}
						}
					}
					if len(live) == 0 || time.Now().After(deadline) {
						break
					}
				}
				if len(live) != 0 {
					t.Fatalf("fixture cleanup left live scripts: %v", live)
				}
			})
		}
	}
}

func fixture2078HealthAgent(t *testing.T) {
	w := health2027Setup(t)
	w.spawnAgent(t, "codex", true)
	w.spawnAgent(t, "claude", false)
}
func fixture2078HealthChain(t *testing.T)   { w := health2027Setup(t); w.spawnChain(t) }
func fixture2078HealthSleeper(t *testing.T) { TestHealth2027StopRefusedWhenIdentityDoesNotMatch(t) }
