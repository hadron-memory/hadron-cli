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
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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

func (t Terminal) identity() string {
	if t.AgentIdentity == nil {
		return ""
	}
	return *t.AgentIdentity
}

// orcaEnvelope is Orca's `--json` envelope.
type orcaEnvelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func parseEnvelope(res RunResult) (orcaEnvelope, bool) {
	var env orcaEnvelope
	if json.Unmarshal([]byte(res.Stdout), &env) != nil {
		return env, false
	}
	return env, true
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (r *router) listTerminals(ctx context.Context) ([]Terminal, error) {
	res := r.orca(ctx, []string{"terminal", "list", "--json"})
	env, ok := parseEnvelope(res)
	if !ok {
		detail := res.Stderr
		if detail == "" {
			detail = res.Stdout
		}
		return nil, routerErrorf("orca terminal list: unparsable output (exit %d): %s", res.Code, clip(detail, 300))
	}
	if !env.OK {
		return nil, routerErrorf("orca terminal list failed: %s", clip(string(env.Error), 300))
	}
	var result struct {
		Terminals []Terminal `json:"terminals"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		return nil, routerErrorf("orca terminal list: malformed result: %v", err)
	}
	return result.Terminals, nil
}

// isIdle is true only on positive proof the agent TUI is idle within the
// timeout. Orca answers a timed-out wait with an error envelope; its guide
// describes `satisfied: false`. Both, and anything else, read as not idle.
func (r *router) isIdle(ctx context.Context, handle string, timeoutMS int) bool {
	res := r.orca(ctx, []string{"terminal", "wait", "--terminal", handle, "--for", "tui-idle",
		"--timeout-ms", strconv.Itoa(timeoutMS), "--json"})
	env, ok := parseEnvelope(res)
	if !ok || !env.OK {
		return false
	}
	var result struct {
		Wait struct {
			Satisfied bool `json:"satisfied"`
		} `json:"wait"`
	}
	return json.Unmarshal(env.Result, &result) == nil && result.Wait.Satisfied
}

// readScreen is the terminal's rendered full-frame screen, or nil when
// unreadable.
func (r *router) readScreen(ctx context.Context, handle string) *Screen {
	res := r.orca(ctx, []string{"terminal", "read", "--terminal", handle, "--screen", "--json"})
	env, ok := parseEnvelope(res)
	if !ok || !env.OK {
		return nil
	}
	var result struct {
		Terminal *struct {
			Source string          `json:"source"`
			Tail   json.RawMessage `json:"tail"`
			Draft  json.RawMessage `json:"draft"`
		} `json:"terminal"`
	}
	if json.Unmarshal(env.Result, &result) != nil || result.Terminal == nil {
		return nil
	}
	sc := &Screen{Source: result.Terminal.Source}
	var tail []string
	if json.Unmarshal(result.Terminal.Tail, &tail) == nil && tail != nil {
		sc.Tail = tail
	}
	// Any present, non-null, non-"" draft is a draft (router.ts), whatever
	// its JSON type. Its text is kept only to be tested, never logged.
	if d := result.Terminal.Draft; len(d) > 0 && string(d) != "null" && string(d) != `""` {
		var s string
		if json.Unmarshal(d, &s) != nil {
			s = string(d)
		}
		sc.Draft = s
	}
	return sc
}

// findRequestID is the first `requestId` anywhere in a receipt: Orca reports
// it for a durable retry.
func findRequestID(node any) string {
	switch v := node.(type) {
	case []any:
		for _, e := range v {
			if id := findRequestID(e); id != "" {
				return id
			}
		}
	case map[string]any:
		if id, ok := v["requestId"].(string); ok {
			return id
		}
		for _, e := range v {
			if id := findRequestID(e); id != "" {
				return id
			}
		}
	}
	return ""
}

// sendNudge types one line into the terminal and submits it: (accepted,
// detail). It retries once, with the same durable request id, only after an
// ambiguous transport failure, as Orca prescribes — never re-sends on silence.
func (r *router) sendNudge(ctx context.Context, handle, text string) (bool, string) {
	argv := []string{"terminal", "send", "--terminal", handle, "--text", text, "--enter",
		"--wait-submit", strconv.Itoa(r.cfg.SubmitWaitS), "--json"}
	type receipt struct {
		OK     bool `json:"ok"`
		Result struct {
			Send struct {
				Accepted bool `json:"accepted"`
				Prompt   struct {
					Stages []string `json:"stages"`
				} `json:"prompt"`
			} `json:"send"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	var rc receipt
	var generic any
	out := r.orca(ctx, argv).Stdout
	if json.Unmarshal([]byte(out), &rc) != nil {
		return false, "unparsable send receipt"
	}
	if rc.OK && rc.Result.Send.Accepted {
		return true, strings.Join(rc.Result.Send.Prompt.Stages, ",")
	}
	_ = json.Unmarshal([]byte(out), &generic)
	if id := findRequestID(generic); id != "" {
		var retry receipt
		if json.Unmarshal([]byte(r.orca(ctx, append(argv, "--retry-request", id)).Stdout), &retry) != nil {
			return false, "unparsable retry receipt"
		}
		if retry.OK && retry.Result.Send.Accepted {
			return true, "accepted on retry"
		}
		rc = retry
	}
	errText := "null"
	if len(rc.Error) > 0 {
		errText = string(rc.Error)
	}
	return false, fmt.Sprintf("send not accepted: %s", errText)
}
