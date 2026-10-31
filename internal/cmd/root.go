// Package cmd assembles the hadron command tree and owns the single
// error → exit-code → rendering path.
package cmd

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/build"
	accesscmd "github.com/hadron-memory/hadron-cli/internal/cmd/access"
	agentcmd "github.com/hadron-memory/hadron-cli/internal/cmd/agent"
	"github.com/hadron-memory/hadron-cli/internal/cmd/agentic"
	aiconfigcmd "github.com/hadron-memory/hadron-cli/internal/cmd/aiconfig"
	"github.com/hadron-memory/hadron-cli/internal/cmd/apicmd"
	appcmd "github.com/hadron-memory/hadron-cli/internal/cmd/app"
	assetcmd "github.com/hadron-memory/hadron-cli/internal/cmd/asset"
	authcmd "github.com/hadron-memory/hadron-cli/internal/cmd/auth"
	automationcmd "github.com/hadron-memory/hadron-cli/internal/cmd/automation"
	billingcmd "github.com/hadron-memory/hadron-cli/internal/cmd/billing"
	calendarcmd "github.com/hadron-memory/hadron-cli/internal/cmd/calendar"
	channelcmd "github.com/hadron-memory/hadron-cli/internal/cmd/channel"
	chatcmd "github.com/hadron-memory/hadron-cli/internal/cmd/chat"
	codingcmd "github.com/hadron-memory/hadron-cli/internal/cmd/coding"
	commentcmd "github.com/hadron-memory/hadron-cli/internal/cmd/comment"
	"github.com/hadron-memory/hadron-cli/internal/cmd/configcmd"
	connectioncmd "github.com/hadron-memory/hadron-cli/internal/cmd/connection"
	drivecmd "github.com/hadron-memory/hadron-cli/internal/cmd/drive"
	edgecmd "github.com/hadron-memory/hadron-cli/internal/cmd/edge"
	emailcmd "github.com/hadron-memory/hadron-cli/internal/cmd/email"
	grantcmd "github.com/hadron-memory/hadron-cli/internal/cmd/grant"
	hacmd "github.com/hadron-memory/hadron-cli/internal/cmd/ha"
	mcpservercmd "github.com/hadron-memory/hadron-cli/internal/cmd/mcpserver"
	memorycmd "github.com/hadron-memory/hadron-cli/internal/cmd/memory"
	nodecmd "github.com/hadron-memory/hadron-cli/internal/cmd/node"
	objectcmd "github.com/hadron-memory/hadron-cli/internal/cmd/object"
	orcacmd "github.com/hadron-memory/hadron-cli/internal/cmd/orca"
	orgcmd "github.com/hadron-memory/hadron-cli/internal/cmd/org"
	"github.com/hadron-memory/hadron-cli/internal/cmd/replacecmd"
	runcmd "github.com/hadron-memory/hadron-cli/internal/cmd/run"
	schedulecmd "github.com/hadron-memory/hadron-cli/internal/cmd/schedule"
	scopecmd "github.com/hadron-memory/hadron-cli/internal/cmd/scope"
	searchcmd "github.com/hadron-memory/hadron-cli/internal/cmd/search"
	secretcmd "github.com/hadron-memory/hadron-cli/internal/cmd/secret"
	"github.com/hadron-memory/hadron-cli/internal/cmd/serverinfo"
	skillcmd "github.com/hadron-memory/hadron-cli/internal/cmd/skill"
	slackcmd "github.com/hadron-memory/hadron-cli/internal/cmd/slack"
	speccmd "github.com/hadron-memory/hadron-cli/internal/cmd/spec"
	taskcmd "github.com/hadron-memory/hadron-cli/internal/cmd/task"
	teamcmd "github.com/hadron-memory/hadron-cli/internal/cmd/team"
	tickercmd "github.com/hadron-memory/hadron-cli/internal/cmd/ticker"
	ticketcmd "github.com/hadron-memory/hadron-cli/internal/cmd/ticket"
	usagecmd "github.com/hadron-memory/hadron-cli/internal/cmd/usage"
	usercmd "github.com/hadron-memory/hadron-cli/internal/cmd/user"
	versioncmd "github.com/hadron-memory/hadron-cli/internal/cmd/version"
	webhookcmd "github.com/hadron-memory/hadron-cli/internal/cmd/webhook"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func NewRootCmd(f *cmdutil.Factory) *cobra.Command {
	f.GraphQLRequestContext = teamcmd.AuthoredWriteContext(f)
	root := &cobra.Command{
		Use:   "hadron <command> <subcommand>",
		Short: "The Hadron platform CLI",
		Long: `Read and write your Hadron memory from the command line. A memory is a
collection of notes (nodes) joined by links (edges); an App bundles memories,
agents and settings for a team or product.

Getting started:
  hadron auth login          sign in (opens your browser)
  hadron auth status         check you are signed in, and to which server
  hadron server-info         check the server answers (works signed out)
  hadron memory list         see the memories you can read
  hadron search "<words>"    find notes across them

Which server, App and memory a command uses: by default the hosted server
(https://srv.hadronmemory.com) and no App. A setting saved with
'hadron config set' (or 'app set-active', 'memory set-active') applies to
every later command until you change it; 'hadron config list' shows them. The
--server and --app flags override the saved setting for one command; the
HADRON_SERVER environment variable overrides the saved server too.

--json prints machine-readable JSON instead of text on commands that have a
JSON form (most do); 'hadron agentic-usage' documents every shape. -v and
--version print the same line as 'hadron version'.

If you work as a team worker in a worktree bound with 'hadron team session',
the node and edge writes you make are also recorded against that worker once
the binding is verified. Otherwise this does not apply to you.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// #914's ended-binding check is armed HERE, on the root that actually
		// runs, after its flags (--server) are parsed. Not at construction:
		// Execute also builds probe roots (unknown-subcommand, --json), each
		// with a fresh Factory, and arming at construction let the last probe
		// win, so a real run checked through a Factory that never saw
		// --server. No subcommand defines its own PersistentPreRun, so this
		// one always runs.
		PersistentPreRunE: func(*cobra.Command, []string) error {
			teamcmd.InstallEndedBindingCheck(f)
			return nil
		},
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
	root.AddCommand(usagecmd.NewCmdUsage(f))
	root.AddCommand(nodecmd.NewCmdNode(f))
	root.AddCommand(objectcmd.NewCmdObject(f))
	root.AddCommand(assetcmd.NewCmdAsset(f))
	root.AddCommand(searchcmd.NewCmdSearch(f))
	root.AddCommand(edgecmd.NewCmdEdge(f))
	root.AddCommand(commentcmd.NewCmd(f, commentcmd.ConnectGraphQL(f)))
	root.AddCommand(taskcmd.NewCmdTask(f))
	root.AddCommand(chatcmd.NewCmdChat(f))
	root.AddCommand(channelcmd.NewCmdChannel(f))
	root.AddCommand(replacecmd.NewCmdReplace(f))
	root.AddCommand(speccmd.NewCmdSpec(f))
	root.AddCommand(skillcmd.NewCmdSkill(f))
	root.AddCommand(codingcmd.NewCmdCoding(f))
	root.AddCommand(appcmd.NewCmdApp(f))
	root.AddCommand(automationcmd.NewCmdAutomation(f))
	root.AddCommand(orgcmd.NewCmdOrg(f))
	root.AddCommand(scopecmd.NewCmdScope(f))
	root.AddCommand(agentcmd.NewCmdAgent(f))
	root.AddCommand(orcacmd.NewCmdOrca(f))
	root.AddCommand(teamcmd.NewCmdTeam(f))
	root.AddCommand(tickercmd.NewCmdTicker(f))
	root.AddCommand(usercmd.NewCmdUser(f))
	root.AddCommand(usercmd.NewCmdProfile(f))
	root.AddCommand(accesscmd.NewCmdAccess(f))
	root.AddCommand(aiconfigcmd.NewCmdAiConfig(f))
	root.AddCommand(billingcmd.NewCmdBilling(f))
	root.AddCommand(runcmd.NewCmdRun(f))
	root.AddCommand(schedulecmd.NewCmdSchedule(f))
	root.AddCommand(webhookcmd.NewCmdWebhook(f))
	root.AddCommand(ticketcmd.NewCmdTicket(f))
	root.AddCommand(grantcmd.NewCmdGrant(f))
	root.AddCommand(connectioncmd.NewCmdConnection(f))
	root.AddCommand(hacmd.NewCmdHa(f))
	root.AddCommand(emailcmd.NewCmdEmail(f))
	root.AddCommand(slackcmd.NewCmdSlack(f))
	root.AddCommand(drivecmd.NewCmdDrive(f))
	root.AddCommand(calendarcmd.NewCmdCalendar(f))
	root.AddCommand(mcpservercmd.NewCmdMcpServer(f))
	root.AddCommand(secretcmd.NewCmdSecret(f))
	root.AddCommand(configcmd.NewCmdConfig(f))
	root.AddCommand(apicmd.NewCmdAPI(f))
	root.AddCommand(versioncmd.NewCmdVersion(f))
	root.AddCommand(serverinfo.NewCmdServerInfo(f))
	root.AddCommand(agentic.NewCmdAgenticUsage(f))

	installNodeWriteWarnings(root, f)
	describeGeneratedCommands(root)
	return root
}

// describeGeneratedCommands replaces cobra's stock text on `help` and
// `completion` with text a user can act on (#1056). Cobra adds both lazily at
// execute time; initializing them here lets the text be set, and cobra then
// reuses them rather than adding its own.
func describeGeneratedCommands(root *cobra.Command) {
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	for _, c := range root.Commands() {
		switch c.Name() {
		case "help":
			c.Short = "Show help for any command"
			c.Long = `Show the help page for a command. 'hadron help <command>' and
'hadron <command> --help' print the same page; every command takes --help.

For the full reference an AI assistant uses (every command, the --json shapes
and exit codes), see 'hadron agentic-usage'.`
			c.Example = `  hadron help node create
  hadron node create --help`
		case "completion":
			c.Short = "Print a tab-completion script for your shell"
			c.Long = `Print a script that lets your shell complete hadron commands and flags
when you press Tab. It only prints the script; nothing changes until you load
it. Run 'hadron completion <shell> --help' for how to install it in bash, zsh,
fish or powershell. 'echo $SHELL' tells you which shell you use.`
		}
	}
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

// jsonRequested reports whether --json appears among args, by an ARITY-AWARE
// WALK of the TARGET COMMAND'S OWN FLAGS.
//
// A scan for the literal string is what this looks like it should be, and it is
// wrong: `--json` can be the VALUE of another flag (`-m --json`, `--name
// --json`), and a substring match would switch a caller into JSON output who
// never asked for it.
//
// But parsing against a bare flagset holding only `json` is wrong for the SAME
// reason, which is worth writing down — my first version did exactly that and
// this function's own test caught it. pflag can only tell a flag from a value
// when it knows the flag takes one, so with `-m` undefined, `-m --json` parses
// `--json` as a flag and the false positive comes straight back. The knowledge
// lives on the target command, so this resolves it first, the way
// checkUnknownSubcommand does.
//
// And PARSING against those flags is wrong too, which took a reviewer to see:
// pflag stops at the first error, so `--limit nope --json` dies converting the
// int and reports no --json — on exactly the malformed-value failure this probe
// exists to render. Hence a walk that reads only arity.
func jsonRequested(args []string) bool {
	root := NewRootCmd(cmdutil.NewFactory())
	target, rest, err := root.Find(args)
	if err != nil || target == nil {
		target, rest = root, args
	}
	target.InitDefaultHelpFlag()
	flags := target.Flags()

	// A WALK, not a parse. pflag stops at the first error, so parsing cannot
	// answer this: `--limit nope --json` dies converting "nope" to an int and
	// reports no --json at all (PR #585 review, @copilot) — and a malformed
	// flag VALUE is precisely when this probe runs, since that is one of the
	// failures it exists to render. The walk needs only arity, which the
	// command's flag definitions supply even when parsing them fails.
	takesValue := func(f *pflag.Flag) bool { return f != nil && f.Value.Type() != "bool" }
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--":
			// Everything after this is positional, including a literal --json.
			return false
		case a == "--json":
			return true
		case strings.HasPrefix(a, "--json="):
			v, perr := strconv.ParseBool(strings.TrimPrefix(a, "--json="))
			return perr == nil && v
		case strings.HasPrefix(a, "--"):
			name := strings.TrimPrefix(a, "--")
			if strings.Contains(name, "=") {
				continue // --flag=value carries its own value
			}
			if takesValue(flags.Lookup(name)) {
				i++ // the next token is this flag's VALUE, not a flag
			}
		case len(a) > 1 && a[0] == '-':
			// Only a lone shorthand can consume the next token; a cluster
			// (-abc) ends in a value-taking flag at most, and mis-skipping one
			// token here costs nothing this probe cares about.
			if sh := strings.TrimPrefix(a, "-"); len(sh) == 1 && takesValue(flags.ShorthandLookup(sh)) {
				i++
			}
		}
	}
	return false
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
// humanMessager is implemented by an error whose raw text is right for the
// --json envelope but unsafe to print on a terminal (cli#1131); the human
// branch asks it for the terminal-safe form.
type humanMessager interface{ HumanMessage() string }

func humanErrorText(err error) string {
	var h humanMessager
	if errors.As(err, &h) {
		return h.HumanMessage()
	}
	return err.Error()
}

func renderError(f *cmdutil.Factory, err error) int {
	code := exitCodeFor(err)

	if !errors.Is(err, exitcode.ErrSilent) {
		failure := map[string]any{"code": code, "message": err.Error()}
		// cli#898: additive fields for an internal fault; `code` already means
		// the exit code, so the server's code is a new key.
		if fault, ok := api.InternalFaultOf(err); ok {
			failure["serverCode"] = fault.ServerCode
			if fault.RequestID != "" {
				failure["requestId"] = fault.RequestID
			}
		}
		// #1306: scripts need the deliberate team-chat refusal's server code
		// alongside numeric exit 5. Keep this bounded to the two named codes;
		// unrelated refusal envelopes retain their established shape.
		if code == exitcode.Conflict {
			for _, serverCode := range []string{"TEAM_CHAT_DUPLICATE", "TEAM_CHAT_POST_RATE_LIMITED"} {
				if api.HasErrorCode(err, serverCode) {
					failure["serverCode"] = serverCode
					break
				}
			}
		}
		// #1327: the chat-search refusals span exits 2 and 5, so they are keyed
		// on the code itself rather than the exit.
		for _, serverCode := range []string{"TEAM_CHAT_SEARCH_QUERY_LENGTH", "TEAM_CHAT_SEARCH_CURSOR_INVALID", "TEAM_CHAT_SEARCH_ENCRYPTED"} {
			if api.HasErrorCode(err, serverCode) {
				failure["serverCode"] = serverCode
				break
			}
		}
		var coded interface{ ServerCode() string }
		if errors.As(err, &coded) {
			failure["serverCode"] = coded.ServerCode()
		}
		details := api.CommentRecoveryDetails(err)
		if details == nil {
			details = api.WorkerReportingDetails(err)
		}
		if cloneDetails := api.CloneAgentDetails(err); cloneDetails != nil {
			details = cloneDetails
		}
		if details != nil {
			failure["extensions"] = details
		}
		envelope := map[string]any{"error": failure}
		switch {
		case f.JSON && !f.IOStreams.Wrote():
			// If STDOUT ITSELF is broken — a full filesystem behind a
			// redirect, a closed pipe, a failing writer from an embedded
			// caller — the envelope has nowhere to land, and discarding that
			// second error would make the failure completely silent: a bare
			// exit code and not one word on either stream (PR #585 review,
			// @codex P2). Before this change the envelope went to stderr and
			// would have survived, so the fallback is what stops a routing
			// improvement from becoming a regression at the one moment the
			// caller most needs to be told something.
			if werr := output.WriteJSON(f.IOStreams.Out, envelope); werr != nil {
				_ = output.WriteJSON(f.IOStreams.ErrOut, envelope)
			}
		case f.JSON:
			// Payload already in flight; keep stdout a single valid document.
			_ = output.WriteJSON(f.IOStreams.ErrOut, envelope)
		default:
			message := humanErrorText(err) + api.CloneAgentDetailText(err)
			if reporting := api.WorkerReportingDetails(err); reporting != nil {
				message += fmt.Sprintf(" (server code: %v)", reporting["code"])
			}
			for _, key := range []string{"currentRevision", "threadId", "class"} {
				if value, ok := details[key]; ok {
					message += fmt.Sprintf(" (%s: %v)", key, value)
				}
			}
			fmt.Fprintf(f.IOStreams.ErrOut, "hadron: %s\n", message)
			// cli#1264: a clone refusal's blockers, one per line, after the answer.
			for _, line := range api.CloneAgentBlockerLines(err) {
				fmt.Fprintf(f.IOStreams.ErrOut, "  %s\n", line)
			}
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
