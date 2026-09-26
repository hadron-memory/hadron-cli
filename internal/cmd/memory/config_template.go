package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// `hadron memory config template …` — memory-config TEMPLATES
// (hadron-server#1325 part c, cli#716 slice 2): a reusable rulebook with
// exactly one owner, COPIED into a memory's config when applied (#1334, a
// later slice — nothing here applies a template or mocks applying one).
//
// Read and managed only by the owner's managers (H4). For anyone else a
// template does not exist, so a refusal is exit 4, never "no templates".

// templateRuleDTO is one rule of a template: a node-role rule without the
// config-only fields (id, revision, locked, provenance). References follow
// nodeRoleRuleDTO's contract: a URN, an id and a state, the URN and id null
// unless OK, and an OK URN null when a legacy memory URN cannot address it.
type templateRuleDTO struct {
	Role                 string  `json:"role"`
	Enabled              bool    `json:"enabled"`
	StrictSubRoles       bool    `json:"strictSubRoles"`
	Writers              string  `json:"writers"`
	ValidateBy           *string `json:"validateBy"`
	AuthorTask           *string `json:"authorTask"`
	AuthorTaskID         *string `json:"authorTaskId"`
	AuthorTaskState      string  `json:"authorTaskState"`
	ValidationTask       *string `json:"validationTask"`
	ValidationTaskID     *string `json:"validationTaskId"`
	ValidationTaskState  string  `json:"validationTaskState"`
	DescriptionNode      *string `json:"descriptionNode"`
	DescriptionNodeID    *string `json:"descriptionNodeId"`
	DescriptionNodeState string  `json:"descriptionNodeState"`
}

// templateDTO is the stable --json shape of a template. It is also the shape
// `template create|update --file` reads back, so `template get --json`
// round-trips: see templateFile.
type templateDTO struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description *string           `json:"description"`
	OwnerType   string            `json:"ownerType"`
	OwnerID     *string           `json:"ownerId"`
	Required    bool              `json:"required"`
	Revision    int               `json:"revision"`
	Rules       []templateRuleDTO `json:"rules"`
	CreatedAt   string            `json:"createdAt"`
	CreatedBy   *string           `json:"createdBy"`
	UpdatedAt   *string           `json:"updatedAt"`
	UpdatedBy   *string           `json:"updatedBy"`
}

type templateListDTO struct {
	Items []templateDTO `json:"items"`
	Total int           `json:"total"`
}

type templateWriteDTO struct {
	Template templateDTO        `json:"template"`
	Warnings []configWarningDTO `json:"warnings"`
}

type templateDeleteDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision int    `json:"revision"`
	Deleted  bool   `json:"deleted"`
}

func dtoFromTemplate(t *gen.MemoryConfigTemplateFields) templateDTO {
	dto := templateDTO{
		ID: t.Id, Name: t.Name, Description: t.Description, OwnerType: string(t.OwnerType), OwnerID: t.OwnerId,
		Required: t.Required, Revision: t.Revision, Rules: []templateRuleDTO{},
		CreatedAt: t.CreatedAt, CreatedBy: t.CreatedBy, UpdatedAt: t.UpdatedAt, UpdatedBy: t.UpdatedBy,
	}
	for _, r := range t.Rules {
		if r == nil {
			continue
		}
		rd := templateRuleDTO{
			Role: r.Role, Enabled: r.Enabled, StrictSubRoles: r.StrictSubRoles, Writers: string(r.Writers),
			AuthorTask: r.AuthorTask, AuthorTaskID: r.AuthorTaskId, AuthorTaskState: string(r.AuthorTaskState),
			ValidationTask: r.ValidationTask, ValidationTaskID: r.ValidationTaskId, ValidationTaskState: string(r.ValidationTaskState),
			DescriptionNode: r.DescriptionNode, DescriptionNodeID: r.DescriptionNodeId, DescriptionNodeState: string(r.DescriptionNodeState),
		}
		if r.ValidateBy != nil {
			v := string(*r.ValidateBy)
			rd.ValidateBy = &v
		}
		dto.Rules = append(dto.Rules, rd)
	}
	return dto
}

