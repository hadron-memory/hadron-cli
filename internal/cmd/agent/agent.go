// Package agent implements `hadron agent ...` — agent lifecycle management.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/api/gqltypes"
	"github.com/hadron-memory/hadron-cli/internal/cmd/bootcompile"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// agentBaseDTO is every field of the stable --json agent shape except
// lifecycle, which write receipts and `agent get` represent DIFFERENTLY (omitted
// when not sent vs an explicit null) and so each declares exactly once, below.
type agentBaseDTO struct {
	ID             string   `json:"id"`
	URN            string   `json:"urn"`
	Name           string   `json:"name"`
	Description    *string  `json:"description"`
	Type           string   `json:"type"`
	Visibility     string   `json:"visibility"`
	OrganizationID *string  `json:"organizationId"`
	Surfaces       []string `json:"surfaces"`
	SystemMemoryID *string  `json:"systemMemoryId"`
	TasksIndexRef  *string  `json:"tasksIndexRef"`
	ProcessesIndex *string  `json:"processesIndexRef"`
	ExtendsAgentID *string  `json:"extendsAgentId"`
	// BootPromptRef / BootPromptTargetDeleted are the Agent's boot-prompt
	// pointer (cli#916 C2, #2045 S8). NOT omitempty: both null means unset OR
	// withheld, and the key must stay to say it.
	BootPromptRef           *string `json:"bootPromptRef"`
	BootPromptTargetDeleted *bool   `json:"bootPromptTargetDeleted"`
	AiProvider              *string `json:"aiProvider"`
	AiModel                 *string `json:"aiModel"`
	HasAiApiKey             bool    `json:"hasAiApiKey"`
	PersonaRole             *string `json:"personaRole"`
	UsesTasks               bool    `json:"usesTasks"`
	// ResetContextAfterTask is recorded only (#1551, cor:agt:050:27); a
	// host with a worker.reset-context handler acts on it (#1043).
	ResetContextAfterTask bool `json:"resetContextAfterTask"`
	// ListedOnMarketplace is the catalogue-discovery flag (cor:acl:080:04),
	// distinct from visibility: it governs discovery, never access (cli#58).
	ListedOnMarketplace bool `json:"listedOnMarketplace"`
	// RegisterDefaults is the Agent's register defaults as returned; null only
	// if the server returned none (cli#58).
	RegisterDefaults *registerDefaultsDTO `json:"registerDefaults"`
	// MemberRoles is the Agent's installationPolicy.memberRoles (#1226, R211):
	// the roles an App it is installed in may offer. [] when none came back.
	MemberRoles []string `json:"memberRoles"`
	CreatedAt   string   `json:"createdAt"`
}

// agentDTO is the --json shape of write receipts, lists and clones: lifecycle
// is present only when the server returned it (a write that sent --lifecycle),
// and omitted otherwise.
type agentDTO struct {
	agentBaseDTO
	Lifecycle *string `json:"lifecycle,omitempty"`
	// ContextCap (#2162) is present only when the server reported it: a write
	// that sent a cap flag. A number, or null when the Agent has no cap.
	ContextCap json.RawMessage `json:"contextCap,omitempty"`
}

type registerDefaultsDTO struct {
	MentionOnly bool `json:"mentionOnly"`
}

func agentDTOFromFields(a gen.AgentFields) agentDTO {
	surfaces := a.Surfaces
	if surfaces == nil {
		surfaces = []string{}
	}
	// installationPolicy is non-null on the server, but a withheld or older
	// answer must render [] rather than crash.
	roles := []string{}
	if a.InstallationPolicy != nil {
		roles = append(roles, a.InstallationPolicy.MemberRoles...)
	}
	var reg *registerDefaultsDTO
	if a.RegisterDefaults != nil {
		reg = &registerDefaultsDTO{MentionOnly: a.RegisterDefaults.MentionOnly}
	}
	return agentDTO{agentBaseDTO: agentBaseDTO{
		MemberRoles: roles, ListedOnMarketplace: a.ListedOnMarketplace, RegisterDefaults: reg,
		ID: a.Id, URN: a.Urn, Name: a.Name, Description: a.Description,
		Type: string(a.Type), Visibility: string(a.Visibility), OrganizationID: a.OrganizationId,
		Surfaces: surfaces, SystemMemoryID: a.SystemMemoryId,
		TasksIndexRef: a.TasksIndexRef, ProcessesIndex: a.ProcessesIndexRef, ExtendsAgentID: a.ExtendsAgentId,
		BootPromptRef: a.BootPromptRef, BootPromptTargetDeleted: a.BootPromptTargetDeleted,
		AiProvider: a.AiProvider, AiModel: a.AiModel, HasAiApiKey: a.HasAiApiKey,
		PersonaRole: a.PersonaRole, UsesTasks: a.UsesTasks, ResetContextAfterTask: a.ResetContextAfterTask, CreatedAt: a.CreatedAt,
	}}
}

// memberRolesArg turns --member-roles into the wire value: nil when the flag
// was not passed (the field is then omitted: create takes the server default,
// update preserves), the list verbatim when it was. An empty value is refused
// here because the server refuses [] and an empty list must never be sent.
// Role validity (format, duplicates, case, count) is the server's alone.
func memberRolesArg(cmd *cobra.Command, roles []string) ([]string, error) {
	if !cmd.Flags().Changed("member-roles") {
		return nil, nil
	}
	for _, r := range roles {
		if strings.TrimSpace(r) != "" {
			return roles, nil
		}
	}
	return nil, exitcode.Newf(exitcode.Usage,
		"--member-roles needs at least one role (comma-separated, e.g. owner,member); it was passed empty — drop the flag to keep the default (create) or the current roles (update)")
}

func dashList(items []string) string {
	if len(items) == 0 {
		return "—"
	}
	return strings.Join(items, ", ")
}

func dash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}

// parseAgentType returns nil for an unset flag (so it's omitted), else the enum.
func parseAgentType(s string) (*gen.AgentType, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ASSISTANT":
		t := gen.AgentTypeAssistant
		return &t, nil
	case "CHATBOT":
		t := gen.AgentTypeChatbot
		return &t, nil
	default:
		return nil, exitcode.Newf(exitcode.Usage, "invalid --type %q (want ASSISTANT or CHATBOT)", s)
	}
}

// ParseVisibility is exported for reuse outside this package (#405 precedent)
// — one place to change if the enum grows a member.
func ParseVisibility(s string) (*gen.AgentVisibility, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ORGANIZATION":
		v := gen.AgentVisibilityOrganization
		return &v, nil
	case "PERSONAL":
		v := gen.AgentVisibilityPersonal
		return &v, nil
	case "PUBLIC":
		v := gen.AgentVisibilityPublic
		return &v, nil
	default:
		return nil, exitcode.Newf(exitcode.Usage, "invalid --visibility %q (want ORGANIZATION, PERSONAL, or PUBLIC)", s)
	}
}

