package team

import (
	"context"
	"fmt"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// chatReadStateDTO is a pre-read snapshot, never an acknowledgement. A null
// cursor means no applicable worker binding or an unavailable cursor read.
type chatReadStateDTO struct {
	Head           int  `json:"head"`
	AllocatedHead  int  `json:"allocatedHead"`
	ReadCursor     *int `json:"readCursor"`
	SuspectedStale bool `json:"suspectedStale"`
}

func probeChatReadState(ctx context.Context, f *cmdutil.Factory, client graphql.Client, appRef string, b *binding) (*chatReadStateDTO, error) {
	resp, err := gen.TeamChatReadHead(ctx, client, appRef)
	if err != nil {
		return nil, api.MapError(err)
	}
	if resp.App == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "App %q not found, or not visible to you", appRef)
	}
	if resp.TeamChatMessages == nil {
		return nil, exitcode.Newf(exitcode.Unavailable, "independent team-chat head probe returned no page; retry the read")
	}
	state := &chatReadStateDTO{}
	for _, m := range resp.TeamChatMessages.Items {
		if m != nil && m.Seq > state.Head {
			state.Head = m.Seq
		}
	}
	channel := resp.App.DefaultChannel
	if channel == nil {
		return state, nil
	}
	state.AllocatedHead = channel.LastSeq
	// Compare canonical identities on the same deployment. Never show another
	// team's/server's cursor under this App's head, and never attach a session:
	// diagnostic reads must not acknowledge anything before delivery.
	if b == nil || b.WorkerID == "" || !bindingServerMatches(f, b) || resp.App.Id != b.AppID {
		return state, nil
	}
	cursor, err := gen.ChannelReadState(ctx, client, channel.Id, b.WorkerID, &appRef)
	if err != nil {
		fmt.Fprintf(f.IOStreams.ErrOut, "note: server read cursor unavailable (%v); this read does not prove the worker is caught up\n", api.MapError(err))
		return state, nil
	}
	seen := 0
	if cursor.ChannelReadState != nil {
		seen = cursor.ChannelReadState.LastSeenSeq
	}
	state.ReadCursor = &seen
	return state, nil
}

// A short unfiltered forward page claims exhaustion, while a tail claims to
// contain the newest message. Neither claim can omit a message observed BEFORE
// the page was requested. Full forward pages, filtered pages and backward
// windows make no such claim. seq arithmetic is deliberately avoided: deleted
// messages and allocator gaps are legitimate.
func chatPageBehindHead(head, highest, since, count, pageSize int, tail, forward, bounded, filtered bool) bool {
	if filtered || bounded {
		return false
	}
	if tail {
		return highest < head
	}
	return forward && count < pageSize && head > since && highest < head
}
