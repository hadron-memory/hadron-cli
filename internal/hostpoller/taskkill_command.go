package hostpoller

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

const taskkillWaitDelay = time.Second

// taskkillCommand is inspected on every host; only the Windows adapter executes it.
func taskkillCommand(ctx context.Context, pid int) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	cmd.WaitDelay = taskkillWaitDelay
	return cmd
}
