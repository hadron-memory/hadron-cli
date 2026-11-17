//go:build linux

package team

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type fixtureResult2078 struct {
	Passed  bool
	Skipped bool
	Started int
}

type fixtureProcess2078 struct {
	pid, parent int
	state       string
	argv        string
}

// A dedicated subprocess is a subreaper, never the shared test runner. It
// waits for a second subprocess to run the real fixture and its t.Cleanups.
// Leaks are then its own children even when fixtures create nested groups.
func TestFixtureCleanup2078(t *testing.T) {
	fixtures := map[string]func(*testing.T){
		"fixture2078HealthAgent":   fixture2078HealthAgent,
		"fixture2078HealthChain":   fixture2078HealthChain,
		"fixture2078HealthSleeper": fixture2078HealthSleeper,
	}
	switch os.Getenv("FIXTURE_2078_ROLE") {
	case "fixture":
		result := fixtureResult2078{}
		fixture2078Started = func() { result.Started++ }
		result.Passed = t.Run("fixture", func(t *testing.T) {
			defer func() { result.Skipped = t.Skipped() }()
			fixtures[os.Getenv("FIXTURE_2078_NAME")](t)
			if os.Getenv("FIXTURE_2078_FAIL") == "1" {
				t.Fatal("intentional failure before fixture cleanup")
			}
		})
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		done := os.Getenv("FIXTURE_2078_DONE")
		if err := os.WriteFile(done+".tmp", data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(done+".tmp", done); err != nil {
			t.Fatal(err)
		}
		return
	case "supervisor":
		if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		// This process owns only the fixture runner and its adopted descendants.
		// Always remove leaks before publishing a failed supervisor result.
		defer cleanupFixtureChildren2078(t)
		c := exec.Command(os.Args[0], "-test.run=^TestFixtureCleanup2078$", "-test.timeout=30s")
		c.Env = append(os.Environ(), "FIXTURE_2078_ROLE=fixture")
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		out, runErr := c.CombinedOutput()
		data, err := os.ReadFile(os.Getenv("FIXTURE_2078_DONE"))
		if err != nil {
			t.Fatalf("fixture result: %v\n%s", err, out)
		}
		var result fixtureResult2078
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatalf("fixture result: %v\n%s", err, out)
		}
		if result.Skipped || result.Started == 0 {
			t.Fatalf("fixture did not exercise cleanup: %+v\n%s", result, out)
		}
		fail := os.Getenv("FIXTURE_2078_FAIL") == "1"
		if result.Passed == fail || (runErr != nil) != fail {
			t.Fatalf("fixture result=%+v error=%v forced-failure=%t\n%s", result, runErr, fail, out)
		}
		var live []string
		for _, p := range fixtureChildren2078(t) {
			if p.state != "Z" {
				live = append(live, fmt.Sprintf("%d:%s", p.pid, p.argv))
			}
		}
		if len(live) != 0 {
			t.Fatalf("fixture cleanup left live descendants: %v", live)
		}
		return
	}
	for name := range fixtures {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fail=%t", name, fail), func(t *testing.T) {
				root := t.TempDir()
				forced := "0"
				if fail {
					forced = "1"
				}
				c := exec.Command(os.Args[0], "-test.run=^TestFixtureCleanup2078$", "-test.timeout=60s")
				c.Env = append(os.Environ(), "TMPDIR="+root, "FIXTURE_2078_ROLE=supervisor", "FIXTURE_2078_NAME="+name, "FIXTURE_2078_FAIL="+forced, "FIXTURE_2078_DONE="+filepath.Join(root, "done"))
				out, err := c.CombinedOutput()
				if err != nil {
					t.Fatalf("cleanup supervisor: %v\n%s", err, out)
				}
			})
		}
	}
}

// The /proc scan selects only descendants of this living subreaper by parent
// identity. No path/name/uid pattern is used to choose a signal target.
func fixtureChildren2078(t *testing.T) []fixtureProcess2078 {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var all []fixtureProcess2078
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		last := strings.LastIndexByte(string(stat), ')')
		if last < 0 {
			continue
		}
		// Fields after comm: state (3), ppid (4). comm itself may contain ')'.
		fields := strings.Fields(string(stat)[last+1:])
		if len(fields) < 2 {
			continue
		}
		parent, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		argv, _ := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		all = append(all, fixtureProcess2078{pid: pid, parent: parent, state: fields[0], argv: strings.ReplaceAll(string(argv), "\x00", " ")})
	}
	owned := map[int]bool{os.Getpid(): true}
	var children []fixtureProcess2078
	for changed := true; changed; {
		changed = false
		for _, p := range all {
			if !owned[p.pid] && owned[p.parent] {
				owned[p.pid] = true
				children = append(children, p)
				changed = true
			}
		}
	}
	return children
}

func cleanupFixtureChildren2078(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		children := fixtureChildren2078(t)
		if len(children) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("fixture descendants did not drain: %+v", children)
			return
		}
		for _, p := range children {
			if p.parent != os.Getpid() {
				continue
			}
			// A direct, unreaped child cannot have its pid reused until our Wait4.
			// Kill it first, then reap; its descendants are adopted on the next pass.
			if err := syscall.Kill(p.pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
				t.Errorf("kill owned child %d: %v", p.pid, err)
			}
			var status syscall.WaitStatus
			for {
				_, err := syscall.Wait4(p.pid, &status, 0, nil)
				if err == syscall.EINTR {
					continue
				}
				if err != nil {
					t.Errorf("reap owned child %d: %v", p.pid, err)
				}
				break
			}
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
