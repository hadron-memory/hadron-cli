package team

// `hadron team task clone` (hadron-cli#1565, server#2646 cloneAgentTask,
// cor:agt:050:02 "What a clone does"): make a NEW task from an existing one, in
// its App, linked to it. The CLI composes nothing: it sends only the overrides
// a flag names and prints the task the server returns.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdTeamTaskClone(f *cmdutil.Factory) *cobra.Command {
	var title, description, parent, source, status, assignee, startBy, finishBy string
	var owner, properties, requireRole, familyIn, familyNotIn, modelGroupIn, modelGroupNotIn, minTier string
	var tags, refs, excludeWorkers []string
	var priority int
	var noParent, closeSession, noCloseSession, project, clearTags, clearRefs, clearProperties, clearRequirements bool
	cmd := &cobra.Command{
		Use:   "clone <task>",
		Short: "Make a new task from an existing one",
		Long: `Make a NEW task from an existing one, in its App and linked to it
(cor:agt:050:02). A clone is a create: whoever may read the source and create a
task there may clone it, in any status. The source is never changed and an ended
task is never reopened.

The server copies the title, description, source, tags, requirements,
references, close-session choice, priority, properties and parent. It does not
copy the assignee, status, result, handle, blockers, start-by, finish-by or
external id. The clone is PENDING (or BACKLOG with --status backlog) and a
queued one is matched like any other. Priority and properties are copied only
for a caller who may set them, and are otherwise left out without a refusal.
A --properties value from a caller who may not set properties is not a refusal
either: the clone is made without properties and the value is not judged
(cor:agt:050:28). A properties refusal (PROPERTIES_*) is only for a caller who
may set them and names an invalid value.
` + "`task get`" + ` on either task shows the link ("cloned from" / "replaces" and the
clones).

Only the overrides you name are sent; an omitted flag keeps the source's value
(or leaves the field unset). A flag that is given sends its value, an empty one
included, and the server judges it.
  --title, --description, --source, --priority   replace the copied value
  --assignee worker:<name> | user:<handle>        assign the clone (not copied)
  --start-by, --finish-by <RFC 3339>              set a deadline (not copied)
  --close-session | --no-close-session            replace the copied choice
  --status pending|backlog                        the clone's create-time status
  --tag <t> (repeatable) | --clear-tags           replace the copied tags / empty them
  --ref <kind>=<target>[|<label>] (repeatable)    replace the copied references
  --clear-refs                                    empty them
  --properties <json|@file> | --clear-properties  replace the copied properties / remove them
  --require-role, --family-in, --family-not-in, --exclude-worker, --min-tier,
  --model-group, --model-group-not                replace the WHOLE requirements
                                                  (name every one to keep; not a merge)
  --clear-requirements                            remove every requirement
  --owner worker:<name> | user:<handle>           name the clone's owner (default:
                                                  you, as on create; never cleared)
  --project                                       make the clone a top-level project
                                                  (with --parent: exit 2; --project
                                                  --no-parent is allowed)
  --parent <task>                                 put the clone under another
                                                  parent in the same App
  --no-parent                                     put it under no parent: a root
                                                  under AT0 (--parent and
                                                  --no-parent together are exit 2)
Without either, the parent is kept for someone who may create a child of it
(otherwise TASK_NOT_ASSIGNEE, exit 8, and nothing is created); a root is cloned
as a root, and a clone under an ended parent goes under AT0. A value flag and its
--clear-… twin together are exit 2, as in task update.

Refusals: an unknown, unreadable or other-App source or parent is exit 4
(TASK_NOT_FOUND, one answer for all three); AT0 itself cannot be cloned
(BAD_USER_INPUT, exit 2); an owner who is not a possible owner is
TASK_OWNER_INVALID (exit 2); TASK_NOT_ASSIGNEE and a priority you may not set
(TASK_NOT_OWNER) are exit 8; a named parent that has ended is TASK_ENDED, exit 5.
A transport failure after the request left can leave the outcome unknown: list
the App's tasks before you clone again, since a clone is not deduplicated.

Needs a provider with hadron-server#2646; an older one rejects the document as
schema skew (exit 2). Success prints the new task's handle, id and status;
--json returns the task with its warnings list, as ` + "`task create`" + ` does.`,
		Example: `  # a plain copy
  hadron team task clone AT0.12

  # copy it as a backlog item for another worker, with a new title
  hadron team task clone AT0.12 --status backlog --assignee worker:Iris --title "Retest the export fix, round 2"

  # lift a child out of its parent as a root task
  hadron team task clone AT1.4 --no-parent

  # copy it with new tags and a different owner, dropping the copied references
  hadron team task clone AT0.12 --tag retest --tag export --owner worker:Iris --clear-refs`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			if noParent && flags.Changed("parent") {
				return exitcode.Newf(exitcode.Usage, "--parent and --no-parent contradict each other: name a parent or none")
			}
			if flags.Changed("no-parent") && !noParent {
				return exitcode.Newf(exitcode.Usage, "--no-parent=false is not a choice: leave both flags out to keep the source's parent")
			}
			// PRESENCE, not value: --close-session=false must override a copied true
			// (an explicitly given flag sends its value, false included).
			if flags.Changed("close-session") && flags.Changed("no-close-session") {
				return exitcode.Newf(exitcode.Usage, "--close-session and --no-close-session contradict each other")
			}
			if flags.Changed("parent") && strings.TrimSpace(parent) == "" {
				return exitcode.Newf(exitcode.Usage, "--parent needs a task id or handle (--no-parent clones it with none)")
			}
			reqNamed := flags.Changed("require-role") || flags.Changed("family-in") || flags.Changed("family-not-in") || flags.Changed("exclude-worker") || flags.Changed("min-tier") || flags.Changed("model-group") || flags.Changed("model-group-not")
			for _, c := range []struct {
				set, clear bool
				a, b       string
			}{
				{flags.Changed("tag"), clearTags, "--tag", "--clear-tags"},
				{flags.Changed("ref"), clearRefs, "--ref", "--clear-refs"},
				{flags.Changed("properties"), clearProperties, "--properties", "--clear-properties"},
				{reqNamed, clearRequirements, "--require-role, --family-in, --family-not-in, --exclude-worker, --min-tier, --model-group or --model-group-not", "--clear-requirements"},
			} {
				if c.set && c.clear {
					return exitcode.Newf(exitcode.Usage, "%s and %s contradict each other: replace the value or clear it", c.a, c.b)
				}
			}
			if flags.Changed("project") && !project {
				return exitcode.Newf(exitcode.Usage, "--project=false is not a choice: leave the flag out to keep the source's shape")
			}
			if project && flags.Changed("parent") {
				return exitcode.Newf(exitcode.Usage, "--project and --parent contradict each other: a project is a top-level task")
			}
			if familyIn != "" && familyNotIn != "" {
				return exitcode.Newf(exitcode.Usage, "--family-in and --family-not-in: give one, not both")
			}
			ref, err := taskRefArg(args)
			if err != nil {
				return err
			}
			input := gen.CloneAgentTaskInput{}
			if flags.Changed("title") {
				input.Title = &title
			}
			if flags.Changed("description") {
				input.Description = &description
			}
			if flags.Changed("source") {
				input.Source = &source
			}
			if flags.Changed("priority") {
				input.Priority = &priority
			}
			if flags.Changed("status") {
				switch strings.ToLower(status) {
				case "backlog":
					s := gen.AgentTaskCreateStatusBacklog
					input.Status = &s
				case "pending":
					s := gen.AgentTaskCreateStatusPending
					input.Status = &s
				default:
					return exitcode.Newf(exitcode.Usage, "--status is backlog or pending at creation (every other status is reached by a move), got %q", status)
				}
			}
			// The same two validators `task create` uses for these two flags
			// (RFC 3339 for --start-by, the strict date-time for --finish-by), so a
			// value one command takes the other takes.
			if flags.Changed("start-by") {
				if _, err := time.Parse(time.RFC3339, startBy); err != nil {
					return exitcode.Newf(exitcode.Usage, "--start-by takes an RFC 3339 timestamp, e.g. 2026-10-13T09:00:00Z, got %q", startBy)
				}
				input.StartBy = &startBy
			}
			if flags.Changed("finish-by") {
				if _, ok := cmdutil.ParseStrictDateTime(finishBy); !ok {
					return exitcode.Newf(exitcode.Usage, "--finish-by takes an RFC 3339 timestamp, e.g. 2099-11-02T09:00:00Z, got %q", finishBy)
				}
				input.FinishBy = &finishBy
			}
			if flags.Changed("assignee") {
				party, err := parseTaskPartyFlag("--assignee", assignee)
				if err != nil {
					return err
				}
				input.Assignee = party
			}
			if flags.Changed("owner") {
				party, err := parseTaskPartyFlag("--owner", owner)
				if err != nil {
					return err
				}
				input.Owner = party
			}
			if project {
				input.Project = boolPtr(true)
			}
			// tags, references, properties and requirements are RawMessage on the
			// wire: nil omits the key (keep the copied value), an explicit [] or null
			// clears (the server reads an omitted field as "preserve").
			switch {
			case clearTags:
				input.Tags = json.RawMessage("[]")
			case flags.Changed("tag"):
				for i, t := range tags {
					if tags[i] = strings.TrimSpace(t); tags[i] == "" {
						return exitcode.Newf(exitcode.Usage, "--tag must not be empty (use --clear-tags to empty the list)")
					}
				}
				input.Tags = mustJSON(tags)
			}
			switch {
			case clearRefs:
				input.References = mustJSON([]gen.AgentTaskReferenceInput{})
			case flags.Changed("ref"):
				parsed, err := parseTaskRefFlags(refs)
				if err != nil {
					return err
				}
				input.References = mustJSON(parsed)
			}
			switch {
			case clearProperties:
				input.Properties = jsonNull
			case flags.Changed("properties"):
				p, err := parsePropertiesFlag(properties)
				if err != nil {
					return err
				}
				input.Properties = p
			}
			if flags.Changed("min-tier") {
				if err := validateTaskMinTier(minTier); err != nil {
					return err
				}
			}
			groupsIn, groupsNotIn, err := taskModelGroupFlags(cmd, modelGroupIn, modelGroupNotIn)
			if err != nil {
				return err
			}
			switch {
			case clearRequirements:
				input.Requirements = jsonNull
			case reqNamed:
				p := taskRequirementsPatch{ExcludeWorkers: excludeWorkers, FamilyIn: commaList(familyIn), FamilyNotIn: commaList(familyNotIn), Role: strings.TrimSpace(requireRole), MinTier: minTier, ModelGroupIn: groupsIn, ModelGroupNotIn: groupsNotIn}
				for i, w := range p.ExcludeWorkers {
					if p.ExcludeWorkers[i] = strings.TrimSpace(w); p.ExcludeWorkers[i] == "" {
						return exitcode.Newf(exitcode.Usage, "--exclude-worker must name a worker")
					}
				}
				if (flags.Changed("require-role") && p.Role == "") || (flags.Changed("family-in") && len(p.FamilyIn) == 0) || (flags.Changed("family-not-in") && len(p.FamilyNotIn) == 0) {
					return exitcode.Newf(exitcode.Usage, "a requirement flag was given an empty value: name the requirement, or use --clear-requirements")
				}
				input.Requirements = mustJSON(p)
			}
			if flags.Changed("close-session") {
				input.CloseSessionWhenDone = boolPtr(closeSession)
			} else if flags.Changed("no-close-session") {
				input.CloseSessionWhenDone = boolPtr(!noCloseSession)
			}
			// parentTaskId is three-state: omitted keeps the source's parent, an
			// id names another, an explicit null means none.
			switch {
			case noParent:
				input.ParentTaskId = json.RawMessage("null")
			case flags.Changed("parent"):
				id, _ := json.Marshal(strings.TrimSpace(parent)) // a string cannot fail JSON encoding
				input.ParentTaskId = id
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			mctx, sink := api.WithWarningSink(cmd.Context())
			resp, err := gen.AgentTaskClone(mctx, client, ref, &input)
			if err != nil {
				return api.WithServerCode(mapTaskError(err), err)
			}
			task := taskDTOFrom(resp.CloneAgentTask.AgentTaskFields)
			return writeTaskMutation(f, task, taskWarningsFor(sink, "cloneAgentTask"), func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ cloned task %s as %s (%s)\n", output.SafeCell(ref), taskRef(task.Handle, task.ID), task.Status); err != nil {
					return err
				}
				if task.Queued {
					if _, err := fmt.Fprintln(w, "  queued: no assignee — the platform's matcher picks one"); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintf(w, "  app: %s\n", output.SafeCell(task.AppID)); err != nil {
					return err
				}
				return writeCloseSessionLine(w, task)
			})
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "replace the copied title")
	cmd.Flags().StringVar(&description, "description", "", "replace the copied description")
	cmd.Flags().StringVar(&source, "source", "", "replace the copied source reference")
	cmd.Flags().IntVar(&priority, "priority", 1, "matching priority: 1-5, 1 = highest (replaces the copied one)")
	cmd.Flags().StringVar(&status, "status", "", "the clone's create-time status: backlog or pending (the default)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "party to assign the clone to (worker:<…> / user:<…>)")
	cmd.Flags().StringVar(&startBy, "start-by", "", "RFC 3339 deadline to start by")
	cmd.Flags().StringVar(&finishBy, "finish-by", "", "RFC 3339 deadline to finish by")
	cmd.Flags().StringVar(&parent, "parent", "", "another parent task (same App) instead of the source's")
	cmd.Flags().BoolVar(&noParent, "no-parent", false, "no parent: a root under AT0 (sends parentTaskId null)")
	cmd.Flags().BoolVar(&closeSession, "close-session", false, "finishing or cancelling asks the assignee to end its worker session")
	cmd.Flags().BoolVar(&noCloseSession, "no-close-session", false, "finishing or cancelling does not ask the assignee to end its worker session")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "a tag, repeatable; --tag replaces the whole copied list")
	cmd.Flags().BoolVar(&clearTags, "clear-tags", false, "the clone has no tags")
	cmd.Flags().StringArrayVar(&refs, "ref", nil, "typed reference <kind>=<target>[|<label>], repeatable; --ref replaces the whole copied list; kind is "+taskRefKindsDoc)
	cmd.Flags().BoolVar(&clearRefs, "clear-refs", false, "the clone has no references")
	cmd.Flags().StringVar(&properties, "properties", "", "the clone's own JSON object, or @file; replaces the copied value (left out, not refused, for a caller who may not set properties)")
	cmd.Flags().BoolVar(&clearProperties, "clear-properties", false, "the clone has no properties")
	cmd.Flags().StringVar(&minTier, "min-tier", "", "minimum model tier: low, mid or high; replaces the whole requirements")
	cmd.Flags().StringVar(&requireRole, "require-role", "", "required role; replaces the whole requirements")
	addTaskModelGroupFlags(cmd, &modelGroupIn, &modelGroupNotIn)
	cmd.Flags().StringVar(&familyIn, "family-in", "", "comma list of model families the assignee must run as; replaces the whole requirements")
	cmd.Flags().StringVar(&familyNotIn, "family-not-in", "", "comma list of model families the assignee must not run as; replaces the whole requirements")
	cmd.Flags().StringArrayVar(&excludeWorkers, "exclude-worker", nil, "a worker the matcher must not pick, repeatable; replaces the whole requirements")
	cmd.Flags().BoolVar(&clearRequirements, "clear-requirements", false, "the clone has no requirements")
	cmd.Flags().StringVar(&owner, "owner", "", "party that owns the clone (worker:<…> / user:<…>); default is the caller")
	cmd.Flags().BoolVar(&project, "project", false, "make the clone a top-level project")
	return cmd
}
