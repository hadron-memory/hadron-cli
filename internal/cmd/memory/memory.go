// Package memory implements `hadron memory ...`.
package memory

import (
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// memoryDTO is the stable --json shape for a memory. Field changes
// here are contract changes (see docs/agentic-usage.md).
type memoryDTO struct {
	ID               string  `json:"id"`
	URN              string  `json:"urn"`
	Name             string  `json:"name"`
	ShortDescription *string `json:"shortDescription"`
	Class            string  `json:"class"`
	Visibility       *string `json:"visibility"`
	OrganizationID   *string `json:"organizationId"`
	IsEncrypted      bool    `json:"isEncrypted"`
	MaxRevCount      int     `json:"maxRevCount"`
	UpdatedAt        string  `json:"updatedAt"`
	// ShareRole/SharedBy come from Memory.myShare and are populated only by
	// `memory ls --shared-with-me` (#316). omitempty by design: every other
	// memory command's shape is then untouched, and their absence reads as
	// "not a shared-with-me listing" rather than "no share". SharedBy is the
	// same accessUserDTO the share/member listings emit — a bare display label
	// would drop the grantor's id, which is what `memory share rm --grantee`
	// takes, and widening it afterwards would be a --json break rather than an
	// additive change.
	ShareRole *string        `json:"shareRole,omitempty"`
	SharedBy  *accessUserDTO `json:"sharedBy,omitempty"`
}

// memoryResult is the common projection selected by memory mutations. The
// generated operation structs expose these getters, so commands can share the
// stable DTO mapping without depending on any one generated type name.
type memoryResult interface {
	GetId() string
	GetUrn() string
	GetName() string
	GetShortDescription() *string
	GetClass() gen.MemoryClass
	GetVisibility() *gen.MemoryVisibility
	GetOrganizationId() *string
	GetIsEncrypted() bool
	GetMaxRevCount() int
	GetUpdatedAt() string
}

func dtoFromMemory(m memoryResult) memoryDTO {
	dto := memoryDTO{
		ID: m.GetId(), URN: m.GetUrn(), Name: m.GetName(),
		ShortDescription: m.GetShortDescription(), Class: string(m.GetClass()),
		OrganizationID: m.GetOrganizationId(), IsEncrypted: m.GetIsEncrypted(),
		MaxRevCount: m.GetMaxRevCount(), UpdatedAt: m.GetUpdatedAt(),
	}
	if m.GetVisibility() != nil {
		v := string(*m.GetVisibility())
		dto.Visibility = &v
	}
	return dto
}

func NewCmdMemory(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "memory <command>",
		Aliases: []string{"memories"},
		Short:   "Work with Hadron memories",
	}
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdSet(f))
	cmd.AddCommand(newCmdAttach(f))
	cmd.AddCommand(newCmdSetActive(f))
	cmd.AddCommand(newCmdRm(f))
	cmd.AddCommand(newCmdClone(f))
	cmd.AddCommand(newCmdExtract(f))
	cmd.AddCommand(newCmdExport(f))
	cmd.AddCommand(newCmdMember(f))
	cmd.AddCommand(newCmdShare(f))
	cmd.AddCommand(newCmdSubscription(f))
	cmd.AddCommand(newCmdEncrypt(f))
	cmd.AddCommand(newCmdLinkUser(f))
	annotateMemoryRefHelp(cmd)
	return cmd
}

// memoryRefToken is the placeholder every command in this group uses in
// its usage line for the memory it acts on.
const memoryRefToken = "<memoryRef>"

// memoryRefHelp spells out what that placeholder accepts. `memory share
// ls <memory>` said nothing about the accepted forms, so there was no
// way to tell from --help whether it wanted an id or a URN (#282).
//
// It has to stay exhaustive to be worth printing: <root> is an org
// domain OR a user handle (an --owner-me memory is minted under your own
// handle), and cmdutil.MemoryParts normalizes urn: to hrn: before
// matching, so both schemes work everywhere. memoryRefFormsAreAccepted
// pins every form named here against the parser.
const memoryRefHelp = `<memoryRef> identifies the memory. It accepts the memory's id, or any of the
URN spellings below, where <root> is an organization domain or a user handle
(a memory created with --owner-me lives under your own handle):

  hrn:mem:<root>:<slug>       canonical
  hrn:memory:<root>::<slug>   legacy
  <root>:<slug>               short form
  <root>::<slug>              short form

urn: is accepted in place of hrn: throughout.`

// annotateMemoryRefHelp appends memoryRefHelp to the long help of every
// command in the tree that takes a <memoryRef>. Doing it here rather
// than pasting the paragraph into ~17 commands keeps the accepted forms
// from drifting apart and covers new commands for free.
func annotateMemoryRefHelp(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		annotateMemoryRefHelp(sub)
	}
	if !strings.Contains(cmd.Use, memoryRefToken) {
		return
	}
	// Cobra shows Long instead of Short once Long is set, so a command
	// with no Long keeps its description by leading with it.
	long := strings.TrimRight(cmd.Long, "\n")
	if long == "" {
		long = cmd.Short + "."
	}
	cmd.Long = long + "\n\n" + memoryRefHelp
}

// resolveMemoryID maps a memory URN to its ID via memory(ref:), which
// dispatches PKs and URNs server-side (hadron-server#473). The mutations
// this feeds (updateMemory, member/share writes) still accept PK ids only.
func resolveMemoryID(cmd *cobra.Command, client graphql.Client, ref string) (string, error) {
	canon := cmdutil.CanonicalMemoryRef(ref)
	if !strings.Contains(canon, ":") {
		return canon, nil // a raw id — no round-trip needed
	}
	resp, err := gen.GetMemory(cmd.Context(), client, canon)
	if err != nil {
		return "", api.MapError(err)
	}
	if resp.Memory == nil {
		return "", notFoundMemory(ref)
	}
	return resp.Memory.Id, nil
}

// notFoundMemory is the shared "no memory" error, naming the accepted forms so a
// rejected short form isn't mistaken for a genuinely-absent memory (#108).
func notFoundMemory(ref string) error {
	return exitcode.Newf(exitcode.NotFound,
		"no memory found for %q — expected a memory id or a URN: hrn:mem:<root>:<slug> (canonical), the <root>::<slug> / <root>:<slug> short forms, or the legacy hrn:memory: prefix", ref)
}
