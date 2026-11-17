package team

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// waitHarness runs `team chat read --wait` over a fake ordinary read: each
// poll prints its index and "returns" the count scripted for it.
func waitHarness(t *testing.T, args []string, counts []int, readErr error) (out, errOut string, polls int, slept []time.Duration, err error) {
	t.Helper()
	prevMin, prevSleep := chatWaitMinInterval, chatWaitSleep
	t.Cleanup(func() { chatWaitMinInterval, chatWaitSleep = prevMin, prevSleep })
	chatWaitMinInterval = time.Millisecond
	chatWaitSleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }

	io, o, e := output.Test()
	f := &cmdutil.Factory{IOStreams: io}
	var wait, interval time.Duration
	var returned int
	cmd := &cobra.Command{Use: "read"}
	cmd.Flags().Int("since", 0, "")
	cmd.Flags().Int("before", 0, "")
	cmd.Flags().Bool("all", false, "")
	cmd.Flags().DurationVar(&wait, "wait", 0, "")
	cmd.Flags().DurationVar(&interval, "poll-interval", chatWaitDefaultInterval, "")
	inner := func(*cobra.Command, []string) error {
		n := 0
		if polls < len(counts) {
			n = counts[polls]
		}
		fmt.Fprintf(f.IOStreams.Out, "poll %d\n", polls)
		fmt.Fprintf(f.IOStreams.ErrOut, "note %d\n", polls)
		polls++
		if readErr != nil {
			return readErr
		}
		returned = n
		return nil
	}
	cmd.RunE = chatWaitRunE(f, inner, &wait, &interval, &returned)
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	err = cmd.Execute()
	return o.String(), e.String(), polls, slept, err
}

