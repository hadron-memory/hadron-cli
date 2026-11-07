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
// sequence means unavailable or inapplicable, never a fabricated zero.
type chatReadStateDTO struct {
	Head           *int `json:"head"`
	AllocatedHead  *int `json:"allocatedHead"`
	ReadCursor     *int `json:"readCursor"`
	SuspectedStale bool `json:"suspectedStale"`
}

// probeChatHead is the independent backward head probe. It MUST complete before
// the page is requested: the staleness claim is about messages observed BEFORE
// the page was asked for, so it can never overlap the page read.
func probeChatHead(ctx context.Context, f *cmdutil.Factory, client graphql.Client, appRef string, attribution *chatReadAttribution) (*chatReadStateDTO, error) {
	state := &chatReadStateDTO{}
	resp, err := attribution.head(ctx, client, appRef)
	if err != nil {
		// Only the known old-schema refusal degrades. Auth, transport and
		// unrelated validation failures must not become a quiet-channel answer.
		if !api.IsGraphQLValidationFor(err, "beforeSeq") {
			return nil, api.MapError(err)
		}
		fmt.Fprintln(f.IOStreams.ErrOut, "note: this server does not support the independent backward head probe; freshness comparison unavailable (head is null)")
	} else {
		if resp.TeamChatMessages == nil {
			return nil, exitcode.Newf(exitcode.Unavailable, "independent team-chat head probe returned no page; retry the read")
		}
		head := 0
		for _, m := range resp.TeamChatMessages.Items {
			if m != nil && m.Seq > head {
				head = m.Seq
			}
		}
		state.Head = &head
	}
	return state, nil
}

// chatReadMetadata is the allocator head and server read cursor, read in the
// background (cli#1677). Neither value takes part in the staleness decision
// (that uses only the head probe), and neither read acknowledges anything, so
// they may overlap the head and page reads: the command pays their latency
// once, alongside the page, instead of after it. The notes they would print are
// held and written by wait, in the order the sequential code wrote them.
type chatReadMetadata struct {
	done          chan struct{}
	allocatedHead *int
	readCursor    *int
	notes         []string
}

func startChatReadMetadata(ctx context.Context, f *cmdutil.Factory, client graphql.Client, appRef string, b *binding) *chatReadMetadata {
	m := &chatReadMetadata{done: make(chan struct{})}
	// Resolved here, on the caller's goroutine: the Factory resolves its server
	// lazily and is not safe to first-touch from two goroutines.
	sameServer := b != nil && bindingServerMatches(f, b)
	go func() {
		defer close(m.done)
		m.read(ctx, client, appRef, b, sameServer)
	}()
	return m
}

// wait blocks for the background read, prints its notes, and records its values
// on state. Safe to call on every exit path; only the first call has effect.
func (m *chatReadMetadata) wait(f *cmdutil.Factory, state *chatReadStateDTO) {
	<-m.done
	for _, n := range m.notes {
		fmt.Fprintln(f.IOStreams.ErrOut, n)
	}
	m.notes = nil
	if state != nil {
		if m.allocatedHead != nil {
			state.AllocatedHead = m.allocatedHead
		}
		if m.readCursor != nil {
			state.ReadCursor = m.readCursor
		}
	}
}

func (m *chatReadMetadata) read(ctx context.Context, client graphql.Client, appRef string, b *binding, sameServer bool) {
	metadata, err := gen.TeamChatReadMetadata(ctx, client, appRef)
	if err != nil || metadata.App == nil {
		m.notes = append(m.notes, "note: optional App/channel metadata unavailable; allocator head and server read cursor are unknown")
		return
	}
	channel := metadata.App.DefaultChannel
	if channel == nil {
		return
	}
	lastSeq := channel.LastSeq
	m.allocatedHead = &lastSeq
	// Compare canonical identities on the same deployment. Never show another
	// team's/server's cursor under this App's head, and never attach a session:
	// diagnostic reads must not acknowledge anything before delivery.
	if b == nil || b.WorkerID == "" || !sameServer || metadata.App.Id != b.AppID {
		return
	}
	cursor, err := gen.ChannelReadState(ctx, client, channel.Id, b.WorkerID, &appRef)
	if err != nil {
		m.notes = append(m.notes, fmt.Sprintf("note: server read cursor unavailable (%v); this read does not prove the worker is caught up", api.MapError(err)))
		return
	}
	if cursor == nil || cursor.ChannelReadState == nil {
		// #1272: null means this worker is not registered on, or cannot be
		// read for, the channel. A registered never-read worker gets a
		// lastSeenSeq-0 row instead, so 0 here would be a forged cursor.
		m.notes = append(m.notes, "note: server read cursor unavailable (no read state for this worker on the team channel); this read does not prove the worker is caught up")
		return
	}
	seen := cursor.ChannelReadState.LastSeenSeq
	m.readCursor = &seen
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
