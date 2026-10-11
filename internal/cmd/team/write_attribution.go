package team

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
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
		if !authoredWriteRequest(req) {
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

// Inspect the selected mutation structurally, not its operation label or
// arbitrary input text. #1606's published fields support #807 regardless of
// its generated operation names. Every root field must be authored; mixed
// maintenance requests and unresolved/cyclic fragments fail closed.
func authoredWriteRequest(req *graphql.Request) bool {
	if req == nil {
		return false
	}
	doc, err := parser.ParseQuery(&ast.Source{Input: req.Query})
	if err != nil {
		return false
	}
	op := doc.Operations.ForName(req.OpName)
	if op == nil && req.OpName == "" && len(doc.Operations) == 1 {
		op = doc.Operations[0]
	}
	return op != nil && op.Operation == ast.Mutation && authoredSelections(doc, op.SelectionSet, map[string]bool{})
}
func authoredSelections(doc *ast.QueryDocument, selections ast.SelectionSet, visiting map[string]bool) bool {
	if len(selections) == 0 {
		return false
	}
	for _, selection := range selections {
		switch s := selection.(type) {
		case *ast.Field:
			if !authoredWriteField(s.Name) {
				return false
			}
		case *ast.InlineFragment:
			if s.TypeCondition != "" && s.TypeCondition != "Mutation" {
				return false
			}
			if !authoredSelections(doc, s.SelectionSet, visiting) {
				return false
			}
		case *ast.FragmentSpread:
			f := doc.Fragments.ForName(s.Name)
			if f == nil || f.TypeCondition != "Mutation" || visiting[s.Name] {
				return false
			}
			visiting[s.Name] = true
			ok := authoredSelections(doc, f.SelectionSet, visiting)
			delete(visiting, s.Name)
			if !ok {
				return false
			}
		default:
			return false
		}
	}
	return true
}
func authoredWriteField(field string) bool {
	switch field {
	case "createNode", "createSpecNode", "createTaskNode", "createReviewNode",
		"updateNode", "updateSpecNode", "updateTaskNode", "updateReviewNode",
		"deleteNode", "importNode", "moveNode", "cloneNode", "mergeNodes", "updateNodeData",
		"createEdge", "updateEdge", "deleteEdge", "searchReplaceInNodes", "searchReplaceInSpecNodes",
		"createObject", "updateObject", "deleteObject", "restoreNodeRevision",
		"createAssetReferenceNode", "extractParentNodeToMemory",
		"createComment", "replyToComment", "editComment", "retractComment",
		"resolveCommentThread", "reopenCommentThread", "hideComment", "deleteCommentThread":
		return true
	default:
		return false
	}
}

func sameWriteBinding(a, b *binding) bool {
	return a != nil && b != nil && a.SessionID == b.SessionID && a.WorkerID == b.WorkerID && a.AppID == b.AppID && a.Server == b.Server
}
