package team

// `hadron team task update` (hadron-cli#1137, server#2645, cor:agt:050:09
// Editing). A thin caller over updateAgentTask: the server decides who may edit,
// which states refuse (TASK_ENDED), and every value's range. This file only
// shapes the strict patch the server reads: a named field is applied, a named
// null clears it, an unnamed field is not sent at all.

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// taskRequirementsPatch is the requirements object, in the key order the wire
// shows. Empty fields drop out, so only what a flag named is sent.
type taskRequirementsPatch struct {
	ExcludeWorkers  []string `json:"excludeWorkers,omitempty"`
	FamilyIn        []string `json:"familyIn,omitempty"`
	FamilyNotIn     []string `json:"familyNotIn,omitempty"`
	ModelGroupIn    []string `json:"modelGroupIn,omitempty"`
	ModelGroupNotIn []string `json:"modelGroupNotIn,omitempty"`
	Role            string   `json:"role,omitempty"`
	MinTier         string   `json:"minTier,omitempty"`
}

var jsonNull = json.RawMessage("null")

func newCmdTeamTaskUpdate(f *cmdutil.Factory) *cobra.Command {
	var title, description, startBy, requireRole, familyIn, familyNotIn, modelGroupIn, modelGroupNotIn, minTier, properties string
	var tags, excludeWorkers, refs []string
	var clearRefs bool
	var clearDescription, clearTags, clearRequirements, clearStartBy, clearProperties bool
	var closeSession, noCloseSession bool
	cmd := &cobra.Command{
		Use:   "update <task> [flags]",
		Short: "Edit a task's title, description, tags, requirements, start-by or close-session choice",
		Long: `Change a task after creating it, without cancelling and recreating it. The task
keeps its id and handle. Name it by id or handle (AT1.2), as task get does.

Only the flags you name are sent; every other field stays as it is. With no
flag at all the command is refused here and nothing is sent. A flag that
replaces a value has a --clear-… twin that empties it; naming both is refused.

- --title <text>: the new title. It cannot be emptied; the server refuses an
  empty one and its refusal is shown.
- --description <text> or --clear-description.
- --tag <tag>, repeatable: --tag replaces the whole list, it never adds to it.
  --clear-tags empties the list.
- --require-role, --family-in, --family-not-in, --exclude-worker and --min-tier replace the
  whole requirements, not one of them: name every requirement you want to keep.
  --model-group and --model-group-not belong to that same replacement group.
  --clear-requirements empties them, including the minimum tier and model groups.
  Group flags take comma lists, both may coexist (even the same name). Empty
  supplied lists are usage errors. Names go unchanged to the server, which
  re-validates every group name on update, including names deleted since creation.
  Groups resolve live at match time: editing a group can change matches without
  a task edit. They are matching inputs only, never authorization.
  All task-record reads and mutation receipts require hadron-server#3029,
  even without group flags (see team task --help). Comma-split items stay
  verbatim for the server to trim and drop empties.
  --min-tier low|mid|high sets a minimum: the lowest sufficient tier is preferred;
  an unknown tier is ineligible when a minimum is set. Requires a server
  containing hadron-server#2983 (31d63828 or later).
- --start-by <RFC 3339 time> or --clear-start-by. --start-by re-anchors only the
  not-started clock (cor:agt:050:14): it moves the deadline that task list
  --overdue and the not-started alarm read. An alarm already raised stays raised.
- --properties <json|@file>: the task's own JSON object (the team's schedule
  and review notes, for example), which the platform stores and never reads.
  It replaces the whole value, never merges: send every key you want to keep.
  --clear-properties removes it. Properties are per task: a child task has its
  own and does not inherit the root's. The server checks that it is an object
  (PROPERTIES_NOT_AN_OBJECT), at most 16 KiB (PROPERTIES_TOO_LARGE) and valid
  against the team's task-properties schema (PROPERTIES_SCHEMA_VIOLATION, or
  PROPERTIES_SCHEMA_UNUSABLE when that schema itself cannot be used). The
  assignee may edit properties alone: combined with any other field the whole
  edit is refused (TASK_NOT_OWNER).
- --close-session asks the assignee's worker session to end when the task
  finishes or is cancelled; --no-close-session says it should not. Neither flag
  leaves the choice as it is. ` + "`--close-session=false`" + ` is refused, because
  it cannot be told from leaving the flag out: say no with --no-close-session.

Not editable here: --source (fixed once the task exists), the owner and assignee
(task assign, task reassign), the status (task start, finish, cancel, park, ready),
the parent, the priority and the blockers (task set-priority, add-blocker).

Who may edit: the task's owner, a worker the owner reports to, or a person who
manages the App; an assignee alone may edit --properties and nothing else, any
other field is refused (TASK_NOT_OWNER). A finished or
cancelled task refuses (TASK_ENDED). An edit is not progress and does not clear
a stall alarm. Success prints the task as task get does; --json returns it.`,
		Example: `  # fix a title and clear the description
  hadron team task update AT1.2 --title "Fix the login redirect" --clear-description

  # replace the tags and the requirements with exactly these
  hadron team task update AT1.2 --tag auth --tag urgent --require-role qa-engineer --family-not-in codex

  # push the not-started deadline out, and ask the session to end with the task
  hadron team task update AT1.2 --start-by 2026-11-02T09:00:00Z --close-session`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskId, err := taskRefArg(args)
			if err != nil {
				return err
			}
			fl := cmd.Flags()
			usage := func(format string, a ...any) error { return exitcode.Newf(exitcode.Usage, format, a...) }
			reqNamed := fl.Changed("require-role") || fl.Changed("family-in") || fl.Changed("family-not-in") || fl.Changed("exclude-worker") || fl.Changed("min-tier") || fl.Changed("model-group") || fl.Changed("model-group-not")
			anyField := fl.Changed("title") || fl.Changed("description") || clearDescription ||
				fl.Changed("tag") || clearTags || fl.Changed("ref") || clearRefs || reqNamed || clearRequirements ||
				fl.Changed("start-by") || clearStartBy || fl.Changed("close-session") || fl.Changed("no-close-session") ||
				fl.Changed("properties") || clearProperties
			if !anyField {
				return usage("pass at least one field flag to change (--title, --description, --tag, --ref, --require-role, --start-by, --close-session, or a --clear-…); nothing was sent")
			}
			for _, c := range []struct {
				set   bool
				clear bool
				a, b  string
			}{
				{fl.Changed("description"), clearDescription, "--description", "--clear-description"},
				{fl.Changed("tag"), clearTags, "--tag", "--clear-tags"},
				{fl.Changed("ref"), clearRefs, "--ref", "--clear-refs"},
				{reqNamed, clearRequirements, "--require-role, --family-in, --family-not-in, --exclude-worker, --min-tier, --model-group or --model-group-not", "--clear-requirements"},
				{fl.Changed("start-by"), clearStartBy, "--start-by", "--clear-start-by"},
				{fl.Changed("properties"), clearProperties, "--properties", "--clear-properties"},
			} {
				if c.set && c.clear {
					return usage("%s and %s contradict each other: replace the value or clear it", c.a, c.b)
				}
			}
			// Presence, not value: `--close-session=false` is a supplied flag that
			// a plain bool cannot tell from an absent one, so it is refused with
			// the spelling that does say no, rather than silently keeping the
			// stored choice (the cli-add-a-tri-state-flag gotcha).
			for _, c := range []struct {
				name string
				on   bool
				use  string
			}{{"close-session", closeSession, "--no-close-session"}, {"no-close-session", noCloseSession, "--close-session"}} {
				if fl.Changed(c.name) && !c.on {
					return usage("--%s=false changes nothing; to say the opposite pass %s", c.name, c.use)
				}
			}
			if closeSession && noCloseSession {
				return usage("--close-session and --no-close-session contradict each other")
			}
			if familyIn != "" && familyNotIn != "" {
				return usage("--family-in and --family-not-in: give one, not both")
			}

			if fl.Changed("min-tier") {
				if err := validateTaskMinTier(minTier); err != nil {
					return err
				}
			}
			groupsIn, groupsNotIn, err := taskModelGroupFlags(cmd, modelGroupIn, modelGroupNotIn)
			if err != nil {
				return err
			}
			input := gen.UpdateAgentTaskInput{}
			if fl.Changed("title") {
				// Presence, not value: an empty --title is sent and the server's
				// refusal is the answer, rather than silently dropping the edit.
				input.Title = &title
			}
			switch {
			case clearDescription:
				input.Description = jsonNull
			case fl.Changed("description"):
				input.Description = mustJSON(description)
			}
			switch {
			case clearTags:
				input.Tags = jsonNull
			case fl.Changed("tag"):
				for i, t := range tags {
					if tags[i] = strings.TrimSpace(t); tags[i] == "" {
						return usage("--tag must not be empty (use --clear-tags to empty the list)")
					}
				}
				input.Tags = mustJSON(tags)
			}
			switch {
			case clearRefs:
				input.References = mustJSON([]gen.AgentTaskReferenceInput{})
			case fl.Changed("ref"):
				parsed, err := parseTaskRefFlags(refs)
				if err != nil {
					return err
				}
				input.References = mustJSON(parsed)
			}
			switch {
			case clearRequirements:
				input.Requirements = jsonNull
			case reqNamed:
				p := taskRequirementsPatch{ExcludeWorkers: excludeWorkers, FamilyIn: commaList(familyIn), FamilyNotIn: commaList(familyNotIn), Role: strings.TrimSpace(requireRole), MinTier: minTier, ModelGroupIn: groupsIn, ModelGroupNotIn: groupsNotIn}
				for i, w := range p.ExcludeWorkers {
					if p.ExcludeWorkers[i] = strings.TrimSpace(w); p.ExcludeWorkers[i] == "" {
						return usage("--exclude-worker must name a worker")
					}
				}
				if (fl.Changed("require-role") && p.Role == "") || (fl.Changed("family-in") && len(p.FamilyIn) == 0) || (fl.Changed("family-not-in") && len(p.FamilyNotIn) == 0) {
					return usage("a requirement flag was given an empty value: name the requirement, or use --clear-requirements")
				}
				input.Requirements = mustJSON(p)
			}
			switch {
			case clearStartBy:
				input.StartBy = jsonNull
			case fl.Changed("start-by"):
				if _, ok := cmdutil.ParseStrictDateTime(startBy); !ok {
					return usage("--start-by takes an RFC 3339 timestamp, e.g. 2026-10-13T09:00:00Z, got %q", startBy)
				}
				input.StartBy = mustJSON(startBy)
			}
			if closeSession {
				input.CloseSessionWhenDone = boolPtr(true)
			} else if noCloseSession {
				input.CloseSessionWhenDone = boolPtr(false)
			}
			switch {
			case clearProperties:
				input.Properties = jsonNull
			case fl.Changed("properties"):
				p, err := parsePropertiesFlag(properties)
				if err != nil {
					return err
				}
				input.Properties = p
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AgentTaskUpdate(cmd.Context(), client, taskId, &input)
			if err != nil {
				// The new requirement refusals retain their code at the JSON sink.
				// Keep ordinary task-write error output unchanged when no group flag is named.
				if groupsIn != nil || groupsNotIn != nil {
					return api.WithServerCode(mapTaskError(err), err)
				}
				return mapTaskError(err)
			}
			task := taskDTOFrom(resp.UpdateAgentTask.AgentTaskFields)
			return output.Write(f.IOStreams, f.JSON, task, func(w io.Writer) error {
				return renderTaskDetail(w, task)
			})
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "the new title (cannot be emptied)")
	cmd.Flags().StringVar(&description, "description", "", "the new description")
	cmd.Flags().BoolVar(&clearDescription, "clear-description", false, "remove the description")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "a tag, repeatable; --tag replaces the whole list")
	cmd.Flags().BoolVar(&clearTags, "clear-tags", false, "remove every tag")
	cmd.Flags().StringArrayVar(&refs, "ref", nil, "typed reference <kind>=<target>[|<label>], repeatable; --ref replaces the whole list; kind is "+taskRefKindsDoc)
	cmd.Flags().BoolVar(&clearRefs, "clear-refs", false, "remove every reference")
	cmd.Flags().StringVar(&minTier, "min-tier", "", "minimum model tier: low, mid or high; replaces the whole requirements")
	cmd.Flags().StringVar(&requireRole, "require-role", "", "required role; replaces the whole requirements")
	addTaskModelGroupFlags(cmd, &modelGroupIn, &modelGroupNotIn)
	cmd.Flags().StringVar(&familyIn, "family-in", "", "comma list of model families the assignee must run as; replaces the whole requirements")
	cmd.Flags().StringVar(&familyNotIn, "family-not-in", "", "comma list of model families the assignee must not run as; replaces the whole requirements")
	cmd.Flags().StringArrayVar(&excludeWorkers, "exclude-worker", nil, "a worker the matcher must not pick, repeatable; replaces the whole requirements")
	cmd.Flags().BoolVar(&clearRequirements, "clear-requirements", false, "remove every requirement")
	cmd.Flags().StringVar(&startBy, "start-by", "", "RFC 3339 time; re-anchors only the not-started clock")
	cmd.Flags().BoolVar(&clearStartBy, "clear-start-by", false, "remove the explicit start-by")
	cmd.Flags().BoolVar(&closeSession, "close-session", false, "ask the assignee's session to end when the task finishes or is cancelled")
	cmd.Flags().BoolVar(&noCloseSession, "no-close-session", false, "state that the session should not end with the task")
	cmd.Flags().StringVar(&properties, "properties", "", "the task's own JSON object, or @file; replaces the whole value")
	cmd.Flags().BoolVar(&clearProperties, "clear-properties", false, "remove the task's properties")
	return cmd
}

// mustJSON marshals a value built from strings and string slices, which cannot
// fail.
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
