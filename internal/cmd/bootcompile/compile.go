// Package bootcompile implements the three manager-only boot-prompt compile
// commands. Authority, source selection and output addresses belong to the server.
package bootcompile

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type compileDTO struct {
	CompiledNodeRef string `json:"compiledNodeRef"`
	Revision        int    `json:"revision"`
	ContentHash     string `json:"contentHash"`
	PointerSet      bool   `json:"pointerSet"`
	ApprovalIssued  bool   `json:"approvalIssued"`
}

// BatchOptions are the flags of `team worker compile` with no worker
// argument (cli#1463): compile the App's workers in one run.
type BatchOptions struct {
	All   bool     // compile every live worker, not only changed ones
	Only  []string // restrict to these workers (names, ids or URNs)
	Apply bool     // write; without it the run is a dry run
	// SetPointer is nil for the server default (set) and false for
	// --no-set-pointer, exactly as the single-worker compile sends it.
	SetPointer *bool
}

// Batch runs the batch compile.
type Batch func(*cobra.Command, BatchOptions) error

// NewAgent compiles the Agent's source into its system memory.
func NewAgent(f *cmdutil.Factory) *cobra.Command { return newCommand(f, "agent", nil, nil) }

// NewAppAgent compiles an installed Agent's source for an App.
func NewAppAgent(f *cmdutil.Factory) *cobra.Command { return newCommand(f, "app", nil, nil) }

// NewWorker resolves a worker name in the selected App before compiling it.
// With no worker argument it runs batch over the App's workers.
func NewWorker(f *cmdutil.Factory, resolve func(*cobra.Command, string) (string, error), batch Batch) *cobra.Command {
	return newCommand(f, "worker", resolve, batch)
}