// NewCmdAgent builds the `hadron agent` command group.
func NewCmdAgent(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "agent <command>",
		Aliases: []string{"agents"},
		Short:   "Work with agents",
	}
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdSubscription(f))
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdClone(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdRm(f))
	cmd.AddCommand(newCmdAgentTasks(f))
	cmd.AddCommand(newCmdAgentTickerTemplates(f))
	cmd.AddCommand(newCmdAgentProcesses(f))
	cmd.AddCommand(newCmdAgentRevision(f), newCmdAgentLock(f))
	cmd.AddCommand(newCmdAgentMemory(f))
	cmd.AddCommand(newCmdAgentSetBase(f))
	cmd.AddCommand(newCmdAgentSetBootPrompt(f))
	cmd.AddCommand(newCmdAgentBootPrompt(f))
	cmd.AddCommand(newCmdAgentModelGroup(f))
	cmd.AddCommand(bootcompile.NewAgent(f))
	return cmd
}

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var org, typ, vis string
	var limit, offset int
	var public, ownedByMe bool
	cmd := &cobra.Command{
		Use:     "list [--org <id> | --owned-by-me] [--type <t>] [--visibility <v>] | --public [--type <t>]",
		Aliases: []string{"ls"},
		Short:   "List agents",
		Long: `List agents. By default this is the member-scoped view — agents in orgs you
belong to.

--public instead lists the cross-org marketplace slice: every live PUBLIC
agent, readable without org membership (a foreign public agent you can grab the
URN of to subscribe/install). It's a separate surface, so --org and
--visibility don't apply to it; --type still filters.

--owned-by-me narrows the member-scoped view to the agents you own outright:
your org-less ones (no organizationId), owned by you. It is the "my agents"
slice, answered by the server — the same predicate ` + "`app list --owned-by-me`" + `
and ` + "`memory list --owned-by-me`" + ` forward. Org-less by definition, so it
does not combine with --org and your active organization is not consulted; an
App-key caller gets an empty list. It also does not combine with --public,
since a PUBLIC agent is never user-owned. --type and --visibility still
narrow it.

--app does NOT narrow this listing: it is the persistent App-context flag,
not a filter, so the same rows come back for any App. For the agents
installed in one App, use ` + "`hadron app agent list <app-ref>`" + `, which reads
the AppAgent join; for its workers, use ` + "`hadron team worker list --app <ref>`" + `.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			f.NoteAppIsContextOnly("agents you can read")
			if limit < 0 || offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit and --offset must be non-negative")
			}
			if public && (org != "" || vis != "") {
				return exitcode.Newf(exitcode.Usage, "--public is the cross-org PUBLIC slice — --org and --visibility don't apply to it")
			}
			// PublicAgentFilter has no ownedByMe FIELD — the server rejects it at
			// the schema rather than returning an empty page, because a PUBLIC
			// agent is never user-owned. Refuse here so the reason names the
			// slices rather than arriving as a GraphQL validation error.
			if public && ownedByMe {
				return exitcode.Newf(exitcode.Usage, "--public is the cross-org PUBLIC slice and --owned-by-me is your own org-less one; a PUBLIC agent is never user-owned, so the two never overlap")
			}
			// The owner slice is org-less by definition (organizationId IS NULL),
			// so the server never consults orgId for it: combining them would be
			// empty by construction rather than a narrowing.
			// Changed(), not the value: `--org=` is "asked for nothing", a
			// different mistake from not asking at all, and a value test would
			// read it as the latter (review:an-empty-flag-is-not-an-absent-flag).
			if ownedByMe && cmd.Flags().Changed("org") {
				return exitcode.Newf(exitcode.Usage, "--owned-by-me lists the agents you own, which have no organization; drop --org (or drop --owned-by-me to list an organization's agents)")
			}
			at, err := parseAgentType(typ)
			if err != nil {
				return err
			}
			av, err := ParseVisibility(vis)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var lim, off *int
			if cmd.Flags().Changed("limit") {
				lim = &limit
			}
			if cmd.Flags().Changed("offset") {
				off = &offset
			}

			agents := []agentDTO{}
			if public {
				// PublicAgentFilter is deliberately narrower than AgentFilter:
				// only --type narrows the marketplace slice.
				var filter *gen.PublicAgentFilter
				if at != nil {
					filter = &gen.PublicAgentFilter{Type: at}
				}
				// No --limit/--offset means the whole list (cli#967): drain to
				// the total. An explicit flag is one deliberate page.
				rows, err := listPages(lim, off, func(l, o *int) ([]*agentRow, int, error) {
					resp, err := gen.PublicAgents(cmd.Context(), client, filter, l, o)
					if err != nil || resp.PublicAgents == nil {
						return nil, 0, err
					}
					out := make([]*agentRow, 0, len(resp.PublicAgents.Items))
					for _, a := range resp.PublicAgents.Items {
						if a != nil {
							out = append(out, &agentRow{&a.AgentFields})
						}
					}
					return out, resp.PublicAgents.Total, nil
				})
				if err != nil {
					return api.MapError(err)
				}
				for _, a := range rows {
					agents = append(agents, agentDTOFromFields(*a.AgentFields))
				}
			} else {
				var filter *gen.AgentFilter
				if at != nil || av != nil || ownedByMe {
					filter = &gen.AgentFilter{Type: at, Visibility: av}
					// Only ever set true: the clauses compose by AND, so a
					// literal false is a no-op the server still has to read,
					// and omitting it keeps an unfiltered request unchanged.
					if ownedByMe {
						filter.OwnedByMe = &ownedByMe
					}
				}
				var orgPtr *string
				if org != "" {
					orgPtr = &org
				}
				rows, err := listPages(lim, off, func(l, o *int) ([]*agentRow, int, error) {
					resp, err := gen.Agents(cmd.Context(), client, orgPtr, filter, l, o)
					if err != nil || resp.Agents == nil {
						return nil, 0, err
					}
					out := make([]*agentRow, 0, len(resp.Agents.Items))
					for _, a := range resp.Agents.Items {
						if a != nil {
							out = append(out, &agentRow{&a.AgentFields})
						}
					}
					return out, resp.Agents.Total, nil
				})
				if err != nil {
					return api.MapError(err)
				}
				for _, a := range rows {
					agents = append(agents, agentDTOFromFields(*a.AgentFields))
				}
			}
			return output.Write(f.IOStreams, f.JSON, agents, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "URN", "NAME", "TYPE", "VISIBILITY")
				for _, a := range agents {
					t.Row(output.SafeCell(a.ID), output.SafeCell(a.URN), output.SafeCell(a.Name), output.SafeCell(a.Type), output.SafeCell(a.Visibility))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "restrict to one organization (ID)")
	cmd.Flags().StringVar(&typ, "type", "", "filter by type: ASSISTANT or CHATBOT")
	cmd.Flags().StringVar(&vis, "visibility", "", "filter by visibility: ORGANIZATION, PERSONAL, or PUBLIC")
	cmd.Flags().BoolVar(&public, "public", false, "list the cross-org PUBLIC marketplace slice instead of your member-scoped agents")
	cmd.Flags().BoolVar(&ownedByMe, "owned-by-me", false, "list only the org-less agents you own")
	cmd.Flags().IntVar(&limit, "limit", 0, "max results (one page of that size); unset lists everything")
	cmd.Flags().IntVar(&offset, "offset", 0, "results to skip")
	return cmd
}

// OfferSubjectDTO is one blocking subject the server named (AgentOfferSubject); only the name is reachable.
type OfferSubjectDTO struct {
	Name string `json:"name"`
}

// OfferReasonDTO is one reason an Agent is not ready to be offered (AgentOfferReason, cor:dmo:050:19). Count is every
// blocked subject; Subjects are the ones the server named; OmittedByCap and HiddenByAccess are counts, never names.
type OfferReasonDTO struct {
	Code           string            `json:"code"`
	Count          int               `json:"count"`
	Subjects       []OfferSubjectDTO `json:"subjects"`
	OmittedByCap   int               `json:"omittedByCap"`
	HiddenByAccess int               `json:"hiddenByAccess"`
}

// OfferReadinessDTO is the server's AgentOfferReadiness. Reasons is empty exactly when Ready, and never null.
type OfferReadinessDTO struct {
	Ready   bool             `json:"ready"`
	Reasons []OfferReasonDTO `json:"reasons"`
}

// offerReadinessDTOFrom copies the server's answer, keeping null as null and every list non-null (a hostile or older
// server's null list becomes [] so --json never carries one).
func offerReadinessDTOFrom(r *gen.GetAgentAgentOfferReadiness) *OfferReadinessDTO {
	if r == nil {
		return nil
	}
	out := &OfferReadinessDTO{Ready: r.Ready, Reasons: []OfferReasonDTO{}}
	for _, reason := range r.Reasons {
		d := OfferReasonDTO{Code: string(reason.Code), Count: reason.Count, Subjects: []OfferSubjectDTO{}, OmittedByCap: reason.OmittedByCap, HiddenByAccess: reason.HiddenByAccess}
		for _, s := range reason.Subjects {
			d.Subjects = append(d.Subjects, OfferSubjectDTO{Name: s.Name})
		}
		out.Reasons = append(out.Reasons, d)
	}
	return out
}

// writeOfferReadiness prints the readiness block. A null answer says only that readiness is not available to the caller:
// it never says "ready" and names no error code (cor:dmo:050:19). Names appear only where the server named them;
// omittedByCap and hiddenByAccess are counts.
func writeOfferReadiness(w io.Writer, r *OfferReadinessDTO) {
	switch {
	case r == nil:
		fmt.Fprintf(w, "  offer readiness: not available to you\n")
	case r.Ready:
		fmt.Fprintf(w, "  offer readiness: ready\n")
	default:
		fmt.Fprintf(w, "  offer readiness: not ready\n")
	}
	if r == nil {
		return
	}
	for _, reason := range r.Reasons {
		fmt.Fprintf(w, "    %s: %d blocked", output.SafeCell(reason.Code), reason.Count)
		var parts []string
		if len(reason.Subjects) > 0 {
			names := make([]string, 0, len(reason.Subjects))
			for _, s := range reason.Subjects {
				names = append(names, output.SafeCell(s.Name))
			}
			parts = append(parts, strings.Join(names, ", "))
		}
		if reason.OmittedByCap > 0 {
			parts = append(parts, fmt.Sprintf("%d more not listed", reason.OmittedByCap))
		}
		if reason.HiddenByAccess > 0 {
			parts = append(parts, fmt.Sprintf("%d hidden", reason.HiddenByAccess))
		}
		if len(parts) > 0 {
			fmt.Fprintf(w, " (%s)", strings.Join(parts, "; "))
		}
		fmt.Fprintln(w)
	}
}

// Origin is get-only; null deliberately does not identify a concealment reason.
type agentGetDTO struct {
	// Lifecycle is `get`'s ONLY lifecycle field: an explicit null when the
	// server cannot report it (it predates #4242), never omitted.
	Lifecycle *string `json:"lifecycle"`
	// ContextCap (#2162) is a number or null when the server reports it, and
	// ABSENT when it cannot (it predates hadron-server#4258).
	ContextCap json.RawMessage `json:"contextCap,omitempty"`
	agentBaseDTO
	ClonedFromAgentRef *string `json:"clonedFromAgentRef"`
	// cor:dmo:050:19: null when the caller does not manage the Agent (cor:dmo:050:13); null is never read as ready.
	OfferReadiness *OfferReadinessDTO `json:"offerReadiness"`
	// Get-only best-effort enrichment; omit on every unavailable answer.
	SystemMemoryURN *string `json:"systemMemoryUrn,omitempty"`
}

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <ref>",
		Short: "Show an agent (by ID or URN)",
		Long: `Show one Agent by ID or URN. It only reads; nothing is changed. An Agent that
does not exist, or that you may not read, exits 4.

The text lists the Agent's name, URN and ID, type and visibility, description,
its system memory (the Memory holding its prompts and tasks, shown as a URN when
you may read it and as an ID alone when not), its tasks and processes index, the
base Agent it extends, its AI provider and model (key: true or false says
whether an API key is set), and the boot prompt it serves. It also shows how its
workers run (uses tasks, lifecycle, whether context resets after each task, the
context cap), whether it is listed on the marketplace, its member roles, and any
surfaces and persona role. A value the Agent does not have prints "-".

"offer readiness" says whether another organization could subscribe to, install
and clone this Agent as it stands: ready, or not ready with each blocking reason
and how many items it blocks. You see it only if you manage the Agent; for
anyone else it reads "not available to you".

"Cloned from" appears only for a clone whose source you may read, and shows the
source's current URN. No line does not say why: the Agent may not be a clone, or
the source may be unreadable or deleted. The lifecycle line is the setting
recorded by "hadron agent update --lifecycle" (LONG_LIVED or SHORT_LIVED); it
does not report a running process.

--json prints the same fields, with clonedFromAgentRef (null when there is no
"Cloned from" line), systemMemoryUrn (left out when the URN cannot be read),
resetContextAfterTask and lifecycle. A server from before 2026-11-15 cannot
report the lifecycle: the line says unknown and --json lifecycle is null, while
every other field still works. Any other read failure is an error.`,
		Example: `  hadron agent get hrn:agent:acme.com:support-bot --json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.GetAgent(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.Agent == nil {
				return exitcode.Newf(exitcode.NotFound, "agent %q not found", args[0])
			}
			dto := agentGetDTO{agentBaseDTO: agentDTOFromFields(resp.Agent.AgentFields).agentBaseDTO, ClonedFromAgentRef: resp.Agent.ClonedFromAgentRef, OfferReadiness: offerReadinessDTOFrom(resp.Agent.OfferReadiness)}
			life, lifeErr := gen.GetAgentLifecycle(cmd.Context(), client, resp.Agent.Id)
			if lifeErr != nil && !api.IsFrameworkValidationOnlyFor(lifeErr, "lifecycle") {
				return api.MapError(lifeErr)
			}
			if lifeErr == nil {
				if life == nil || life.Agent == nil {
					return exitcode.Newf(exitcode.Error, "server returned no agent lifecycle")
				}
				v := string(life.Agent.Lifecycle)
				dto.Lifecycle = &v
			}

			capResp, capErr := gen.GetAgentContextCap(cmd.Context(), client, resp.Agent.Id)
			if capErr != nil && !api.IsFrameworkValidationOnlyFor(capErr, "contextCap") {
				return api.MapError(capErr)
			}
			if capErr == nil && capResp != nil && capResp.Agent != nil {
				dto.ContextCap = contextCapRaw(capResp.Agent.ContextCap)
			}

			// Read through the normal memory gate using the same client/credential.
			// Never compose a URN from Agent metadata: unreadable and nonexistent
			// must have the same id-only output. Even partial data with an error
			// is unavailable, and there is no retry or alternate read surface.
			if dto.SystemMemoryID != nil && *dto.SystemMemoryID != "" {
				memory, lookupErr := gen.AgentSystemMemory(cmd.Context(), client, *dto.SystemMemoryID)
				if lookupErr == nil && memory != nil && memory.Memory != nil && memory.Memory.Id == *dto.SystemMemoryID && memory.Memory.Urn != "" {
					dto.SystemMemoryURN = &memory.Memory.Urn
				}
			}
			memoryLabel := dash(dto.SystemMemoryID)
			if dto.SystemMemoryURN != nil {
				memoryLabel = fmt.Sprintf("%s (%s)", *dto.SystemMemoryURN, *dto.SystemMemoryID)
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "%s\n  urn: %s\n  id: %s\n  type: %s   visibility: %s\n", output.SafeCell(dto.Name), output.SafeCell(dto.URN), output.SafeCell(dto.ID), output.SafeCell(dto.Type), output.SafeCell(dto.Visibility))
				fmt.Fprintf(w, "  description: %s\n  system memory: %s\n  tasks index: %s\n  processes index: %s\n  extends: %s\n  ai: %s/%s (key: %v)\n",
					output.SafeCell(dash(dto.Description)), output.SafeCell(memoryLabel), output.SafeCell(dash(dto.TasksIndexRef)), output.SafeCell(dash(dto.ProcessesIndex)), output.SafeCell(dash(dto.ExtendsAgentID)), output.SafeCell(dash(dto.AiProvider)), output.SafeCell(dash(dto.AiModel)), dto.HasAiApiKey)
				fmt.Fprintf(w, "  boot prompt: %s\n", output.SafeCell(cmdutil.BootPromptLabel(dto.BootPromptRef, dto.BootPromptTargetDeleted)))
				if dto.ClonedFromAgentRef != nil {
					fmt.Fprintf(w, "  Cloned from: %s\n", output.SafeCell(*dto.ClonedFromAgentRef))
				}
				writeOfferReadiness(w, dto.OfferReadiness)
				fmt.Fprintf(w, "  uses tasks: %t\n", dto.UsesTasks)
				lifeLabel := "unknown (server before #4242)"
				if dto.Lifecycle != nil {
					lifeLabel = *dto.Lifecycle
				}
				fmt.Fprintf(w, "  lifecycle: %s\n", output.SafeCell(lifeLabel))
				fmt.Fprintf(w, "  reset context after task: %t\n", dto.ResetContextAfterTask)
				capLabel := "unknown (server before #4258)"
				if len(dto.ContextCap) > 0 {
					capLabel = contextCapLabel(dto.ContextCap)
				}
				fmt.Fprintf(w, "  context cap: %s\n", capLabel)
				fmt.Fprintf(w, "  listed on marketplace: %t\n", dto.ListedOnMarketplace)
				if dto.RegisterDefaults != nil {
					fmt.Fprintf(w, "  register mention only: %t\n", dto.RegisterDefaults.MentionOnly)
				}
				fmt.Fprintf(w, "  member roles: %s\n", output.SafeCell(dashList(dto.MemberRoles)))
				if len(dto.Surfaces) > 0 {
					fmt.Fprintf(w, "  surfaces: %s\n", output.SafeCell(strings.Join(dto.Surfaces, ", ")))
				}
				if dto.PersonaRole != nil {
					fmt.Fprintf(w, "  persona role: %s\n", output.SafeCell(dash(dto.PersonaRole)))
				}
				return nil
			})
		},
	}
}

