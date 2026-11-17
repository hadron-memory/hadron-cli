//go:build !windows

package team

import (
	"os/exec"
	"syscall"
	"testing"
)

// fixture2078Started observes a successful start in the isolated regression child.
var fixture2078Started func()

// startFixture2078 gives the fixture and its descendants a private group.
// Cleanup is registered before any assertion can fail, signals only that
// retained child's group, then reaps the direct child. Never use a name/uid kill.
func startFixture2078(t *testing.T, c *exec.Cmd) {
	t.Helper()
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		t.Fatalf("start fixture %q: %v", c.Args, err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		_ = c.Wait()
	})
	if fixture2078Started != nil {
		fixture2078Started()
	}
}
