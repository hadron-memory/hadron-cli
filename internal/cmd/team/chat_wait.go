package team

import (
	"bytes"
	"context"
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
		parent := cmd.Context()
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithDeadline(parent, deadline)
		defer cancel()
		cmd.SetContext(ctx)
		defer cmd.SetContext(parent)
		var lastOut, lastErr []byte
		for {
			var out, errOut bytes.Buffer
			f.IOStreams.Out, f.IOStreams.ErrOut = &out, &errOut
			*returned = 0
			err := inner(cmd, args)
			if err != nil && *returned == 0 && ctx.Err() == context.DeadlineExceeded && parent.Err() == nil {
				// The deadline cut this poll short: that is a timeout, not a
				// failure. Show the last completed (empty) poll, if any.
				f.IOStreams.Out, f.IOStreams.ErrOut = outStream, errStream
				_, _ = outStream.Write(lastOut)
				_, _ = errStream.Write(lastErr)
				return exitcode.Newf(exitcode.WaitTimeout, "no new message after %s of polling every %s; nothing changed", *wait, *interval)
			}
			lastOut, lastErr = out.Bytes(), errOut.Bytes()
			if err != nil || *returned > 0 || !time.Now().Before(deadline) {
				f.IOStreams.Out, f.IOStreams.ErrOut = outStream, errStream
				_, _ = outStream.Write(out.Bytes())
				_, _ = errStream.Write(errOut.Bytes())
				if err != nil {
					return err
				}
				if *returned > 0 {
					return nil
				}
				return exitcode.Newf(exitcode.WaitTimeout, "no new message after %s of polling every %s; nothing changed", *wait, *interval)
			}
			if serr := chatWaitSleep(ctx, min(*interval, time.Until(deadline))); serr != nil {
				return exitcode.Newf(exitcode.Cancelled, "interrupted while waiting")
			}
		}
	}
}