// The old flags remain parseable only to give scripts a local, actionable
// refusal before credentials or a request are touched. They no longer map to
// a server field and do not write a prompt.
func refuseRetiredPersonaPromptFlags(cmd *cobra.Command) error {
	if cmd.Flags().Changed("persona-prompt") || cmd.Flags().Changed("persona-prompt-file") {
		return exitcode.Newf(exitcode.Usage,
			"--persona-prompt and --persona-prompt-file were retired; write boot:source, then use hadron agent compile and hadron node approve")
	}
	return nil
}

func registerRetiredPersonaPromptFlags(cmd *cobra.Command) {
	cmd.Flags().String("persona-prompt", "", "retired; write boot:source and compile")
	cmd.Flags().String("persona-prompt-file", "", "retired; write boot:source and compile")
	_ = cmd.Flags().MarkHidden("persona-prompt")
	_ = cmd.Flags().MarkHidden("persona-prompt-file")
}

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var org, name, description, typ, vis, systemMemory string
	var personaRole, installInto, lifecycle string
	var surfaces, memberRoles []string
	var ownerMe, createUsesTasks, createReset bool
	var createContextCap int
	cmd := &cobra.Command{
		Use:   "create --name <n> [--org <id> | --owner-me] [--install-into <app>]",
		Short: "Create an agent (org-owned, or user-owned with --owner-me)",
		Long: `Create an Agent. Pass --org (an organization ID or URN, for example
hrn:org:acme.com) to create an organization-owned Agent; you must be an ADMIN of
that organization. Without --org, or with --owner-me, the Agent is yours: it
lives in your own handle's namespace (its URN is hrn:agent:<handle>:<slug>), it
is PERSONAL and visible only to you, and the server refuses any other
--visibility. --org and --owner-me exclude each other.

A new Agent has no prompt and is in no App yet:
  1. Give it a prompt: write boot:source (role boot-prompt) in the Agent's system
     memory, then use "hadron agent compile" and "hadron node approve".
  2. Put it in an App so workers can be created from it: --install-into <app>
     does this in the same run, which is the common case when the Agent is a
     role Agent for a team you already have:

  hadron agent create --org hrn:org:acme.com --name "DevOps Engineer" \
      --persona-role devops-engineer \
      --install-into hrn:app:acme.com:eng-team

--install-into is deliberately not spelled --app: --app is the persistent App
context flag, which never acts on an App ("hadron agent list --app" explains).
--install-into names a target and has an effect, so it has its own name.

The two steps are not atomic. If the install fails the Agent still exists; the
failure says so and prints the "hadron app agent add" command that finishes the
job. The Agent is never rolled back, because deleting an Agent you asked to
create would be the more destructive repair. A lost connection is reported as
unknown, not failed (exit 7): the install may have gone through, so check "hadron
app agent list" instead of retrying blind into a duplicate. Installing needs
CONTRIBUTOR or higher on the App's owning organization (or ownership of a
user-owned App), which is stricter than creating the Agent, so it can refuse
after the create succeeded.

Settings, all optional (left out, the server's default stands):
  --description, --persona-role  free text, and the role the Agent presents as.
  --surface       a surface the Agent is available on (repeatable).
  --member-roles  the roles the Agent offers an App's members, comma-separated
                  (default owner,member; the server validates them).
  --type          ASSISTANT or CHATBOT.
  --visibility    ORGANIZATION, PERSONAL or PUBLIC.
  --lifecycle     LONG_LIVED keeps workers running; SHORT_LIVED runs them only
                  while they hold a runnable task. Omitted: LONG_LIVED. A host
                  acts on it when its sweep is enabled.
  --context-cap   a context size in tokens (a whole number from 1). A
                  long-lived worker whose context grows past it, and whose host
                  reports it idle at an empty prompt, is reset, at most once an
                  hour per worker. Omitted: no cap.
  --uses-tasks, --reset-context-after-task  whether the Agent's workers use
                  assigned tasks (default true), and whether they get a fresh
                  context after each task (default false). An explicit =false
                  is sent as false. The reset is only a recorded setting: the
                  program that runs your workers (their host) does it, and only
                  a host that supports it acts on it. team host run wires the
                  protocol on Linux when its config explicitly declares
                  worker.reset-context and supplies reset_context.orca_bin, but
                  currently refuses new resets without a verifiable
                  terminal/process binding.
  --system-memory normally left out: the server gives every new Agent a system
                  memory. Pass an ID only to name one yourself.

Server compatibility: every create sends a request that carries the task
settings, so it needs a server from 2026-10-26 or later. Naming --lifecycle or
--context-cap needs a still newer server; leaving them out keeps the older
request. An older server rejects the create as a schema mismatch (exit 2):
upgrade that server first, including self-hosted or staging ones.`,
		Example: `  hadron agent create --owner-me --name "My Agent" --type ASSISTANT
  hadron agent create --org hrn:org:acme.com --name "Support Bot" --type CHATBOT --visibility ORGANIZATION
  hadron agent create --org hrn:org:acme.com --name "DevOps Engineer" --persona-role devops-engineer --install-into hrn:app:acme.com:eng-team`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseRetiredPersonaPromptFlags(cmd); err != nil {
				return err
			}
			// spec 047: an agent is owned by EXACTLY ONE of an org or the caller.
			if ownerMe && org != "" {
				return exitcode.Newf(exitcode.Usage, "--owner-me creates an agent you own in your own namespace; drop --org (or drop --owner-me to create an organization agent)")
			}
			// #543 review (@codex P2). An EXPLICIT `--install-into=` is a usage
			// error, not an absent flag. `--install-into="$APP"` with an unset
			// variable arrives here as Changed-but-empty, and the emptiness test
			// further down is the FLAG-ABSENT path — so without this the agent is
			// created, "✓ created" is printed, and the command exits 0 having
			// silently skipped the install the caller asked for. Under --json the
			// `install` key is simply omitted, which is indistinguishable from
			// never having asked.
			//
			// Refused HERE, before CreateAgent, deliberately: refusing after the
			// create would leave an orphan agent behind on a pure usage error,
			// trading a silent no-op for a messier one.
			//
			// NORMALIZED, not just validated: `--install-into=" "` is checked and
			// forwarded as the same trimmed value. Testing TrimSpace while sending
			// the raw string is the worst of both — it admits a target the server
			// then fails to resolve, in a message naming a ref nobody typed.
			installInto = strings.TrimSpace(installInto)
			if cmd.Flags().Changed("install-into") && installInto == "" {
				return exitcode.Newf(exitcode.Usage,
					"--install-into needs an App (ID or URN); it was passed empty, which usually means an unset variable — drop the flag to create an agent without installing it")
			}
			// Shape-checked for the same reason it is refused when empty: a
			// target the server cannot resolve must not cost an orphan agent.
			var err error
			if installInto, err = cmdutil.CanonicalAppRef("--install-into", installInto); err != nil {
				return err
			}
			rolesArg, err := memberRolesArg(cmd, memberRoles)
			if err != nil {
				return err
			}
			at, err := parseAgentType(typ)
			if err != nil {
				return err
			}
			av, err := ParseVisibility(vis)
			if err != nil {
				return err
			}
			lifecycleArg, err := agentLifecycleArg(cmd, lifecycle)
			if err != nil {
				return err
			}
			capArg, err := contextCapArg(cmd, createContextCap, false)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var usesTasksArg, resetArg *bool
			if cmd.Flags().Changed("uses-tasks") {
				usesTasksArg = &createUsesTasks
			}
			if cmd.Flags().Changed("reset-context-after-task") {
				resetArg = &createReset
			}
			var createdFields *gen.AgentFields
			var lifecycleValue *string
			var capValue json.RawMessage
			if capArg != nil {
				// The cap rides in its own document (#2162): the legacy ones stay
				// valid on a server older than hadron-server#4258.
				resp, err := gen.CreateAgentWithContextCap(cmd.Context(), client, name, lifecycleArg, optStr(org),
					optStr(description), at, av, optStr(systemMemory), surfaces,
					optStr(personaRole), rolesArg, usesTasksArg, resetArg, capArg)
				if err != nil {
					return api.MapError(err)
				}
				if resp.CreateAgent == nil {
					return exitcode.Newf(exitcode.Error, "server returned no agent")
				}
				createdFields = &resp.CreateAgent.AgentFields
				if lifecycleArg != nil {
					value := string(resp.CreateAgent.Lifecycle)
					lifecycleValue = &value
				}
				capValue = contextCapRaw(resp.CreateAgent.ContextCap)
			} else if lifecycleArg == nil {
				resp, err := gen.CreateAgent(cmd.Context(), client, name, optStr(org),
					optStr(description), at, av, optStr(systemMemory), surfaces,
					optStr(personaRole), rolesArg, usesTasksArg, resetArg)
				if err != nil {
					return api.MapError(err)
				}
				if resp.CreateAgent == nil {
					return exitcode.Newf(exitcode.Error, "server returned no agent")
				}
				createdFields = &resp.CreateAgent.AgentFields
			} else {
				resp, err := gen.CreateAgentWithLifecycle(cmd.Context(), client, name, lifecycleArg, optStr(org),
					optStr(description), at, av, optStr(systemMemory), surfaces,
					optStr(personaRole), rolesArg, usesTasksArg, resetArg)
				if err != nil {
					return api.MapError(err)
				}
				if resp.CreateAgent == nil {
					return exitcode.Newf(exitcode.Error, "server returned no agent")
				}
				createdFields = &resp.CreateAgent.AgentFields
				value := string(resp.CreateAgent.Lifecycle)
				lifecycleValue = &value
			}
			created := agentCreateDTO{agentDTO: agentDTOFromFields(*createdFields)}
			created.Lifecycle = lifecycleValue
			created.ContextCap = capValue

			// Reaching here with an empty installInto now means ONE thing — the
			// flag was not passed. "Asked for nothing" was refused above, so do
			// not collapse that guard into this test on the grounds that both
			// look at the same emptiness: this branch is only unambiguous
			// BECAUSE the other one runs first.
			if installInto == "" {
				// #535: "✓ created" reads as done, but the agent is inert until it
				// is in some App's cast pool. Stderr keeps the --json contract clean.
				defer f.NoteAgentNotInstalled(created.URN)
				return emitAgent(f, created.agentDTO, "✓ created")
			}

			inst, installErr := installCreatedAgent(cmd.Context(), client, installInto, created.ID)
			created.Install = inst
			// The agent exists either way, so it is always emitted — losing the URN
			// of a just-created agent is the one outcome with no cheap recovery.
			if emitErr := emitCreatedAgent(f, created); emitErr != nil {
				return emitErr
			}
			return installErr
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "owning organization (ID or URN, for example hrn:org:acme.com); omit (or use --owner-me) for a user-owned agent")
	cmd.Flags().BoolVar(&ownerMe, "owner-me", false, "create a user-owned agent in your own @handle namespace (org-less; PERSONAL only)")
	cmd.Flags().StringVar(&name, "name", "", "agent name (required)")
	cmd.Flags().StringVar(&description, "description", "", "agent description")
	cmd.Flags().StringVar(&typ, "type", "", "type: ASSISTANT or CHATBOT (server default when unset)")
	cmd.Flags().StringVar(&vis, "visibility", "", "visibility: ORGANIZATION, PERSONAL, or PUBLIC (server default when unset)")
	cmd.Flags().StringVar(&systemMemory, "system-memory", "", "an existing memory's ID to use as the Agent's system memory; normally left out (the server gives each Agent one)")
	cmd.Flags().StringArrayVar(&surfaces, "surface", nil, "surface the agent is available on (repeatable)")
	cmd.Flags().StringVar(&personaRole, "persona-role", "", "persona dressing: the role this agent presents as (metadata)")
	cmd.Flags().StringSliceVar(&memberRoles, "member-roles", nil, "roles the agent offers an App's members, comma-separated (default owner,member; the server validates them)")
	cmd.Flags().StringVar(&installInto, "install-into", "", "App (ID or URN) to install the new agent into, so it is castable in one run")
	cmd.Flags().StringVar(&lifecycle, "lifecycle", "", "worker process lifecycle: LONG_LIVED or SHORT_LIVED (omitted: LONG_LIVED; needs a newer server)")
	cmd.Flags().BoolVar(&createUsesTasks, "uses-tasks", false, "Whether workers from this Agent use assigned tasks (server default true when left out; =false is sent as false)")
	cmd.Flags().IntVar(&createContextCap, "context-cap", 0, contextCapFlagUsage)
	cmd.Flags().BoolVar(&createReset, "reset-context-after-task", false, "Record that this Agent's workers should get a fresh context after each task (server default false when left out). Recorded only: it takes effect where the workers' host supports it")
	registerRetiredPersonaPromptFlags(cmd)
	_ = cmd.MarkFlagRequired("name")
	cmdutil.RetireFlags(cmd, "an Agent's boot prompt is now a node: write the source at boot:source in its system memory (role boot-prompt, hadron node update), compile it with `hadron agent compile <agent>`, then have the Agent's manager approve the compiled node with `hadron node approve <compiled-node-ref>`",
		cmdutil.RetiredFlag{Name: "system-prompt"}, cmdutil.RetiredFlag{Name: "system-prompt-file"}, cmdutil.RetiredFlag{Name: "clear-system-prompt", Bool: true})
	return cmd
}

