package team

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/guardedpush"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// The runner has no retry. Do not use orcaterm.Client.Send: it may retry an ambiguous send.
var pushRunner = func() orcaterm.Runner { return orcaterm.Exec("orca", 5*time.Second) }

func newCmdTeamPush(f *cmdutil.Factory) *cobra.Command {
	var text, file string
	var seq int
	o := guardedpush.Defaults()
	cmd := &cobra.Command{Use: "push <worker> (--text <message> | --message-file <path>)", Short: "Send once through a guarded agent composer", Long: `Send one message directly to an agent's console, using code-only guards.
Requires this worktree's open worker-session binding. The recipient must have exactly
one connected writable local terminal with a matching App/server/session binding
and positively recognized Claude/Codex interface. Busy waits; unknown holds.

An empty idle composer sends promptly. A draft waits at least 30s and must remain
byte-for-byte unchanged for at least 30s (overlapping intervals, not their sum).
A captured draft is cleared with the proven interface sequence, then included once
as labelled unfinished/not-author-submitted text in the SAME input as the message.
Fresh on-screen evidence of this attempt's turn starting is required; accepted
send receipts alone are not confirmation. No automatic resend or follow-up Enter.

Command-specific exits: 0 sent; 9 sent-with-draft (SUCCESS, NEVER RETRY);
10 held-busy; 11 held-unknown (both zero composer input); 12 unconfirmed
(composer mutation may have occurred, DO NOT RETRY). General errors keep their
usual codes. JSON carries outcome/phase/reason/recipient/seq/draftIncluded only.

Defaults: total pre-send wait120s, draft wait30s, exact-byte stability30s,
confirmation10s, poll250ms. Stability cannot be below30s. All durations must be
positive and bounded to24h. --seq labels provenance only: no chat post or mark.
Prefer --message-file for sensitive text: --text has ordinary argv visibility.
No input/screen/draft text is logged or persisted. Unknown clear semantics hold.

The local lock serializes only this command's attempts. Orca has no atomic
conditional send: a human/external sender can race the final read and input.
Draft bytes remain in memory only; process death can lose a captured draft.
Source merge does not migrate unguarded callers, install or enable anything.`, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !o.Validate() || seq < 0 {
			return exitcode.Newf(exitcode.Usage, "invalid push timing or seq (stability must be at least30s)")
		}
		if cmd.Flags().Changed("text") == cmd.Flags().Changed("message-file") {
			return exitcode.Newf(exitcode.Usage, "choose exactly one of --text or --message-file")
		}
		started := time.Now()
		parent := cmd.Context()
		preCtx, cancelPre := context.WithTimeout(parent, o.Wait)
		defer cancelPre()
		message := text
		if file != "" {
			rd, err := os.Open(file)
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "message file unavailable")
			}
			defer func() { _ = rd.Close() }()
			info, err := rd.Stat()
			if err != nil || !info.Mode().IsRegular() {
				return exitcode.Newf(exitcode.Usage, "message file must be a regular file")
			}
			b, err := io.ReadAll(io.LimitReader(rd, orcaterm.MaxSendTextBytes+1))
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "message file unreadable")
			}
			message = string(b)
		}
		if message == "" || len(message) > orcaterm.MaxSendTextBytes {
			return exitcode.Newf(exitcode.Usage, "message empty or too large")
		}
		b, _, err := readBindingFile(preCtx)
		if err != nil || b == nil || b.WorkerID == "" || b.SessionID == "" || b.AppID == "" || !pushServerMatches(f, b) {
			return exitcode.Newf(exitcode.Usage, "push needs a matching worker-session binding")
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return exitcode.Newf(exitcode.AuthRequired, "push credentials unavailable")
		}
		if !pushSession(preCtx, client, b) {
			return exitcode.Newf(exitcode.Usage, "push sender session unavailable or ended")
		}
		sender, err := gen.GetWorker(preCtx, client, b.WorkerID)
		if err != nil || sender.Worker == nil || sender.Worker.Id != b.WorkerID || sender.Worker.RetiredAt != nil || sender.Worker.AppId != b.AppID {
			return exitcode.Newf(exitcode.Usage, "push sender identity unavailable")
		}
		app, err := resolveTeamApp(preCtx, f, b)
		if err != nil {
			return exitcode.Newf(exitcode.Usage, "push App unavailable")
		}
		w, err := resolveWorker(preCtx, client, app, args[0])
		if err != nil || w.Id == "" || w.AppId != b.AppID || w.RetiredAt != nil {
			return exitcode.Newf(exitcode.NotFound, "push recipient unavailable in sender App")
		}
		nonce := make([]byte, 8)
		if _, err = rand.Read(nonce); err != nil {
			return exitcode.Newf(exitcode.Error, "push attempt unavailable")
		}
		runner := pushRunner()
		a := &pushTerminal{run: runner, verify: func(ctx context.Context, path, family string) (string, error) {
			target, err := pushBinding(ctx, path)
			if err != nil || target.WorkerID != w.Id {
				return "", errPushUnknown
			}
			if target.AppID != b.AppID || target.SessionID == "" || !pushServerMatches(f, target) || !pushSessionFamily(ctx, client, target, family) {
				return "", errPushUnknown
			}
			return target.SessionID, nil
		}}
		cfg, err := os.UserCacheDir()
		if err != nil {
			return exitcode.Newf(exitcode.Error, "push lock directory unavailable")
		}
		e := guardedpush.Engine{Now: time.Now, Sleep: func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}, Observe: a.observe, Clear: a.clear, Submit: a.submit, Locked: func(id string, work func(func() bool) guardedpush.Result) guardedpush.Result {
			var r guardedpush.Result
			sum := sha256.Sum256([]byte(strings.SplitN(id, "\x00", 2)[0]))
			err := delivery.WithWorkerLock(filepath.Join(cfg, "hadron", "team-push"), hex.EncodeToString(sum[:]), func(owned func() bool) error { r = work(owned); return nil })
			if err != nil {
				return guardedpush.Result{Outcome: guardedpush.HeldBusy, Phase: "preflight", Reason: "locked"}
			}
			return r
		}}
		var r guardedpush.Result
		remaining := o.Wait - time.Since(started)
		if remaining <= 0 || preCtx.Err() != nil {
			r = guardedpush.Result{Outcome: guardedpush.HeldBusy, Phase: "preflight", Reason: "deadline-or-cancelled"}
		} else {
			o.Wait = remaining
			// Poll is bounded by the remaining budget without weakening draft stability.
			if o.Poll > o.Wait {
				o.Poll = o.Wait
			}
			r = e.Run(parent, guardedpush.Request{Sender: sender.Worker.Name, Message: message, Attempt: hex.EncodeToString(nonce), Seq: seq}, o)
		}
		dto := struct {
			guardedpush.Result
			Recipient string `json:"recipient"`
			Seq       *int   `json:"seq"`
		}{Result: r, Recipient: w.Id}
		if cmd.Flags().Changed("seq") {
			dto.Seq = &seq
		}
		if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
			_, err := fmt.Fprintf(w, "%s to %s in App %s (from sender binding; %s: %s)\n", r.Outcome, dto.Recipient, b.AppID, r.Phase, r.Reason)
			return err
		}); err != nil {
			if r.Outcome == guardedpush.Sent || r.Outcome == guardedpush.SentWithDraft || r.Outcome == guardedpush.Unconfirmed {
				return exitcode.Newf(12, "push receipt output failed; outcome unconfirmed; do not retry")
			}
			return exitcode.Newf(exitcode.Error, "push receipt output failed; no composer input")
		}
		if code := pushOutcomeExit(r.Outcome); code != 0 {
			return exitcode.Silent(code)
		}
		return nil
	}}
	cmd.Flags().StringVar(&text, "text", "", "message (argv-visible; prefer --message-file)")
	cmd.Flags().StringVar(&file, "message-file", "", "UTF-8 message file")
	cmd.Flags().IntVar(&seq, "seq", 0, "optional chat sequence label only")
	cmd.Flags().DurationVar(&o.Wait, "wait-timeout", o.Wait, "total pre-send wait")
	cmd.Flags().DurationVar(&o.DraftWait, "draft-wait", o.DraftWait, "minimum observed draft wait")
	cmd.Flags().DurationVar(&o.Stable, "draft-stable-for", o.Stable, "unchanged draft interval (minimum30s)")
	cmd.Flags().DurationVar(&o.Confirm, "confirm-timeout", o.Confirm, "fresh screen confirmation deadline")
	cmd.Flags().DurationVar(&o.Poll, "poll-interval", o.Poll, "observation interval")
	return cmd
}

