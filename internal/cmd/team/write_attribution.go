package team

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// AuthoredWriteContext is command-local, never a default session on the
// transport. Snapshot before the first lookup, then verify the binding again
// for every authored mutation. Other requests retain their existing context.
func AuthoredWriteContext(f *cmdutil.Factory) func(context.Context, *graphql.Request) context.Context {
	var first sync.Once
	var diagnostic sync.Once
	var initial *binding
	var initialErr error
	note := func(err error) {
		diagnostic.Do(func() {
			fmt.Fprintf(f.IOStreams.ErrOut, "note: worker attribution unavailable (%v); this write carries no worktree session\n", err)
		})
	}
	return func(ctx context.Context, req *graphql.Request) context.Context {
		first.Do(func() { initial, _, initialErr = readBinding(ctx) })
		if req == nil || !authoredWriteOperation(req.OpName) {
			return ctx
		}
		if initialErr != nil {
			if !errors.Is(initialErr, errNoWorktree) {
				note(initialErr)
			}
			return ctx
		}
		if initial == nil || initial.SessionID == "" || initial.WorkerID == "" || initial.AppID == "" || initial.Server == "" {
			return ctx
		}
		current, _, err := readBinding(ctx)
		if err != nil {
			note(err)
			return ctx
		}
		if current == nil || current.SessionID != initial.SessionID || current.WorkerID != initial.WorkerID || current.AppID != initial.AppID || current.Server != initial.Server {
			return ctx
		}
		server, err := f.Server()
		if err != nil {
			note(err)
			return ctx
		}
		if server == "" || server != current.Server {
			return ctx
		}
		app, err := f.App()
		if err != nil {
			note(err)
			return ctx
		}
		if app != "" {
			same, err := bindingsAppIdentity(ctx, f, app, current.AppID)
			if err != nil {
				note(err)
				return ctx
			}
			if !same {
				return ctx
			}
			// The ambiguous App lookup is a round trip: never use the pre-lookup
			// snapshot if that lookup raced a rebind.
			fresh, _, err := readBinding(ctx)
			if err != nil {
				note(err)
				return ctx
			}
			if !sameWriteBinding(fresh, current) {
				return ctx
			}
		}
		return api.WithSession(ctx, current.SessionID)
	}
}

// Deliberately finite: no blanket mutation attribution, no reads/maintenance,
// no raw-api inference. #807 adds its authoritative comment operations here.
func authoredWriteOperation(op string) bool {
	switch op {
	case "CreateNode", "CreateSpecNode", "CreateTaskNode", "CreateReviewNode",
		"UpdateNode", "UpdateSpecNode", "UpdateTaskNode", "UpdateReviewNode",
		"DeleteNode", "ImportNode", "MoveNode", "CloneNode", "MergeNodes", "UpdateNodeData",
		"CreateEdge", "UpdateEdge", "DeleteEdge", "SearchReplaceInNodes", "SearchReplaceInSpecNodes",
		"CreateObject", "UpdateObject", "DeleteObject", "RestoreNodeRevision",
		"CreateAssetReferenceNode", "ExtractParentNodeToMemory":
		return true
	default:
		return false
	}
}

func sameWriteBinding(a, b *binding) bool {
	return a != nil && b != nil && a.SessionID == b.SessionID && a.WorkerID == b.WorkerID && a.AppID == b.AppID && a.Server == b.Server
}
