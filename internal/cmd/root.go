// Package cmd assembles the hadron command tree and owns the single
// error → exit-code → rendering path.
package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmd/agentic"
	"github.com/hadron-memory/hadron-cli/internal/cmd/apicmd"
	appcmd "github.com/hadron-memory/hadron-cli/internal/cmd/app"
	authcmd "github.com/hadron-memory/hadron-cli/internal/cmd/auth"
	"github.com/hadron-memory/hadron-cli/internal/cmd/configcmd"
	edgecmd "github.com/hadron-memory/hadron-cli/internal/cmd/edge"
	memorycmd "github.com/hadron-memory/hadron-cli/internal/cmd/memory"
	nodecmd "github.com/hadron-memory/hadron-cli/internal/cmd/node"
	speccmd "github.com/hadron-memory/hadron-cli/internal/cmd/spec"
	versioncmd "github.com/hadron-memory/hadron-cli/internal/cmd/version"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func NewRootCmd(f *cmdutil.Factory) *cobra.Command {
	root := &cobra.Command{
		Use:           "hadron <command> <subcommand>",
		Short:         "The Hadron platform CLI",
		Long:          "Work with Hadron memories, nodes, and Apps from the command line.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().BoolVar(&f.JSON, "json", false, "output JSON instead of text")
	root.PersistentFlags().StringVar(&f.ServerFlag, "server", "", "Hadron server base URL (overrides config)")
	root.PersistentFlags().StringVar(&f.AppFlag, "app", "", "App URN context for this invocation (overrides config)")

	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return exitcode.New(exitcode.Usage, err)
	})

	root.AddCommand(authcmd.NewCmdAuth(f))
	root.AddCommand(memorycmd.NewCmdMemory(f))
	root.AddCommand(nodecmd.NewCmdNode(f))
	root.AddCommand(edgecmd.NewCmdEdge(f))
	root.AddCommand(speccmd.NewCmdSpec(f))
	root.AddCommand(appcmd.NewCmdApp(f))
	root.AddCommand(configcmd.NewCmdConfig(f))
	root.AddCommand(apicmd.NewCmdAPI(f))
	root.AddCommand(versioncmd.NewCmdVersion(f))
	root.AddCommand(agentic.NewCmdAgenticUsage(f))

	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	f := cmdutil.NewFactory()
	root := NewRootCmd(f)

	err := root.Execute()
	if err == nil {
		return exitcode.OK
	}

	code := exitcode.FromError(err)
	// Cobra reports unknown commands/arguments as plain errors;
	// classify them as usage errors so exit code 2 stays meaningful.
	if code == exitcode.Error && isUsageError(err) {
		code = exitcode.Usage
	}

	if !errors.Is(err, exitcode.ErrSilent) {
		if f.JSON {
			_ = output.WriteJSON(f.IOStreams.ErrOut, map[string]any{
				"error": map[string]any{"code": code, "message": err.Error()},
			})
		} else {
			fmt.Fprintf(f.IOStreams.ErrOut, "hadron: %s\n", err.Error())
		}
	}
	return code
}

func isUsageError(err error) bool {
	msg := err.Error()
	return strings.HasPrefix(msg, "unknown command") ||
		strings.HasPrefix(msg, "unknown flag") ||
		strings.HasPrefix(msg, "accepts ") ||
		strings.HasPrefix(msg, "requires ")
}
