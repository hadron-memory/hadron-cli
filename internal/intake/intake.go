// Package intake runs a team's task intake adapter (hadron-cli#1961,
// cor:agt:120:04): a local executable that knows a tracker, which the CLI
// calls to list the outside items nobody tracks yet, and to mark the ones it
// claims. Hadron makes no tracker call (cor:agt:120, D2); the adapter holds the
// team's own tracker credential, never Hadron's.
//
// The contract's calls and what they carry are the spec's. HOW a call is
// invoked is the CLI owner's to decide (cor:agt:120:04), and is decided here:
//
//	<adapter> <call>        e.g. `hadron-task-adapter-github list-untracked`
//
// One argument, the call's name, never a shell string. The request is ONE JSON
// object on stdin (it always carries "contractVersion"); the answer is ONE JSON
// object on stdout and exit 0. Any other exit is a refusal by the adapter. The
// adapter's text is untrusted terminal data: it is size-capped, parsed
// strictly, never executed, and printed terminal-safe by the caller.
package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hadron-memory/hadron-cli/internal/output"
)

// ContractVersion is the one contract version this CLI speaks. An adapter that
// describes another is refused with a SkewError, never guessed at.
const ContractVersion = 1

// Bounds on what an adapter may hand back. A hostile or broken adapter hits one
// of these and the call is refused; nothing is truncated into a plausible
// answer.
const (
	MaxOutputBytes   = 1 << 20 // stdout of one call
	MaxStderrBytes   = 4096    // kept for the refusal message only
	MaxItemsPerPage  = 100
	MaxPages         = 50
	MaxExternalIDLen = 2048 // the server's own cap is 512; this only bounds memory
	MaxCursorLen     = 4096
	MaxTextLen       = 1024 // adapter-supplied display text: name, title
	DefaultTimeout   = 60 * time.Second
)

// Item is one outside item the adapter says is not yet tracked.
type Item struct {
	// ExternalID is the item's canonical URL exactly as the adapter wrote it:
	// compared exactly by the server, so the CLI never normalises it
	// (cor:agt:120:02; `normalize` is the adapter's optional call).
	ExternalID string `json:"externalId"`
	Title      string `json:"title"`
}

// Describe is what `describe` answers.
type Describe struct {
	Name            string   `json:"name"`
	ContractVersion int      `json:"contractVersion"`
	Calls           []string `json:"calls"`
	Filters         []string `json:"filters"`
	Order           string   `json:"order"`
}

// Task and Worker are what `mark-tracked` is given (cor:agt:120:04). Empty
// fields are omitted: the portal URL is the server's or nothing, and a worker
// detail the CLI does not know is left out rather than invented.
type Task struct {
	ID        string `json:"id"`
	Handle    string `json:"handle,omitempty"`
	PortalURL string `json:"portalUrl,omitempty"`
}

type Worker struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Role  string `json:"role,omitempty"`
	Model string `json:"model,omitempty"`
}

// SkewError is a contract-version mismatch. The command maps it to its own
// exit code.
type SkewError struct{ Got int }

func (e *SkewError) Error() string {
	return fmt.Sprintf("the intake adapter speaks contract version %d; this CLI speaks version %d, so nothing was asked of it", e.Got, ContractVersion)
}

// RefusedError is an adapter that ran and exited non-zero (or could not run).
type RefusedError struct {
	Call   string
	Detail string // already terminal-safe
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("the intake adapter refused %s: %s", e.Call, e.Detail)
}

// OutputError is an adapter answer the CLI will not use: oversized, not one
// JSON object, a field out of bounds, a cursor that loops.
type OutputError struct {
	Call   string
	Reason string
}

func (e *OutputError) Error() string {
	return fmt.Sprintf("the intake adapter's %s answer was refused: %s", e.Call, e.Reason)
}

// Runner calls one adapter executable.
type Runner struct {
	// Path is the executable: a path, or a bare name looked up on PATH. It is
	// run directly, never through a shell, and gets no arguments but the call.
	Path    string
	Timeout time.Duration
}