func warningsFrom[W interface {
	GetCode() string
	GetRole() string
	GetField() *string
	GetTaskUrn() *string
	GetTaskState() *gen.NodeRoleRuleRefState
}](ws []W) []configWarningDTO {
	out := []configWarningDTO{}
	for _, w := range ws {
		f := gen.MemoryConfigWarningFields{Code: w.GetCode(), Role: w.GetRole(), Field: w.GetField(), TaskUrn: w.GetTaskUrn(), TaskState: w.GetTaskState()}
		out = append(out, dtoFromWarning(&f))
	}
	return out
}

func newCmdConfigTemplate(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template <command>",
		Short: "Manage memory-config templates: reusable, owned rulebooks",
		Long: `A template is a reusable rulebook with exactly one owner — the Hadron server, an
organization, a user or an App. Applying one COPIES its rules into a memory's
config; it is never a live link.

Only the owner's managers can see or change a template: a platform admin (server),
an org ADMIN/OWNER (organization), the user themselves (user), the App's owner or
an org ADMIN/OWNER (App). For anyone else it does not exist (exit 4).

Templates are addressed by id; "template list" shows them.`,
	}
	cmd.AddCommand(newCmdTemplateList(f))
	cmd.AddCommand(newCmdTemplateGet(f))
	cmd.AddCommand(newCmdTemplateCreate(f))
	cmd.AddCommand(newCmdTemplateUpdate(f))
	cmd.AddCommand(newCmdTemplateRm(f))
	return cmd
}

// ownerFlags are the template owner selectors, shared by create (exactly one)
// and list (at most one). Presence comes from Changed, so an EMPTY --owner-org
// (an unset shell variable) is refused rather than read as "no owner".
type ownerFlags struct {
	server bool
	me     bool
	org    string
	app    string
}

func (o *ownerFlags) register(cmd *cobra.Command, verb string) {
	cmd.Flags().BoolVar(&o.server, "owner-server", false, verb+" the Hadron server (platform admins only)")
	cmd.Flags().BoolVar(&o.me, "owner-me", false, verb+" you")
	cmd.Flags().StringVar(&o.org, "owner-org", "", verb+" this organization (ID or URN)")
	cmd.Flags().StringVar(&o.app, "owner-app", "", verb+" this App (ID or URN)")
}

const ownerFlagList = "--owner-server, --owner-org, --owner-me or --owner-app"

// resolve returns the owner type and the ownerRef to send. required is true for
// create. A server- or user-owned template sends NO ref: the server resolves
// its own row, and a user owns templates for themselves only.
func (o *ownerFlags) resolve(cmd *cobra.Command, required bool) (*gen.MemoryConfigTemplateOwnerType, *string, error) {
	changed := cmd.Flags().Changed
	var chosen []string
	var ownerType gen.MemoryConfigTemplateOwnerType
	var ref *string
	if changed("owner-server") && o.server {
		chosen = append(chosen, "--owner-server")
		ownerType = gen.MemoryConfigTemplateOwnerTypeHadronServer
	}
	if changed("owner-me") && o.me {
		chosen = append(chosen, "--owner-me")
		ownerType = gen.MemoryConfigTemplateOwnerTypeUser
	}
	if changed("owner-org") {
		v := strings.TrimSpace(o.org)
		if v == "" {
			return nil, nil, exitcode.Newf(exitcode.Usage, "--owner-org is empty (an unset shell variable?): pass an organization ID or URN")
		}
		chosen = append(chosen, "--owner-org")
		ownerType, ref = gen.MemoryConfigTemplateOwnerTypeOrganization, &v
	}
	if changed("owner-app") {
		// CanonicalAppRef reads an exact "" as "no App", which here would send
		// ownerType APP with an empty ref — refuse it as --owner-org does.
		if strings.TrimSpace(o.app) == "" {
			return nil, nil, exitcode.Newf(exitcode.Usage, "--owner-app is empty (an unset shell variable?): pass an App ID or URN")
		}
		canon, err := cmdutil.CanonicalAppRef("--owner-app", o.app)
		if err != nil {
			return nil, nil, err
		}
		chosen = append(chosen, "--owner-app")
		ownerType, ref = gen.MemoryConfigTemplateOwnerTypeApp, &canon
	}
	switch {
	case len(chosen) > 1:
		return nil, nil, exitcode.Newf(exitcode.Usage,
			"a template has exactly one owner — pass one of %s, got %s", ownerFlagList, strings.Join(chosen, ", "))
	case len(chosen) == 0 && required:
		return nil, nil, exitcode.Newf(exitcode.Usage, "a template has exactly one owner — pass one of %s", ownerFlagList)
	case len(chosen) == 0:
		return nil, nil, nil
	}
	return &ownerType, ref, nil
}

