package team

// cli#935 — the operator attention surface is retired with no replacement
// API (hadron-server#2027, comment 6567317725). `team attention`, its
// switchover and `team router` stay as discoverable commands so an old script
// or habit gets a readable answer instead of "unknown command". Each one
// returns the retirement error, Usage exit 2, and does nothing else: no
// config, credential or binding read, no request, no terminal, no state
// directory, and no echo of a --since or --proof value. Their old flags still
// parse, so the message is reached; an unknown flag stays an ordinary usage
// error. Removing a command outright is a separately gated change.

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const (
	retiredAttention  = `hadron team attention is retired (hadron-server#2027). It has no replacement unread or token view. Read your own chat with "hadron team chat read".`
	retiredSwitchover = retiredAttention + ` No backlog was marked read.`
	retiredRouter     = `hadron team router is retired (hadron-server#2027): the attention API it polled is being removed. Wakes come from tickers (hadron-server#2138). Nothing was polled or typed.`
)

// retired is the RunE of every retired command: the fixed message, nothing
// else. Plain text with no format verbs, so no argument can reach it.
func retired(msg string) func(*cobra.Command, []string) error {
	return func(*cobra.Command, []string) error {
		return exitcode.Newf(exitcode.Usage, "%s", msg)
	}
}

const retiredHelpTail = `

It is kept so that an old script or habit gets this answer rather than
"unknown command". Its flags are still accepted and ignored. It reads no
config or credentials and makes no request.`

// The Factory is deliberately unused: a stub resolves nothing through it.
func newCmdAttention(_ *cmdutil.Factory) *cobra.Command {
	var since string
	cmd := &cobra.Command{
		Use:   "attention [--since <token>]",
		Short: "Retired (hadron-server#2027): exits 2 with a retirement notice",
		Long: `This command is retired (hadron-server#2027). It has no replacement unread
or token view. Read your own chat with "hadron team chat read".` + retiredHelpTail,
		Args: cobra.NoArgs,
		RunE: retired(retiredAttention),
	}
	cmd.Flags().StringVar(&since, "since", "", "ignored; the command is retired")
	cmd.AddCommand(newCmdAttentionSwitchover())
	return cmd
}

func newCmdAttentionSwitchover() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "switchover <command>",
		Short: "Retired (hadron-server#2027): preview and apply exit 2 and mark nothing read",
		Long: `The switchover is retired (hadron-server#2027) with team attention. Its
preview and apply subcommands exit 2 and mark no backlog read.`,
	}
	var proof string
	var yes bool
	preview := &cobra.Command{
		Use:   "preview",
		Short: "Retired (hadron-server#2027): exits 2, marks nothing read",
		Long: `This command is retired (hadron-server#2027). It has no replacement unread
or token view, and no backlog is marked read.` + retiredHelpTail,
		Args: cobra.NoArgs,
		RunE: retired(retiredSwitchover),
	}
	apply := &cobra.Command{
		Use:   "apply [--proof <proof>] [--yes]",
		Short: "Retired (hadron-server#2027): exits 2, marks nothing read",
		Long:  preview.Long,
		Args:  cobra.NoArgs,
		RunE:  retired(retiredSwitchover),
	}
	apply.Flags().StringVar(&proof, "proof", "", "ignored; the command is retired")
	apply.Flags().BoolVar(&yes, "yes", false, "ignored; the command is retired")
	cmd.AddCommand(preview, apply)
	return cmd
}

func newCmdTeamRouter(_ *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "router <command>",
		Short: "Retired (hadron-server#2027): poll, loop and status exit 2",
		Long: `The router is retired (hadron-server#2027): the attention API it polled is
being removed. Wakes come from tickers (hadron-server#2138). Its poll, loop
and status subcommands exit 2 and poll, type or read nothing.`,
	}
	for _, sub := range []struct{ use, short string }{
		{"poll [--config <path>]", "Retired (hadron-server#2027): exits 2, polls nothing"},
		{"loop [--config <path>] [--interval 60s]", "Retired (hadron-server#2027): exits 2, polls nothing"},
		{"status [--config <path>]", "Retired (hadron-server#2027): exits 2, reads nothing"},
	} {
		var config string
		var interval time.Duration
		c := &cobra.Command{
			Use:   sub.use,
			Short: sub.short,
			Long: `This command is retired (hadron-server#2027): the attention API it polled is
being removed. Wakes come from tickers (hadron-server#2138). Nothing is
polled or typed.` + retiredHelpTail,
			Args: cobra.NoArgs,
			RunE: retired(retiredRouter),
		}
		c.Flags().StringVar(&config, "config", "", "ignored; the command is retired")
		if c.Name() == "loop" {
			c.Flags().DurationVar(&interval, "interval", 60*time.Second, "ignored; the command is retired")
		}
		cmd.AddCommand(c)
	}
	return cmd
}
