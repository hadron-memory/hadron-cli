package memory

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// `hadron memory config …` — a memory's config: its node-role rules
// (hadron-server#1325 part b, cli#716 slice 1). Templates and applying them are
// later slices (#1325 part c, #1334); nothing here mocks them.
//
// Every operation is for the memory's MANAGERS (H3/H4): the strict owner of a
// personal/private memory, else its user owner or an org ADMIN/OWNER. For
// anyone else the server answers exactly as for a missing memory, so a refusal
// is exit 4 and is never rendered as "no rules".

// nodeRoleRuleDTO is the stable --json shape of one rule. Field changes here are
// contract changes (docs/agentic-usage.md).
//
// A reference is its URN AND its state. The URN is null unless the state is OK:
// BROKEN (the node was deleted) and UNREADABLE (it exists; you may not read it)
// both withhold it, and the CLI never guesses one.
type nodeRoleRuleDTO struct {
	ID                   string             `json:"id"`
	Role                 string             `json:"role"`
	Revision             int                `json:"revision"`
	Enabled              bool               `json:"enabled"`
	StrictSubRoles       bool               `json:"strictSubRoles"`
	Writers              string             `json:"writers"`
	ValidateBy           *string            `json:"validateBy"`
	AuthorTask           *string            `json:"authorTask"`
	AuthorTaskState      string             `json:"authorTaskState"`
	ValidationTask       *string            `json:"validationTask"`
	ValidationTaskState  string             `json:"validationTaskState"`
	DescriptionNode      *string            `json:"descriptionNode"`
	DescriptionNodeState string             `json:"descriptionNodeState"`
	Locked               bool               `json:"locked"`
	SourceTemplateID     *string            `json:"sourceTemplateId"`
	SourceTemplate       *sourceTemplateDTO `json:"sourceTemplate"`
	CreatedAt            string             `json:"createdAt"`
	CreatedBy            *string            `json:"createdBy"`
	UpdatedAt            *string            `json:"updatedAt"`
	UpdatedBy            *string            `json:"updatedBy"`
}

type sourceTemplateDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

// memoryConfigDTO is `config get`'s --json shape. `id` is null, and `rules` is
// [], for a memory whose manager has written no rule yet — that is the empty
// config, a success, not a refusal.
type memoryConfigDTO struct {
	ID        *string           `json:"id"`
	MemoryID  string            `json:"memoryId"`
	Rules     []nodeRoleRuleDTO `json:"rules"`
	CreatedAt *string           `json:"createdAt"`
	CreatedBy *string           `json:"createdBy"`
	UpdatedAt *string           `json:"updatedAt"`
	UpdatedBy *string           `json:"updatedBy"`
}

// configWarningDTO is a non-fatal finding about a saved rule. It never changes
// the command's exit code. `taskUrn` is present only when you can read the task.
type configWarningDTO struct {
	Code      string  `json:"code"`
	Role      string  `json:"role"`
	Field     *string `json:"field"`
	TaskURN   *string `json:"taskUrn"`
	TaskState *string `json:"taskState"`
}

// ruleWriteDTO is the --json shape of `rule add` and `rule update`.
type ruleWriteDTO struct {
	Rule     nodeRoleRuleDTO    `json:"rule"`
	Warnings []configWarningDTO `json:"warnings"`
}

// ruleDeleteDTO is the --json shape of `rule rm`.
type ruleDeleteDTO struct {
	MemoryID string `json:"memoryId"`
	Role     string `json:"role"`
	ID       string `json:"id"`
	Revision int    `json:"revision"`
	Deleted  bool   `json:"deleted"`
}