func newCmdTemplateList(f *cmdutil.Factory) *cobra.Command {
	var owner ownerFlags
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the templates you manage",
		Long: `List the templates you manage, name-ascending — every page, not just the first.
Narrow by owner with at most one of --owner-server, --owner-org, --owner-me or
--owner-app; an owner you do not manage lists nothing.`,
		Example: `  hadron memory config template list
  hadron memory config template list --owner-org acme.com --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ownerType, ref, err := owner.resolve(cmd, false)
			if err != nil {
				return err
			}
			var filter *gen.MemoryConfigTemplateFilter
			if ownerType != nil {
				filter = &gen.MemoryConfigTemplateFilter{OwnerType: ownerType, OwnerRef: ref}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			dto, err := listAllTemplates(cmd, client, filter)
			if err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.Items) == 0 {
					_, err := fmt.Fprintln(w, "no templates you manage")
					return err
				}
				t := output.NewTable(w, "ID", "NAME", "OWNER", "REQUIRED", "RULES", "REVISION")
				for _, it := range dto.Items {
					t.Row(it.ID, it.Name, ownerCell(it), yesNo(it.Required), fmt.Sprint(len(it.Rules)), fmt.Sprint(it.Revision))
				}
				return t.Flush()
			})
		},
	}
	owner.register(cmd, "only templates owned by")
	return cmd
}

type templateListItem = gen.MemoryConfigTemplatesMemoryConfigTemplatesMemoryConfigTemplatesPageItemsMemoryConfigTemplate

// listAllTemplates pages to exhaustion, at the server's cap (api.PageLimit,
// cor:api:120): the contract is "the templates you manage", not "the first page
// of them". It stops at `total`, and ALSO on an empty page — a shrinking set
// must end the loop, never spin it — in which case what was read is what exists
// now.
func listAllTemplates(cmd *cobra.Command, client graphql.Client, filter *gen.MemoryConfigTemplateFilter) (templateListDTO, error) {
	dto := templateListDTO{Items: []templateDTO{}}
	// api.CollectAll advances by the rows actually SERVED, so a short page (a
	// lower enforced cap, a concurrent delete) never skips the rows after it.
	items, err := api.CollectAll(func(limit, offset int) ([]*templateListItem, int, error) {
		resp, err := gen.MemoryConfigTemplates(cmd.Context(), client, filter, &limit, &offset)
		if err != nil {
			return nil, 0, api.MapError(err)
		}
		page := resp.MemoryConfigTemplates
		if page == nil {
			return nil, 0, exitcode.Newf(exitcode.Error, "the server returned no template page")
		}
		dto.Total = page.Total
		return page.Items, page.Total, nil
	})
	if err != nil {
		return dto, err
	}
	for _, it := range items {
		if it != nil {
			dto.Items = append(dto.Items, dtoFromTemplate(&it.MemoryConfigTemplateFields))
		}
	}
	return dto, nil
}

func ownerCell(t templateDTO) string {
	if t.OwnerID == nil {
		return t.OwnerType
	}
	return t.OwnerType + " " + *t.OwnerID
}

// fetchTemplate reads one template you manage, or the concealed not-found.
func fetchTemplate(cmd *cobra.Command, client graphql.Client, ref string) (*gen.MemoryConfigTemplateFields, error) {
	resp, err := gen.MemoryConfigTemplate(cmd.Context(), client, strings.TrimSpace(ref))
	if err != nil {
		return nil, api.MapError(err)
	}
	if resp.MemoryConfigTemplate == nil {
		return nil, exitcode.Newf(exitcode.NotFound,
			"no template %q that you manage — templates are addressed by id; `hadron memory config template list` shows yours", ref)
	}
	return &resp.MemoryConfigTemplate.MemoryConfigTemplateFields, nil
}

func newCmdTemplateGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <templateId>",
		Short: "Show a template and its rules",
		Long: `Show a template you manage and its rules. Its --json is the file format
"template update --file" reads, revision included — so edit it and send it back.`,
		Example: `  hadron memory config template get tmpl_123
  hadron memory config template get tmpl_123 --json > spec-template.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			t, err := fetchTemplate(cmd, client, args[0])
			if err != nil {
				return err
			}
			dto := dtoFromTemplate(t)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return renderTemplate(w, dto)
			})
		},
	}
}