// agentLifecycleArg keeps omission distinct from an explicitly empty flag.
// The same enum and omission contract applies to create and update.
func agentLifecycleArg(cmd *cobra.Command, raw string) (*gen.AgentLifecycle, error) {
	if !cmd.Flags().Changed("lifecycle") {
		return nil, nil
	}
	if raw != "LONG_LIVED" && raw != "SHORT_LIVED" {
		return nil, exitcode.Newf(exitcode.Usage, "--lifecycle must be LONG_LIVED or SHORT_LIVED (omit the flag to keep the default/current value)")
	}
	value := gen.AgentLifecycle(raw)
	return &value, nil
}

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var name, description, typ, vis, systemMemory, urn string
	var personaRole, lifecycle string
	var usesTasks, resetContextAfterTask, listedOnMarketplace, registerMentionOnly, clearRegisterDefaults bool
	var tasksIndex, processesIndex string
	var updateContextCap int
	var noContextCap bool
	var surfaces, memberRoles []string
	cmd := &cobra.Command{
		Use:   "update <ref>",
		Short: "Update an agent by ID or URN (only the fields you pass change)",
		Long: `Update an Agent. Only the fields you pass change; <ref> is an Agent ID or URN.
The flags fall into these groups.

Identity and discovery:
  --name, --description, --type, --visibility, --surface, --member-roles,
  --system-memory, --persona-role (metadata: the role this Agent presents as; its
  prompt is not set here, see below), and
  --listed-on-marketplace (=false unlists it), which lists the Agent in the
  marketplace catalogue and governs discovery only: it does not change the
  Agent's visibility or who can reach it.
  --urn renames the Agent's slug, the last part of its URN. It works for
  organization-owned Agents only. Existing workers are not affected, but
  anything that named the Agent by its old URN must now use the new one; an ID
  never changes, so use the ID where a reference has to survive a rename.

The Agent's prompt: edit it by writing the source at boot:source (role
boot-prompt) in the Agent's system memory, then use "hadron agent compile" and
"hadron node approve". New Agents start without a prompt.

How its workers run:
  --lifecycle LONG_LIVED keeps workers running; SHORT_LIVED runs them only while
  they hold a runnable task. Omitted keeps the current value (the default is
  LONG_LIVED). It records desired behaviour; a host acts on it only when its
  sweep is enabled. The server checks the same permission as --uses-tasks.
  --context-cap <tokens> sets the context cap: a whole number from 1. A
  long-lived worker whose context grows past it, and whose host reports it idle
  at an empty prompt, is reset, at most once an hour per worker; a busy worker is
  never reset for it. --no-context-cap removes the cap; omitted keeps the
  current cap; not both.
  --reset-context-after-task records that the Agent's workers should get a fresh
  context after each task (default false; "hadron agent get" shows it).
  It does not reset anything by itself: the program that runs your workers
  (their host) does the reset, and only a host that supports it acts on the
  setting. team host run wires the protocol on Linux when its config
  explicitly declares worker.reset-context and supplies
  reset_context.orca_bin, but currently refuses new resets without a
  verifiable terminal/process binding. Turn it off with
  --reset-context-after-task=false. "hadron agent create" takes the same flag,
  to set it at creation.

Where it looks for its tasks and processes:
  --tasks-index and --processes-index point the Agent at the node that lists its
  tasks, or its processes ("hadron agent tasks list" and "hadron agent processes
  list" read them). Each takes a node ref; an explicit empty value clears it.
  Re-pointing an index changes which tasks or processes the Agent's workers see
  from then on; it does not edit the nodes themselves.

Register defaults: a register row is how an Agent's workers attend a chat
channel, and mention-only means they are woken only by messages that mention
them. --register-mention-only[=false] sets the default an install of this Agent
starts with; --clear-register-defaults returns the defaults to the platform's.
Pass at most one of them; left out, the defaults are unchanged. Both together,
or --clear-register-defaults=false, are refused before anything is sent.

Server compatibility: --lifecycle and --context-cap need a newer server than the
other flags. Against an older server the write is refused as a schema mismatch
(exit 2) and every other agent command is unaffected.`,
		Example: `  hadron agent update hrn:agent:acme.com:support-bot --name "Support Bot v2" --visibility PUBLIC
  hadron agent update agt_123 --description "…"
  hadron agent update agt_123 --reset-context-after-task
  hadron agent update agt_123 --listed-on-marketplace
  hadron agent update agt_123 --register-mention-only
  hadron agent update agt_123 --clear-register-defaults`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseRetiredPersonaPromptFlags(cmd); err != nil {
				return err
			}
			changed := cmd.Flags().Changed
			lifecycleArg, err := agentLifecycleArg(cmd, lifecycle)
			if err != nil {
				return err
			}
			if !changed("name") && !changed("description") && !changed("type") && !changed("visibility") &&
				!changed("system-memory") && !changed("surface") && !changed("urn") &&
				!changed("lifecycle") && !changed("persona-role") && !changed("uses-tasks") && !changed("reset-context-after-task") &&
				!changed("tasks-index") && !changed("processes-index") && !changed("member-roles") &&
				!changed("listed-on-marketplace") && !changed("register-mention-only") && !changed("clear-register-defaults") &&
				!changed("context-cap") && !changed("no-context-cap") {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
			}
			registerArg, err := registerDefaultsArg(cmd, registerMentionOnly, clearRegisterDefaults)
			if err != nil {
				return err
			}
			capArg, err := contextCapArg(cmd, updateContextCap, true)
			if err != nil {
				return err
			}
			// The server prepends the owner namespace, so --urn is the agent
			// slug — which may carry an author org or @handle atom, hence an
			// agent-context path check rather than a plain node-loc check.
			if changed("urn") {
				if err := cmdutil.ValidateAgentURNPath("--urn", urn); err != nil {
					return err
				}
			}
			rolesArg, err := memberRolesArg(cmd, memberRoles)
			if err != nil {
				return err
			}
			at, err := parseAgentType(typ)
			if err != nil {
				return err
			}
			av, err := ParseVisibility(vis)
			if err != nil {
				return err
			}
			var surfacesArg []string
			if changed("surface") {
				surfacesArg = surfaces
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var updated *gen.AgentFields
			var lifecycleValue *string
			var capValue json.RawMessage
			if changed("name") || changed("description") || changed("type") || changed("visibility") ||
				changed("system-memory") || changed("surface") || changed("urn") ||
				changed("lifecycle") || changed("persona-role") || changed("uses-tasks") || changed("reset-context-after-task") || changed("member-roles") ||
				changed("listed-on-marketplace") || registerArg != nil || capArg != nil {
				var usesTasksArg, resetArg, listedArg *bool
				if changed("uses-tasks") {
					usesTasksArg = &usesTasks
				}
				if changed("reset-context-after-task") {
					resetArg = &resetContextAfterTask
				}
				if changed("listed-on-marketplace") {
					listedArg = &listedOnMarketplace
				}
				if capArg != nil {
					resp, err := gen.UpdateAgentWithContextCap(cmd.Context(), client, args[0], lifecycleArg,
						changedStr(cmd, "name", name), changedStr(cmd, "description", description),
						at, av,
						changedStr(cmd, "system-memory", systemMemory), surfacesArg, changedStr(cmd, "urn", urn),
						changedStr(cmd, "persona-role", personaRole), usesTasksArg, resetArg, rolesArg, listedArg, registerArg, capArg)
					if err != nil {
						return api.MapError(err)
					}
					if resp.UpdateAgent == nil {
						return exitcode.Newf(exitcode.Error, "server returned no agent")
					}
					updated = &resp.UpdateAgent.AgentFields
					if lifecycleArg != nil {
						value := string(resp.UpdateAgent.Lifecycle)
						lifecycleValue = &value
					}
					capValue = contextCapRaw(resp.UpdateAgent.ContextCap)
				} else if lifecycleArg == nil {
					resp, err := gen.UpdateAgent(cmd.Context(), client, args[0],
						changedStr(cmd, "name", name), changedStr(cmd, "description", description),
						at, av,
						changedStr(cmd, "system-memory", systemMemory), surfacesArg, changedStr(cmd, "urn", urn),
						changedStr(cmd, "persona-role", personaRole), usesTasksArg, resetArg, rolesArg, listedArg, registerArg)
					if err != nil {
						return api.MapError(err)
					}
					if resp.UpdateAgent == nil {
						return exitcode.Newf(exitcode.Error, "server returned no agent")
					}
					updated = &resp.UpdateAgent.AgentFields
				} else {
					resp, err := gen.UpdateAgentWithLifecycle(cmd.Context(), client, args[0], lifecycleArg,
						changedStr(cmd, "name", name), changedStr(cmd, "description", description),
						at, av,
						changedStr(cmd, "system-memory", systemMemory), surfacesArg, changedStr(cmd, "urn", urn),
						changedStr(cmd, "persona-role", personaRole), usesTasksArg, resetArg, rolesArg, listedArg, registerArg)
					if err != nil {
						return api.MapError(err)
					}
					if resp.UpdateAgent == nil {
						return exitcode.Newf(exitcode.Error, "server returned no agent")
					}
					updated = &resp.UpdateAgent.AgentFields
					value := string(resp.UpdateAgent.Lifecycle)
					lifecycleValue = &value
				}
			}
			// The two index refs are their own mutation (setAgentIndexRefs):
			// a value sets, an explicit empty clears, absent preserves.
			fields, err := applyAgentIndexRefChanges(cmd.Context(), client, args[0],
				changed("tasks-index"), tasksIndex, changed("processes-index"), processesIndex)
			if err != nil {
				return err
			}
			if fields == nil {
				fields = updated
			}
			if fields == nil {
				return exitcode.Newf(exitcode.Error, "server returned no agent")
			}
			dto := agentDTOFromFields(*fields)
			dto.Lifecycle = lifecycleValue
			dto.ContextCap = capValue
			return emitAgent(f, dto, "✓ updated")
		},
	}
	cmd.Flags().StringVar(&lifecycle, "lifecycle", "", "worker process lifecycle: LONG_LIVED or SHORT_LIVED (omitted keeps the current value; needs a newer server)")
	cmd.Flags().StringVar(&name, "name", "", "agent name")
	cmd.Flags().StringVar(&description, "description", "", "agent description")
	cmd.Flags().StringVar(&typ, "type", "", "type: ASSISTANT or CHATBOT")
	cmd.Flags().StringVar(&vis, "visibility", "", "visibility: ORGANIZATION, PERSONAL, or PUBLIC")
	cmd.Flags().StringVar(&systemMemory, "system-memory", "", "system memory ID")
	cmd.Flags().StringArrayVar(&surfaces, "surface", nil, "surface the agent is available on (repeatable; replaces the set)")
	cmd.Flags().StringVar(&urn, "urn", "", "rename the Agent's slug (the last part of its URN); organization-owned Agents only")
	cmd.Flags().StringVar(&personaRole, "persona-role", "", "persona dressing: the role this agent presents as (metadata)")
	cmd.Flags().StringSliceVar(&memberRoles, "member-roles", nil, "replace the roles the agent offers an App's members, comma-separated (existing members keep theirs; the server validates)")
	cmd.Flags().StringVar(&tasksIndex, "tasks-index", "", `the Agent's tasks index node (a ref; an explicit empty clears it)`)
	cmd.Flags().StringVar(&processesIndex, "processes-index", "", `the Agent's processes index node (a ref; an explicit empty clears it)`)
	cmd.Flags().BoolVar(&usesTasks, "uses-tasks", false, "Whether workers from this Agent use assigned tasks; false keeps them expected to check the team channel without a task (omitted preserves)")
	cmd.Flags().BoolVar(&resetContextAfterTask, "reset-context-after-task", false, "Record that this Agent's workers should get a fresh context after each task (default false; =false clears it; omitted keeps the current value). Recorded only: it takes effect where the workers' host supports it")
	cmd.Flags().IntVar(&updateContextCap, "context-cap", 0, contextCapFlagUsage+" (omitted keeps the current cap)")
	cmd.Flags().BoolVar(&noContextCap, "no-context-cap", false, "remove the Agent's context cap (no cap; omitted keeps the current cap). Not with --context-cap")
	cmd.Flags().BoolVar(&listedOnMarketplace, "listed-on-marketplace", false, "list the Agent in the marketplace catalogue (=false unlists it; omitted keeps the current value). Catalogue discovery only: it does not change visibility or who can reach the Agent")
	cmd.Flags().BoolVar(&registerMentionOnly, "register-mention-only", false, "set the Agent's register default mentionOnly to this value (=false sets false; omitted keeps the current defaults). Not with --clear-register-defaults")
	cmd.Flags().BoolVar(&clearRegisterDefaults, "clear-register-defaults", false, "return the Agent's register defaults to the platform defaults (omitted keeps the current defaults). Not with --register-mention-only")
	registerRetiredPersonaPromptFlags(cmd)
	cmdutil.RetireFlags(cmd, "an Agent's boot prompt is now a node: write the source at boot:source in its system memory (role boot-prompt, hadron node update), compile it with `hadron agent compile <agent>`, then have the Agent's manager approve the compiled node with `hadron node approve <compiled-node-ref>`",
		cmdutil.RetiredFlag{Name: "system-prompt"}, cmdutil.RetiredFlag{Name: "system-prompt-file"}, cmdutil.RetiredFlag{Name: "clear-system-prompt", Bool: true})
	return cmd
}