func dtoFromRule(r *gen.NodeRoleRuleFields) nodeRoleRuleDTO {
	dto := nodeRoleRuleDTO{
		ID: r.Id, Role: r.Role, Revision: r.Revision, Enabled: r.Enabled,
		StrictSubRoles: r.StrictSubRoles, Writers: string(r.Writers),
		AuthorTask: r.AuthorTask, AuthorTaskState: string(r.AuthorTaskState),
		ValidationTask: r.ValidationTask, ValidationTaskState: string(r.ValidationTaskState),
		DescriptionNode: r.DescriptionNode, DescriptionNodeState: string(r.DescriptionNodeState),
		Locked: r.Locked, SourceTemplateID: r.SourceTemplateId,
		CreatedAt: r.CreatedAt, CreatedBy: r.CreatedBy, UpdatedAt: r.UpdatedAt, UpdatedBy: r.UpdatedBy,
	}
	if r.ValidateBy != nil {
		v := string(*r.ValidateBy)
		dto.ValidateBy = &v
	}
	if t := r.SourceTemplate; t != nil {
		dto.SourceTemplate = &sourceTemplateDTO{ID: t.Id, Name: t.Name, Deleted: t.Deleted}
	}
	return dto
}

func dtoFromWarning(w *gen.MemoryConfigWarningFields) configWarningDTO {
	dto := configWarningDTO{Code: w.Code, Role: w.Role, Field: w.Field, TaskURN: w.TaskUrn}
	if w.TaskState != nil {
		s := string(*w.TaskState)
		dto.TaskState = &s
	}
	return dto
}

func newCmdConfig(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config <command>",
		Short: "Inspect and change a memory's config (its node-role rules)",
		Long: `A memory's config holds its node-role rules: one per role or declared
sub-role, each naming the tasks that author and validate nodes of that role,
who may write them, and whether undeclared sub-roles are refused.

Only the memory's managers can read or change it: the owner of a personal or
private memory, otherwise its user owner or an org ADMIN/OWNER. Anyone else
gets the same answer as for a memory that does not exist (exit 4).`,
	}
	cmd.AddCommand(newCmdConfigGet(f))
	cmd.AddCommand(newCmdConfigRule(f))
	return cmd
}

func newCmdConfigGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <memoryRef>",
		Short: "Show a memory's node-role rules",
		Long: `Show a memory's node-role rules, role-ascending.

A memory with no rules yet prints "no rules" and exits 0. A memory you do not
manage exits 4, exactly like one that does not exist: the server does not say
which, and "no rules" would be a claim it never made.

Each task or description reference shows its state:
  OK          the node's URN
  —           not configured
  BROKEN      the node was deleted; a manager must repoint it
  UNREADABLE  the node exists but you may not read it, so its URN is withheld`,
		Example: `  hadron memory config get hrn:mem:acme.com:kb
  hadron memory config get hrn:mem:acme.com:kb --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.MemoryConfig(cmd.Context(), client, cmdutil.CanonicalMemoryRef(args[0]))
			if err != nil {
				return api.MapError(err)
			}
			cfg := resp.MemoryConfig
			if cfg == nil {
				return notFoundMemory(args[0])
			}
			dto := memoryConfigDTO{
				ID: cfg.Id, MemoryID: cfg.MemoryId, Rules: []nodeRoleRuleDTO{},
				CreatedAt: cfg.CreatedAt, CreatedBy: cfg.CreatedBy, UpdatedAt: cfg.UpdatedAt, UpdatedBy: cfg.UpdatedBy,
			}
			for _, r := range cfg.Rules {
				if r != nil {
					dto.Rules = append(dto.Rules, dtoFromRule(&r.NodeRoleRuleFields))
				}
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.Rules) == 0 {
					_, err := fmt.Fprintf(w, "memory %s: no rules\n", dto.MemoryID)
					return err
				}
				if _, err := fmt.Fprintf(w, "memory %s: %d rule(s)\n", dto.MemoryID, len(dto.Rules)); err != nil {
					return err
				}
				for _, r := range dto.Rules {
					if _, err := fmt.Fprintln(w); err != nil {
						return err
					}
					if err := renderRule(w, r); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}

// renderRule prints one rule as a labelled block: a rule carries too many
// fields, and URNs too long, for a table row to stay readable.
func renderRule(w io.Writer, r nodeRoleRuleDTO) error {
	validateBy := "—"
	if r.ValidateBy != nil {
		validateBy = *r.ValidateBy
	}
	rows := [][2]string{
		{"enabled", yesNo(r.Enabled)},
		{"strict sub-roles", yesNo(r.StrictSubRoles)},
		{"writers", r.Writers},
		{"validate by", validateBy},
		{"author task", refCell(r.AuthorTask, r.AuthorTaskState, "--author-task")},
		{"validation task", refCell(r.ValidationTask, r.ValidationTaskState, "--validation-task")},
		{"description node", refCell(r.DescriptionNode, r.DescriptionNodeState, "--description-node")},
		{"locked", yesNo(r.Locked)},
		{"source template", templateCell(r)},
	}
	if _, err := fmt.Fprintf(w, "%s  (rule %s, revision %d)\n", r.Role, r.ID, r.Revision); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(w, "  %-17s %s\n", row[0], row[1]); err != nil {
			return err
		}
	}
	return nil
}

// refCell renders a reference by its STATE, never by guessing a URN the server
// withheld. BROKEN and UNREADABLE are different facts with different remedies
// (#1325 H5, #1327), so they must not share a rendering.
func refCell(urn *string, state, flag string) string {
	switch state {
	case "OK":
		if urn != nil {
			return *urn
		}
		return "OK (URN not returned)"
	case "NONE":
		return "—"
	case "BROKEN":
		return "BROKEN — the node was deleted; a config manager must repoint it (" + flag + ")"
	case "UNREADABLE":
		return "UNREADABLE — it exists but you may not read it; ask for read access, or repoint it (" + flag + ")"
	default:
		return state
	}
}

func templateCell(r nodeRoleRuleDTO) string {
	switch {
	case r.SourceTemplate != nil && r.SourceTemplate.Deleted:
		return r.SourceTemplate.Name + " (deleted)"
	case r.SourceTemplate != nil:
		return r.SourceTemplate.Name
	case r.SourceTemplateID != nil:
		return *r.SourceTemplateID + " (no longer available)"
	default:
		return "—"
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// renderWarnings prints save warnings to stderr in the human branch. They never
// change the exit code; in --json they are the payload's `warnings` array.
func renderWarnings(io *output.IOStreams, warnings []configWarningDTO) {
	for _, w := range warnings {
		field := ""
		if w.Field != nil {
			field = " (" + *w.Field + ")"
		}
		fmt.Fprintf(io.ErrOut, "warning: %s on role %s%s\n", w.Code, w.Role, field)
	}
}

// findRule reads the memory's config and returns the rule for role. The same
// read supplies the rule's id (a rule has no URN) and its revision, which the
// write then sends as expectedRevision — so a rule changed in between is
// refused CONFLICT, never overwritten.
func findRule(cmd *cobra.Command, f *cmdutil.Factory, memoryRef, role string) (*gen.NodeRoleRuleFields, string, error) {
	client, err := f.GraphQLClient()
	if err != nil {
		return nil, "", err
	}
	resp, err := gen.MemoryConfig(cmd.Context(), client, cmdutil.CanonicalMemoryRef(memoryRef))
	if err != nil {
		return nil, "", api.MapError(err)
	}
	if resp.MemoryConfig == nil {
		return nil, "", notFoundMemory(memoryRef)
	}
	for _, r := range resp.MemoryConfig.Rules {
		if r != nil && r.Role == role {
			return &r.NodeRoleRuleFields, resp.MemoryConfig.MemoryId, nil
		}
	}
	return nil, "", exitcode.Newf(exitcode.NotFound,
		"memory %s has no rule for role %q — `hadron memory config get %s` lists its rules",
		memoryRef, role, memoryRef)
}
