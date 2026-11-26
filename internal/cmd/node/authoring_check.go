package node

import (
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/api/gqltypes"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#3089 — the CLI reach for Query.nodeAuthoringCheck (cor:dmo:060:17): the
// verdict a proposed write's authoring attestation check would give, without
// writing. The state is an answer, never an error: UNAVAILABLE (the caller reads
// the memory but does not write it) is its own state, not a pass and not
// not-found. Only a caller who cannot READ the node or memory gets the mapped
// not-found exit.

type authoringCheckLoadDTO struct {
	CLI     *string `json:"cli"`
	GraphQL string  `json:"graphql"`
	MCP     *string `json:"mcp"`
}

// authoringCheckRecoveryDTO is the refusal's recovery exactly as the server
// gave it. It is never filled in here: for a task the caller cannot read the
// server sends nulls, and so does this.
type authoringCheckRecoveryDTO struct {
	TaskAccess   string                 `json:"taskAccess"`
	Task         *string                `json:"task"`
	TaskID       *string                `json:"taskId"`
	TaskRevision *int                   `json:"taskRevision"`
	Load         *authoringCheckLoadDTO `json:"load"`
}

type authoringCheckDTO struct {
	State    string                     `json:"state"`
	Code     *string                    `json:"code"`
	Message  *string                    `json:"message"`
	Role     *string                    `json:"role"`
	Recovery *authoringCheckRecoveryDTO `json:"recovery"`
}

func newCmdAuthoringCheck(f *cmdutil.Factory) *cobra.Command {
	var memory, role, authoredWith string
	var authoredRevision int
	var runnable, noContentChange bool
	cmd := &cobra.Command{
		Use:   "authoring-check <node> [-m <memory>] [--role <role>] [--runnable[=false]] [--no-content-change] [--authored-with <task> --authored-revision <n>]",
		Short: "Preview whether a governed write would pass the authoring check",
		Long: `Ask the server what the authoring attestation check would answer for a write
you are about to make, without making it. Use it before writing a node whose
role is governed, to learn which task the write must be authored from.

Name the node as an id or node URN, or as a bare loc with -m/--memory. A loc
that does not exist yet is checked as a new node. Describe the write with the
flags you would give the real command: --role (the role the node would be left
with; "" clears it; omit to keep the node's own), --runnable / --runnable=false,
--no-content-change (a write that changes no authored content needs no claim),
and the claim itself, --authored-with <task> with --authored-revision <n>
(pass both or neither). Nothing is written; one request is made.

The answer is one of three states:
  PASS         the write would pass the check
  REFUSED      the write would be refused: the code, the governed role, the
               message and the recovery (the task, its revision and how to
               load it) are shown exactly as the real write returns them
  UNAVAILABLE  you can read the memory but do not write it. This is the same
               answer for every node and role, so it shows nothing about the
               memory's rules. It is not a pass.

A PASS is a preview, never a reservation: it promises nothing about a later
write, and it is not proof that the task was read or followed. A caller who
cannot read the node or memory gets the not-found error, the same as for a
missing node.`,
		Example: "  hadron node authoring-check specs:x -m hrn:mem:acme.com:kb --role spec\n  hadron node authoring-check hrn:node:acme.com:kb:specs:x --authored-with hrn:node:acme.com:kb:tasks:author-spec --authored-revision 7 --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in, err := authoringCheckInput(cmd, args[0], memory, role, authoredWith, authoredRevision, runnable, noContentChange)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.NodeAuthoringCheck(cmd.Context(), client, in)
			if err != nil {
				return api.MapError(err)
			}
			dto := authoringCheckToDTO(resp.NodeAuthoringCheck)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return renderAuthoringCheck(w, dto) })
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (hrn:mem:<root>:<slug>) to resolve a bare <loc> against")
	cmd.Flags().StringVar(&role, "role", "", `the role the write would leave the node with ("" clears it; omit to keep the node's own)`)
	cmd.Flags().BoolVar(&runnable, "runnable", false, "the write would leave the node runnable (--runnable=false: not runnable; omit to keep the node's own)")
	cmd.Flags().BoolVar(&noContentChange, "no-content-change", false, "the write changes no authored content, so it needs no claim (default: it does)")
	cmd.Flags().StringVar(&authoredWith, "authored-with", "", "task URN or id the write would claim to be authored from (pair with --authored-revision)")
	cmd.Flags().IntVar(&authoredRevision, "authored-revision", 0, "task revision the write would claim (pair with --authored-with)")
	return cmd
}