// registerDefaultsArg turns --register-mention-only / --clear-register-defaults
// into the three-state wire value (cli#58 slice 2): nil when neither flag was
// passed (the variable is omitted and the server PRESERVES the defaults), an
// object for --register-mention-only[=bool], an explicit null for
// --clear-register-defaults. Omission is never turned into a clear. Both
// together, and --clear-register-defaults=false, are usage errors here, before
// any request: "false" on a clear is not a choice, and a clear next to a value
// is ambiguous.
func registerDefaultsArg(cmd *cobra.Command, mentionOnly, clear bool) (*gqltypes.RegisterDefaultsInput, error) {
	moSet, clSet := cmd.Flags().Changed("register-mention-only"), cmd.Flags().Changed("clear-register-defaults")
	if moSet && clSet {
		return nil, exitcode.Newf(exitcode.Usage, "--register-mention-only and --clear-register-defaults cannot be combined: set a value or clear the defaults, never both")
	}
	if clSet {
		if !clear {
			return nil, exitcode.Newf(exitcode.Usage, "--clear-register-defaults=false is not a choice: pass --clear-register-defaults to clear the register defaults, or leave the flag out to keep them")
		}
		return &gqltypes.RegisterDefaultsInput{Clear: true}, nil
	}
	if moSet {
		v := mentionOnly
		return &gqltypes.RegisterDefaultsInput{MentionOnly: &v}, nil
	}
	return nil, nil
}

func newCmdRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <ref>",
		Aliases: []string{"delete"},
		Short:   "Delete an Agent and its system memory",
		Long: `Delete an Agent and its system memory. This is a soft delete, and the CLI has
no command to bring the Agent back. <ref> is an Agent ID or URN.

You need management access to the Agent. The server refuses while the Agent is
still in use: an App has it installed (see "hadron app uninstall" and "hadron
app agent remove"), another Agent imports it, or another Agent extends it as its
base. Memories attached to the Agent ("hadron agent memory add") do not block
the delete, and only the attachment goes with the Agent. The delete is also
refused (DECISION_CONTAINED) while the Agent's system memory holds a decision or
a decision log, and a server that must keep minted content may add its own
restrictions.

This removes the whole Agent. To detach one Memory from it, use "hadron agent
memory rm"; to take the Agent out of one App, use "hadron app agent remove".

Asks for confirmation on a terminal; a non-interactive caller must pass --yes.
Prints the result; --json is {"id": <ref>, "status": "deleted"}.`,
		Example: `  hadron agent rm hrn:agent:acme.com:support-bot --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "agent "+args[0]); err != nil {
				return err
			}
			resp, err := gen.DeleteAgent(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if !resp.DeleteAgent {
				return exitcode.Newf(exitcode.Error, "agent %s was not deleted", args[0])
			}
			dto := map[string]string{"id": args[0], "status": "deleted"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ deleted agent %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func emitAgent(f *cmdutil.Factory, dto agentDTO, verb string) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "%s agent %s (%s)\n", verb, dto.Name, dto.URN); err != nil {
			return err
		}
		if err := writeLifecycleLine(w, dto.Lifecycle); err != nil {
			return err
		}
		return writeContextCapLine(w, dto.ContextCap)
	})
}

// writeLifecycleLine confirms the lifecycle the SERVER returned for a write that
// sent --lifecycle, in the same words `agent get` uses. A write that did not
// send it (the legacy document, nothing returned) prints nothing: the receipt
// never claims a lifecycle the server did not say (cli#2114).
func writeLifecycleLine(w io.Writer, lifecycle *string) error {
	if lifecycle == nil {
		return nil
	}
	_, err := fmt.Fprintf(w, "  lifecycle: %s\n", output.SafeCell(*lifecycle))
	return err
}

const contextCapFlagUsage = "context size in tokens, a whole number from 1, above which a long-lived worker of this Agent that its host reports idle at an empty prompt is reset (at most once an hour per worker)"

// contextCapArg turns --context-cap / --no-context-cap into the three-state wire
// value (#2162): nil when neither was passed (the variable is omitted: preserved
// on update, no cap on create), the number for --context-cap, an explicit null
// for --no-context-cap (update only). Both together, a cap below 1, and
// --no-context-cap=false are usage errors here, before any request.
func contextCapArg(cmd *cobra.Command, tokens int, allowClear bool) (*gqltypes.ContextCapInput, error) {
	setFlag := cmd.Flags().Changed("context-cap")
	clearFlag := allowClear && cmd.Flags().Changed("no-context-cap")
	if setFlag && clearFlag {
		return nil, exitcode.Newf(exitcode.Usage, "--context-cap and --no-context-cap cannot be combined: set a cap or remove it, never both")
	}
	if clearFlag {
		if v, _ := cmd.Flags().GetBool("no-context-cap"); !v {
			return nil, exitcode.Newf(exitcode.Usage, "--no-context-cap=false is not a choice: pass --no-context-cap to remove the cap, or leave the flag out to keep it")
		}
		return &gqltypes.ContextCapInput{Clear: true}, nil
	}
	if setFlag {
		if tokens < 1 {
			return nil, exitcode.Newf(exitcode.Usage, "--context-cap takes a whole number of tokens from 1, not %d", tokens)
		}
		return &gqltypes.ContextCapInput{Tokens: tokens}, nil
	}
	return nil, nil
}

// contextCapRaw is the cap the server returned, as the --json value: a number,
// or null when the Agent has none.
func contextCapRaw(c *int) json.RawMessage {
	if c == nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(strconv.Itoa(*c))
}

// contextCapLabel is the cap in words, as `agent get` and the write receipts say it.
func contextCapLabel(raw json.RawMessage) string {
	if string(raw) == "null" {
		return "none"
	}
	return string(raw) + " tokens"
}

// writeContextCapLine confirms the cap the SERVER returned for a write that sent
// a cap flag; a write that sent none prints nothing about it.
func writeContextCapLine(w io.Writer, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	_, err := fmt.Fprintf(w, "  context cap: %s\n", contextCapLabel(raw))
	return err
}

// agentInstallDTO reports the --install-into outcome.
//
// Status is "installed", "failed", or "unknown" — the last for exit-code 7
// (#394), where the request never reached the server or its answer never came
// back, so whether the install committed is genuinely not known. Error carries
// the server's sentence so a --json caller can branch without parsing stderr.
//
// AppRef is what was sent — the caller's --install-into after
// cmdutil.CanonicalAppRef, so an App ID verbatim or the canonical
// hrn:app:<root>:<slug> whatever spelling was typed (#540); appId/appUrn are
// the server's resolved values and are therefore present only on success.
// Putting the ref in appUrn would let that field carry a non-URN (an id),
// which is worse than omitting it.
type agentInstallDTO struct {
	AppRef string `json:"appRef"`
	AppID  string `json:"appId,omitempty"`
	AppURN string `json:"appUrn,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// installStatusFor distinguishes a refusal from an unknown outcome. Only
// exitcode.Unavailable means the mutation may in fact have committed.
func installStatusFor(err error) string {
	if exitcode.FromError(err) == exitcode.Unavailable {
		return "unknown"
	}
	return "failed"
}

// agentCreateDTO is `agent create`'s --json shape: the agent, plus an install
// report ONLY when --install-into was passed. The embedded agentDTO inlines its
// fields, and the omitempty pointer means a plain create emits byte-identical
// JSON to before — the existing contract is extended, never changed.
type agentCreateDTO struct {
	agentDTO
	Install *agentInstallDTO `json:"install,omitempty"`
}

