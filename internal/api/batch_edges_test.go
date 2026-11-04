package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type batchClient1210 func(context.Context, *graphql.Request, *graphql.Response) error

func (f batchClient1210) MakeRequest(ctx context.Context, q *graphql.Request, r *graphql.Response) error {
	return f(ctx, q, r)
}
func capErr1210() error {
	return gqlerror.List{&gqlerror.Error{Message: "SECRET provider body", Extensions: map[string]any{"code": "BAD_USER_INPUT", "limit": "edges", "max": float64(2000)}}}
}
func refs1210(q *graphql.Request) []string {
	b, _ := json.Marshal(q.Variables)
	var v struct{ Refs []string }
	_ = json.Unmarshal(b, &v)
	return v.Refs
}
func result1210(r *graphql.Response, refs []string) error {
	rows := []string{}
	for _, id := range refs {
		rows = append(rows, fmt.Sprintf(`{"id":%q,"loc":%q,"outgoingEdges":[{"id":"out","target":null}],"incomingEdges":[{"id":"in","source":{"id":"base","loc":"base:x","memoryId":"inherited"}}]}`, id, id))
	}
	return json.Unmarshal([]byte(`{"nodeBatch":{"nodes":[`+strings.Join(rows, ",")+`],"omitted":[],"unavailable":[],"truncated":false}}`), r.Data)
}

func TestReadNodeBatchBoundAndProjection1210(t *testing.T) {
	refs := make([]string, 200)
	for i := range refs {
		refs[i] = fmt.Sprint(i)
	}
	calls := 0
	c := batchClient1210(func(_ context.Context, q *graphql.Request, r *graphql.Response) error {
		calls++
		if q.Query != gen.NodeBatch_Operation {
			t.Fatal("projection changed")
		}
		refs := refs1210(q)
		if len(refs) > 1 {
			return capErr1210()
		}
		return result1210(r, refs)
	})
	resp, err := ReadNodeBatch(context.Background(), c, refs, nil, nil)
	if err != nil || len(resp.NodeBatch.Nodes) != 200 || calls != 399 {
		t.Fatal(err, calls)
	}
	for i, n := range resp.NodeBatch.Nodes {
		if n.Id != refs[i] || n.OutgoingEdges[0].Target != nil || n.IncomingEdges[0].Source.MemoryId != "inherited" {
			t.Fatal("projection/order changed", n)
		}
	}
}
func TestReadNodeBatchErrorFences1210(t *testing.T) {
	for _, failure := range []error{
		errors.New("ordinary transport"), gqlerror.List{&gqlerror.Error{Message: "Read edges in pages", Extensions: map[string]any{"code": "BAD_USER_INPUT"}}},
		gqlerror.List{&gqlerror.Error{Extensions: map[string]any{"code": "FORBIDDEN", "limit": "edges"}}},
		gqlerror.List{capErr1210().(gqlerror.List)[0], &gqlerror.Error{Message: "other", Extensions: map[string]any{"code": "FORBIDDEN"}}},
		&graphql.HTTPError{StatusCode: 502, Response: graphql.Response{Errors: capErr1210().(gqlerror.List)}},
	} {
		calls := 0
		c := batchClient1210(func(context.Context, *graphql.Request, *graphql.Response) error { calls++; return failure })
		resp, err := ReadNodeBatch(context.Background(), c, []string{"a", "b"}, nil, nil)
		if resp == nil || (err == nil || err.Error() != failure.Error()) || calls != 1 {
			t.Fatalf("ordinary error changed: %v %d", err, calls)
		}
	}
}
func TestReadNodeBatchTerminalRefusal1210(t *testing.T) {
	calls := 0
	c := batchClient1210(func(context.Context, *graphql.Request, *graphql.Response) error { calls++; return capErr1210() })
	resp, err := ReadNodeBatch(context.Background(), c, []string{"safe\n\x1bsecret"}, nil, nil)
	if resp != nil || !IsNodeBatchEdgeCapRefusal(err) || exitcode.FromError(MapError(err)) != exitcode.Usage || calls != 1 || !strings.Contains(err.Error(), "2000") || !strings.Contains(err.Error(), "hadron edge list") || strings.ContainsAny(err.Error(), "\n\x1b") || strings.Contains(err.Error(), "SECRET") {
		t.Fatal(resp, err, calls)
	}
}
func TestReadNodeBatchNoPartialAndDedup1210(t *testing.T) {
	calls := 0
	c := batchClient1210(func(_ context.Context, q *graphql.Request, r *graphql.Response) error {
		calls++
		refs := refs1210(q)
		if len(refs) > 1 {
			return capErr1210()
		}
		if refs[0] == "bad" {
			return capErr1210()
		}
		return result1210(r, []string{"same-resolved-id"})
	})
	resp, err := ReadNodeBatch(context.Background(), c, []string{"a", "bad"}, nil, nil)
	if err == nil || resp != nil || calls != 3 {
		t.Fatal("partial escaped", resp, err, calls)
	}
	calls = 0
	resp, err = ReadNodeBatch(context.Background(), c, []string{"pk", "urn"}, nil, nil)
	if err != nil || len(resp.NodeBatch.Nodes) != 1 {
		t.Fatal("aliases duplicated", resp, err)
	}
}
func TestReadNodeBatchContentProjection1210(t *testing.T) {
	c := batchClient1210(func(_ context.Context, q *graphql.Request, r *graphql.Response) error {
		raw, _ := json.Marshal(q.Variables)
		var vars map[string]json.RawMessage
		_ = json.Unmarshal(raw, &vars)
		for _, k := range []string{"memory", "locPrefix"} {
			if _, present := vars[k]; present {
				t.Fatalf("unset optional %s is present: %s", k, raw)
			}
		}
		if q.OpName != "NodeBatchContent" || strings.Contains(q.Query, "outgoingEdges") || strings.Contains(q.Query, "incomingEdges") {
			t.Fatal("selected unused edges", q.Query)
		}
		return json.Unmarshal([]byte(`{"nodeBatch":{"truncated":true,"omitted":["next"],"unavailable":["denied"],"nodes":[{"id":"a","role":"spec.rule","content":"stored {{text}}","properties":{"x":1},"tags":[]}]}}`), r.Data)
	})
	r, err := ReadNodeBatchContent(context.Background(), c, []string{"a"})
	if err != nil || !r.NodeBatch.Truncated || r.NodeBatch.Omitted[0] != "next" || r.NodeBatch.Unavailable[0] != "denied" || *r.NodeBatch.Nodes[0].Role != "spec.rule" || *r.NodeBatch.Nodes[0].Content != "stored {{text}}" || r.NodeBatch.Nodes[0].OutgoingEdges != nil {
		t.Fatal(r, err)
	}
}

func TestReadNodeBatchSplitKeepsByteSpillover1210(t *testing.T) {
	c := batchClient1210(func(_ context.Context, q *graphql.Request, r *graphql.Response) error {
		refs := refs1210(q)
		if len(refs) > 1 {
			return capErr1210()
		}
		if refs[0] == "a" {
			return json.Unmarshal([]byte(`{"nodeBatch":{"nodes":[{"id":"a"}],"truncated":true,"omitted":["a-tail"],"unavailable":[]}}`), r.Data)
		}
		return result1210(r, refs)
	})
	r, err := ReadNodeBatch(context.Background(), c, []string{"a", "b"}, nil, nil)
	if err != nil || !r.NodeBatch.Truncated || len(r.NodeBatch.Nodes) != 2 || r.NodeBatch.Omitted[0] != "a-tail" {
		t.Fatal(r, err)
	}
}
