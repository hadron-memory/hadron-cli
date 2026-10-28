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

// NewAgent compiles the Agent's source into its system memory.
func NewAgent(f *cmdutil.Factory) *cobra.Command { return newCommand(f, "agent", nil) }

// NewAppAgent compiles an installed Agent's source for an App.
func NewAppAgent(f *cmdutil.Factory) *cobra.Command { return newCommand(f, "app", nil) }

// NewWorker resolves a worker name in the selected App before compiling it.
func NewWorker(f *cmdutil.Factory, resolve func(*cobra.Command, string) (string, error)) *cobra.Command {
	return newCommand(f, "worker", resolve)
}

func newCommand(f *cmdutil.Factory, kind string, resolve func(*cobra.Command, string) (string, error)) *cobra.Command {
	var noSetPointer bool
	use, example := "compile <agent>", "  hadron agent compile hrn:agent:acme.com:iris"
	target := "Agent's system memory"
	if kind == "app" {
		use = "compile <app> <agent>"
		example = "  hadron app agent compile hrn:app:acme.com:eng-team hrn:agent:acme.com:iris"
		target = "App's install memory for this Agent"
	}
	if kind == "worker" {
		use = "compile <worker>"
		example = "  hadron team worker compile Iris --no-set-pointer"
		target = "worker's App install memory"
	}
	pointerHelp := "The pointer is set by default; --no-set-pointer leaves it unchanged."
	if kind == "app" {
		pointerHelp = "An App-Agent compile always sets the pointer; there is no --no-set-pointer flag."
	}
	cmd := &cobra.Command{
		Use: use, Short: "Compile a boot prompt without approving or serving it",
		Long: fmt.Sprintf(`Compile the server-selected source boot prompt into the %s.
Each successful compile writes a revision, even if the text is unchanged.
%s Sources and output addresses are chosen by the server, not by flags.

Sign in as a manager of the target Agent or App. This command acts as you and
never sends this worktree's worker-session header. Agent and App references
are ids or fully-qualified URNs. A worker may also be a name in the selected
App (--app, the configured App context or the worktree binding).

Compile does not approve or serve a prompt. The result reports the compiled
node, revision, content hash, whether the pointer was set and approvalIssued
(false). It does not report the revision's approval status: use node get.

Exit codes: 2 for malformed or disallowed include/input; 4 for an unavailable include
(missing and unreadable have one answer); 5 for a retired target, missing or
invalid source, unavailable include revision or conflict; 8 for permission
or grant refusals. A lost response is exit 7: check the compiled node with
node get before compiling again. The command sends the mutation once and
never retries it.`, target, pointerHelp),
		Example: example, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
