//go:build !windows

package team

import (
	"os/exec"
	"syscall"
	"testing"
)

// startFixture2078 gives the fixture and its descendants a private group.
// Cleanup is registered before any assertion can fail, signals only that
// retained child's group, then reaps the direct child. Never use a name/uid kill.
func startFixture2078(t *testing.T, c *exec.Cmd) {
	t.Helper()
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		_ = c.Wait()
	})
}
