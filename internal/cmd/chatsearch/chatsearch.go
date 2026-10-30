// Package chatsearch is the output half shared by `team chat search` and
// `channel search` (hadron-cli#1327): one page of references into a chat's
// history. The two commands differ only in how they address the chat.
package chatsearch

import (
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// MatchDTO is one hit: a reference, never a body or snippet.
type MatchDTO struct {
	Seq                int     `json:"seq"`
	URN                *string `json:"urn"`
	NodeID             string  `json:"nodeId"`
	ChannelID          string  `json:"channelId"`
	ChannelIncarnation int     `json:"channelIncarnation"`
}

// PageDTO is the stable --json shape of TeamChatSearchPage. Complete and Cursor
// are passed through untouched: complete is true only when the search reached
// the start of the history, and an empty page with complete=false means the
// server's scan bound fired and the search may continue with the cursor.
type PageDTO struct {
	ChannelID          *string    `json:"channelId"`
	ChannelIncarnation *int       `json:"channelIncarnation"`
	Complete           bool       `json:"complete"`
	Cursor             *string    `json:"cursor"`
	Items              []MatchDTO `json:"items"`
}

// FromGen projects the generated page onto the DTO, so --json does not move
// when the client is regenerated.
func FromGen(p gen.ChatSearchPageFields) PageDTO {
	dto := PageDTO{
		ChannelID: p.ChannelId, ChannelIncarnation: p.ChannelIncarnation,
		Complete: p.Complete, Cursor: p.Cursor, Items: []MatchDTO{},
	}
	for _, m := range p.Items {
		if m == nil {
			continue
		}
		dto.Items = append(dto.Items, MatchDTO{
			Seq: m.Seq, URN: m.Urn, NodeID: m.NodeId,
			ChannelID: m.ChannelId, ChannelIncarnation: m.ChannelIncarnation,
		})
	}
	return dto
}

// Render writes the page. readHint returns the command that reads one hit by
// its seq; the text view prints it once, for the newest hit, because a search
// returns references only and the caller reads the message to see it.
func Render(f *cmdutil.Factory, page PageDTO, readHint func(seq int) string) error {
	return output.Write(f.IOStreams, f.JSON, page, func(w io.Writer) error {
		for _, m := range page.Items {
			ref := m.NodeID
			if m.URN != nil && *m.URN != "" {
				ref = *m.URN
			}
			if _, err := fmt.Fprintf(w, "#%d  %s\n", m.Seq, ref); err != nil {
				return err
			}
		}
		if len(page.Items) > 0 {
			if _, err := fmt.Fprintf(w, "read a hit: %s\n", readHint(page.Items[0].Seq)); err != nil {
				return err
			}
		}
		if page.Complete {
			if len(page.Items) == 0 {
				if _, err := io.WriteString(w, "no matches\n"); err != nil {
					return err
				}
			}
			_, err := io.WriteString(w, "complete\n")
			return err
		}
		next := "(none returned)"
		if page.Cursor != nil {
			next = *page.Cursor
		}
		_, err := fmt.Fprintf(w, "incomplete: stopped before the start of the history; continue with --cursor %s\n", next)
		return err
	})
}

// ContextFlags returns the persistent flags that selected this search, quoted
// for the shell, so a printed read hint reads the SAME server (and, with
// withApp, the same team App) rather than whatever the environment resolves.
func ContextFlags(f *cmdutil.Factory, withApp bool) string {
	out := ""
	if f.ServerFlag != "" {
		out += " --server " + cmdutil.ShellQuote(f.ServerFlag)
	}
	if withApp && f.AppFlag != "" {
		out += " --app " + cmdutil.ShellQuote(f.AppFlag)
	}
	return out
}

// refusal marks a mapped search error whose server code must reach the --json
// error envelope. The root handler reads it through ServerCode.
type refusal struct {
	error
	code string
}

func (r *refusal) Unwrap() error      { return r.error }
func (r *refusal) ServerCode() string { return r.code }

// MapError is api.MapError plus the BAD_USER_INPUT code for `--limit` below 1,
// which the issue lists among the typed refusals a search passes through. It is
// scoped to these commands: BAD_USER_INPUT is the server's generic validation
// code, so no other command's error envelope changes.
func MapError(err error) error {
	mapped := api.MapError(err)
	if mapped != nil && api.HasErrorCode(err, "BAD_USER_INPUT") {
		return &refusal{error: mapped, code: "BAD_USER_INPUT"}
	}
	return mapped
}
