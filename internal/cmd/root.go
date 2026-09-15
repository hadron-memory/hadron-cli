// Package cmd assembles the hadron command tree and owns the single
// error → exit-code → rendering path.
package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/build"
	accesscmd "github.com/hadron-memory/hadron-cli/internal/cmd/access"
	agentcmd "github.com/hadron-memory/hadron-cli/internal/cmd/agent"
	"github.com/hadron-memory/hadron-cli/internal/cmd/agentic"
	aiconfigcmd "github.com/hadron-memory/hadron-cli/internal/cmd/aiconfig"
	"github.com/hadron-memory/hadron-cli/internal/cmd/apicmd"
	appcmd "github.com/hadron-memory/hadron-cli/internal/cmd/app"
	assetcmd "github.com/hadron-memory/hadron-cli/internal/cmd/asset"
	authcmd "github.com/hadron-memory/hadron-cli/internal/cmd/auth"
	chatcmd "github.com/hadron-memory/hadron-cli/internal/cmd/chat"
	codingcmd "github.com/hadron-memory/hadron-cli/internal/cmd/coding"
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
	"github.com/hadron-memory/hadron-cli/internal/cmd/serverinfo"
	speccmd "github.com/hadron-memory/hadron-cli/internal/cmd/spec"
	taskcmd "github.com/hadron-memory/hadron-cli/internal/cmd/task"
	teamcmd "github.com/hadron-memory/hadron-cli/internal/cmd/team"
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
		// #393: `--version` and `-v` are what a human reaches for first —
		// setting Version registers both (cobra takes the free `v` shorthand),
		// and the template reproduces `hadron version`'s human line so the two
		// spellings agree. `hadron version` stays the fuller surface (--json,
		// Go/OS/arch); this is the quick spelling. Output routes through the
		// factory streams (SetOut/SetErr below) so it is not written straight
		// to os.Stdout.
		Version: build.Version,
	}
	root.SetVersionTemplate("hadron {{.Version}} (" + build.Commit + ", " + build.Date + ")\n")
	root.SetOut(f.IOStreams.Out)
	root.SetErr(f.IOStreams.ErrOut)

	root.PersistentFlags().BoolVar(&f.JSON, "json", false, "output JSON instead of text")
	root.PersistentFlags().StringVar(&f.ServerFlag, "server", "", "Hadron server base URL (overrides config)")
	root.PersistentFlags().StringVar(&f.AppFlag, "app", "", "App context for this invocation: hrn:app:<root>:<slug>, <root>:<slug>, or an App id (overrides config)")

	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return exitcode.New(exitcode.Usage, err)
	})

	root.AddCommand(authcmd.NewCmdAuth(f))
	root.AddCommand(memorycmd.NewCmdMemory(f))
	root.AddCommand(nodecmd.NewCmdNode(f))
	root.AddCommand(objectcmd.NewCmdObject(f))
	root.AddCommand(assetcmd.NewCmdAsset(f))
	root.AddCommand(searchcmd.NewCmdSearch(f))
	root.AddCommand(edgecmd.NewCmdEdge(f))
	root.AddCommand(taskcmd.NewCmdTask(f))
	root.AddCommand(chatcmd.NewCmdChat(f))
	root.AddCommand(replacecmd.NewCmdReplace(f))
	root.AddCommand(speccmd.NewCmdSpec(f))
	root.AddCommand(codingcmd.NewCmdCoding(f))
	root.AddCommand(appcmd.NewCmdApp(f))
	root.AddCommand(orgcmd.NewCmdOrg(f))
	root.AddCommand(agentcmd.NewCmdAgent(f))
	root.AddCommand(teamcmd.NewCmdTeam(f))
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
	root.AddCommand(serverinfo.NewCmdServerInfo(f))
	root.AddCommand(agentic.NewCmdAgenticUsage(f))

	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	f := cmdutil.NewFactory()
	root := NewRootCmd(f)

	// Catch an unknown subcommand before cobra dispatches — it would
	// otherwise print the group's help and exit 0 (#232). This runs
	// before cobra binds --json, so take that from the probe.
	if jsonOut, err := checkUnknownSubcommand(os.Args[1:]); err != nil {
		f.JSON = jsonOut
		return renderError(f, err)
	}

	err := root.Execute()
	if err == nil {
		return exitcode.OK
	}

	return renderFailure(f, os.Args[1:], err)
}

// renderFailure is the post-Execute half of the failure path, extracted so a
// test can measure the WIRING rather than only the helpers it calls.
//
// That extraction is the point, not tidiness: a test that calls jsonRequested
// itself and then hands the result to renderError passes whether or not
// anything actually connects the two — which is what mine did until removing
// the binding below failed to turn it red. Same reasoning as exitCodeFor's
// extraction for #533, one function over.
//
// A FLAG-parse failure never binds --json (#334): cobra aborts the parse at the
// bad flag, so f.JSON is still false and the envelope would degrade to plain
// text — leaving `--json` a promise with a hole in it, since a script that has
// switched to parsing JSON meets raw prose the first time someone typos a flag.
// Unknown SUBCOMMANDS were already covered by the probe in Execute taking
// --json for itself; this is the same remedy one layer down.
//
// Consulted ONLY when f.JSON is already false, so a successful bind always wins
// and this can never turn --json off.
func renderFailure(f *cmdutil.Factory, args []string, err error) int {
	if !f.JSON {
		f.JSON = jsonRequested(args)
	}
	return renderError(f, err)
}