func renderTemplate(w io.Writer, t templateDTO) error {
	description := "—"
	if t.Description != nil {
		description = *t.Description
	}
	if _, err := fmt.Fprintf(w, "template %s  (%s, revision %d)\n", t.Name, t.ID, t.Revision); err != nil {
		return err
	}
	for _, row := range [][2]string{
		{"owner", ownerCell(t)},
		{"required", yesNo(t.Required)},
		{"description", description},
		{"rules", fmt.Sprint(len(t.Rules))},
	} {
		if _, err := fmt.Fprintf(w, "  %-17s %s\n", row[0], row[1]); err != nil {
			return err
		}
	}
	for _, r := range t.Rules {
		validateBy := "—"
		if r.ValidateBy != nil {
			validateBy = *r.ValidateBy
		}
		if _, err := fmt.Fprintf(w, "\n%s\n", r.Role); err != nil {
			return err
		}
		for _, row := range [][2]string{
			{"enabled", yesNo(r.Enabled)},
			{"strict sub-roles", yesNo(r.StrictSubRoles)},
			{"writers", r.Writers},
			{"validate by", validateBy},
			{"author task", refCell(r.AuthorTask, r.AuthorTaskID, r.AuthorTaskState, "authorTask")},
			{"validation task", refCell(r.ValidationTask, r.ValidationTaskID, r.ValidationTaskState, "validationTask")},
			{"description node", refCell(r.DescriptionNode, r.DescriptionNodeID, r.DescriptionNodeState, "descriptionNode")},
		} {
			if _, err := fmt.Fprintf(w, "  %-17s %s\n", row[0], row[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// templateFile is what --file carries. It is `template get --json`'s shape, so
// a read → edit → update round-trips: the server-owned keys (id, ownerType,
// ownerId, created*, updated*) are ACCEPTED so that output can be fed back,
// and never sent; every other unknown key is refused, so a typo fails loudly
// instead of silently doing nothing.
//
// A key's PRESENCE is what the file says: an omitted key is unchanged on
// update, and `description: null` clears it (the server's own contract).
type templateFile struct {
	Name        *string             `json:"name"`
	Description json.RawMessage     `json:"description"`
	Required    *bool               `json:"required"`
	Rules       *[]templateFileRule `json:"rules"`
	Revision    *int                `json:"revision"`

	ID        json.RawMessage `json:"id"`
	OwnerType json.RawMessage `json:"ownerType"`
	OwnerID   json.RawMessage `json:"ownerId"`
	CreatedAt json.RawMessage `json:"createdAt"`
	CreatedBy json.RawMessage `json:"createdBy"`
	UpdatedAt json.RawMessage `json:"updatedAt"`
	UpdatedBy json.RawMessage `json:"updatedBy"`
}

type templateFileRule struct {
	Role                 *string `json:"role"`
	Enabled              *bool   `json:"enabled"`
	StrictSubRoles       *bool   `json:"strictSubRoles"`
	Writers              *string `json:"writers"`
	ValidateBy           *string `json:"validateBy"`
	AuthorTask           *string `json:"authorTask"`
	AuthorTaskID         *string `json:"authorTaskId"`
	AuthorTaskState      *string `json:"authorTaskState"`
	ValidationTask       *string `json:"validationTask"`
	ValidationTaskID     *string `json:"validationTaskId"`
	ValidationTaskState  *string `json:"validationTaskState"`
	DescriptionNode      *string `json:"descriptionNode"`
	DescriptionNodeID    *string `json:"descriptionNodeId"`
	DescriptionNodeState *string `json:"descriptionNodeState"`
}

// descriptionState is the tri-state of the file's description key.
type descriptionState int

const (
	descriptionAbsent descriptionState = iota
	descriptionClear
	descriptionSet
)

func (tf *templateFile) description() (descriptionState, *string, error) {
	raw := bytes.TrimSpace(tf.Description)
	if len(raw) == 0 {
		return descriptionAbsent, nil, nil
	}
	if bytes.Equal(raw, []byte("null")) {
		return descriptionClear, nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, nil, exitcode.Newf(exitcode.Usage, "--file: description must be a string or null")
	}
	return descriptionSet, &s, nil
}

func readTemplateFile(f *cmdutil.Factory, path string) (*templateFile, error) {
	var data []byte
	if path == "-" {
		s, err := cmdutil.ReadDocumentStdin(f.IOStreams.In, f.IOStreams.IsInputTerminal(), "--file -", "--file <path>")
		if err != nil {
			return nil, err
		}
		data = []byte(s)
	} else {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "--file: %v", err)
		}
		data = b
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var tf templateFile
	if err := dec.Decode(&tf); err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "--file must be a template as JSON (the shape `template get --json` prints): %v", err)
	}
	// Decode stops after the first value, so `{"name":"a"}{"requird":true}`
	// would apply the first object and ignore the rest — the typo the strict
	// key check exists to catch, one object later.
	if cmdutil.HasTrailingJSON(dec) {
		return nil, exitcode.Newf(exitcode.Usage, "--file must hold exactly ONE template object; it has content after it")
	}
	if err := refuseNulls(data); err != nil {
		return nil, err
	}
	return &tf, nil
}

// Keys `template get --json` never prints as null. encoding/json reads an
// explicit null as ABSENT, which on `update` means "unchanged" for a top-level
// key and, because rules are replaced wholesale, the server's CREATION DEFAULT
// for a rule field: `"writers": null` would quietly turn an ADMIN rule into
// ALL. A null here is a hand edit, so it is refused rather than guessed at.
// Nullable keys (description, validateBy, reference URNs and ids, the
// server-owned keys) are not listed: null is a value they really print.
var (
	nonNullTemplateKeys = []string{"name", "required", "rules", "revision"}
	nonNullRuleKeys     = []string{"role", "enabled", "strictSubRoles", "writers",
		"authorTaskState", "validationTaskState", "descriptionNodeState"}
)

// refuseNulls runs after the strict decode has accepted the file's shape, so
// the re-decode below cannot fail on anything the first one allowed.
func refuseNulls(data []byte) error {
	isNull := func(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return exitcode.Newf(exitcode.Usage, "--file must be a template as JSON: %v", err)
	}
	for _, k := range nonNullTemplateKeys {
		if isNull(top[k]) {
			return exitcode.Newf(exitcode.Usage,
				"--file: %q is null, which `template get --json` never prints — give its value, or remove the key to leave it unchanged", k)
		}
	}
	var rules []map[string]json.RawMessage
	if raw, ok := top["rules"]; ok {
		if err := json.Unmarshal(raw, &rules); err != nil {
			return exitcode.Newf(exitcode.Usage, "--file: rules must be a list of rule objects: %v", err)
		}
	}
	for i, r := range rules {
		for _, k := range nonNullRuleKeys {
			if isNull(r[k]) {
				return exitcode.Newf(exitcode.Usage,
					"--file: rules[%d].%s is null, which `template get --json` never prints — give its value, "+
						"or remove the key (a rule is replaced whole, so an omitted field takes the server's default; "+
						"an omitted *State drops that reference)", i, k)
			}
		}
	}
	return nil
}

// rulesInput converts the file's rules. A reference is taken from its URN, or
// else its id (an OK reference whose memory's legacy URN cannot address it).
// A reference the file only knows as BROKEN or UNREADABLE — the server withheld
// which node it is — is REFUSED: sending nothing would silently drop it from
// the template, which is the stale-snapshot overwrite #716 forbids.
func (tf *templateFile) rulesInput() ([]*gen.CreateNodeRoleRuleInput, error) {
	if tf.Rules == nil {
		return nil, nil
	}
	// NON-nil even when empty: `"rules": []` removes every rule, and must reach
	// the wire as [] — nil would be sent as null, which the server reads as
	// "unchanged".
	out := make([]*gen.CreateNodeRoleRuleInput, 0, len(*tf.Rules))
	for i, r := range *tf.Rules {
		if r.Role == nil {
			return nil, exitcode.Newf(exitcode.Usage, "--file: rules[%d] has no role", i)
		}
		in := &gen.CreateNodeRoleRuleInput{Role: *r.Role, Enabled: r.Enabled, StrictSubRoles: r.StrictSubRoles}
		var err error
		if in.AuthorTaskRef, err = fileRef(i, *r.Role, "authorTask", r.AuthorTask, r.AuthorTaskID, r.AuthorTaskState); err != nil {
			return nil, err
		}
		if in.ValidationTaskRef, err = fileRef(i, *r.Role, "validationTask", r.ValidationTask, r.ValidationTaskID, r.ValidationTaskState); err != nil {
			return nil, err
		}
		if in.DescriptionNodeRef, err = fileRef(i, *r.Role, "descriptionNode", r.DescriptionNode, r.DescriptionNodeID, r.DescriptionNodeState); err != nil {
			return nil, err
		}
		if r.Writers != nil {
			w, err := parseWriters(*r.Writers)
			if err != nil {
				return nil, exitcode.Newf(exitcode.Usage, "--file: rules[%d] (%s): writers must be all, admin or owner, got %q", i, *r.Role, *r.Writers)
			}
			in.Writers = &w
		}
		if r.ValidateBy != nil {
			v, err := parseValidateBy(*r.ValidateBy)
			if err != nil {
				return nil, exitcode.Newf(exitcode.Usage, "--file: rules[%d] (%s): validateBy must be agent, platform or null, got %q", i, *r.Role, *r.ValidateBy)
			}
			in.ValidateBy = &v
		}
		out = append(out, in)
	}
	return out, nil
}

// fileRef reads one rule reference from the file. The STATE is validated
// strictly, since `update` replaces every rule and the state itself is never
// sent: a state the file cannot back with a reference would silently DROP it.
//
//   - no state key, or NONE: no reference unless the file gives one;
//   - OK: the file must give the URN or the id (it did, when `get` printed it);
//   - BROKEN / UNREADABLE: the server withheld which node it is, so the file
//     cannot name it — refused unless the caller has set a new reference, which
//     is the documented remedy;
//   - anything else (a typo, `ok`, `UNREADBLE`) is refused outright. An explicit
//     null never reaches here: refuseNulls rejects it when the file is read.
func fileRef(i int, role, key string, urn, id, state *string) (*string, error) {
	pick := func(v *string) string {
		if v == nil {
			return ""
		}
		return strings.TrimSpace(*v)
	}
	s := pick(state)
	switch s {
	case "", "NONE", "OK", "BROKEN", "UNREADABLE":
	default:
		return nil, exitcode.Newf(exitcode.Usage,
			"--file: rules[%d] (%s): %sState %q is not a reference state — expected NONE, OK, BROKEN or UNREADABLE", i, role, key, s)
	}
	value := pick(urn)
	if value == "" {
		value = pick(id)
	}
	if value == "" {
		switch s {
		case "OK":
			return nil, exitcode.Newf(exitcode.Usage,
				"--file: rules[%d] (%s): %s is OK but the file gives neither its URN nor its id — "+
					"restore %s or %sId, or remove %sState to drop the reference", i, role, key, key, key, key)
		case "BROKEN", "UNREADABLE":
			return nil, exitcode.Newf(exitcode.Usage,
				"--file: rules[%d] (%s): %s is %s, so the file does not say which node it is (the server withholds it) — "+
					"set %s to a node id or URN, or remove %sState to drop the reference", i, role, key, s, key, key)
		}
		return nil, nil
	}
	canon, err := canonicalRuleRef(value)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "--file: rules[%d] (%s): %s: %v", i, role, key, err)
	}
	return &canon, nil
}