// authoringCheckInput validates the flags and builds the request. Every refusal
// here happens before any request.
func authoringCheckInput(cmd *cobra.Command, nodeRef, memory, role, task string, rev int, runnable, noContentChange bool) (*gqltypes.NodeAuthoringCheckInput, error) {
	flags := cmd.Flags()
	nodeRef = strings.TrimSpace(nodeRef)
	if nodeRef == "" {
		return nil, exitcode.Newf(exitcode.Usage, "a node (id, node URN, or a bare loc with -m/--memory) is required")
	}
	if flags.Changed("memory") && strings.TrimSpace(memory) == "" {
		return nil, exitcode.Newf(exitcode.Usage, "-m/--memory must not be empty")
	}
	in := &gqltypes.NodeAuthoringCheckInput{}
	switch {
	case cmdutil.IsNodeID(nodeRef) || cmdutil.IsActionRef(nodeRef) || cmdutil.IsQualifiedNodeRef(nodeRef):
		// Names its own memory: -m has nothing to compose onto it.
		in.Ref = &nodeRef
	case strings.TrimSpace(memory) != "":
		mem := cmdutil.CanonicalMemoryRef(memory)
		in.MemoryRef, in.Loc = &mem, &nodeRef
	default:
		return nil, exitcode.Newf(exitcode.Usage, "a bare loc needs -m/--memory <memory>; otherwise pass a node id or node URN")
	}
	if flags.Changed("role") {
		// Blank clears, exactly as node update's clearRole decides
		// (TrimSpace == ""), so the preview agrees with the write; a role with
		// surrounding spaces is sent as typed, never trimmed into a different one.
		if strings.TrimSpace(role) == "" {
			in.ClearRole = true
		} else {
			in.Role = &role
		}
	}
	if flags.Changed("runnable") {
		in.IsRunnable = &runnable
	}
	if noContentChange {
		no := false
		in.ChangesAuthoredContent = &no
	}
	taskSet, revSet := flags.Changed("authored-with"), flags.Changed("authored-revision")
	if taskSet != revSet {
		return nil, exitcode.Newf(exitcode.Usage, "pass both --authored-with and --authored-revision, or neither; nothing was requested")
	}
	if taskSet {
		task = strings.TrimSpace(task)
		if task == "" || int64(rev) < math.MinInt32 || int64(rev) > math.MaxInt32 {
			return nil, exitcode.Newf(exitcode.Usage, "--authored-with must name a task and --authored-revision must be a GraphQL integer")
		}
		in.AuthoredWith = &gqltypes.AuthoredWith{Task: task, Revision: rev}
	}
	return in, nil
}

func authoringCheckToDTO(c *gen.NodeAuthoringCheckNodeAuthoringCheck) authoringCheckDTO {
	dto := authoringCheckDTO{State: string(c.State), Message: c.Message, Role: c.Role}
	if c.Code != nil {
		code := string(*c.Code)
		dto.Code = &code
	}
	if r := c.Recovery; r != nil {
		rec := &authoringCheckRecoveryDTO{TaskAccess: string(r.TaskAccess), Task: r.Task, TaskID: r.TaskId, TaskRevision: r.TaskRevision}
		if r.Load != nil {
			rec.Load = &authoringCheckLoadDTO{CLI: r.Load.Cli, GraphQL: r.Load.Graphql, MCP: r.Load.Mcp}
		}
		dto.Recovery = rec
	}
	return dto
}

// renderAuthoringCheck prints the verdict. Server text is neutralised: it is
// the server's prose and a task ref, shown to a terminal. The wording never
// says a write will succeed: a PASS is a preview, not a reservation.
func renderAuthoringCheck(w io.Writer, d authoringCheckDTO) error {
	switch gen.NodeAuthoringCheckState(d.State) {
	case gen.NodeAuthoringCheckStatePass:
		fmt.Fprintln(w, "PASS: the write would pass the authoring check now. This is a preview, not a reservation, and not proof the task was read or followed.")
	case gen.NodeAuthoringCheckStateUnavailable:
		fmt.Fprintln(w, "UNAVAILABLE: you can read this memory but do not write it, so the check shows nothing about its rules. This is not a pass.")
	case gen.NodeAuthoringCheckStateRefused:
		fmt.Fprintln(w, "REFUSED: the write would be refused.")
		if d.Code != nil {
			fmt.Fprintf(w, "  code:    %s\n", output.SafeLine(*d.Code, 128))
		}
		if d.Role != nil {
			fmt.Fprintf(w, "  role:    %s\n", output.SafeLine(*d.Role, 256))
		}
		if d.Message != nil {
			fmt.Fprintf(w, "  message: %s\n", output.SafeLine(*d.Message, 2048))
		}
		if r := d.Recovery; r != nil {
			if gen.AuthoringCheckTaskAccess(r.TaskAccess) == gen.AuthoringCheckTaskAccessReadable && r.Task != nil {
				fmt.Fprintf(w, "  task:    %s", output.SanitizeLine(*r.Task))
				if r.TaskRevision != nil {
					fmt.Fprintf(w, ", revision %d", *r.TaskRevision)
				}
				fmt.Fprintln(w)
				if r.Load != nil && r.Load.CLI != nil {
					fmt.Fprintf(w, "  load:    %s\n", output.SanitizeLine(*r.Load.CLI))
				}
			} else {
				fmt.Fprintln(w, "  task:    unavailable to you (unreadable or unusable); an owner must fix access or the rule")
			}
		}
	default:
		fmt.Fprintf(w, "%s\n", output.SafeLine(d.State, 64))
	}
	return nil
}
