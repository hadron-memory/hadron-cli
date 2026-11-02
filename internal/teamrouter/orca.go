package teamrouter

// The exec'd `orca` CLI: terminal list / wait / read / send. There is no Orca
// SDK, and the CLI boundary has served the two-signal design well (cli#829
// design notes), so this half stays an exec. Every reader here is
// fail-closed: an envelope it cannot parse proves nothing.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"time"
)

// RunResult is one exec'd `orca` invocation's outcome.
type RunResult struct {
	Code   int
	Stdout string
	Stderr string
}

// OrcaRunner execs the `orca` CLI. argv starts at the verb ("terminal",
// "list", …): the binary is the runner's business, so a fake never sees it.
type OrcaRunner func(ctx context.Context, argv []string) RunResult

// orcaCallTimeout bounds one exec (router.ts: spawnSync timeout 300 s). A
// `terminal wait` carries its own --timeout-ms well under this; the bound is
// for a wedged Orca, which must cost one call, not the loop (Hugo #11532).
const orcaCallTimeout = 300 * time.Second

// ExecOrca is the production runner: it execs bin with argv and never errors
// on a non-zero exit — the envelope (or its absence) is the verdict.
func ExecOrca(bin string) OrcaRunner {
	return func(ctx context.Context, argv []string) RunResult {
		ctx, cancel := context.WithTimeout(ctx, orcaCallTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, argv...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			code = 1
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				code = exitErr.ExitCode()
			} else if stderr.Len() == 0 {
				stderr.WriteString(err.Error())
			}
		}
		return RunResult{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
	}
}

// Terminal is one `orca terminal list` row.
type Terminal struct {
	Handle        string  `json:"handle"`
	WorktreePath  string  `json:"worktreePath"`
	AgentIdentity *string `json:"agentIdentity"`
	Connected     bool    `json:"connected"`
	Writable      bool    `json:"writable"`
	LastOutputAt  *int64  `json:"lastOutputAt"` // nil = untracked (#7398)
}

// UnmarshalJSON reads Orca's row with router.ts's defaults: connected and
// writable count unless explicitly false, and ANY non-null lastOutputAt is
// tracking (only null means it was lost).
func (t *Terminal) UnmarshalJSON(b []byte) error {
	var raw struct {
		Handle        string          `json:"handle"`
		WorktreePath  string          `json:"worktreePath"`
		AgentIdentity *string         `json:"agentIdentity"`
		Connected     *bool           `json:"connected"`
		Writable      *bool           `json:"writable"`
		LastOutputAt  json.RawMessage `json:"lastOutputAt"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*t = Terminal{Handle: raw.Handle, WorktreePath: raw.WorktreePath, AgentIdentity: raw.AgentIdentity,
		Connected: raw.Connected == nil || *raw.Connected, Writable: raw.Writable == nil || *raw.Writable}
	if len(raw.LastOutputAt) > 0 && string(raw.LastOutputAt) != "null" {
		var ms float64
		if json.Unmarshal(raw.LastOutputAt, &ms) != nil {
			ms = 1 // present but not a number: still tracked, as in router.ts
		}
		v := int64(ms)
		t.LastOutputAt = &v
	}
	return nil
}
