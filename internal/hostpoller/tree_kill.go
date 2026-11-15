package hostpoller

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const taskkillTimeout = 10 * time.Second

// runTreeKill is the Windows taskkill result boundary, shared so the runner's
// cancellation and authorization lifetime can be tested on other hosts.
func runTreeKill(pid int, _ time.Duration, run func(context.Context, int) ([]byte, error)) (bool, string) {
	out, err := run(context.Background(), pid)
	if err != nil {
		return false, fmt.Sprintf("could not stop the launched process tree (pid %d): %v: %s", pid, err, strings.TrimSpace(string(out)))
	}
	return true, fmt.Sprintf("terminated the launched process tree (pid %d)", pid)
}
