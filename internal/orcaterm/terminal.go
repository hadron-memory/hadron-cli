package orcaterm

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
)

// Terminal is one `orca terminal list` row.
type Terminal struct {
	Handle        string  `json:"handle"`
	WorktreePath  string  `json:"worktreePath"`
	AgentIdentity *string `json:"agentIdentity"`
	Connected     bool    `json:"connected"`
	Writable      bool    `json:"writable"`
}

// UnmarshalJSON reads Orca's row with router.ts's defaults: connected and
// writable count unless explicitly false.
func (t *Terminal) UnmarshalJSON(b []byte) error {
	var raw struct {
		Handle        string  `json:"handle"`
		WorktreePath  string  `json:"worktreePath"`
		AgentIdentity *string `json:"agentIdentity"`
		Connected     *bool   `json:"connected"`
		Writable      *bool   `json:"writable"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*t = Terminal{Handle: raw.Handle, WorktreePath: raw.WorktreePath, AgentIdentity: raw.AgentIdentity,
		Connected: raw.Connected == nil || *raw.Connected, Writable: raw.Writable == nil || *raw.Writable}
	return nil
}

// Identity is the agent Orca says runs in the terminal, or "".
func (t Terminal) Identity() string {
	if t.AgentIdentity == nil {
		return ""
	}
	return *t.AgentIdentity
}

// envelope is Orca's `--json` envelope. Error is read for its code only: an
// error's message may quote input, and nothing here repeats input.
type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func parse(res Result) (envelope, bool) {
	var env envelope
	if json.Unmarshal([]byte(res.Stdout), &env) != nil {
		return env, false
	}
	return env, true
}

// ErrOrca is a failed or unparsable `orca` call. Its text is a code, never
// Orca's output.
var ErrOrca = errors.New("orca call failed")

// Client makes the four terminal calls through a guarded runner.
type Client struct {
	run Runner
}

// NewClient wraps run in Guard.
func NewClient(run Runner) *Client { return &Client{run: Guard(run)} }

// List is every live terminal, looked up fresh: delivery never caches one.
func (c *Client) List(ctx context.Context) ([]Terminal, error) {
	env, ok := parse(c.run(ctx, []string{"terminal", "list", "--json"}))
	if !ok || !env.OK {
		return nil, ErrOrca
	}
	var result struct {
		Terminals []Terminal `json:"terminals"`
	}
	if json.Unmarshal(env.Result, &result) != nil {
		return nil, ErrOrca
	}
	return result.Terminals, nil
}

// Screen is one `orca terminal read --screen` result: the rendered tail, the
// optional draft field, and the source (only "screen" is a full frame).
// Draft is held only to be tested; it is never logged or reported.
type Screen struct {
	Source string
	Tail   []string
	Draft  string
}

func (sc *Screen) usable() bool { return sc != nil && sc.Source == "screen" && sc.Tail != nil }

// ReadScreen is the terminal's rendered full-frame screen, or nil when
// unreadable.
func (c *Client) ReadScreen(ctx context.Context, handle string) *Screen {
	env, ok := parse(c.run(ctx, []string{"terminal", "read", "--terminal", handle, "--screen", "--json"}))
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
	// Any present, non-null, non-"" draft is a draft, whatever its JSON type.
	if d := result.Terminal.Draft; len(d) > 0 && string(d) != "null" && string(d) != `""` {
		var s string
		if json.Unmarshal(d, &s) != nil {
			s = string(d)
		}
		sc.Draft = s
	}
	return sc
}

// Receipt is what a send established. It carries Orca's stages and an error
// code, never the text sent.
type Receipt struct {
	// Accepted is Orca's input acceptance.
	Accepted bool
	// TurnStarted is the strong delivery evidence: the agent began a turn.
	TurnStarted bool
	Stages      []string
	// ErrCode is Orca's error code when not accepted.
	ErrCode string
}

// SendOptions shape one send.
type SendOptions struct {
	// Interrupt sends as interrupt-style input. Only a batch that says so
	// (a halt, S6) sets it.
	Interrupt bool
	// EnterOnly submits what the composer already holds: no text is typed.
	EnterOnly bool
	// NoEnter types text without submitting it: a family's clearing
	// keystroke, never a message.
	NoEnter bool
}

// Send types text into the terminal and submits it. It retries once, with
// Orca's durable request id, only after an ambiguous transport failure, as
// Orca prescribes: the id is bound to the payload and the terminal process,
// so a retry re-issues, never re-types. Nothing else ever retries a send.
func (c *Client) Send(ctx context.Context, handle, text string, o SendOptions) Receipt {
	argv := []string{"terminal", "send", "--terminal", handle}
	if !o.EnterOnly {
		argv = append(argv, "--text", text)
	}
	if !o.NoEnter {
		argv = append(argv, "--enter")
	}
	if o.Interrupt {
		argv = append(argv, "--interrupt")
	}
	argv = append(argv, "--wait-submit", strconv.Itoa(SubmitWait), "--json")
	rc, retryID := readReceipt(c.run(ctx, argv))
	if rc.Accepted || retryID == "" {
		return rc
	}
	rc, _ = readReceipt(c.run(ctx, append(argv, "--retry-request", retryID)))
	return rc
}

func readReceipt(res Result) (Receipt, string) {
	var raw struct {
		OK     bool `json:"ok"`
		Result struct {
			Send struct {
				Accepted bool `json:"accepted"`
				Prompt   struct {
					Stages []string `json:"stages"`
				} `json:"prompt"`
			} `json:"send"`
		} `json:"result"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(res.Stdout), &raw) != nil {
		return Receipt{ErrCode: "unparsable"}, ""
	}
	if raw.OK && raw.Result.Send.Accepted {
		stages := raw.Result.Send.Prompt.Stages
		return Receipt{Accepted: true, Stages: stages, TurnStarted: slices.Contains(stages, "turn_started")}, ""
	}
	var generic any
	_ = json.Unmarshal([]byte(res.Stdout), &generic)
	code := "unknown"
	if raw.Error != nil && raw.Error.Code != "" {
		code = raw.Error.Code
	}
	return Receipt{ErrCode: code}, findRequestID(generic)
}

// findRequestID is the first `requestId` anywhere in a receipt.
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
