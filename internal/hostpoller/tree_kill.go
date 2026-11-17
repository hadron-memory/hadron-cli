package hostpoller

import (
	"context"
	"fmt"
	"strings"
	"time"
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
		return false, fmt.Sprintf("taskkill for the launched process tree (pid %d) received cancellation after %s; stop not confirmed, records kept", pid, timeout)
	}
	if err != nil {
		return false, fmt.Sprintf("could not stop the launched process tree (pid %d): %v: %s", pid, err, strings.TrimSpace(string(out)))
	}
	return true, fmt.Sprintf("terminated the launched process tree (pid %d)", pid)
}
