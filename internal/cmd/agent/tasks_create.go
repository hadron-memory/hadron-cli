package agent

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// roleTaskTemplateRef is the node that holds the role-task body template. `--template` reads it with an
// ordinary node read (hadron-server#4864 dropped its server-side template read), the way `spec new` scaffolds
// from a spec node.
const roleTaskTemplateRef = "hrn:node:hadronmemory.com:agent-team-system:tasks:add-role-task"

// roleTaskEdgeDTO is the tasks-index edge the server wrote with the node.
type roleTaskEdgeDTO struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	TargetLoc string `json:"targetLoc"`
	TargetURN string `json:"targetUrn"`
}

// roleTaskCreatedDTO is `agent tasks create --json`: the created node's loc and URN and its edge(s).
type roleTaskCreatedDTO struct {
	ID    string            `json:"id"`
	Loc   string            `json:"loc"`
	URN   string            `json:"urn"`
	Name  string            `json:"name"`
	Edges []roleTaskEdgeDTO `json:"edges"`
}

func newCmdAgentTasksCreate(f *cmdutil.Factory) *cobra.Command {
	var name, abstract, bodyFile, trigger, exportSkill, skillDescription string
	var template bool
	cmd := &cobra.Command{
		Use:   "create <agentRef> <slug> --name <name> --abstract <text> --body-file <path> --trigger <label>",
		Short: "Create a role task in the Agent's system memory, indexed, in one call",
		Long: `Create a role task for an Agent in ONE server call: the runnable task node, at
tasks:<slug> in the Agent's own system memory, and its edge to the Agent's tasks
index, written together, so a task never exists unindexed. The server owns every
rule (hadron-server's task door): it refuses an existing slug, an Agent with no
tasks index, an index outside the Agent's system memory, a malformed slug and a
caller below the writer level. The CLI has no content rule or lint of its own: it
only asks for the inputs the call needs (and the two skill flags together); each
refusal is printed with the server's message and its usual exit code.

<agentRef> is the Agent (an id or hrn:agent:... URN), not its memory. <slug> is
short kebab-case starting with a verb, e.g. restart-the-poller. --name is the
task's name, --abstract says in a sentence what it does, --body-file is the task
body (see --template), and --trigger is the label of the index edge: the trigger
only, in the role's own words and about ten words or fewer (the task's name says
what it does).

Write a task after doing the work once, never speculatively (a task written
before anyone did the work describes what someone imagined), and a rule only one
team follows belongs in that team's process, not a shared role memory.

--template prints the body template (the fenced block of the add-role-task node,
read as an ordinary node) and creates nothing, so it takes no agent, slug or
other flag, --json included (given any, it refuses with exit 2 before reading
anything; only the global --app and --server apply); start the body file from it.

By default NO skill export is written. --export-skill <name> with
--skill-description <text> (both together) asks the server for an enabled
properties.exports.claudeSkill block; the server validates the declaration and
refuses the whole call if its lint finds an error. Any advice the server attaches
to its answer is printed to stderr and never blocks the create.

Prints the created node's loc and URN and the index edge; --json gives them as
{id, loc, urn, name, edges:[{id, label, targetLoc, targetUrn}]}. The server writes
the edge with the node, so an answer that lists none gets a note on stderr (the
create still succeeds; --json stdout is unchanged). An answer with no node at all
exits 1 with the outcome unknown: the task may have been recorded, so check
` + "`agent tasks list`" + ` before retrying. Editing an
existing task is not done here (node update --content-file keeps it runnable).`,
		Example: `  hadron agent tasks create --template
  hadron agent tasks create hrn:agent:acme.com:support-bot restart-the-poller --name "Restart the poller" --abstract "Bring the poller back when it stalls." --body-file body.md --trigger "when the poller stalls"`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			// --template prints the template and creates nothing, so every other flag is an
			// instruction it would silently drop: refuse them by name before any request (cli#3038).
			// The list is DERIVED from this command's own flags, so a flag added later is refused too;
			// --json (persistent, but it asks for a shape the template text does not have) is refused
			// explicitly; --app and --server stay: the template read honours them.
			if template {
				if len(args) > 0 {
					return exitcode.Newf(exitcode.Usage, "--template prints the template and takes no agent or slug")
				}
				var given []string
				// VisitAll + Changed: the derived flag set has no record of what was parsed, the flags do.
				cmd.LocalNonPersistentFlags().VisitAll(func(fl *pflag.Flag) {
					if fl.Changed && fl.Name != "template" {
						given = append(given, "--"+fl.Name)
					}
				})
				if cmd.Flags().Changed("json") {
					given = append(given, "--json")
				}
				if len(given) > 0 {
					return exitcode.Newf(exitcode.Usage, "--template prints the template and creates nothing, so it takes no other flag (got %s); drop them, or drop --template to create the task (--app and --server still apply)", strings.Join(given, ", "))
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if template {
				resp, err := gen.GetNode(ctx, client, roleTaskTemplateRef)
				if err != nil {
					return api.MapError(err)
				}
				if resp == nil || resp.Node == nil || resp.Node.Content == nil {
					return exitcode.Newf(exitcode.Error, "the template node %s has no readable content", roleTaskTemplateRef)
				}
				block, ok := roleTaskTemplateBlock(*resp.Node.Content)
				if !ok {
					return exitcode.Newf(exitcode.Error, "the template node %s has no fenced markdown block under \"## Template\"", roleTaskTemplateRef)
				}
				_, err = io.WriteString(f.IOStreams.Out, block)
				return err
			}
			if len(args) != 2 {
				return exitcode.Newf(exitcode.Usage, "name the Agent and the task's slug: create <agentRef> <slug> --name ... (or --template)")
			}
			ref, slug := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
			if ref == "" || slug == "" {
				return exitcode.Newf(exitcode.Usage, "the agent reference and the slug are required")
			}
			for _, need := range []struct{ flag, val string }{{"--name", name}, {"--abstract", abstract}, {"--body-file", bodyFile}, {"--trigger", trigger}} {
				if strings.TrimSpace(need.val) == "" {
					return exitcode.Newf(exitcode.Usage, "%s is required", need.flag)
				}
			}
			if (exportSkill == "") != (skillDescription == "") {
				return exitcode.Newf(exitcode.Usage, "--export-skill and --skill-description go together (a skill name and its description)")
			}
			body, err := os.ReadFile(bodyFile)
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "reading --body-file: %v", err)
			}
			// The one read: the Agent's own system memory, which the new node must be created in.
			agentResp, err := gen.GetAgent(ctx, client, ref)
			if err != nil {
				return api.MapError(err)
			}
			if agentResp == nil || agentResp.Agent == nil {
				return exitcode.Newf(exitcode.NotFound, "agent %s was not found", ref)
			}
			content := string(body)
			nodeType, runnable, abs := "task", true, abstract
			input := &gen.CreateNodeInput{
				Loc:        "tasks:" + slug,
				Name:       name,
				Abstract:   &abs,
				Content:    &content,
				NodeType:   &nodeType,
				IsRunnable: &runnable,
			}
			if sm := agentResp.Agent.SystemMemoryId; sm != nil {
				input.MemoryRef = *sm // omitted when the Agent has none: the server refuses, the CLI does not pre-check
			}
			var skill *gen.TaskSkillExportInput
			if exportSkill != "" {
				skill = &gen.TaskSkillExportInput{Name: exportSkill, Description: skillDescription}
			}
			resp, err := gen.CreateRoleTask(ctx, client, input, &gen.TaskIndexInput{Agent: ref, Trigger: trigger}, skill)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.CreateTaskNode == nil {
				// The lost-answer wording team.nullMutationResult uses (this package cannot import it): exit 1, the
				// outcome unknown, and the command that shows it before any retry. Exit 1 is that helper's class;
				// `agent clone` answers its own null differently (exit 7), so this is not "every write command".
				return exitcode.Newf(exitcode.Error, "the server answered createTaskNode with no node and no error, so the outcome is unknown: the task may have been recorded; run `hadron agent tasks list %s` before retrying", ref)
			}
			n := resp.CreateTaskNode
			dto := roleTaskCreatedDTO{ID: n.Id, Loc: n.Loc, URN: n.Urn, Name: n.Name, Edges: []roleTaskEdgeDTO{}}
			for _, e := range n.OutgoingEdges {
				row := roleTaskEdgeDTO{ID: e.Id}
				if e.Name != nil {
					row.Label = *e.Name
				}
				if e.Target != nil {
					row.TargetLoc, row.TargetURN = e.Target.Loc, e.Target.Urn
				}
				dto.Edges = append(dto.Edges, row)
			}
			// The server writes the index edge WITH the node, so an answer listing none is surprising: say so on
			// stderr and still succeed. No CLI-side check or retry, and --json stdout is unchanged (cli#3038).
			if len(dto.Edges) == 0 {
				fmt.Fprintf(f.IOStreams.ErrOut, "note: the server's answer lists no index edge, though it writes the edge with the task; check that the task is indexed with `hadron agent tasks list %s`\n", ref)
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ created role task %s\n  urn: %s\n", output.SafeCell(dto.Loc), output.SafeCell(dto.URN)); err != nil {
					return err
				}
				for _, e := range dto.Edges {
					if _, err := fmt.Fprintf(w, "  indexed: %s -> %s (%s)\n", output.SafeCell(e.Label), output.SafeCell(e.TargetLoc), output.SafeCell(e.TargetURN)); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "the task's name (required)")
	cmd.Flags().StringVar(&abstract, "abstract", "", "one sentence saying what the task does (required)")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "file holding the task body (required; start from --template)")
	cmd.Flags().StringVar(&trigger, "trigger", "", "the index edge's label: the trigger only, about ten words or fewer (required)")
	cmd.Flags().StringVar(&exportSkill, "export-skill", "", "opt in to a skill export: the skill's name (needs --skill-description; none is written by default)")
	cmd.Flags().StringVar(&skillDescription, "skill-description", "", "the exported skill's description (with --export-skill)")
	cmd.Flags().BoolVar(&template, "template", false, "print the body template and create nothing")
	return cmd
}

// roleTaskTemplateBlock returns the first fenced block under a "## Template" heading, without its fence lines.
func roleTaskTemplateBlock(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	in, open := false, false
	var out []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		switch {
		case !in && strings.EqualFold(trimmed, "## Template"):
			in = true
		case in && !open && strings.HasPrefix(trimmed, "```"):
			open = true
		case in && open && strings.HasPrefix(trimmed, "```"):
			return strings.Join(out, "\n") + "\n", true
		case in && open:
			out = append(out, l)
		}
	}
	return "", false
}