func newCmdTemplateCreate(f *cmdutil.Factory) *cobra.Command {
	var owner ownerFlags
	var file string
	cmd := &cobra.Command{
		Use:   "create --file <path> (--owner-server | --owner-org <ref> | --owner-me | --owner-app <ref>)",
		Short: "Create a template from a file",
		Long: `Create a template owned by exactly one owner. Its name, description, required
flag and rules come from --file (JSON; "-" reads stdin): the shape "template get
--json" prints, so an existing template's output can be fed back to copy it.
The server-owned keys it carries (id, revision, ownerType, …) are ignored.

A name is unique per owner (exit 5 when taken). Rules are validated as a whole
before anything is written; a role listed twice is exit 5.`,
		Example: `  hadron memory config template create --owner-org acme.com --file spec-template.json
  hadron memory config template get tmpl_123 --json | hadron memory config template create --owner-me --file -`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ownerType, ref, err := owner.resolve(cmd, true)
			if err != nil {
				return err
			}
			tf, err := readTemplateFile(f, file)
			if err != nil {
				return err
			}
			if tf.Name == nil || strings.TrimSpace(*tf.Name) == "" {
				return exitcode.Newf(exitcode.Usage, "--file: a template needs a name")
			}
			state, description, err := tf.description()
			if err != nil {
				return err
			}
			rules, err := tf.rulesInput()
			if err != nil {
				return err
			}
			input := &gen.CreateMemoryConfigTemplateInput{Name: *tf.Name, Required: tf.Required, Rules: rules}
			if state == descriptionSet {
				input.Description = description
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CreateMemoryConfigTemplate(cmd.Context(), client, *ownerType, ref, input)
			if err != nil {
				return api.MapError(err)
			}
			p := resp.CreateMemoryConfigTemplate
			if p == nil || p.Template == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no template")
			}
			return writeTemplateResult(f, "Created", templateWriteDTO{
				Template: dtoFromTemplate(&p.Template.MemoryConfigTemplateFields), Warnings: warningsFrom(p.Warnings)})
		},
	}
	owner.register(cmd, "owned by")
	cmd.Flags().StringVar(&file, "file", "", `the template as JSON; "-" reads stdin (required)`)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newCmdTemplateUpdate(f *cmdutil.Factory) *cobra.Command {
	var file string
	var expected int
	cmd := &cobra.Command{
		Use:   "update <templateId> --file <path> [--expected-revision <n>]",
		Short: "Change a template from a file, guarded by the revision it was read at",
		Long: `Change a template from --file (JSON; "-" reads stdin), the shape "template get
--json" prints. Every key the file HAS is applied; a key it omits is unchanged;
"description": null clears the description; "rules", when present, REPLACES all
of the template's rules.

The change is guarded by the revision the FILE was derived from — its "revision"
key, or --expected-revision — never by a fresh read: a template someone changed
since you read it is refused (exit 5) instead of overwritten by your stale copy.
Read it again, re-apply your edit, and retry.`,
		Example: `  hadron memory config template get tmpl_123 --json > t.json   # edit t.json
  hadron memory config template update tmpl_123 --file t.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			tf, err := readTemplateFile(f, file)
			if err != nil {
				return err
			}
			// A file read from ANOTHER template must not be applied to this one.
			if raw := bytes.TrimSpace(tf.ID); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
				var fileID string
				if err := json.Unmarshal(raw, &fileID); err != nil || fileID != id {
					return exitcode.Newf(exitcode.Usage,
						"--file describes template %s, not %s — refusing to apply one template's file to another", string(raw), id)
				}
			}
			revision, err := expectedRevision(cmd, tf, expected)
			if err != nil {
				return err
			}
			state, description, err := tf.description()
			if err != nil {
				return err
			}
			rules, err := tf.rulesInput()
			if err != nil {
				return err
			}
			if tf.Name == nil && state == descriptionAbsent && tf.Required == nil && tf.Rules == nil {
				return exitcode.Newf(exitcode.Usage, "--file changes nothing: give at least one of name, description, required, rules")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			dto, err := updateTemplate(cmd, client, id, revision, tf, state, description, rules)
			if err != nil {
				return err
			}
			return writeTemplateResult(f, "Updated", dto)
		},
	}
	cmd.Flags().StringVar(&file, "file", "", `the template as JSON; "-" reads stdin (required)`)
	cmd.Flags().IntVar(&expected, "expected-revision", 0, `the revision the file was derived from, when it has no "revision" key`)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

// expectedRevision is the revision the FILE was derived from. It is never
// fetched fresh: a fresh read would certify a stale file as current, which is
// exactly the overwrite the guard exists to refuse.
func expectedRevision(cmd *cobra.Command, tf *templateFile, flag int) (int, error) {
	fromFlag := cmd.Flags().Changed("expected-revision")
	switch {
	case tf.Revision != nil && fromFlag && *tf.Revision != flag:
		return 0, exitcode.Newf(exitcode.Usage,
			`--expected-revision %d disagrees with the file's "revision": %d — pass one, or make them match`, flag, *tf.Revision)
	case tf.Revision != nil:
		return *tf.Revision, nil
	case fromFlag:
		return flag, nil
	}
	return 0, exitcode.Newf(exitcode.Usage,
		`update needs the revision the file was derived from: "template get <id> --json" carries it as "revision", or pass --expected-revision`)
}