// scrubbedEnv is the adapter's environment: the CLI's own, minus every
// Hadron credential. The adapter holds the team's tracker credential, never
// Hadron's (cor:agt:120, D2).
func scrubbedEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		u := strings.ToUpper(name)
		if strings.HasPrefix(u, "HADRON_") && (strings.Contains(u, "TOKEN") || strings.Contains(u, "KEY") || strings.Contains(u, "SECRET")) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// capWriter keeps at most max bytes and cancels the call the moment one more
// arrives, so an endless writer is stopped instead of read to the timeout.
type capWriter struct {
	buf    bytes.Buffer
	max    int
	over   bool
	cancel context.CancelFunc
	// swallow keeps the first max bytes and silently drops the rest (stderr,
	// which is only ever a refusal's detail): failing the write would leave a
	// chatty process blocked on a full pipe.
	swallow bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.swallow {
		if room := w.max - w.buf.Len(); room > 0 {
			w.buf.Write(p[:min(room, len(p))])
		}
		return len(p), nil
	}
	if w.buf.Len()+len(p) > w.max {
		w.over = true
		if w.cancel != nil {
			w.cancel()
		}
		return 0, errors.New("output cap exceeded")
	}
	return w.buf.Write(p)
}

// call runs one adapter call and decodes its single JSON object into out.
func (r *Runner) call(ctx context.Context, name string, req map[string]any, out any) error {
	if r.Path == "" {
		return &RefusedError{Call: name, Detail: "no adapter is configured"}
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body := map[string]any{"contractVersion": ContractVersion}
	for k, v := range req {
		body[k] = v
	}
	stdin, err := json.Marshal(body)
	if err != nil {
		return &OutputError{Call: name, Reason: "the request could not be encoded"}
	}
	cmd := exec.CommandContext(cctx, r.Path, name)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = scrubbedEnv(os.Environ())
	stdout := &capWriter{max: MaxOutputBytes, cancel: cancel}
	stderr := &capWriter{max: MaxStderrBytes, swallow: true}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := cmd.Run()
	if stdout.over {
		return &OutputError{Call: name, Reason: fmt.Sprintf("more than %d bytes on stdout", MaxOutputBytes)}
	}
	if runErr != nil {
		if errors.Is(cctx.Err(), context.DeadlineExceeded) {
			return &RefusedError{Call: name, Detail: fmt.Sprintf("no answer within %s", timeout)}
		}
		detail := output.SafeLine(strings.TrimSpace(stderr.buf.String()), 300)
		var ee *exec.ExitError
		switch {
		case errors.As(runErr, &ee):
			if detail == "" {
				detail = fmt.Sprintf("exit status %d", ee.ExitCode())
			} else {
				detail = fmt.Sprintf("exit status %d: %s", ee.ExitCode(), detail)
			}
		default:
			detail = output.SafeLine(runErr.Error(), 300)
		}
		return &RefusedError{Call: name, Detail: detail}
	}
	raw := bytes.TrimSpace(stdout.buf.Bytes())
	if len(raw) == 0 || raw[0] != '{' {
		return &OutputError{Call: name, Reason: "stdout is not one JSON object"}
	}
	if !utf8.Valid(raw) {
		return &OutputError{Call: name, Reason: "stdout is not valid UTF-8"}
	}
	// Unmarshal rejects text after the first value, so a second document or
	// trailing junk is refused here too.
	if err := json.Unmarshal(raw, out); err != nil {
		return &OutputError{Call: name, Reason: "stdout is not one well-formed JSON object"}
	}
	return nil
}

func boundedText(call, field, s string, max int) error {
	if len(s) > max {
		return &OutputError{Call: call, Reason: fmt.Sprintf("%s is longer than %d bytes", field, max)}
	}
	return nil
}

// Describe asks what the adapter is and checks the contract version. It is
// the first call of every run, so a skewed adapter is refused before it is
// asked for anything else.
func (r *Runner) Describe(ctx context.Context) (Describe, error) {
	var d Describe
	if err := r.call(ctx, "describe", nil, &d); err != nil {
		return Describe{}, err
	}
	if d.ContractVersion != ContractVersion {
		return Describe{}, &SkewError{Got: d.ContractVersion}
	}
	if strings.TrimSpace(d.Name) == "" {
		return Describe{}, &OutputError{Call: "describe", Reason: "no adapter name"}
	}
	if err := boundedText("describe", "name", d.Name, MaxTextLen); err != nil {
		return Describe{}, err
	}
	d.Name = output.SafeLine(d.Name, 80)
	return d, nil
}

type listResponse struct {
	Items      []Item  `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// Page is one validated page of `list-untracked`.
type Page struct {
	Items      []Item
	NextCursor string // "" when the list is exhausted
}

// ListUntracked asks for one page. cursor is the opaque value the previous
// page returned, "" for the first: the CLI never builds or offsets one
// (cor:agt:120:04). forWorker is `--for X`, passed as written.
func (r *Runner) ListUntracked(ctx context.Context, cursor, forWorker string, limit int) (Page, error) {
	req := map[string]any{"limit": limit}
	if cursor != "" {
		req["cursor"] = cursor
	}
	if forWorker != "" {
		req["for"] = forWorker
	}
	var resp listResponse
	if err := r.call(ctx, "list-untracked", req, &resp); err != nil {
		return Page{}, err
	}
	if len(resp.Items) > MaxItemsPerPage {
		return Page{}, &OutputError{Call: "list-untracked", Reason: fmt.Sprintf("more than %d items in one page", MaxItemsPerPage)}
	}
	for i, it := range resp.Items {
		if it.ExternalID == "" {
			return Page{}, &OutputError{Call: "list-untracked", Reason: fmt.Sprintf("item %d has no externalId", i)}
		}
		if err := boundedText("list-untracked", "an externalId", it.ExternalID, MaxExternalIDLen); err != nil {
			return Page{}, err
		}
		if err := boundedText("list-untracked", "a title", it.Title, MaxTextLen); err != nil {
			return Page{}, err
		}
	}
	p := Page{Items: resp.Items}
	if resp.NextCursor != nil {
		if err := boundedText("list-untracked", "nextCursor", *resp.NextCursor, MaxCursorLen); err != nil {
			return Page{}, err
		}
		p.NextCursor = *resp.NextCursor
	}
	return p, nil
}

// Walk pages list-untracked by its opaque cursor until the adapter says there
// is no more, yield asks to stop, or a bound is hit. A cursor the adapter has
// already given (or an empty page that keeps promising more) is a loop and is
// refused; so is a list longer than MaxPages. Items seen on an earlier page are
// not offered to yield again.
func (r *Runner) Walk(ctx context.Context, forWorker string, limit int, yield func(Page) (stop bool, err error)) error {
	seenCursor := map[string]bool{"": true}
	seenItem := map[string]bool{}
	cursor := ""
	for pages := 0; ; pages++ {
		if pages >= MaxPages {
			return &OutputError{Call: "list-untracked", Reason: fmt.Sprintf("the list did not end within %d pages", MaxPages)}
		}
		page, err := r.ListUntracked(ctx, cursor, forWorker, limit)
		if err != nil {
			return err
		}
		fresh := page.Items[:0:0]
		for _, it := range page.Items {
			if !seenItem[it.ExternalID] {
				seenItem[it.ExternalID] = true
				fresh = append(fresh, it)
			}
		}
		stop, err := yield(Page{Items: fresh, NextCursor: page.NextCursor})
		if err != nil || stop {
			return err
		}
		if page.NextCursor == "" {
			return nil
		}
		if seenCursor[page.NextCursor] {
			return &OutputError{Call: "list-untracked", Reason: "the adapter repeated a cursor it had already given"}
		}
		seenCursor[page.NextCursor] = true
		cursor = page.NextCursor
	}
}

// MarkTracked tells the adapter an item is tracked, after the CLI's create
// claimed it (cor:agt:120:04). The task's portal URL is the server's, passed
// as printed; the CLI builds none.
func (r *Runner) MarkTracked(ctx context.Context, externalID string, task Task, worker Worker) error {
	req := map[string]any{"externalId": externalID, "task": task, "worker": worker}
	var ignored map[string]json.RawMessage
	return r.call(ctx, "mark-tracked", req, &ignored)
}
