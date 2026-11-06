package cmd

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

const authoredHelp = `

--authored-with <task URN or id> and --authored-revision <n> state the task
revision used to author these writes. Pass both or neither; one alone is
refused before any request. With neither, no claim is sent. This slice never
infers the task. A multi-write command carries the same claim on each node
write that accepts it. This is attested authorship, not verified compliance:
the check does not prove that the task was read or followed.`

type authoredCommandError struct{ error }

func (e *authoredCommandError) Unwrap() error { return e.error }

// Install on the assembled tree so validation precedes every command's reads,
// including all scaffold/retirement writes that share the command context.
func installAuthoredWith(root *cobra.Command) {
	paths := map[string]bool{"node create": true, "node update": true, "node set-properties": true, "node merge": true, "replace text": true, "spec new": true, "spec edit": true, "spec replace": true, "spec supersede": true, "spec extract": true}
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		path := strings.TrimPrefix(cmd.CommandPath(), root.Name()+" ")
		if paths[path] && cmd.RunE != nil {
			cmd.Flags().String("authored-with", "", "task URN or id explicitly used to author the write (pair with --authored-revision)")
			cmd.Flags().Int("authored-revision", 0, "task revision explicitly used (pair with --authored-with)")
			cmd.Long += authoredHelp
			run := cmd.RunE
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				taskSet, revSet := cmd.Flags().Changed("authored-with"), cmd.Flags().Changed("authored-revision")
				if taskSet != revSet {
					return exitcode.Newf(exitcode.Usage, "pass both --authored-with and --authored-revision, or neither; nothing was requested")
				}
				var claim *gen.AuthoredWithInput
				if taskSet {
					task, _ := cmd.Flags().GetString("authored-with")
					task = strings.TrimSpace(task)
					rev, _ := cmd.Flags().GetInt("authored-revision")
					if task == "" || int64(rev) < math.MinInt32 || int64(rev) > math.MaxInt32 {
						return exitcode.Newf(exitcode.Usage, "--authored-with must name a task and --authored-revision must be a GraphQL integer")
					}
					claim = &gen.AuthoredWithInput{Task: task, Revision: rev}
				}
				previous := cmd.Context()
				cmd.SetContext(cmdutil.WithAuthoredWith(previous, claim))
				defer cmd.SetContext(previous)
				if err := run(cmd, args); err != nil {
					return &authoredCommandError{err}
				}
				return nil
			}
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
}

func authoredRefusalText(err error) (string, bool) {
	d := api.AuthoringAttestationDetails(err)
	if d == nil {
		return "", false
	}
	if d["taskAccess"] != "READABLE" {
		return "the authoring task is unreadable or unusable; an owner must fix access or the rule", true
	}
	task, _ := d["task"].(string)
	if task == "" {
		task, _ = d["taskId"].(string)
	}
	msg := humanErrorText(err) + fmt.Sprintf("\nRequired task: %s, revision %v", output.SafeLine(task, 512), d["taskRevision"])
	if load, ok := d["load"].(map[string]any); ok {
		if cli, ok := load["cli"].(string); ok && cli != "" {
			msg += "\nLoad: " + output.SafeLine(cli, 2048)
		}
	}
	var flags *authoredCommandError
	if errors.As(err, &flags) && task != "" && output.SafeLine(task, 512) == task && d["taskRevision"] != nil {
		msg += fmt.Sprintf("\nRetry with: --authored-with %s --authored-revision %v", cmdutil.ShellQuote(task), d["taskRevision"])
	}
	return msg, true
}
