// Package cmd assembles the hadron command tree and owns the single
// error → exit-code → rendering path.
package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	accesscmd "github.com/hadron-memory/hadron-cli/internal/cmd/access"
	agentcmd "github.com/hadron-memory/hadron-cli/internal/cmd/agent"
	"github.com/hadron-memory/hadron-cli/internal/cmd/agentic"
	aiconfigcmd "github.com/hadron-memory/hadron-cli/internal/cmd/aiconfig"
	"github.com/hadron-memory/hadron-cli/internal/cmd/apicmd"
	appcmd "github.com/hadron-memory/hadron-cli/internal/cmd/app"
	authcmd "github.com/hadron-memory/hadron-cli/internal/cmd/auth"
	chatcmd "github.com/hadron-memory/hadron-cli/internal/cmd/chat"
	"github.com/hadron-memory/hadron-cli/internal/cmd/configcmd"
	connectioncmd "github.com/hadron-memory/hadron-cli/internal/cmd/connection"
	edgecmd "github.com/hadron-memory/hadron-cli/internal/cmd/edge"
	grantcmd "github.com/hadron-memory/hadron-cli/internal/cmd/grant"
	mcpservercmd "github.com/hadron-memory/hadron-cli/internal/cmd/mcpserver"
	memorycmd "github.com/hadron-memory/hadron-cli/internal/cmd/memory"
	nodecmd "github.com/hadron-memory/hadron-cli/internal/cmd/node"
	objectcmd "github.com/hadron-memory/hadron-cli/internal/cmd/object"
	orgcmd "github.com/hadron-memory/hadron-cli/internal/cmd/org"
	"github.com/hadron-memory/hadron-cli/internal/cmd/replacecmd"
	runcmd "github.com/hadron-memory/hadron-cli/internal/cmd/run"
	schedulecmd "github.com/hadron-memory/hadron-cli/internal/cmd/schedule"
	searchcmd "github.com/hadron-memory/hadron-cli/internal/cmd/search"
	secretcmd "github.com/hadron-memory/hadron-cli/internal/cmd/secret"
	speccmd "github.com/hadron-memory/hadron-cli/internal/cmd/spec"
	taskcmd "github.com/hadron-memory/hadron-cli/internal/cmd/task"
	ticketcmd "github.com/hadron-memory/hadron-cli/internal/cmd/ticket"
	usercmd "github.com/hadron-memory/hadron-cli/internal/cmd/user"
	versioncmd "github.com/hadron-memory/hadron-cli/internal/cmd/version"
	webhookcmd "github.com/hadron-memory/hadron-cli/internal/cmd/webhook"
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
	root.AddCommand(objectcmd.NewCmdObject(f))
	root.AddCommand(searchcmd.NewCmdSearch(f))
	root.AddCommand(edgecmd.NewCmdEdge(f))
	root.AddCommand(taskcmd.NewCmdTask(f))
	root.AddCommand(chatcmd.NewCmdChat(f))
	root.AddCommand(replacecmd.NewCmdReplace(f))
	root.AddCommand(speccmd.NewCmdSpec(f))
	root.AddCommand(appcmd.NewCmdApp(f))
	root.AddCommand(orgcmd.NewCmdOrg(f))
	root.AddCommand(agentcmd.NewCmdAgent(f))
	root.AddCommand(usercmd.NewCmdUser(f))
	root.AddCommand(usercmd.NewCmdProfile(f))
	root.AddCommand(accesscmd.NewCmdAccess(f))
	root.AddCommand(aiconfigcmd.NewCmdAiConfig(f))
	root.AddCommand(runcmd.NewCmdRun(f))
	root.AddCommand(schedulecmd.NewCmdSchedule(f))
	root.AddCommand(webhookcmd.NewCmdWebhook(f))
	root.AddCommand(ticketcmd.NewCmdTicket(f))
	root.AddCommand(grantcmd.NewCmdGrant(f))
	root.AddCommand(connectioncmd.NewCmdConnection(f))
	root.AddCommand(mcpservercmd.NewCmdMcpServer(f))
	root.AddCommand(secretcmd.NewCmdSecret(f))
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
		strings.HasPrefix(msg, "requires ") ||
		// cobra's MarkFlagRequired failure ("required flag(s) \"x\" not set") —
		// a missing required flag is a usage error, so it must exit 2 like the
		// other flag/arg validation failures, not the generic error code.
		strings.HasPrefix(msg, "required flag")
}
