// Package scope implements `hadron scope` — named search scopes ("lenses")
// over a set of memories (spec 049 Phase 2; hadron-cli#578).
//
// A scope is a LENS, never a grant: the server intersects its memories with
// what the caller may read and discloses the remainder as a count only. The
// precedence ladder that turns a bare name into a scope is the SERVER's
// (App > Agent > organization, never unioned) — this package never matches a
// name itself; it calls scopeExplain and passes the resolved id on. Keeping
// that rule is what lets the portal and MCP behave identically.
package scope

import (
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

// NewCmdScope builds the `hadron scope` command group.
func NewCmdScope(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "scope <command>",
		Aliases: []string{"scopes", "lens"},
		Short:   "Work with named search scopes (lenses) over a set of memories",
		Long: `Work with named search scopes — "lenses".

A scope is an ORDERED list of memories owned by exactly one organization, App,
or installed Agent. It narrows what a search looks at; it never widens what you
may read. Memories in a scope you have no access to are reported as a count,
never by name.

A scope is addressed by its name (resolved in an App's context) or by its id.`,
	}
	cmd.AddCommand(newCmdList(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdRm(f))
	cmd.AddCommand(newCmdExplain(f))
	return cmd
}

// scopeFields is the shared projection every scope read returns.
type scopeFields = gen.ScopeFields

// byNameUsage documents the escape hatch for the one case where the shape
// classifier is wrong. Scope names allow [a-z0-9_-], so a 32-character all-hex
// NAME is also id-shaped and would otherwise be sent as a primary key, leaving
// the scope unreachable by every verb that takes <name|id>.
const byNameUsage = "treat the argument as a NAME even if it is id-shaped (a 32-character all-hex name is a legal scope name)"

// scopeDTO is the stable --json shape of one scope.
//
// memoryCount and hiddenMemoryCount are BOTH present and neither is omitted:
// Memories carries only the entries the caller may read, so rendering it
// without the hidden count understates the scope. That is the visibility gap
// CLAUDE.md names — an unreadable entry is surfaced, never dropped silently.
type scopeDTO struct {
	ID                string           `json:"id"`
	Name              string           `json:"name"`
	Description       *string          `json:"description"`
	OwnerType         string           `json:"ownerType"`
	OwnerID           string           `json:"ownerId"`
	OwnerURN          *string          `json:"ownerUrn"`
	MemoryCount       int              `json:"memoryCount"`
	HiddenMemoryCount int              `json:"hiddenMemoryCount"`
	Memories          []scopeMemoryDTO `json:"memories"`
	CreatedAt         string           `json:"createdAt"`
	UpdatedAt         *string          `json:"updatedAt"`
}

// scopeMemoryDTO is one memory in a scope, in scope order.
type scopeMemoryDTO struct {
	Position         int     `json:"position"`
	ID               string  `json:"id"`
	URN              string  `json:"urn"`
	Name             string  `json:"name"`
	ShortDescription *string `json:"shortDescription"`
}

// dtoFromFields projects the shared fragment. Memories is initialized empty so
// `--json` renders [] rather than null on a scope whose entries were not
// selected.
func dtoFromFields(s scopeFields) scopeDTO {
	d := scopeDTO{
		ID:                s.Id,
		Name:              s.Name,
		Description:       s.Description,
		OwnerType:         string(s.OwnerType),
		OwnerID:           s.OwnerId,
		OwnerURN:          s.OwnerUrn,
		MemoryCount:       s.MemoryCount,
		HiddenMemoryCount: s.HiddenMemoryCount,
		Memories:          []scopeMemoryDTO{},
		CreatedAt:         s.CreatedAt,
		UpdatedAt:         s.UpdatedAt,
	}
	return d
}

// summaryFromFields projects a scope for a context that did not request its
// memory list, so the DTO has no field that could misreport one as empty.
func summaryFromFields(s scopeFields) scopeSummaryDTO {
	return scopeSummaryDTO{
		ID:                s.Id,
		Name:              s.Name,
		Description:       s.Description,
		OwnerType:         string(s.OwnerType),
		OwnerID:           s.OwnerId,
		OwnerURN:          s.OwnerUrn,
		MemoryCount:       s.MemoryCount,
		HiddenMemoryCount: s.HiddenMemoryCount,
		CreatedAt:         s.CreatedAt,
		UpdatedAt:         s.UpdatedAt,
	}
}

// memoriesFromEntries projects the scope's ordered entries, preserving the
// server's order and its 0-based position. Returns an empty slice rather than
// nil so `--json` renders [].
func memoriesFromEntries(s gen.ScopeMemories) []scopeMemoryDTO {
	out := make([]scopeMemoryDTO, 0, len(s.Memories))
	for _, m := range s.Memories {
		if m == nil || m.Memory == nil {
			continue
		}
		out = append(out, scopeMemoryDTO{
			Position:         m.Position,
			ID:               m.Memory.Id,
			URN:              m.Memory.Urn,
			Name:             m.Memory.Name,
			ShortDescription: m.Memory.ShortDescription,
		})
	}
	return out
}

// hiddenNote renders the visibility disclosure for the human branch. Returns
// "" when nothing is hidden, so callers can append it unconditionally.
//
// The wording never names what is hidden, because the server never tells us —
// it reports a count precisely so a scope cannot be used to enumerate memories
// the caller may not read.
func hiddenNote(hidden int) string {
	return hiddenNoteIn(hidden, "this scope")
}

// hiddenNoteIn is hiddenNote with the containing phrase supplied, so the
// aggregate branch does not read "across these scopes, 1 memory in this
// scope…" — which misstates what the number covers.
func hiddenNoteIn(hidden int, where string) string {
	switch {
	case hidden <= 0:
		return ""
	case hidden == 1:
		return "1 memory in " + where + " is not readable by you and is not listed"
	default:
		return itoa(hidden) + " memories in " + where + " are not readable by you and are not listed"
	}
}

// itoa avoids importing strconv into every file for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// exactlyOneOwner enforces the "exactly one owner" arity of the owner flags.
//
// This is FLAG ARITY, not business logic: the server owns who may create a
// scope on a given owner and rejects a bad combination itself. Checking here
// only buys a message that names our flags instead of the server's input
// fields, and it refuses loudly rather than letting an ambiguous request
// through. Returns the chosen owner and its type.
func exactlyOneOwner(org, app, agent string) (ref string, ownerType gen.ScopeOwnerType, err error) {
	var chosen []string
	if org != "" {
		chosen = append(chosen, "--owner-org")
		ref, ownerType = org, gen.ScopeOwnerTypeOrganization
	}
	if app != "" {
		chosen = append(chosen, "--owner-app")
		ref, ownerType = app, gen.ScopeOwnerTypeApp
	}
	if agent != "" {
		chosen = append(chosen, "--owner-agent")
		ref, ownerType = agent, gen.ScopeOwnerTypeAgent
	}
	switch len(chosen) {
	case 1:
		return ref, ownerType, nil
	case 0:
		return "", "", exitcode.Newf(exitcode.Usage,
			"a scope is owned by exactly one of --owner-org, --owner-app or --owner-agent — pass one")
	default:
		return "", "", exitcode.Newf(exitcode.Usage,
			"a scope is owned by exactly one of --owner-org, --owner-app or --owner-agent — got %s", strings.Join(chosen, ", "))
	}
}

// ownerFilter builds the listing filter. Unlike create, NO owner is a valid
// choice here (it means "every scope I can see"), so the arity check only runs
// once something was passed.
func ownerFilter(org, app, agent, name string) (*gen.ScopeFilter, error) {
	filter := &gen.ScopeFilter{}
	any := false
	if org != "" || app != "" || agent != "" {
		ref, ownerType, err := exactlyOneOwner(org, app, agent)
		if err != nil {
			return nil, err
		}
		filter.OwnerRef = &ref
		filter.OwnerType = &ownerType
		any = true
	}
	if name != "" {
		filter.Name = &name
		any = true
	}
	if !any {
		return nil, nil
	}
	return filter, nil
}

// resolveScopeID turns a user-supplied scope reference into the scope's id.
//
// A ref that is already an id is returned untouched. Anything else is treated
// as a NAME and resolved through scopeExplain — the server's own ladder
// (App > Agent > organization, never unioned), which is why this is one
// round trip rather than a client-side scan of `scopes`. Matching a name here
// would put the precedence rule in three surfaces and let them drift.
//
// The two input domains OVERLAP, unlike nodes. A node loc is distinguishable
// from a node id by shape, but a scope NAME is 1-64 of [a-z0-9_-] — so
// `deadbeefdeadbeefdeadbeefdeadbeef` is a legal name that is also id-shaped.
// Shape still decides, because it is the only classifier that costs no round
// trip and the collision needs a 32-character all-hex name; byName is the
// explicit escape hatch for the case where it is wrong, rather than a
// speculative id lookup that falls back on null (which would charge every
// id-based command an extra round trip to serve a pathological name).
//
// The App context is resolved LAZILY, and only for a name: an id is
// unambiguous, so an unrelated ambient setting — a hand-edited config whose
// App ref no longer parses — must not be able to break `scope get <id>`.
func resolveScopeID(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, ref string, byName bool) (string, error) {
	// Trim FIRST, and pass the trimmed value on. IsBareID trims before
	// matching, so ` 0123…  ` classifies as an id — returning the untrimmed
	// original then sends whitespace to the server, which resolves nothing.
	// ResolveNodeRef trims for the same reason (internal/cmdutil/noderef.go).
	ref = strings.TrimSpace(ref)
	// cmdutil.IsBareID is the ONE place the id-shape rule lives (IsNodeID is
	// the same rule for nodes) — a second copy here is what drifts.
	if !byName && cmdutil.IsBareID(ref) {
		return ref, nil
	}
	appRef, err := f.App()
	if err != nil {
		return "", err
	}
	// Refuse BEFORE the round trip when a name has no App context. The server
	// does refuse — but in its own words: "Resolving a scope by name needs an
	// App context: pass appRef", naming a GraphQL field that has no flag. The
	// reader then has nothing to type. This is the same reason CanonicalAppRef
	// exists (#540), and it is context arity rather than business logic: the
	// precedence ladder itself stays server-side.
	if appRef == "" {
		return "", exitcode.Newf(exitcode.Usage,
			"a scope name resolves in an App's context — pass --app <ref>, run `hadron app set-active <ref>`, or give the scope's id instead")
	}
	namePtr, appPtr := &ref, &appRef
	resp, err := gen.ScopeExplain(cmd.Context(), client, nil, namePtr, appPtr, nil)
	if err != nil {
		return "", api.MapError(err)
	}
	if resp == nil || resp.ScopeExplain == nil || resp.ScopeExplain.Scope == nil {
		// The server returns an empty resolution for a scope that does not
		// exist AND for one we may not read, identically and on purpose — no
		// existence disclosure. Say only what is known.
		return "", exitcode.Newf(exitcode.NotFound, "no scope %q is readable in this App context", ref)
	}
	return resp.ScopeExplain.Scope.Id, nil
}

// writeScope renders one scope in both branches.
func writeScope(f *cmdutil.Factory, d scopeDTO) error {
	return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		t := output.NewTable(w, "FIELD", "VALUE")
		t.Row("id", d.ID)
		t.Row("name", d.Name)
		t.Row("owner", d.OwnerType+" "+ownerLabel(d.OwnerURN, d.OwnerID))
		if d.Description != nil && *d.Description != "" {
			t.Row("description", *d.Description)
		}
		t.Row("memories", itoa(d.MemoryCount))
		if err := t.Flush(); err != nil {
			return err
		}
		if len(d.Memories) > 0 {
			mt := output.NewTable(w, "#", "URN", "NAME")
			for _, m := range d.Memories {
				mt.Row(itoa(m.Position), m.URN, m.Name)
			}
			if err := mt.Flush(); err != nil {
				return err
			}
		}
		if note := hiddenNote(d.HiddenMemoryCount); note != "" {
			if _, err := io.WriteString(w, note+"\n"); err != nil {
				return err
			}
		}
		return nil
	})
}

// ownerLabel prefers the owner's URN and falls back to its id, which is all
// there is for an owner with no URN. Takes the two fields rather than a DTO so
// the full and summary shapes share one renderer.
func ownerLabel(ownerURN *string, ownerID string) string {
	if ownerURN != nil && *ownerURN != "" {
		return *ownerURN
	}
	return ownerID
}