// jsonRequested reports whether --json appears among args, by a TOLERANT parse
// against the TARGET COMMAND'S OWN FLAGS.
//
// A scan for the literal string is what this looks like it should be, and it is
// wrong: `--json` can be the VALUE of another flag (`-m --json`, `--name
// --json`), and a substring match would switch a caller into JSON output who
// never asked for it.
//
// But parsing against a bare flagset holding only `json` is wrong for the SAME
// reason, which is the part worth writing down — my first version did exactly
// that and this function's own test caught it. pflag can only tell a flag from
// a value when it knows the flag takes one, so with `-m` undefined, `-m --json`
// parses `--json` as a flag and the false positive comes straight back. The
// knowledge lives on the target command, so this resolves it first, the way
// checkUnknownSubcommand does.
//
// Errors are ignored on purpose: this runs only because a parse ALREADY failed,
// so a second failure means the flag could not be determined, and false — plain
// text — is the safe answer.
func jsonRequested(args []string) bool {
	root := NewRootCmd(cmdutil.NewFactory())
	target, rest, err := root.Find(args)
	if err != nil || target == nil {
		target, rest = root, args
	}
	target.InitDefaultHelpFlag()
	flags := target.Flags()
	flags.ParseErrorsAllowlist.UnknownFlags = true
	flags.SetOutput(io.Discard)
	if perr := flags.Parse(rest); perr != nil {
		return false
	}
	v, _ := flags.GetBool("json")
	return v
}

// renderError maps err to an exit code and writes it in the format the --json
// contract requires.
//
// UNDER --json THE ENVELOPE GOES TO STDOUT (#334), so `--json` means "stdout is
// always valid JSON" whatever the outcome. Before this, a single-entity failure
// left stdout EMPTY and put the envelope on stderr, so the obvious consumer —
//
//	json.loads(subprocess.run([...,'--json'], capture_output=True).stdout)
//
// died with "Expecting value: line 1 column 1", which reads like corrupt data
// or a parser bug rather than a server error. The diagnostic existed, in
// structured form, somewhere the caller was not looking. It also made the two
// `node get` forms disagree: the BATCHED one already keeps stdout valid JSON
// and reports failure through the exit code plus an `unavailable` array.
//
// Exit codes are untouched. They were already load-bearing and correct, and a
// caller should still branch on them first — the envelope tells you WHAT went
// wrong, the code tells you it went wrong at all.
//
// The one case that still goes to stderr is a command that has ALREADY written
// to stdout. Appending an envelope there would concatenate two JSON values and
// produce the unparseable stdout this change exists to remove, arrived at from
// the other end. Asked of the stream rather than assumed of the caller: see
// output.Wrote for the two paths that legitimately print and then fail.
func renderError(f *cmdutil.Factory, err error) int {
	code := exitCodeFor(err)

	if !errors.Is(err, exitcode.ErrSilent) {
		switch {
		case f.JSON && !f.IOStreams.Wrote():
			_ = output.WriteJSON(f.IOStreams.Out, map[string]any{
				"error": map[string]any{"code": code, "message": err.Error()},
			})
		case f.JSON:
			// Payload already in flight; keep stdout a single valid document.
			_ = output.WriteJSON(f.IOStreams.ErrOut, map[string]any{
				"error": map[string]any{"code": code, "message": err.Error()},
			})
		default:
			fmt.Fprintf(f.IOStreams.ErrOut, "hadron: %s\n", err.Error())
		}
	}
	return code
}

// exitCodeFor is THE mapping from a command error to the process exit code, and
// it is extracted so a test can measure what a user actually gets
// (hadron-cli#533).
//
// The classification below is not reachable from `exitcode.FromError` alone:
// cobra's own refusals are plain errors carrying no exit code, so they read as
// the generic 1 until this runs. A command-level test calling
// `exitcode.FromError(root.Execute())` therefore measures a DIFFERENT value
// from the binary — and silently, since both are ints and the test's answer is
// only wrong for the errors cobra raises itself.
//
// That bit on #533: marking flags required moved three refusals from a
// hand-rolled `exitcode.Usage` to cobra's `required flag(s) … not set`. The
// user-visible exit code stayed 2 throughout, and three tests went red anyway,
// because they were asserting the pre-classification value.
func exitCodeFor(err error) int {
	code := exitcode.FromError(err)
	// Cobra reports unknown commands/arguments as plain errors;
	// classify them as usage errors so exit code 2 stays meaningful.
	if code == exitcode.Error && isUsageError(err) {
		return exitcode.Usage
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
		strings.HasPrefix(msg, "required flag") ||
		// cobra's flag-group failures, same reasoning: MarkFlagsMutuallyExclusive
		// ("if any flags in the group …") and MarkFlagsOneRequired ("at least one
		// of the flags in the group …"). Both are the user combining flags
		// wrongly, and both were exiting 1 for every command that declares a
		// group (chat post/read, auth login, mcpserver update, …).
		strings.HasPrefix(msg, "if any flags in the group") ||
		strings.HasPrefix(msg, "at least one of the flags in the group")
}
