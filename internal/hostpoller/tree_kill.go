package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/output"
)

// runTreeKill is the Windows taskkill result boundary, shared so the runner's
// cancellation and authorization lifetime can be tested on other hosts.
func runTreeKill(pid int, timeout time.Duration, run func(context.Context, int) ([]byte, error)) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// Run synchronously: a deadline requests cancellation, but it must not
	// release the caller's binding authority while the runner still acts.
	out, err := run(ctx, pid)
	if ctx.Err() != nil {
		return false, fmt.Sprintf("taskkill for the launched process tree (pid %d) received cancellation after %s; stop not confirmed, records kept", pid, timeout) + treeKillRunnerContext(out, err)
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		return false, fmt.Sprintf("taskkill for the launched process tree (pid %d): output collection incomplete; stop not confirmed, records kept", pid) + treeKillRunnerContext(out, err)
	}
	if err != nil {
		// Runner text is untrusted: single-line and bounded, like the branches above.
		msg := fmt.Sprintf("could not stop the launched process tree (pid %d): %s", pid, output.SafeLine(strings.TrimSpace(err.Error()), 512))
		if detail := output.SafeLine(strings.TrimSpace(string(out)), 512); strings.TrimSpace(detail) != "" {
			msg += ": " + detail
		}
		return false, msg
	}
	return true, fmt.Sprintf("terminated the launched process tree (pid %d)", pid)
}

// Only runner-provided context is appended: no argv or environment collection.
func treeKillRunnerContext(out []byte, err error) string {
	var parts []string
	if err != nil {
		if detail := strings.TrimSpace(output.SafeLine(strings.TrimSpace(err.Error()), 512)); detail != "" {
			parts = append(parts, "error: "+detail)
		}
	}
	if detail := strings.TrimSpace(output.SafeLine(strings.TrimSpace(string(out)), 512)); detail != "" {
		parts = append(parts, "output: "+detail)
	}
	if len(parts) == 0 {
		return ""
	}
	return "; runner context: " + strings.Join(parts, "; ")
}