// updateTemplate sends ONE update under the file's revision. Clearing the
// description needs an explicit null, which only the literal-null operation can
// send; it carries every other given field too, so it stays a single update.
func updateTemplate(cmd *cobra.Command, client graphql.Client, id string, revision int, tf *templateFile,
	state descriptionState, description *string, rules []*gen.CreateNodeRoleRuleInput) (templateWriteDTO, error) {
	if state == descriptionClear {
		resp, err := gen.UpdateMemoryConfigTemplateClearingDescription(cmd.Context(), client, id, revision, tf.Name, tf.Required, rules)
		if err != nil {
			return templateWriteDTO{}, api.MapError(err)
		}
		p := resp.UpdateMemoryConfigTemplate
		if p == nil || p.Template == nil {
			return templateWriteDTO{}, exitcode.Newf(exitcode.Error, "the server returned no template")
		}
		return templateWriteDTO{Template: dtoFromTemplate(&p.Template.MemoryConfigTemplateFields), Warnings: warningsFrom(p.Warnings)}, nil
	}
	input := &gen.UpdateMemoryConfigTemplateInput{Name: tf.Name, Required: tf.Required, Rules: rules}
	if state == descriptionSet {
		input.Description = description
	}
	resp, err := gen.UpdateMemoryConfigTemplate(cmd.Context(), client, id, input, revision)
	if err != nil {
		return templateWriteDTO{}, api.MapError(err)
	}
	p := resp.UpdateMemoryConfigTemplate
	if p == nil || p.Template == nil {
		return templateWriteDTO{}, exitcode.Newf(exitcode.Error, "the server returned no template")
	}
	return templateWriteDTO{Template: dtoFromTemplate(&p.Template.MemoryConfigTemplateFields), Warnings: warningsFrom(p.Warnings)}, nil
}

func newCmdTemplateRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <templateId>",
		Aliases: []string{"remove"},
		Short:   "Delete a template",
		Long: `Delete a template you manage. Configs it was applied to keep their copies of its
rules. Prompts on a terminal; non-interactively --yes is required.

The template's revision is read first and sent with the delete, so a template
someone else changed in between is refused (exit 5) rather than removed unseen.`,
		Example: `  hadron memory config template rm tmpl_123 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			t, err := fetchTemplate(cmd, client, args[0])
			if err != nil {
				return err
			}
			// Confirm, not ConfirmDeletion: the server's delete is SOFT, so
			// "cannot be undone" would overstate it — but there is also no
			// command that restores one, and the prompt says both
			// (review:confirm-prompt-tells-the-truth).
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf(
				"Delete template %q (%s)? Configs it was applied to keep their copies; no command restores a deleted template.", t.Name, t.Id)); err != nil {
				return err
			}
			resp, err := gen.DeleteMemoryConfigTemplate(cmd.Context(), client, t.Id, t.Revision)
			if err != nil {
				return api.MapError(err)
			}
			if !resp.DeleteMemoryConfigTemplate {
				return exitcode.Newf(exitcode.Error, "the server did not delete template %q", t.Name)
			}
			dto := templateDeleteDTO{ID: t.Id, Name: t.Name, Revision: t.Revision, Deleted: true}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Deleted template %s (%s)\n", dto.Name, dto.ID)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (required non-interactively)")
	return cmd
}

func writeTemplateResult(f *cmdutil.Factory, verb string, dto templateWriteDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "%s template %s (%s, revision %d)\n\n", verb, dto.Template.Name, dto.Template.ID, dto.Template.Revision); err != nil {
			return err
		}
		if err := renderTemplate(w, dto.Template); err != nil {
			return err
		}
		renderWarnings(f.IOStreams, dto.Warnings)
		return nil
	})
}
