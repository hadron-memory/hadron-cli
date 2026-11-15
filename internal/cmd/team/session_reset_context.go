package team

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
session (hadron-cli#2161). In order:

  1. the checkpoint node, ` + "`checkpoints:<YYYY-MM-DD-HHMMSS>`" + ` (UTC) in the worker's
     own memory, read back, exactly as ` + "`session shed`" + ` writes its node
     (the same headings, --note-file and --transcript);
  2. the resume claim, last of the server writes;
  3. a relaunch stamp beside the session binding, naming this worker,
     session and checkpoint;
  4. the end of this agent: SIGTERM to exactly the pid the host's Orca launch
     exported as ` + AgentPIDEnv + `, and only while that pid is an
     ancestor of this command. Never a pattern, never another process.

The host's poller sees the agent it launched exit, finds the fresh stamp for
the same session, and starts a new agent in the same worktree through the
same launcher, guards and hourly cap as its periodic relaunch. The new
agent's first prompt is ` + "`session recover`" + `, which finds the checkpoint.
If anything does not line up (no stamp, another session, a refusal, the cap,
no poller), nothing is started early and the host's ordinary relaunch sweep
brings the worker back. A failed checkpoint writes no stamp and ends nothing.

--json prints {"status":"written|failed","node","claim":"written|failed|none",
"relaunch":"pending|failed|none","exit":"signalled|exit-now|none","reason"}.
"exit-now" means this agent was not launched through Orca by the host (or its
pid could not be proven): exit your agent yourself within two minutes.
Exit 0 unless the command line is wrong.`,
		Example: `  # reset with your own narrative carried into the checkpoint
  hadron team session reset-context --note-file /tmp/reset-notes.md --json`,
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
	end := d.Relaunch == relaunchPending
	if end {
		// Decided before the receipt, sent after it: the receipt is the last
		// thing this agent reads.
		d.Exit = agentExitNow
		if ownAgentPID(os.Getenv(AgentPIDEnv)) {
			d.Exit = agentEnded
		}
	}
	_ = output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		if d.Status == shedFailed {
			return nil
		}
		node := ""
		if d.Node != nil {
			node = *d.Node
		}
		_, e := fmt.Fprintf(w, "✓ context reset %s: %s (claim: %s, relaunch: %s)\n", d.Status, node, d.Claim, d.Relaunch)
		if e == nil && d.Exit == agentExitNow {
			_, e = fmt.Fprintln(w, "This agent was not launched by the host through Orca: exit it now; the host relaunches the worker.")
		}
		return e
	})
	if end && d.Exit == agentEnded {
		_ = endOwnAgent(os.Getenv(AgentPIDEnv))
	}
	return nil
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
	return withBindingLockAt(loc, func() error {
		var nonce [8]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		tmp := filepath.Join(loc.dir, delivery.RelaunchStampFile+"."+hex.EncodeToString(nonce[:])+".tmp")
		fh, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // O_EXCL: never through an existing link
		if err != nil {
			return err
		}
		if _, err := fh.Write(raw); err != nil {
			_ = fh.Close()
			_ = os.Remove(tmp)
			return err
		}
		if err := fh.Close(); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, filepath.Join(loc.dir, delivery.RelaunchStampFile)); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		return nil
	})
}
