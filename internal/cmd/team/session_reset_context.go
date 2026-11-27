package team

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#2161 (C12): `team session reset-context` is a context reset that
// relaunches at once (Holger, #109643; the name, #109658). It writes the
// checkpoint and the resume claim exactly as `shed` does, under checkpoints:,
// then leaves a relaunch stamp beside the binding and ends its own agent. The
// host's poller sees that agent exit and relaunches the worker through C5's
// Runner; the stamp only ever makes a relaunch C5 would make happen sooner.

// AgentPIDEnv is the agent's own pid, exported by the Orca launch.
const AgentPIDEnv = delivery.AgentPIDEnv

const (
	relaunchPending = "pending"
	relaunchFailed  = "failed"
	relaunchNone    = "none"

	// agentSignalPending: the agent's pid is proven and pinned, and SIGTERM is
	// sent after this receipt. The receipt cannot report the signal's own
	// result (the agent it goes to is the one that reads it); a signal that
	// still fails says so on stderr (Jibril's delta P2).
	agentSignalPending = "signal-pending"
	// agentEnded is endOwnAgent's word for a signal that was delivered.
	agentEnded   = "signalled"
	agentExitNow = "exit-now"
	agentNone    = "none"
)

// resetContextDTO is the stable --json shape of `session reset-context`.
type resetContextDTO struct {
	Status   string  `json:"status"`
	Node     *string `json:"node"`
	Claim    string  `json:"claim"`
	Relaunch string  `json:"relaunch"`
	Exit     string  `json:"exit"`
	Reason   *string `json:"reason"`
}

