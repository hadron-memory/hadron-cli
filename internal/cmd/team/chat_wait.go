package team

import (
	"bytes"
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// chatWaitDefaultInterval and chatWaitMinInterval bound the poll loop behind
// `team chat read --wait` (cli#1261). The platform never pushes team chat
// (cor:agt:020:04), so a wait is a documented poll; the floor keeps it from
// being a busy loop. The floor is a variable only so tests can poll quickly.
const chatWaitDefaultInterval = 5 * time.Second

var chatWaitMinInterval = 2 * time.Second

// chatReadDeferKey carries a *func() in the read's context: when present the
// ordinary read leaves its post-render bookkeeping there instead of running it.
type chatReadDeferKey struct{}

// chatWaitSleep is replaceable in tests.
var chatWaitSleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// chatWaitRunE wraps the ordinary read so --wait polls it. Every poll IS the
// ordinary read (same gates, attribution, watermark and read-state rules), run
// with its output held back: an empty poll is discarded, and the first poll
// that returns messages — or fails — is printed as that read would have been.
// On timeout the last (empty) poll is printed, so the cursor and read-state
// lines are still shown, and the command exits with exitcode.WaitTimeout.
func chatWaitRunE(f *cmdutil.Factory, inner func(*cobra.Command, []string) error,
	wait, interval *time.Duration, returned *int) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("wait") {
			if cmd.Flags().Changed("poll-interval") {
				return exitcode.Newf(exitcode.Usage, "--poll-interval only applies with --wait")
			}
			return inner(cmd, args)
		}
		switch {
		case *wait <= 0:
			return exitcode.Newf(exitcode.Usage, "--wait must be a positive duration (e.g. --wait 2m)")
		case *interval < chatWaitMinInterval:
			return exitcode.Newf(exitcode.Usage, "--poll-interval must be at least %s: --wait polls, and a shorter interval would be a busy loop", chatWaitMinInterval)
		case !cmd.Flags().Changed("since"):
			return exitcode.Newf(exitcode.Usage, "--wait needs --since <seq>: it waits for messages after that seq (use the nextSince of your last read)")
		case cmd.Flags().Changed("before"), cmd.Flags().Changed("all"):
			return exitcode.Newf(exitcode.Usage, "--wait waits for NEW messages after --since; it cannot be combined with --before or --all")
		}
		deadline := time.Now().Add(*wait)
		outStream, errStream := f.IOStreams.Out, f.IOStreams.ErrOut
		defer func() { f.IOStreams.Out, f.IOStreams.ErrOut = outStream, errStream }()
		// Every poll runs under the wait deadline, so a read that starts late or
		// hangs is cancelled at the deadline instead of stretching the wait.
		root := cmd.Context()
		if root == nil {
			root = context.Background()
		}
		// Nothing else in the CLI turns an interrupt into a context cancel, so
		// without this Ctrl-C kills the process with 130 and never reaches the
		// documented Cancelled exit (QA on cli#1269).
		parent, stopSignals := signal.NotifyContext(root, os.Interrupt, syscall.SIGTERM)
		defer stopSignals()
		ctx, cancel := context.WithDeadline(parent, deadline)
		defer cancel()
		cmd.SetContext(ctx)
		defer cmd.SetContext(root)
		var lastOut, lastErr []byte
		var lastDone func()
		// deliver writes a held poll to the real streams and only then runs the
		// read's bookkeeping (watermark, server read mark), so a closed pipe
		// never marks messages read that were not shown.
		deliver := func(out, errOut []byte, done func()) error {
			f.IOStreams.Out, f.IOStreams.ErrOut = outStream, errStream
			if _, err := outStream.Write(out); err != nil {
				return err
			}
			if _, err := errStream.Write(errOut); err != nil {
				return err
			}
			if done != nil {
				done()
			}
			return nil
		}
		timeout := func() error {
			if err := deliver(lastOut, lastErr, lastDone); err != nil {
				return err
			}
			return exitcode.Newf(exitcode.WaitTimeout, "no new message after %s of polling every %s; nothing changed", *wait, *interval)
		}
		deadlineHit := func() bool { return ctx.Err() == context.DeadlineExceeded && parent.Err() == nil }
		for {
			var out, errOut bytes.Buffer
			f.IOStreams.Out, f.IOStreams.ErrOut = &out, &errOut
			*returned = 0
			var done func()
			cmd.SetContext(context.WithValue(ctx, chatReadDeferKey{}, &done))
			err := inner(cmd, args)
			if err != nil && *returned == 0 && parent.Err() != nil {
				return exitcode.Newf(exitcode.Cancelled, "interrupted while waiting")
			}
			if err != nil && *returned == 0 && deadlineHit() {
				// The deadline cut this poll short: a timeout, not a failure.
				return timeout()
			}
			lastOut, lastErr, lastDone = out.Bytes(), errOut.Bytes(), done
			if err != nil || *returned > 0 {
				if derr := deliver(lastOut, lastErr, nil); derr != nil {
					return derr
				}
				if err != nil {
					return err
				}
				if done != nil {
					done()
				}
				return nil
			}
			if !time.Now().Before(deadline) {
				return timeout()
			}
			if serr := chatWaitSleep(ctx, min(*interval, time.Until(deadline))); serr != nil {
				if deadlineHit() {
					return timeout()
				}
				return exitcode.Newf(exitcode.Cancelled, "interrupted while waiting")
			}
		}
	}
}
