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
	OrganizationID   string  `json:"organizationId"`
	IsEncrypted      bool    `json:"isEncrypted"`
	MaxRevCount      int     `json:"maxRevCount"`
	UpdatedAt        string  `json:"updatedAt"`
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
	GetOrganizationId() string
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
	return cmd
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
		"no memory found for %q — expected a memory id or an <org>::<slug> URN (single-colon <org>:<slug> and an hrn:memory: prefix are also accepted)", ref)
}
