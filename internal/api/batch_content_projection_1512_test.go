package api

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// cli#1512 — hardening pins for the nodeBatch edge-cap refusal and the
// content projection, each named for the mutant that must redden it:
//
//   - a hostile structured cap (terminal controls or a fractional numeric in
//     extensions.max) reads as "maximum not supplied" and never echoes the
//     extension text (kills a raw fmt.Sprint of the extension value, M5);
//   - a memory/prefix batch that meets the cap refuses after EXACTLY ONE
//     request, keeping its sanitized prefix identity (kills removal of the
//     memory/prefix terminal branch, M7);
//   - ReadNodeBatchContent's explicit field-by-field adapter projects EVERY
//     generated content field (kills a dropped adapter field) and every
//     generated field has a slot on the edge-free projection (a future
//     generated scalar/content field cannot be silently unprojectable).

// nodeJSON1512 sets every generated content field distinctively, so a field
// the adapter drops stays zero on the output while its source value is
// pinned, and a field the fixture forgot to set fails the pin instead of
// silently passing.
const nodeJSON1512 = `{"id":"id-1512","urn":"hrn:node:o:m:loc:1512","actionUrn":"hrn:action:o:a:reg","portalUrl":"https://p.example/app/u/x","memoryId":"mem-1512","loc":"loc:1512","name":"Name 1512","alias":"alias-1512","nodeType":"info","role":"spec.rule","objectType":"competitor","isRunnable":true,"description":"desc 1512","abstract":"abs 1512","abstractOriginHash":"a1b2c3d4","tags":["t1","t2"],"seq":7,"data":{"d":1512},"properties":{"p":1512},"content":"content 1512","createdAt":"2026-11-18T00:00:00Z","updatedAt":"2026-11-18T01:00:00Z"}`

func TestNodeBatchContentProjectsEveryGeneratedField1512(t *testing.T) {
	body := `{"nodeBatch":{"truncated":true,"omitted":["o-1512"],"unavailable":["u-1512"],"nodes":[` + nodeJSON1512 + `]}}`
	c := batchClient1210(func(_ context.Context, q *graphql.Request, r *graphql.Response) error {
		if q.OpName != "NodeBatchContent" || strings.Contains(q.Query, "outgoingEdges") || strings.Contains(q.Query, "incomingEdges") {
			t.Fatalf("the content document gained an edge selection: %s", q.Query)
		}
		return json.Unmarshal([]byte(body), r.Data)
	})
	got, err := ReadNodeBatchContent(context.Background(), c, []string{"id-1512"})
	if err != nil || got == nil || got.NodeBatch == nil || len(got.NodeBatch.Nodes) != 1 || got.NodeBatch.Nodes[0] == nil {
		t.Fatal(got, err)
	}
	// Decode the same fixture through the generated type: this is the source
	// the adapter read, so the comparison pins the copy, not the decoding.
	var want gen.NodeBatchContentResponse
	if json.Unmarshal([]byte(body), &want) != nil || len(want.NodeBatch.Nodes) != 1 || want.NodeBatch.Nodes[0] == nil {
		t.Fatal("fixture does not decode")
	}
	src, out := *want.NodeBatch.Nodes[0], *got.NodeBatch.Nodes[0]
	srcType := reflect.TypeOf(src)
	dstType := reflect.TypeOf(gen.NodeBatchNodeBatchNodeBatchResultNodesNode{})
	dstFields := map[string]bool{}
	for i := 0; i < dstType.NumField(); i++ {
		dstFields[dstType.Field(i).Name] = true
	}
	sv := reflect.ValueOf(src)
	ov := reflect.ValueOf(out)
	for i := 0; i < srcType.NumField(); i++ {
		f := srcType.Field(i)
		if !dstFields[f.Name] {
			t.Fatalf("generated content field %s has no slot on the batch projection: extend the adapter", f.Name)
		}
		wantV := sv.Field(i).Interface()
		if reflect.DeepEqual(wantV, reflect.Zero(f.Type).Interface()) {
			t.Fatalf("fixture left %s zero: the pin could not see a dropped adapter field", f.Name)
		}
		if gotV := ov.FieldByName(f.Name).Interface(); !reflect.DeepEqual(wantV, gotV) {
			t.Fatalf("adapter dropped or changed %s: want %#v got %#v", f.Name, wantV, gotV)
		}
	}
	if !got.NodeBatch.Truncated || got.NodeBatch.Omitted[0] != "o-1512" || got.NodeBatch.Unavailable[0] != "u-1512" {
		t.Fatalf("envelope projection changed: %+v", got.NodeBatch)
	}
}

func TestNodeBatchCapMaxNeverEchoesExtensionText1512(t *testing.T) {
	for _, ext := range []map[string]any{
		{"code": "BAD_USER_INPUT", "limit": "edges", "max": "\u0007bell\u001b[2Jwipe"},
		{"code": "BAD_USER_INPUT", "limit": "edges", "max": float64(150.5)},
		{"code": "BAD_USER_INPUT", "limit": "edges", "max": float64(1 << 54)}, // integral but over 2^53
	} {
		c := batchClient1210(func(context.Context, *graphql.Request, *graphql.Response) error {
			return gqlerror.List{&gqlerror.Error{Message: "SECRET provider body", Extensions: ext}}
		})
		_, err := ReadNodeBatch(context.Background(), c, []string{"one"}, nil, nil)
		if err == nil || !IsNodeBatchEdgeCapRefusal(err) || exitcode.FromError(MapError(err)) != exitcode.Usage {
			t.Fatalf("a structured cap must still refuse terminally: %v", err)
		}
		if !strings.Contains(err.Error(), "maximum not supplied") {
			t.Fatalf("a hostile max must read as not-supplied: %v", err)
		}
		if strings.Contains(err.Error(), "150.5") || strings.ContainsAny(err.Error(), "\u0007\u001b") ||
			strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "18014398509481984") {
			t.Fatalf("extension text was echoed into the refusal: %v", err)
		}
	}
}

func TestNodeBatchPrefixAndMemoryCapRefuseInOneRequest1512(t *testing.T) {
	for _, mode := range []string{"prefix", "memory"} {
		calls := 0
		c := batchClient1210(func(context.Context, *graphql.Request, *graphql.Response) error {
			calls++
			return capErr1210()
		})
		var err error
		if mode == "prefix" {
			prefix := "p\u001b[2J" // SafeLine keeps the runes, neutralising the control
			_, err = ReadNodeBatch(context.Background(), c, []string{"a", "b"}, nil, &prefix)
		} else {
			memory := "hrn:mem:o:m"
			_, err = ReadNodeBatch(context.Background(), c, []string{"a", "b"}, &memory, nil)
		}
		if err == nil || !IsNodeBatchEdgeCapRefusal(err) || exitcode.FromError(MapError(err)) != exitcode.Usage {
			t.Fatalf("%s: must refuse as a terminal cap refusal: %v", mode, err)
		}
		if calls != 1 {
			t.Fatalf("%s: a memory/prefix cap must refuse after exactly one request, got %d", mode, calls)
		}
		if !strings.Contains(err.Error(), "hadron edge list") {
			t.Fatalf("%s: refusal lost the edge-list guidance: %v", mode, err)
		}
		if mode == "prefix" {
			if !strings.Contains(err.Error(), "prefix p [2J") || strings.Contains(err.Error(), "\x1b") {
				t.Fatalf("sanitized prefix identity lost: %v", err)
			}
		} else if !strings.Contains(err.Error(), "the requested batch") {
			t.Fatalf("memory subject lost: %v", err)
		}
	}
}
