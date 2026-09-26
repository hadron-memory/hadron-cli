package memory

import (
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdConfigRule(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule <command>",
		Short: "Add, change or remove a memory's node-role rules",
	}
	cmd.AddCommand(newCmdConfigRuleAdd(f))
	cmd.AddCommand(newCmdConfigRuleUpdate(f))
	cmd.AddCommand(newCmdConfigRuleRm(f))
	return cmd
}

// ruleFlags are the settable fields of a rule, shared by `rule add` and
// `rule update` so the two verbs cannot drift apart. Which flags were GIVEN is
// read from cobra (Changed), never from the value: on update an omitted flag
// leaves the field alone, while an empty task/node ref clears it.
type ruleFlags struct {
	authorTask      string
	validationTask  string
	descriptionNode string
	writers         string
	validateBy      string
	strictSubRoles  bool
	enabled         bool
}

func (rf *ruleFlags) register(cmd *cobra.Command, update bool) {
	clearNote := ""
	if update {
		clearNote = `; "" clears it`
	}
	cmd.Flags().StringVar(&rf.authorTask, "author-task", "", "runnable task that authors nodes of this role (node id or URN)"+clearNote)
	cmd.Flags().StringVar(&rf.validationTask, "validation-task", "", "runnable task that validates nodes of this role (node id or URN)"+clearNote)
	cmd.Flags().StringVar(&rf.descriptionNode, "description-node", "", "node documenting this role (node id or URN)"+clearNote)
	cmd.Flags().StringVar(&rf.writers, "writers", "", "who may write nodes of this role: all, admin or owner")
	cmd.Flags().StringVar(&rf.validateBy, "validate-by", "", "who runs the validation task: agent or platform (needs a validation task)"+clearNote)
	cmd.Flags().BoolVar(&rf.strictSubRoles, "strict-sub-roles", false, "refuse this role's undeclared sub-roles instead of falling back to it (--strict-sub-roles=false turns it off)")
	cmd.Flags().BoolVar(&rf.enabled, "enabled", false, "whether the rule applies: --enabled=false disables it, --enabled re-enables it (omitted: the server default on add, unchanged on update)")
}

// ruleInput is the parsed, validated form of the flags: nil means "not given".
// A non-nil pointer to "" is an explicit CLEAR (update only).
type ruleInput struct {
	authorTaskRef      *string
	validationTaskRef  *string
	descriptionNodeRef *string
	writers            *gen.NodeRoleWriters
	validateBy         *gen.ContentValidator
	clearValidateBy    bool
	strictSubRoles     *bool
	enabled            *bool
}

func (in ruleInput) empty() bool {
	return in.authorTaskRef == nil && in.validationTaskRef == nil && in.descriptionNodeRef == nil &&
		in.writers == nil && in.validateBy == nil && !in.clearValidateBy &&
		in.strictSubRoles == nil && in.enabled == nil
}

// parse validates every given flag before any request is sent. A malformed
// value is refused here (exit 2) rather than passed on to fail later: the
// flags are a contract, and a permissive parse is the bug this avoids.
func (rf *ruleFlags) parse(cmd *cobra.Command, update bool) (ruleInput, error) {
	var in ruleInput
	changed := cmd.Flags().Changed
	ref := func(flag, value string) (*string, error) {
		if !changed(flag) {
			return nil, nil
		}
		if strings.TrimSpace(value) == "" {
			if !update {
				return nil, exitcode.Newf(exitcode.Usage,
					"--%s is empty (an unset shell variable?): a new rule needs a node id or URN here — an empty value means CLEAR, and there is nothing to clear yet", flag)
			}
			empty := ""
			return &empty, nil
		}
		canon, err := canonicalRuleRef(value)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "--%s: %v", flag, err)
		}
		return &canon, nil
	}
	var err error
	if in.authorTaskRef, err = ref("author-task", rf.authorTask); err != nil {
		return in, err
	}
	if in.validationTaskRef, err = ref("validation-task", rf.validationTask); err != nil {
		return in, err
	}
	if in.descriptionNodeRef, err = ref("description-node", rf.descriptionNode); err != nil {
		return in, err
	}
	if changed("writers") {
		w, err := parseWriters(rf.writers)
		if err != nil {
			return in, err
		}
		in.writers = &w
	}
	if changed("validate-by") {
		if strings.TrimSpace(rf.validateBy) == "" {
			if !update {
				return in, exitcode.Newf(exitcode.Usage,
					"--validate-by is empty (an unset shell variable?): a new rule needs agent or platform here — an empty value means CLEAR, and there is nothing to clear yet")
			}
			in.clearValidateBy = true
		} else {
			v, err := parseValidateBy(rf.validateBy)
			if err != nil {
				return in, err
			}
			in.validateBy = &v
		}
	}
	if changed("strict-sub-roles") {
		v := rf.strictSubRoles
		in.strictSubRoles = &v
	}
	if changed("enabled") {
		v := rf.enabled
		in.enabled = &v
	}
	return in, nil
}