func newCommand(f *cmdutil.Factory, kind string, resolve func(*cobra.Command, string) (string, error), batch Batch) *cobra.Command {
	var noSetPointer, all, apply bool
	var only []string
	use, example := "compile <agent>", "  hadron agent compile hrn:agent:acme.com:iris"
	target := "Agent's system memory"
	if kind == "app" {
		use = "compile <app> <agent>"
		example = "  hadron app agent compile hrn:app:acme.com:eng-team hrn:agent:acme.com:iris"
		target = "App's install memory for this Agent"
	}
	recipe := ""
	if kind == "worker" {
		use = "compile [<worker>]"
		example = `  # Every worker whose sources changed since its last compile: a dry run,
  # then compile, then approve each printed revision after reading it
  hadron team worker compile --app acme.com:eng-team
  hadron team worker compile --app acme.com:eng-team --apply
  hadron team worker compile --app acme.com:eng-team --only Iris,Theo --apply

  # 1. write the worker's source (needs its id: team worker get Iris --json)
  hadron node add --install hrn:app:acme.com:eng-team/hrn:agent:acme.com:backend-engineer \
      --loc boot:workers:<worker-id>:source --role boot-prompt --name "Iris boot" \
      --content "You are {{worker.name}}, the {{worker.role}}. ..."
  # 2. compile it (sets the worker's pointer), then 3. approve what it printed
  hadron team worker compile Iris --app acme.com:eng-team
  hadron node approve hrn:node:acme.com:eng-team-install:boot:workers:<worker-id>:compiled`
		target = "worker's App install memory"
		recipe = `

A WORKER HAS NO SOURCE UNTIL YOU WRITE ONE (exit 5, BOOT_PROMPT_NO_SOURCE).
The server reads it from boot:workers:<worker-id>:source, role boot-prompt, in
the App's install memory for the worker's Agent, and writes the result to
boot:workers:<worker-id>:compiled there. Three separate steps, three effects:
  1. node add --install <app>/<agent> ...   write the source (example below)
  2. team worker compile <worker>           compile; sets the worker's pointer
  3. node approve <compiled node>           approve; only now is it served
A served worker prompt replaces the App's and the Agent's (worker pointer,
else the App install's, else the Agent's): the newest APPROVED revision is
served, so a compile alone changes nothing a worker sees.

WITHOUT A WORKER ARGUMENT the command compiles the App's workers in one run
(cli#1463). By default it picks only the workers whose sources changed since
their last compile: the server marks such a compiled prompt RECOMPILE_AVAILABLE
(cor:agt:040:13), and a worker with no compiled prompt yet gets its first
compile. Retired workers are never included. Nothing is written without
--apply; the dry run lists what each worker would get:
  toCompile  a source changed, or no compiled prompt yet
  unchanged  no newer source revision: not compiled (no needless revision)
  unknown    the server could not answer for this worker: not compiled
After --apply each selected worker is compiled once, by id, and lands in
  compiled   with the revision and the node approve line to run after review
  skipped    BOOT_PROMPT_NO_SOURCE or BOOT_PROMPT_TARGET_RETIRED
  failed     any other refusal, with its server code
  unknown    the response was lost (exit 7): one read checks whether a new
             revision landed (outcome landed, not-landed or unknown); the
             compile is never sent twice
--all compiles every live worker, changed or not. --only a,b limits the run to
those workers; a name that is not a live worker in the App is refused before
anything is written. The batch never approves: it prints the approve lines.

Blind spot: a NEW install-memory override at an address the last compile read
from the Agent is not recorded there, so it is not flagged. Use --all after
adding one.

Exit code: 0 when every worker is compiled, unchanged or skipped; otherwise the
highest of 7 for a lost response, 1 for an undecided worker and each failure's
own code.`
	}
	short := "Compile a boot prompt without approving or serving it"
	if kind == "worker" && batch != nil {
		short = "Compile a worker's boot prompt, or every changed one in the App, without approving it"
	}
	pointerHelp := "The pointer is set by default; --no-set-pointer leaves it unchanged."
	if kind == "app" {
		pointerHelp = "An App-Agent compile always sets the pointer; there is no --no-set-pointer flag."
	}
	cmd := &cobra.Command{
		Use: use, Short: short,
		Long: fmt.Sprintf(`Compile the server-selected source boot prompt into the %s.
Each successful compile writes a revision, even if the text is unchanged.
%s Sources and output addresses are chosen by the server, not by flags.

Sign in as a manager of the target Agent or App. This command acts as you and
never sends this worktree's worker-session header. Agent and App references
are ids or fully-qualified URNs. A worker may also be a name in the selected
App (--app, the configured App context or a binding for this server).
A worker id or fully-qualified worker URN ignores the ambient App and binding.
A missing, corrupt or unknown/different-server binding cannot select an App
for a name; pass --app explicitly or use a fully-qualified worker URN.

Compile does not approve or serve a prompt. The result reports the compiled
node, revision, content hash, whether the pointer was set and approvalIssued
(false). It does not report the revision's approval status: use node get.

Exit codes: 2 for malformed or disallowed include/input; 4 for an unavailable include
(missing and unreadable have one answer); 5 for a retired target, missing or
invalid source, unavailable include revision or conflict; 8 for permission
or grant refusals. A lost response is exit 7: check the compiled node with
node get before compiling again. The command sends the mutation once and
never retries it.%s`, target, pointerHelp, recipe),
		Example: example, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if kind == "worker" && batch != nil {
				batchFlags := all || apply || len(only) > 0
				if len(args) == 1 && batchFlags {
					return exitcode.Newf(exitcode.Usage, "--all, --only and --apply compile the App's workers in one run; pass no worker argument with them")
				}
				if len(args) == 0 {
					opts := BatchOptions{All: all, Apply: apply}
					for _, name := range only {
						if name = strings.TrimSpace(name); name != "" {
							opts.Only = append(opts.Only, name)
						}
					}
					if len(only) > 0 && len(opts.Only) == 0 {
						return exitcode.Newf(exitcode.Usage, "--only needs at least one worker")
					}
					if noSetPointer {
						v := false
						opts.SetPointer = &v
					}
					return batch(cmd, opts)
				}
			}
			for _, arg := range args {
				if strings.TrimSpace(arg) == "" {
					return exitcode.Newf(exitcode.Usage, "the target reference is empty")
				}
			}
			var setPointer *bool
			if noSetPointer {
				v := false
				setPointer = &v
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var result *gen.BootCompileResult
			switch kind {
			case "agent":
				r, e := gen.CompileAgentBootPrompt(cmd.Context(), client, args[0], setPointer)
				err = e
				if e == nil && r.CompileAgentBootPrompt != nil {
					result = &r.CompileAgentBootPrompt.BootCompileResult
				}
			case "app":
				appRef, e := cmdutil.CanonicalAppRef("<app>", args[0])
				if e != nil {
					return e
				}
				r, e := gen.CompileAppAgentBootPrompt(cmd.Context(), client, appRef, args[1])
				err = e
				if e == nil && r.CompileAppAgentBootPrompt != nil {
					result = &r.CompileAppAgentBootPrompt.BootCompileResult
				}
			case "worker":
				workerRef, e := resolve(cmd, args[0])
				if e != nil {
					return e
				}
				r, e := gen.CompileWorkerBootPrompt(cmd.Context(), client, workerRef, setPointer)
				err = e
				if e == nil && r.CompileWorkerBootPrompt != nil {
					result = &r.CompileWorkerBootPrompt.BootCompileResult
				}
			}
			if err != nil {
				return compileError(err)
			}
			if result == nil || result.Node == nil || result.Node.Urn == "" {
				return exitcode.Newf(exitcode.Error, "server returned no compiled boot prompt; check with node get before compiling again")
			}
			dto := compileDTO{result.Node.Urn, result.RevNo, result.ContentHash, result.PointerSet, result.ApprovalIssued}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				pointer := "unchanged"
				if dto.PointerSet {
					pointer = "set"
				}
				_, err := fmt.Fprintf(w, "compiled %s revision %d; pointer %s.\nA compile issues no approval: read the revision's approval status with node get.\n", dto.CompiledNodeRef, dto.Revision, pointer)
				return err
			})
		},
	}
	if kind == "worker" && batch != nil {
		cmd.Args = cobra.MaximumNArgs(1)
		cmd.Flags().BoolVar(&all, "all", false, "with no worker: compile every live worker, not only those whose sources changed")
		cmd.Flags().StringSliceVar(&only, "only", nil, "with no worker: compile only these workers (comma-separated names, ids or URNs)")
		cmd.Flags().BoolVar(&apply, "apply", false, "with no worker: compile (without it the batch is a dry run)")
	}
	if kind == "app" {
		cmd.Args = cobra.ExactArgs(2)
	} else {
		cmd.Flags().BoolVar(&noSetPointer, "no-set-pointer", false, "compile without changing the boot-prompt pointer")
	}
	return cmd
}

func compileError(err error) error {
	mapped := api.MapError(err)
	if api.HasErrorCode(err, "CONFLICT") {
		return exitcode.Newf(exitcode.Conflict, "the compile conflicted with another change; check the compiled node with node get before compiling again")
	}
	if exitcode.FromError(mapped) == exitcode.Unavailable {
		return exitcode.Newf(exitcode.Unavailable, "the outcome is unknown; check the compiled node with node get before compiling again")
	}
	return mapped
}