var errPushUnknown = errors.New("push observation unavailable")

func pushSession(ctx context.Context, c graphql.Client, b *binding) bool {
	r, err := gen.GetTeamSession(ctx, c, b.SessionID)
	return err == nil && r.Session != nil && r.Session.Id == b.SessionID && r.Session.WorkerId != nil && *r.Session.WorkerId == b.WorkerID && r.Session.EndedAt == nil
}
func pushBinding(ctx context.Context, dir string) (*binding, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, errPushUnknown
	}
	raw, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return nil, errPushUnknown
	}
	data, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(raw)), bindingFileName))
	if err != nil {
		return nil, errPushUnknown
	}
	var b binding
	if json.Unmarshal(data, &b) != nil {
		return nil, errPushUnknown
	}
	return &b, nil
}

// Terminal metadata and binding/session authority are freshly read on EVERY observation.
// Missing booleans, malformed drafts and ambiguous candidates never prove permission to type.
type pushTerminal struct {
	run    orcaterm.Runner
	verify func(context.Context, string, string) (string, error)
	handle string
}

func (a *pushTerminal) observe(ctx context.Context) (guardedpush.Frame, error) {
	var env struct {
		OK     bool `json:"ok"`
		Result struct {
			Terminals []struct {
				Handle, WorktreePath string
				AgentIdentity        *string
				Connected, Writable  *bool
			} `json:"terminals"`
		} `json:"result"`
	}
	res := a.run(ctx, []string{"terminal", "list", "--json"})
	if res.Code != 0 || json.Unmarshal([]byte(res.Stdout), &env) != nil || !env.OK {
		return guardedpush.Frame{}, errPushUnknown
	}
	var f guardedpush.Frame
	count := 0
	for _, t := range env.Result.Terminals {
		if t.AgentIdentity == nil || *t.AgentIdentity == "" {
			continue
		}
		session, err := a.verify(ctx, t.WorktreePath, *t.AgentIdentity)
		if err != nil {
			continue
		}
		count++
		if count > 1 || t.Handle == "" || t.Connected == nil || t.Writable == nil || !*t.Connected || !*t.Writable {
			return guardedpush.Frame{}, errPushUnknown
		}
		var screen struct {
			OK     bool `json:"ok"`
			Result struct {
				Terminal *struct {
					Source string          `json:"source"`
					Tail   []string        `json:"tail"`
					Draft  json.RawMessage `json:"draft"`
				} `json:"terminal"`
			} `json:"result"`
		}
		r := a.run(ctx, []string{"terminal", "read", "--terminal", t.Handle, "--screen", "--json"})
		if r.Code != 0 || !utf8.ValidString(r.Stdout) || json.Unmarshal([]byte(r.Stdout), &screen) != nil || !screen.OK || screen.Result.Terminal == nil {
			return guardedpush.Frame{}, errPushUnknown
		}
		sc := screen.Result.Terminal
		if sc.Source != "screen" || sc.Tail == nil {
			return guardedpush.Frame{}, errPushUnknown
		}
		family := orcaterm.ScreenAgentFamily(&orcaterm.Screen{Source: sc.Source, Tail: sc.Tail})
		if family != *t.AgentIdentity || (family != "claude" && family != "codex") {
			return guardedpush.Frame{}, errPushUnknown
		}
		var draft string
		if len(sc.Draft) > 0 && string(sc.Draft) != "null" && json.Unmarshal(sc.Draft, &draft) != nil {
			return guardedpush.Frame{}, errPushUnknown
		}
		if strings.ContainsRune(draft, utf8.RuneError) {
			return guardedpush.Frame{}, errPushUnknown
		}
		state := guardedpush.Unknown
		live, _ := orcaterm.IdleProof(sc.Tail, family)
		if live == orcaterm.Idle {
			state = guardedpush.Idle
		}
		if live == orcaterm.Busy {
			state = guardedpush.Busy
		}
		composer, _ := orcaterm.ReadComposer(&orcaterm.Screen{Source: sc.Source, Tail: sc.Tail, Draft: draft}, family)
		f = guardedpush.Frame{Identity: t.Handle + "\x00" + t.WorktreePath + "\x00" + session, Family: family, Draft: draft, Screen: strings.Join(sc.Tail, "\n"), State: state, Empty: draft == "" && composer == orcaterm.ComposerEmpty}
		a.handle = t.Handle
	}
	if count != 1 {
		return guardedpush.Frame{}, errPushUnknown
	}
	return f, nil
}
func (a *pushTerminal) send(ctx context.Context, text string, enter bool) error {
	args := []string{"terminal", "send", "--terminal", a.handle, "--text", text}
	if enter {
		args = append(args, "--enter")
	}
	args = append(args, "--json")
	r := a.run(ctx, args)
	var env struct {
		OK     bool `json:"ok"`
		Result struct {
			Send struct {
				Accepted bool `json:"accepted"`
			} `json:"send"`
		} `json:"result"`
	}
	if r.Code != 0 || json.Unmarshal([]byte(r.Stdout), &env) != nil || !env.OK || !env.Result.Send.Accepted {
		return errPushUnknown
	}
	return nil
}
func (a *pushTerminal) clear(ctx context.Context, draft string) error {
	return a.send(ctx, delivery.ClearKeys(draft), false)
}
func (a *pushTerminal) submit(ctx context.Context, text string) error { return a.send(ctx, text, true) }

// Push never accepts the legacy empty-server binding fallback used by observational commands.
func pushServerMatches(f *cmdutil.Factory, b *binding) bool {
	server, err := f.Server()
	return err == nil && server != "" && b != nil && b.Server != "" && api.Endpoint(server) == api.Endpoint(b.Server)
}

func pushSessionFamily(ctx context.Context, c graphql.Client, b *binding, family string) bool {
	r, err := gen.GetTeamSession(ctx, c, b.SessionID)
	if err != nil || r.Session == nil || r.Session.Id != b.SessionID || r.Session.WorkerId == nil || *r.Session.WorkerId != b.WorkerID || r.Session.EndedAt != nil || r.Session.Tool == nil {
		return false
	}
	tool := *r.Session.Tool
	return (family == "claude" && (tool == "claude-code" || tool == "claude")) || (family == "codex" && tool == "codex")
}

func pushOutcomeExit(k guardedpush.Outcome) int {
	switch k {
	case guardedpush.Sent:
		return 0
	case guardedpush.SentWithDraft:
		return 9
	case guardedpush.HeldBusy:
		return 10
	case guardedpush.HeldUnknown:
		return 11
	default:
		return 12
	}
}