// canonicalRuleRef validates one task/description reference — from a flag or a
// template file — without resolving it: the SERVER resolves it, answers an
// unreadable node exactly like a missing one, and has no resolveUrn lag.
//
// A server id of either shape passes through. Rule references have no -m, so a
// colon-free token can only be an id: the CUID/loc ambiguity that keeps
// IsNodeID narrow (a bare loc composed through -m) cannot arise here, and a
// Node's id defaults to a CUID server-side. Anything else must be a
// fully-qualified node URN (cmdutil.BatchNodeRef canonicalizes it).
func canonicalRuleRef(value string) (string, error) {
	if id := strings.TrimSpace(value); cmdutil.IsEntityID(id) {
		return id, nil
	}
	return cmdutil.BatchNodeRef("", value)
}

func parseWriters(s string) (gen.NodeRoleWriters, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "all":
		return gen.NodeRoleWritersAll, nil
	case "admin":
		return gen.NodeRoleWritersAdmin, nil
	case "owner":
		return gen.NodeRoleWritersOwner, nil
	}
	return "", exitcode.Newf(exitcode.Usage, "--writers must be all, admin or owner, got %q", s)
}

func parseValidateBy(s string) (gen.ContentValidator, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "agent":
		return gen.ContentValidatorAgent, nil
	case "platform":
		return gen.ContentValidatorPlatform, nil
	}
	return "", exitcode.Newf(exitcode.Usage, "--validate-by must be agent or platform, got %q", s)
}

func newCmdConfigRuleAdd(f *cmdutil.Factory) *cobra.Command {
	var rf ruleFlags
	cmd := &cobra.Command{
		Use:   "add <memoryRef> <role> [flags]",
		Short: "Add a node-role rule to a memory's config",
		Long: `Add a rule for one role (or dotted sub-role) to a memory's config, creating
the config on first use. A role that already has a rule is refused (exit 5):
change it with "rule update" instead.

The role grammar is the server's: lower-case letter/digit/dash segments joined
by dots, 1-64 characters. A bad role exits 2 and names the reason.

Task references must be runnable task nodes you can read. A node you cannot
read is refused exactly like a missing one (exit 4).`,
		Example: `  hadron memory config rule add hrn:mem:acme.com:kb spec \
    --author-task hrn:node:acme.com:kb:tasks:write-spec \
    --validation-task hrn:node:acme.com:kb:tasks:check-spec --validate-by agent
  hadron memory config rule add hrn:mem:acme.com:kb spec.rule --writers admin --strict-sub-roles`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			in, err := rf.parse(cmd, false)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CreateNodeRoleRule(cmd.Context(), client, cmdutil.CanonicalMemoryRef(args[0]), &gen.CreateNodeRoleRuleInput{
				Role:               args[1],
				AuthorTaskRef:      in.authorTaskRef,
				ValidationTaskRef:  in.validationTaskRef,
				DescriptionNodeRef: in.descriptionNodeRef,
				Writers:            in.writers,
				ValidateBy:         in.validateBy,
				StrictSubRoles:     in.strictSubRoles,
				Enabled:            in.enabled,
			})
			if err != nil {
				return api.MapError(err)
			}
			p := resp.CreateNodeRoleRule
			if p == nil || p.Rule == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no rule for role %q", args[1])
			}
			dto := ruleWriteDTO{Rule: dtoFromRule(&p.Rule.NodeRoleRuleFields), Warnings: []configWarningDTO{}}
			for _, w := range p.Warnings {
				if w != nil {
					dto.Warnings = append(dto.Warnings, dtoFromWarning(&w.MemoryConfigWarningFields))
				}
			}
			return writeRuleResult(f, "Added", args[0], dto)
		},
	}
	rf.register(cmd, false)
	return cmd
}

func newCmdConfigRuleUpdate(f *cmdutil.Factory) *cobra.Command {
	var rf ruleFlags
	cmd := &cobra.Command{
		Use:   "update <memoryRef> <role> [flags]",
		Short: "Change a node-role rule",
		Long: `Change the rule for one role. Only the flags you give change; every other
field keeps its value. An empty task or node reference clears it, and an empty
--validate-by clears the validator.

The rule's current revision is read first and sent with the change, so a rule
someone else changed in between is refused (exit 5) instead of overwritten:
re-run to apply your change on top of theirs. Clearing --validate-by is part of
the same single update, never a separate save.

The role is matched exactly against the rules that exist: a role with no rule
exits 4 and names the roles there are. The role itself is the rule's identity
and cannot be renamed: remove the rule and add it again.`,
		Example: `  hadron memory config rule update hrn:mem:acme.com:kb spec --writers owner
  hadron memory config rule update hrn:mem:acme.com:kb spec --validation-task "" --validate-by ""`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			in, err := rf.parse(cmd, true)
			if err != nil {
				return err
			}
			if in.empty() {
				return exitcode.Newf(exitcode.Usage,
					"nothing to update: give at least one of --author-task, --validation-task, --description-node, --writers, --validate-by, --strict-sub-roles, --enabled")
			}
			rule, _, err := findRule(cmd, f, args[0], args[1])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			dto, err := updateRule(cmd, client, rule, in)
			if err != nil {
				return err
			}
			return writeRuleResult(f, "Updated", args[0], dto)
		},
	}
	rf.register(cmd, true)
	return cmd
}