func newCmdSessionResetContext(f *cmdutil.Factory) *cobra.Command {
	o := &shedOptions{resetContext: true}
	cmd := &cobra.Command{
		Use:   "reset-context [--note-file <path>] [--transcript <path>]",
		Short: "Reset your context: checkpoint, end this agent, and have the host relaunch it at once",
		Long: `Reset this worktree's worker context and come back at once, in the same
session. In order:

  1. the checkpoint node, ` + "`checkpoints:<YYYY-MM-DD-HHMMSS>`" + ` (UTC) in the worker's
     own memory, read back, exactly as ` + "`session shed`" + ` writes its node
     (the same headings, --note-file and --transcript);
  2. the resume claim, last of the server writes;
  3. a relaunch stamp beside the session binding, naming this worker,
     session and checkpoint;
  4. the end of this agent: SIGTERM to exactly the pid the host's Orca launch
     exported as ` + AgentPIDEnv + `, and only while that pid is an
     ancestor of this command. Never a pattern, never another process.

The host (team host run) sees the agent it launched exit, finds the fresh stamp for
the same session, and starts a new agent in the same worktree through the
same launcher, guards and hourly cap as its periodic relaunch. The new
agent's first prompt is ` + "`session recover`" + `, which finds the checkpoint.
If anything does not line up (no stamp, another session, a refusal, the cap,
the host is not running), nothing is started early and the host's ordinary relaunch sweep
brings the worker back. A failed checkpoint writes no stamp and ends nothing.

--json prints {"status":"written|failed","node","claim":"written|failed|none",
"relaunch":"pending|failed|none","exit":"signal-pending|helper-pending|exit-now|none","reason"}.
"signal-pending" is NOT success: it means the CLI signals next, and the
outcome is not in this receipt. The agent's pid is proven and pinned, and
SIGTERM goes to it right after this receipt is written. A receipt that
cannot be written signals nothing. A signal that still fails says so on
stderr ("exit it now").
"helper-pending" is NOT success either: this host names an exit helper (below),
it was validated, and it is run right after this receipt. Then end your turn now
and run nothing more: the helper ends this AGENT once it is idle, never the
session: the binding and the session stay, and the host relaunches the agent.
A receipt that cannot be written runs no helper (stderr says so), and a helper
that fails or times out after the receipt is said on stderr: exit your agent
yourself.
"exit-now": nothing will be signalled, because this agent was not launched
through Orca by the host or its pid could not be proven, and no exit helper
ran (none is configured, or "reason" says why the configured one was refused).
Exit your agent yourself within two minutes.

Exit helper (host configuration, only on the "exit-now" branch, after the
checkpoint and the relaunch stamp are written): the file ` + exitHelperFile + ` in the
Hadron config directory holds one absolute path. It is never read from a
repository, a binding or the server. The file and the helper must be regular
files you own that group and others cannot write; a symlink is refused. The
helper is run without a shell as "<helper> --checkpoint <node URN>" with a
bounded timeout, its output discarded and never echoed. It must return
promptly and end the agent only after this command has returned and the agent
is idle. A refused helper is not run: "exit-now" with the reason.
Exit 0 unless the command line is wrong.`,
		Example: `  # reset with your own narrative carried into the checkpoint
  hadron team session reset-context --note-file reset-notes.md --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o.transcriptEmpty = cmd.Flags().Changed("transcript") && strings.TrimSpace(o.transcript) == ""
			if o.transcriptEmpty {
				return exitcode.Newf(exitcode.Usage, "--transcript names a file but was empty (an unset variable?); omit it for no transcript tail")
			}
			o.noteFileSet = cmd.Flags().Changed("note-file")
			if o.noteFileSet {
				text, refusal := shedNotesOf(o.noteFile)
				if refusal != "" {
					return exitcode.Newf(exitcode.Usage, "%s", refusal)
				}
				o.notes, o.notesRead = text, true
			}
			o.now = time.Now
			return runResetContext(cmd.Context(), f, *o)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&o.transcript, "transcript", "", "append the tail of this host transcript file to the node (opt-in; NOT scrubbed)")
	fl.StringVar(&o.noteFile, "note-file", "", "append this file's text (UTF-8, 64 KiB at most) to the node as ## Notes (NOT scrubbed)")
	return cmd
}

func runResetContext(ctx context.Context, f *cmdutil.Factory, o shedOptions) error {
	out := runShed(ctx, f, o).dto
	d := resetContextDTO{Status: out.Status, Node: out.Node, Claim: out.Claim, Relaunch: relaunchNone, Exit: agentNone, Reason: out.Reason}
	if out.Status == shedWritten && out.Node != nil {
		if err := writeRelaunchStamp(ctx, *out.Node, o.now()); err != nil {
			// No stamp: nothing is ended, so the agent does not wait on a
			// relaunch that will not come early.
			d.Relaunch = relaunchFailed
			reason := "the relaunch stamp could not be written; nothing was ended"
			d.Reason = &reason
		} else {
			d.Relaunch = relaunchPending
		}
	}
	if d.Reason != nil && (d.Status == shedFailed || d.Claim == shedClaimFailed || d.Relaunch == relaunchFailed) {
		_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "hadron: team session reset-context: %s\n", *d.Reason)
	}
	var agent ownAgent
	if d.Relaunch == relaunchPending {
		d.Exit = agentExitNow
		if a, ok := pinOwnAgent(os.Getenv(AgentPIDEnv)); ok {
			agent, d.Exit = a, agentSignalPending
		} else if path, refusal, configured := lookupExitHelper(); configured {
			// cli#3299: only on this branch, only after the checkpoint and the stamp are written. A refused
			// helper is not run and the receipt says exit-now with the reason; a validated one is run after
			// the receipt, as the signal is.
			if refusal != "" {
				reason := exitHelperRefusal(refusal)
				d.Reason = &reason
			} else {
				reason := helperPendingReason
				agent, d.Exit, d.Reason = exitHelper{path: path, checkpoint: *out.Node}, agentHelperPending, &reason
			}
		}
	}
	finishResetContext(f.IOStreams.ErrOut, agent, func() error {
		return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
			if d.Status == shedFailed {
				return nil
			}
			node := ""
			if d.Node != nil {
				node = *d.Node
			}
			_, e := fmt.Fprintf(w, "✓ context reset %s: %s (claim: %s, relaunch: %s)\n", d.Status, node, d.Claim, d.Relaunch)
			if e == nil && d.Exit == agentExitNow {
				_, e = fmt.Fprintln(w, "This agent could not be ended by its own pid (not launched by the host through Orca, or its pid could not be proven): exit it now; the host relaunches the worker.")
				if e == nil && d.Reason != nil {
					_, e = fmt.Fprintln(w, *d.Reason)
				}
			}
			if e == nil && d.Exit == agentHelperPending {
				_, e = fmt.Fprintln(w, "End your turn now and run nothing more: "+*d.Reason)
			}
			return e
		})
	})
	return nil
}

// ownAgent is this command's agent, pinned and proven, ready to signal.
type ownAgent interface {
	signal() error
	close()
}

// finishResetContext writes the receipt FIRST, while the agent still reads
// it, and only then signals the pinned agent (Jibril's delta P2s). Every
// refusal came before, in the pin. A receipt that could not be written
// signals nothing: the agent has not been told, so it is not ended. A signal
// that still fails (the process already gone) is said on stderr: exit now.
// The pinned handle is closed on every path.
func finishResetContext(errOut io.Writer, agent ownAgent, receipt func() error) {
	err := receipt()
	if agent == nil {
		return
	}
	defer agent.close()
	receiptFailed := "hadron: team session reset-context: the receipt could not be written, so nothing was signalled: exit your agent yourself; the host relaunches the worker."
	signalFailed := "hadron: team session reset-context: the signal to this agent failed after the receipt: exit it now; the host relaunches the worker."
	if n, ok := agent.(interface{ receiptFailed() string }); ok {
		receiptFailed = n.receiptFailed()
	}
	if n, ok := agent.(interface{ signalFailed() string }); ok {
		signalFailed = n.signalFailed()
	}
	if err != nil {
		_, _ = fmt.Fprintln(errOut, receiptFailed)
		return
	}
	if agent.signal() != nil {
		_, _ = fmt.Fprintln(errOut, signalFailed)
	}
}

// writeRelaunchStamp writes the stamp beside the binding, under the binding's
// lock, atomically (a private temp file renamed into place).
func writeRelaunchStamp(ctx context.Context, checkpoint string, now time.Time) error {
	b, _, err := readBindingFile(ctx)
	if err != nil || b == nil || b.WorkerID == "" || b.SessionID == "" {
		return fmt.Errorf("no bound session")
	}
	loc, err := locateBinding(ctx, false)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(delivery.RelaunchStamp{Version: 1, WorkerID: b.WorkerID, SessionID: b.SessionID, Checkpoint: checkpoint, RequestedAt: now.UTC()})
	if err != nil {
		return err
	}
	return withBindingLockAt(loc, func(h cacheHandle) error {
		return h.writeAtomic(delivery.RelaunchStampFile, raw, 0o600)
	})
}