// installCreatedAgent runs the second half of `agent create --install-into`.
// It always returns a DTO — a failed install is a reported outcome, not a
// missing one — alongside the error that should become the exit code.
func installCreatedAgent(ctx context.Context, client graphql.Client, appRef, agentID string) (*agentInstallDTO, error) {
	resp, err := gen.InstallAgentIntoApp(ctx, client, appRef, agentID, nil)
	if err != nil {
		mapped := cmdutil.InstallForbiddenGuidance(err)
		incomplete := installIncompleteError(mapped, appRef, agentID)
		// #681 (PR #683 review, @copilot): `app agent add` fails the same way
		// until the MCP-only key is replaced, so it is the SECOND step here, not
		// the finishing one. MapError has already named the credential fix.
		if api.OAuthScopeRefusal(err) != "" {
			incomplete = exitcode.Newf(exitcode.FromError(mapped),
				"agent was CREATED but NOT installed into %s: %v\n"+
					"The agent exists — do not re-run `agent create`, it would make a second one.\n"+
					"Once the key is replaced, finish with: hadron app agent add %s %s",
				appRef, mapped, appRef, agentID)
		}
		return &agentInstallDTO{AppRef: appRef, Status: installStatusFor(mapped), Error: mapped.Error()}, incomplete
	}
	if resp.InstallAgentIntoApp == nil || resp.InstallAgentIntoApp.AppAgent == nil {
		err := exitcode.Newf(exitcode.Error, "server returned no AppAgent row")
		return &agentInstallDTO{AppRef: appRef, Status: "failed", Error: err.Error()},
			installIncompleteError(err, appRef, agentID)
	}
	dto := &agentInstallDTO{AppRef: appRef, Status: "installed"}
	if app := resp.InstallAgentIntoApp.AppAgent.App; app != nil {
		dto.AppID, dto.AppURN = app.Id, app.Urn
	}
	return dto, nil
}

// installIncompleteError states plainly that the create SUCCEEDED, and names the
// right next move for the two very different failures underneath.
//
// Without it, the natural reading of a failed `agent create` is that nothing was
// created, and the user's next move is to re-run it — making a second agent.
//
// The two cases must not be collapsed. A refusal is definite: the install did
// NOT happen, so `app agent add` finishes the job. Exit-code 7 (#394) is not a
// refusal — the request may have reached the server and committed — so telling
// the caller to run `app agent add` would invite a Conflict (exit 5, "duplicate
// install") on an install that actually succeeded. There, the honest
// instruction is to look before acting.
func installIncompleteError(cause error, appRef, agentID string) error {
	if exitcode.FromError(cause) == exitcode.Unavailable {
		return exitcode.Newf(exitcode.Unavailable,
			"agent was CREATED; whether it was installed into %s is UNKNOWN: %v\n"+
				"The agent exists — do not re-run `agent create`, it would make a second one.\n"+
				"Check before retrying: hadron app agent list %s\n"+
				"If it is not there: hadron app agent add %s %s",
			appRef, cause, appRef, appRef, agentID)
	}
	return exitcode.Newf(exitcode.FromError(cause),
		"agent was CREATED but NOT installed into %s: %v\n"+
			"The agent exists — do not re-run `agent create`, it would make a second one.\n"+
			"Finish with: hadron app agent add %s %s",
		appRef, cause, appRef, agentID)
}

// emitCreatedAgent renders the create+install result: the agent line first
// (so the URN is on screen even when the install failed), then the install.
func emitCreatedAgent(f *cmdutil.Factory, dto agentCreateDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "✓ created agent %s (%s)\n", dto.Name, dto.URN); err != nil {
			return err
		}
		if err := writeLifecycleLine(w, dto.Lifecycle); err != nil {
			return err
		}
		if err := writeContextCapLine(w, dto.ContextCap); err != nil {
			return err
		}
		if dto.Install == nil || dto.Install.Status != "installed" {
			// A failed or unknown install is reported by the returned error, which
			// carries the remedy; repeating it here would say it twice.
			return nil
		}
		_, err := fmt.Fprintf(w, "✓ installed into %s\n", dto.Install.AppURN)
		return err
	})
}

// optStr returns a pointer for a non-empty value, else nil (omitted on the wire).
func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// changedStr returns a pointer only when the flag was explicitly set, so an
// unset flag is omitted (preserve) while an explicit "" is sent (clear).
func changedStr(cmd *cobra.Command, flag, val string) *string {
	if cmd.Flags().Changed(flag) {
		return &val
	}
	return nil
}

// ---- #874: the Agent's two index refs (cor:dmo:050:15 / :16) --------------

// inheritedFromDTO names the base a listed node came through.
type inheritedFromDTO struct {
	AgentID  string `json:"agentId"`
	AgentUrn string `json:"agentUrn"`
}

// listedNodeDTO is the node half of a list row, in the server's own shape.
type listedNodeDTO struct {
	ID            string            `json:"id"`
	URN           string            `json:"urn"`
	Name          string            `json:"name"`
	InheritedFrom *inheritedFromDTO `json:"inheritedFrom"`
}

// listedTaskDTO is one `agent tasks list` row: the server shape unchanged.
// matchedBy appears only on a --symptom search (the unfiltered list's JSON is
// unchanged) and carries the server's VECTOR/KEYWORD/BOTH as given.
type listedTaskDTO struct {
	Node      listedNodeDTO `json:"node"`
	Trigger   *string       `json:"trigger"`
	MatchedBy string        `json:"matchedBy,omitempty"`
}

// listedProcessDTO is one `agent processes list` row; kind keeps the server's
// enum in --json and renders lower-case in the table.
type listedProcessDTO struct {
	Node    listedNodeDTO `json:"node"`
	Kind    string        `json:"kind"`
	Trigger *string       `json:"trigger"`
}

// inheritedCell renders the base for a table cell: the base Agent's URN when
// the node came through inheritance, blank for the Agent's own.
func inheritedCell(n listedNodeDTO) string {
	if n.InheritedFrom != nil {
		return n.InheritedFrom.AgentUrn
	}
	return ""
}

// triggerCell renders an index edge's name, blank when no edge lists the row.
func triggerCell(t *string) string {
	if t == nil {
		return ""
	}
	return *t
}

func newCmdAgentTasks(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tasks <command>",
		Short: "The tasks the Agent's workers see",
	}
	cmd.AddCommand(newCmdAgentTasksList(f))
	return cmd
}