func TestChatWaitReturnsOnFirstMessageAndPrintsOnlyThatPoll(t *testing.T) {
	out, errOut, polls, slept, err := waitHarness(t, []string{"--since", "5", "--wait", "1h", "--poll-interval", "10ms"}, []int{0, 0, 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if polls != 3 || len(slept) != 2 {
		t.Fatalf("polls=%d slept=%v", polls, slept)
	}
	if out != "poll 2\n" || errOut != "note 2\n" {
		t.Fatalf("only the poll that returned messages is printed, got %q / %q", out, errOut)
	}
}

func TestChatWaitTimeoutExitsDistinctAndPrintsLastRead(t *testing.T) {
	out, _, polls, _, err := waitHarness(t, []string{"--since", "5", "--wait", "1ms", "--poll-interval", "1ms"}, nil, nil)
	if got := exitcode.FromError(err); got != exitcode.WaitTimeout {
		t.Fatalf("exit %d (%v), want %d", got, err, exitcode.WaitTimeout)
	}
	for _, other := range []int{exitcode.Usage, exitcode.NotFound, exitcode.Unavailable, 9} {
		if exitcode.WaitTimeout == other {
			t.Fatalf("timeout code collides with %d", other)
		}
	}
	if polls < 1 || out == "" {
		t.Fatalf("polls=%d out=%q: the last empty read should still print", polls, out)
	}
}

func TestChatWaitReadErrorStopsAtOnceWithItsOwnCode(t *testing.T) {
	boom := exitcode.Newf(exitcode.Forbidden, "refused")
	out, _, polls, _, err := waitHarness(t, []string{"--since", "5", "--wait", "1h", "--poll-interval", "10ms"}, nil, boom)
	if !errors.Is(err, boom) || polls != 1 {
		t.Fatalf("polls=%d err=%v", polls, err)
	}
	if out != "poll 0\n" {
		t.Fatalf("the failing read's output must still be shown, got %q", out)
	}
}

func TestChatWaitFlagRefusalsNeverPoll(t *testing.T) {
	for name, args := range map[string][]string{
		"needs --since":           {"--wait", "1m"},
		"below the floor":         {"--since", "1", "--wait", "1m", "--poll-interval", "1ns"},
		"zero wait":               {"--since", "1", "--wait", "0s"},
		"before":                  {"--since", "1", "--before", "9", "--wait", "1m"},
		"all":                     {"--since", "1", "--all", "--wait", "1m"},
		"interval without a wait": {"--since", "1", "--poll-interval", "10s"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, polls, _, err := waitHarness(t, args, nil, nil)
			if exitcode.FromError(err) != exitcode.Usage || polls != 0 {
				t.Fatalf("exit %d polls %d err %v", exitcode.FromError(err), polls, err)
			}
		})
	}
}

func TestChatReadWithoutWaitIsTheOrdinaryRead(t *testing.T) {
	_, _, polls, slept, err := waitHarness(t, []string{"--since", "5"}, []int{0}, nil)
	if err != nil || polls != 1 || len(slept) != 0 {
		t.Fatalf("polls=%d slept=%v err=%v", polls, slept, err)
	}
}

// A read that is still in flight at the deadline is cancelled and reported as
// the timeout — the wait is never stretched by a late poll (cli#1269 review).
func TestChatWaitDeadlineCancelsInFlightRead(t *testing.T) {
	prevMin := chatWaitMinInterval
	t.Cleanup(func() { chatWaitMinInterval = prevMin })
	chatWaitMinInterval = time.Millisecond

	io, _, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: io}
	var wait, interval time.Duration
	var returned int
	cmd := &cobra.Command{Use: "read"}
	cmd.Flags().Int("since", 0, "")
	cmd.Flags().Int("before", 0, "")
	cmd.Flags().Bool("all", false, "")
	cmd.Flags().DurationVar(&wait, "wait", 0, "")
	cmd.Flags().DurationVar(&interval, "poll-interval", chatWaitDefaultInterval, "")
	inner := func(c *cobra.Command, _ []string) error {
		<-c.Context().Done() // a hung network read
		return c.Context().Err()
	}
	cmd.RunE = chatWaitRunE(f, inner, &wait, &interval, &returned)
	cmd.SetArgs([]string{"--since", "5", "--wait", "50ms", "--poll-interval", "10ms"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	start := time.Now()
	err := cmd.ExecuteContext(context.Background())
	if got := exitcode.FromError(err); got != exitcode.WaitTimeout {
		t.Fatalf("exit %d (%v), want %d", got, err, exitcode.WaitTimeout)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("wait overran its deadline: %s", d)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func waitCmd(f *cmdutil.Factory, inner func(*cobra.Command, []string) error, args ...string) *cobra.Command {
	var wait, interval time.Duration
	var returned int
	cmd := &cobra.Command{Use: "read"}
	cmd.Flags().Int("since", 0, "")
	cmd.Flags().Int("before", 0, "")
	cmd.Flags().Bool("all", false, "")
	cmd.Flags().DurationVar(&wait, "wait", 0, "")
	cmd.Flags().DurationVar(&interval, "poll-interval", chatWaitDefaultInterval, "")
	wrapped := func(c *cobra.Command, a []string) error {
		err := inner(c, a)
		if err == nil {
			returned = chatWaitTestReturned
		}
		return err
	}
	cmd.RunE = chatWaitRunE(f, wrapped, &wait, &interval, &returned)
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	return cmd
}

var chatWaitTestReturned int

// The deadline expiring while the loop sleeps is the timeout (exit 13), not a
// cancellation, however the context and the sleep timer race.
func TestChatWaitSleepExpiryIsTimeoutNotCancelled(t *testing.T) {
	prevMin := chatWaitMinInterval
	t.Cleanup(func() { chatWaitMinInterval = prevMin })
	chatWaitMinInterval = time.Millisecond
	chatWaitTestReturned = 0
	for i := 0; i < 100; i++ {
		io, _, _ := output.Test()
		f := &cmdutil.Factory{IOStreams: io}
		cmd := waitCmd(f, func(*cobra.Command, []string) error { return nil },
			"--since", "5", "--wait", "20ms", "--poll-interval", "1h")
		err := cmd.ExecuteContext(context.Background())
		if got := exitcode.FromError(err); got != exitcode.WaitTimeout {
			t.Fatalf("iteration %d: exit %d (%v), want %d", i, got, err, exitcode.WaitTimeout)
		}
	}
}

// Bookkeeping (the read mark) runs only after the held output was delivered.
func TestChatWaitDoesNotMarkReadWhenDeliveryFails(t *testing.T) {
	prevMin := chatWaitMinInterval
	t.Cleanup(func() { chatWaitMinInterval = prevMin })
	chatWaitMinInterval = time.Millisecond
	chatWaitTestReturned = 2
	t.Cleanup(func() { chatWaitTestReturned = 0 })
	for _, broken := range []bool{true, false} {
		io, _, _ := output.Test()
		if broken {
			io.Out = brokenWriter{}
		}
		f := &cmdutil.Factory{IOStreams: io}
		marked := false
		cmd := waitCmd(f, func(c *cobra.Command, _ []string) error {
			fmt.Fprintln(f.IOStreams.Out, "msg")
			if d, ok := c.Context().Value(chatReadDeferKey{}).(*func()); ok {
				*d = func() { marked = true }
			}
			return nil
		}, "--since", "5", "--wait", "1h", "--poll-interval", "10ms")
		err := cmd.ExecuteContext(context.Background())
		if broken && (err == nil || marked) {
			t.Fatalf("broken pipe: err=%v marked=%v, want error and no mark", err, marked)
		}
		if !broken && (err != nil || !marked) {
			t.Fatalf("healthy: err=%v marked=%v, want nil and mark", err, marked)
		}
	}
}