// updateRule sends ONE update under the revision just read. Clearing validateBy
// needs an explicit null, which only the literal-null operation can send; it
// carries every other given field too, so a clear plus a change is still a
// single atomic update (team chat #1888), never a clear and a second save.
func updateRule(cmd *cobra.Command, client graphql.Client, rule *gen.NodeRoleRuleFields, in ruleInput) (ruleWriteDTO, error) {
	dto := ruleWriteDTO{Warnings: []configWarningDTO{}}
	if in.clearValidateBy {
		resp, err := gen.UpdateNodeRoleRuleClearingValidateBy(cmd.Context(), client, rule.Id, rule.Revision,
			in.authorTaskRef, in.validationTaskRef, in.descriptionNodeRef, in.strictSubRoles, in.writers, in.enabled)
		if err != nil {
			return dto, api.MapError(err)
		}
		p := resp.UpdateNodeRoleRule
		if p == nil || p.Rule == nil {
			return dto, exitcode.Newf(exitcode.Error, "the server returned no rule for role %q", rule.Role)
		}
		dto.Rule = dtoFromRule(&p.Rule.NodeRoleRuleFields)
		for _, w := range p.Warnings {
			if w != nil {
				dto.Warnings = append(dto.Warnings, dtoFromWarning(&w.MemoryConfigWarningFields))
			}
		}
		return dto, nil
	}
	resp, err := gen.UpdateNodeRoleRule(cmd.Context(), client, rule.Id, &gen.UpdateNodeRoleRuleInput{
		AuthorTaskRef:      in.authorTaskRef,
		ValidationTaskRef:  in.validationTaskRef,
		DescriptionNodeRef: in.descriptionNodeRef,
		Writers:            in.writers,
		ValidateBy:         in.validateBy,
		StrictSubRoles:     in.strictSubRoles,
		Enabled:            in.enabled,
	}, rule.Revision)
	if err != nil {
		return dto, api.MapError(err)
	}
	p := resp.UpdateNodeRoleRule
	if p == nil || p.Rule == nil {
		return dto, exitcode.Newf(exitcode.Error, "the server returned no rule for role %q", rule.Role)
	}
	dto.Rule = dtoFromRule(&p.Rule.NodeRoleRuleFields)
	for _, w := range p.Warnings {
		if w != nil {
			dto.Warnings = append(dto.Warnings, dtoFromWarning(&w.MemoryConfigWarningFields))
		}
	}
	return dto, nil
}

func newCmdConfigRuleRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <memoryRef> <role>",
		Aliases: []string{"remove"},
		Short:   "Remove a node-role rule",
		Long: `Remove the rule for one role from a memory's config. Prompts on a terminal;
non-interactively --yes is required.

The role is matched exactly against the rules that exist: a role with no rule
exits 4 and names the roles there are. The rule's revision is read first and
sent with the delete, so a rule someone else changed in between is refused
(exit 5) rather than removed unseen.`,
		Example: `  hadron memory config rule rm hrn:mem:acme.com:kb spec.draft --yes`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rule, memoryID, err := findRule(cmd, f, args[0], args[1])
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, fmt.Sprintf("the %q rule from memory %s", rule.Role, args[0])); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.DeleteNodeRoleRule(cmd.Context(), client, rule.Id, rule.Revision)
			if err != nil {
				return api.MapError(err)
			}
			if !resp.DeleteNodeRoleRule {
				return exitcode.Newf(exitcode.Error, "the server did not delete the %q rule", rule.Role)
			}
			dto := ruleDeleteDTO{MemoryID: memoryID, Role: rule.Role, ID: rule.Id, Revision: rule.Revision, Deleted: true}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Removed rule %s from memory %s\n", dto.Role, args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (required non-interactively)")
	return cmd
}

func writeRuleResult(f *cmdutil.Factory, verb, memoryRef string, dto ruleWriteDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "%s rule %s in memory %s (revision %d)\n\n", verb, dto.Rule.Role, memoryRef, dto.Rule.Revision); err != nil {
			return err
		}
		if err := renderRule(w, dto.Rule); err != nil {
			return err
		}
		renderWarnings(f.IOStreams, dto.Warnings)
		return nil
	})
}