func newCmdAgentTasksList(f *cmdutil.Factory) *cobra.Command {
	var (
		symptomFlag string
		limitFlag   int
	)
	cmd := &cobra.Command{
		Use:     "list <agentRef>",
		Aliases: []string{"ls"},
		Short:   "List the tasks the Agent's workers see (own list plus inherited)",
		Long: `List the tasks the Agent's workers see: the tasks the Agent lists itself plus
the ones it inherits from its base Agents (the Agents it extends, a chain of up
to four; see "hadron agent set-base"), with duplicates removed by the server.
The Agent lists its own tasks in its tasks index, a node in its system memory
that you point it at with "hadron agent update --tasks-index".

Columns: NAME and URN identify the task node. TRIGGER is the name of the entry in
the tasks index that lists the task (blank when no entry does). INHERITED FROM
names the base Agent a task comes from (blank for the Agent's own tasks).

--symptom is experimental and may change: it returns up to 3 tasks from the
Agent's effective system memory, matched on the abstract against the symptom
text (the effective memory is the Agent's own plus its base Agents'), and adds
a MATCHED BY column (VECTOR, KEYWORD or BOTH).
--limit applies only with --symptom (without it the flag is ignored and the
request is unchanged); the server owns its rules (default 3, larger values clamp
to 3, below 1 is refused) and the CLI sends the value as given.`,
		Example: `  hadron agent tasks list hrn:agent:acme.com:support-bot
  hadron agent tasks list hrn:agent:acme.com:support-bot --symptom "build fails on windows" --limit 2`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return exitcode.Newf(exitcode.Usage, "the agent reference is empty")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// --symptom is a separate document on the same server field, so a
			// plain list never sends the new arguments to an older server. The
			// limit is sent as given (cor:api:240:01): the server validates it.
			searching := cmd.Flags().Changed("symptom")
			rows := []listedTaskDTO{}
			if searching {
				var limit *int
				if cmd.Flags().Changed("limit") {
					limit = &limitFlag
				}
				resp, err := gen.AgentTaskSymptomSearch(cmd.Context(), client, ref, symptomFlag, limit)
				if err != nil {
					return api.MapError(err)
				}
				for _, row := range resp.AgentTaskList {
					dto := listedTaskDTO{
						Node: listedNodeDTO{
							ID:            row.Node.Id,
							URN:           row.Node.Urn,
							Name:          row.Node.Name,
							InheritedFrom: symptomInheritedFromDTO(row.Node.InheritedFrom),
						},
						Trigger: row.Trigger,
					}
					if row.MatchedBy != nil {
						dto.MatchedBy = string(*row.MatchedBy)
					}
					rows = append(rows, dto)
				}
			} else {
				resp, err := gen.AgentTaskIndexList(cmd.Context(), client, ref)
				if err != nil {
					return api.MapError(err)
				}
				for _, row := range resp.AgentTaskList {
					rows = append(rows, listedTaskDTO{
						Node: listedNodeDTO{
							ID:            row.Node.Id,
							URN:           row.Node.Urn,
							Name:          row.Node.Name,
							InheritedFrom: taskInheritedFromDTO(row.Node.InheritedFrom),
						},
						Trigger: row.Trigger,
					})
				}
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				if !searching {
					t := output.NewTable(w, "TRIGGER", "NAME", "URN", "INHERITED FROM")
					for _, row := range rows {
						t.Row(output.SafeCell(triggerCell(row.Trigger)), output.SafeCell(row.Node.Name), output.SafeCell(row.Node.URN), output.SafeCell(inheritedCell(row.Node)))
					}
					return t.Flush()
				}
				t := output.NewTable(w, "TRIGGER", "NAME", "URN", "INHERITED FROM", "MATCHED BY")
				for _, row := range rows {
					t.Row(output.SafeCell(triggerCell(row.Trigger)), output.SafeCell(row.Node.Name), output.SafeCell(row.Node.URN), output.SafeCell(inheritedCell(row.Node)), output.SafeCell(row.MatchedBy))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&symptomFlag, "symptom", "", "experimental: find tasks matching this symptom (up to 3, matched on the abstract)")
	cmd.Flags().IntVar(&limitFlag, "limit", 0, "with --symptom only: how many results to ask for (server default and maximum 3); ignored without --symptom")
	return cmd
}

func newCmdAgentProcesses(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "processes <command>",
		Short: "The processes the Agent's workers see",
	}
	cmd.AddCommand(newCmdAgentProcessesList(f))
	return cmd
}

func newCmdAgentProcessesList(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list <agentRef>",
		Aliases: []string{"ls"},
		Short:   "List the processes the Agent's workers see (own list plus inherited)",
		Long: `List the processes the Agent's workers see: the processes the Agent starts
plus the ones it inherits from its base Agent (the Agent it extends; see "hadron
agent set-base"), with duplicates removed by the server. A process is a
multi-step workflow written as nodes in a memory; the Agent lists the processes
it starts in its processes index, a node in its system memory that you point it
at with "hadron agent update --processes-index".

Columns: KIND says why the process is listed: STARTS (the Agent's processes index
lists it, so this Agent starts it) or INVOLVED (one of the Agent's tasks is a
step of the process, though the Agent does not start it). TRIGGER is the name of
the index entry that lists the process (blank for INVOLVED rows). INHERITED FROM
names the base Agent a row comes from, where it applies.`,
		Example: `  hadron agent processes list hrn:agent:acme.com:support-bot
  hadron agent processes list hrn:agent:acme.com:support-bot --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return exitcode.Newf(exitcode.Usage, "the agent reference is empty")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AgentProcessIndexList(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			rows := make([]listedProcessDTO, 0, len(resp.AgentProcessList))
			for _, row := range resp.AgentProcessList {
				rows = append(rows, listedProcessDTO{
					Node: listedNodeDTO{
						ID:            row.Node.Id,
						URN:           row.Node.Urn,
						Name:          row.Node.Name,
						InheritedFrom: processInheritedFromDTO(row.Node.InheritedFrom),
					},
					Kind:    string(row.Kind),
					Trigger: row.Trigger,
				})
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				t := output.NewTable(w, "TRIGGER", "KIND", "NAME", "URN", "INHERITED FROM")
				for _, row := range rows {
					t.Row(output.SafeCell(triggerCell(row.Trigger)), output.SafeCell(strings.ToLower(row.Kind)), output.SafeCell(row.Node.Name), output.SafeCell(row.Node.URN), output.SafeCell(inheritedCell(row.Node)))
				}
				return t.Flush()
			})
		},
	}
	return cmd
}

// The generated row types are deeply nested; alias the ones reused here.
type taskListedInherited = gen.AgentTaskIndexListAgentTaskListAgentListedTaskNodeInheritedFrom
type symptomListedInherited = gen.AgentTaskSymptomSearchAgentTaskListAgentListedTaskNodeInheritedFrom
type processListedInherited = gen.AgentProcessIndexListAgentProcessListAgentListedProcessNodeInheritedFrom

func taskInheritedFromDTO(inherited *taskListedInherited) *inheritedFromDTO {
	if inherited == nil {
		return nil
	}
	return &inheritedFromDTO{AgentID: inherited.AgentId, AgentUrn: inherited.AgentUrn}
}

func symptomInheritedFromDTO(inherited *symptomListedInherited) *inheritedFromDTO {
	if inherited == nil {
		return nil
	}
	return &inheritedFromDTO{AgentID: inherited.AgentId, AgentUrn: inherited.AgentUrn}
}

func processInheritedFromDTO(inherited *processListedInherited) *inheritedFromDTO {
	if inherited == nil {
		return nil
	}
	return &inheritedFromDTO{AgentID: inherited.AgentId, AgentUrn: inherited.AgentUrn}
}

// applyAgentIndexRefChanges applies --tasks-index/--processes-index: a value
// SETS the ref, an explicit empty CLEARS it, an absent flag PRESERVES it —
// the server's three-state contract (an omitted argument preserves, an
// explicit null clears). Value flags share one call; each cleared ref takes
// its own clear call, which preserves the other by omitting it.
func applyAgentIndexRefChanges(ctx context.Context, client graphql.Client, ref string, tasksChanged bool, tasksIndex string, processesChanged bool, processesIndex string) (*gen.AgentFields, error) {
	var tasksVal, processesVal *string
	if tasksChanged && tasksIndex != "" {
		t := tasksIndex
		tasksVal = &t
	}
	if processesChanged && processesIndex != "" {
		p := processesIndex
		processesVal = &p
	}
	var fields *gen.AgentFields
	if tasksVal != nil || processesVal != nil {
		resp, err := gen.SetAgentIndexRefs(ctx, client, ref, tasksVal, processesVal)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp.SetAgentIndexRefs != nil {
			fields = &resp.SetAgentIndexRefs.AgentFields
		}
	}
	if tasksChanged && tasksIndex == "" {
		resp, err := gen.ClearAgentTasksIndex(ctx, client, ref, nil)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp.SetAgentIndexRefs != nil {
			fields = &resp.SetAgentIndexRefs.AgentFields
		}
	}
	if processesChanged && processesIndex == "" {
		resp, err := gen.ClearAgentProcessesIndex(ctx, client, ref, nil)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp.SetAgentIndexRefs != nil {
			fields = &resp.SetAgentIndexRefs.AgentFields
		}
	}
	return fields, nil
}

// newCmdAgentSetBase backs `hadron agent set-base` (#881, cor:dmo:050:14):
// a positional base sets the Agent's base; --clear removes it. Exactly one of
// the two is required — the command is deliberately explicit, because
// inheritance changes what every worker of the Agent sees.
func newCmdAgentSetBase(f *cmdutil.Factory) *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "set-base <agent> (<base> | --clear)",
		Short: "Set or clear the Agent this one builds on (its base)",
		Long: `Set the base this Agent extends, or clear it with --clear. The base's
system memory is inherited: its tasks and processes lists, and the nodes its
workers read, come through the chain (deduped by the server), and a chain may
hold at most four Agents. Setting a base needs a license to it — a PUBLIC
base needs an active subscription, an org-owned one an org grant. Whoever
manages the Agent may set its base.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return exitcode.Newf(exitcode.Usage, "the agent reference is empty")
			}
			var baseRef *string
			switch {
			case clear && len(args) == 2:
				return exitcode.Newf(exitcode.Usage, "pass either a base or --clear, not both")
			case clear:
				// An omitted baseRef is the server's clear.
			case len(args) == 2:
				b := strings.TrimSpace(args[1])
				if b == "" {
					return exitcode.Newf(exitcode.Usage, "the base reference is empty")
				}
				baseRef = &b
			default:
				return exitcode.Newf(exitcode.Usage, "pass the base to extend, or --clear to remove it")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SetAgentBase(cmd.Context(), client, ref, baseRef)
			if err != nil {
				return api.MapError(err)
			}
			if resp.SetAgentBase == nil {
				return exitcode.Newf(exitcode.Error, "server returned no agent")
			}
			verb := "✓ set base of"
			if clear {
				verb = "✓ cleared base of"
			}
			return emitAgent(f, agentDTOFromFields(resp.SetAgentBase.AgentFields), verb)
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the Agent's base instead of setting one")
	return cmd
}

// newCmdAgentSetBootPrompt backs `hadron agent set-boot-prompt` (cli#916 C2,
// #2045 S8): point the Agent at a compiled boot prompt in its own system
// memory, or clear the pointer with --clear. One mutation, nodeRef always on
// the wire (an explicit null clears). The manager acts as themselves (R23),
// so the write never carries a bound worktree's session.
func newCmdAgentSetBootPrompt(f *cmdutil.Factory) *cobra.Command {
	var clear bool
	var memory string
	cmd := &cobra.Command{
		Use:   "set-boot-prompt <agent> (<node-ref> | --clear)",
		Short: "Point the Agent at a compiled boot prompt, or clear the pointer",
		Long: `Set the Agent's boot-prompt pointer to a compiled boot prompt, or remove it
with --clear. The node must be a compiled boot prompt in this Agent's own system
memory; the server checks that, and the CLI looks nothing up first. Setting the
pointer does not approve the prompt: approval is checked when it is served.

<node-ref> is a node id or a fully-qualified node URN, or a bare loc with
-m/--memory. Only the Agent's manager may set it, acting as themselves: the
command never sends a bound worktree's worker session, and the server refuses
one (exit 8).

Exit codes: 2 for a node that is not a compiled prompt, has the wrong role or
is in the wrong memory; 4 when the node is missing or not readable (one answer
for both); 8 when you may not manage the Agent.`,
		Example: `  hadron agent set-boot-prompt hrn:agent:acme.com:iris hrn:node:acme.com:iris-system:boot:compiled
  hadron agent set-boot-prompt hrn:agent:acme.com:iris boot:compiled -m hrn:mem:acme.com:iris-system
  hadron agent set-boot-prompt hrn:agent:acme.com:iris --clear`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return exitcode.Newf(exitcode.Usage, "the agent reference is empty")
			}
			nodeRef, err := cmdutil.BootPromptNodeRef(memory, args[1:], clear)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SetAgentBootPrompt(cmd.Context(), client, ref, nodeRef)
			if err != nil {
				return api.MapError(err)
			}
			if resp.SetAgentBootPrompt == nil {
				return exitcode.Newf(exitcode.Error, "server returned no agent")
			}
			dto := agentDTOFromFields(resp.SetAgentBootPrompt.AgentFields)
			verb := "✓ set boot prompt of"
			if clear {
				verb = "✓ cleared boot prompt of"
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "%s agent %s (%s)\n  boot prompt: %s\n", verb, dto.Name, dto.URN, cmdutil.BootPromptLabel(dto.BootPromptRef, dto.BootPromptTargetDeleted))
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the Agent's boot-prompt pointer instead of setting one")
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory of a bare <node-ref> loc (hrn:mem:<root>:<slug>)")
	return cmd
}

// agentRow carries the shared AgentFields of one list item.
type agentRow struct{ *gen.AgentFields }

// listPages runs one deliberate page when the user passed --limit/--offset,
// and otherwise drains the whole list (cli#967): an unset limit never means
// "whatever the server defaults to".
func listPages(lim, off *int, fetch func(limit, offset *int) ([]*agentRow, int, error)) ([]*agentRow, error) {
	if lim != nil || off != nil {
		rows, _, err := fetch(lim, off)
		return rows, err
	}
	return api.CollectAll(func(l, o int) ([]*agentRow, int, error) { return fetch(&l, &o) })
}
