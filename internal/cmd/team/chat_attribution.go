package team

import (
	"context"
	"fmt"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// One command-local capability decision covers the head and every page. Older
// servers never receive an attributed legacy read: that would acknowledge
// before delivery. Recheck the binding before every attributed request.
type chatReadAttribution struct {
	f           *cmdutil.Factory
	session     string
	unsupported bool
}

func newChatReadAttribution(ctx context.Context, f *cmdutil.Factory, appRef string, b *binding) *chatReadAttribution {
	a := &chatReadAttribution{f: f}
	if b != nil && b.SessionID != "" && bindingServerMatches(f, b) && isBindingsApp(ctx, f, appRef, b.AppID) && bindingIsSession(ctx, b.SessionID) {
		a.session = b.SessionID
	}
	return a
}
func (a *chatReadAttribution) enabled(ctx context.Context) bool {
	return a.session != "" && !a.unsupported && bindingIsSession(ctx, a.session)
}
func (a *chatReadAttribution) fallback(err error) bool {
	if !api.IsUnknownGraphQLArgument(err, "advanceReadState", "Query.teamChatMessages") {
		return false
	}
	a.unsupported = true
	fmt.Fprintln(a.f.IOStreams.ErrOut, "note: this server lacks acknowledgement suppression; chat reads are headerless and session attribution is unavailable")
	return true
}
func (a *chatReadAttribution) head(ctx context.Context, client graphql.Client, appRef string) (*gen.TeamChatReadHeadResponse, error) {
	if a.enabled(ctx) {
		r, err := gen.TeamChatReadHeadAttributed(api.WithSession(ctx, a.session), client, appRef)
		if err != nil {
			if !a.fallback(err) {
				return nil, err
			}
		} else {
			out := &gen.TeamChatReadHeadResponse{}
			if r.TeamChatMessages != nil {
				out.TeamChatMessages = &gen.TeamChatReadHeadTeamChatMessagesTeamChatMessagesPage{}
				for _, m := range r.TeamChatMessages.Items {
					if m == nil {
						out.TeamChatMessages.Items = append(out.TeamChatMessages.Items, nil)
					} else {
						out.TeamChatMessages.Items = append(out.TeamChatMessages.Items, &gen.TeamChatReadHeadTeamChatMessagesTeamChatMessagesPageItemsTeamChatMessage{Seq: m.Seq})
					}
				}
			}
			return out, nil
		}
	}
	return gen.TeamChatReadHead(ctx, client, appRef)
}
func (a *chatReadAttribution) page(ctx context.Context, client graphql.Client, appRef string, since *int, mentions *string, size *int, before *int) (*gen.TeamChatMessagesResponse, error) {
	if a.enabled(ctx) {
		r, err := gen.TeamChatMessagesAttributed(api.WithSession(ctx, a.session), client, appRef, since, mentions, size, nil, before)
		if err != nil {
			if !a.fallback(err) {
				return nil, err
			}
		} else {
			out := &gen.TeamChatMessagesResponse{}
			if r.TeamChatMessages != nil {
				out.TeamChatMessages = &gen.TeamChatMessagesTeamChatMessagesTeamChatMessagesPage{Total: r.TeamChatMessages.Total}
				for _, m := range r.TeamChatMessages.Items {
					if m == nil {
						out.TeamChatMessages.Items = append(out.TeamChatMessages.Items, nil)
					} else {
						out.TeamChatMessages.Items = append(out.TeamChatMessages.Items, &gen.TeamChatMessagesTeamChatMessagesTeamChatMessagesPageItemsTeamChatMessage{TeamChatMessageFields: m.TeamChatMessageFields})
					}
				}
			}
			return out, nil
		}
	}
	return gen.TeamChatMessages(ctx, client, appRef, since, mentions, size, nil, before)
}
