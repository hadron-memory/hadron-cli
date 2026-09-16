// Package channel implements `hadron channel` — the chat rooms of spec 049
// Phase 4 (hadron-cli#593).
//
// The addressing rule is the point of this package, and it is a rule about what
// NOT to do. A channelRef is the Channel's id or its ADDRESS (the chat root's
// node URN), and hadron-server#1172 made the server resolve both at every site.
// So nothing here classifies a ref: the string the user typed is passed through
// unexamined. A client-side matcher would be a third copy of one addressing
// rule, beside the portal's and MCP's, and the three would drift.
package channel

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// NewCmdChannel builds the `hadron channel` command group.
func NewCmdChannel(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "channel <command>",
		Aliases: []string{"channels"},
		Short:   "Work with Channels — the chat rooms hosted in a memory",
		Long: `Work with Channels.

A Channel is a chat room hosted at a reserved address in a memory. It is named
by its id, or by its ADDRESS — the chat root's node URN, which "channel list"
prints and which is what "channel create" composes from --memory and --loc.

Copy an address rather than composing one. Some Channels have no address the
server can safely advertise; their id always works.`,
	}
	cmd.AddCommand(newCmdList(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdRm(f))
	cmd.AddCommand(newCmdRead(f))
	cmd.AddCommand(newCmdPost(f))
	cmd.AddCommand(newCmdMarkRead(f))
	cmd.AddCommand(newCmdReadState(f))
	return cmd
}

// channelDTO is the stable --json shape of one Channel.
//
// ChatRootURN is a *string and is NOT omitempty: it is null for a Channel whose
// host memory has no flat-v2 address the server will advertise (#1171), and an
// agent has to be able to see that the field was answered with "none" rather
// than merely absent from this projection. `id` always works there.
type channelDTO struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Description    *string `json:"description"`
	Kind           string  `json:"kind"`
	Loc            string  `json:"loc"`
	ChatRootURN    *string `json:"chatRootUrn"`
	ChatRootNodeID string  `json:"chatRootNodeId"`
	MemoryID       string  `json:"memoryId"`
	MemoryURN      string  `json:"memoryUrn"`
	MemoryName     string  `json:"memoryName"`
	LastSeq        int     `json:"lastSeq"`
	LastMessageAt  *string `json:"lastMessageAt"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      *string `json:"updatedAt"`
}

// dtoFrom projects the shared fragments.
func dtoFrom(c gen.ChannelFields, m gen.ChannelMemory) channelDTO {
	d := channelDTO{
		ID:             c.Id,
		Name:           c.Name,
		Description:    c.Description,
		Kind:           string(c.Kind),
		Loc:            c.Loc,
		ChatRootURN:    c.ChatRootUrn,
		ChatRootNodeID: c.ChatRootNodeId,
		MemoryID:       c.MemoryId,
		LastSeq:        c.LastSeq,
		LastMessageAt:  c.LastMessageAt,
		CreatedAt:      c.CreatedAt,
		UpdatedAt:      c.UpdatedAt,
	}
	if m.Memory != nil {
		d.MemoryURN = m.Memory.Urn
		d.MemoryName = m.Memory.Name
	}
	return d
}

// noAddressNote explains a missing address once, rather than leaving a reader
// to wonder why a column is empty for some rows.
func noAddressNote(missing int) string {
	if missing <= 0 {
		return ""
	}
	noun := "Channel has"
	if missing > 1 {
		noun = "Channels have"
	}
	return itoa(missing) + " " + noun + " no address the server will advertise " +
		"(their host memory predates the flat URN grammar) — use the ID column for those; it always works"
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

// writeChannel renders one Channel in both branches.
func writeChannel(f *cmdutil.Factory, d channelDTO) error {
	return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		t := output.NewTable(w, "FIELD", "VALUE")
		t.Row("name", d.Name)
		t.Row("id", d.ID)
		if d.ChatRootURN != nil && *d.ChatRootURN != "" {
			t.Row("address", *d.ChatRootURN)
		} else {
			t.Row("address", "(none advertised — use the id)")
		}
		t.Row("memory", d.MemoryURN)
		t.Row("loc", d.Loc)
		if d.Description != nil && *d.Description != "" {
			t.Row("description", *d.Description)
		}
		t.Row("last seq", itoa(d.LastSeq))
		return t.Flush()
	})
}
