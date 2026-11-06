package api

import (
	"context"
	"sync/atomic"

	"github.com/vektah/gqlparser/v2/gqlerror"
)

// GraphQLErrorShape records only whether normal error sanitization removed a
// null entry. It holds no messages or response data and changes no mapping.
// A caller that needs an exact original validation envelope can use this to
// disqualify fallback after the ordinary client has safely cleaned the errors.
type GraphQLErrorShape struct {
	nullEntry atomic.Bool
}

type graphQLErrorShapeKey struct{}

// WithGraphQLErrorShape observes requests using only the returned context.
// Install a fresh observer for each initial read whose fallback depends on the
// original envelope; do not reuse it for the legacy read or unrelated calls.
func WithGraphQLErrorShape(ctx context.Context) (context.Context, *GraphQLErrorShape) {
	shape := &GraphQLErrorShape{}
	return context.WithValue(ctx, graphQLErrorShapeKey{}, shape), shape
}

// HadNullEntry reports whether a request removed a null original error.
func (s *GraphQLErrorShape) HadNullEntry() bool { return s.nullEntry.Load() }

func recordGraphQLErrorShape(ctx context.Context, list gqlerror.List) {
	shape, _ := ctx.Value(graphQLErrorShapeKey{}).(*GraphQLErrorShape)
	if shape == nil {
		return
	}
	for _, e := range list {
		if e == nil {
			shape.nullEntry.Store(true)
			return
		}
	}
}
