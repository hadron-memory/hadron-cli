package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

// cli#962: response extensions.warnings reach a caller only through a sink it
// installed on its own context, and only from a successful response.

func c962Server(t *testing.T, body string) graphql.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	client, err := NewClient(srv.URL, "", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func c962Request(ctx context.Context, client graphql.Client) error {
	var data map[string]any
	return client.MakeRequest(ctx, &graphql.Request{Query: "mutation M { m }", OpName: "M"}, &graphql.Response{Data: &data})
}

const c962Body = `{"data":{"m":true},"extensions":{"warnings":[` +
	`{"path":"m","code":"task.create.no-source","text":"No source.","register":{"state":"mapped","taskLoc":"tasks:a","taskUrn":"hrn:node:acme.com:sys:tasks:a"}},` +
	`{"path":"m","code":"task.future.unknown","text":"Unknown.","register":{"state":"unmapped"}},` +
	`{"path":"m","code":7,"text":"malformed"},` +
	`{"path":"other","code":"task.create.no-source","text":"Another key's."}]}}`

func TestWarningSinkCollectsOnlyItsOwnContext(t *testing.T) {
	client := c962Server(t, c962Body)

	// No sink installed: the request succeeds and nothing is collected.
	if err := c962Request(context.Background(), client); err != nil {
		t.Fatal(err)
	}

	ctxA, sinkA := WithWarningSink(context.Background())
	ctxB, sinkB := WithWarningSink(context.Background())
	if err := c962Request(ctxA, client); err != nil {
		t.Fatal(err)
	}
	if got := sinkB.For("m"); len(got) != 0 {
		t.Fatalf("sink B saw request A's warnings: %+v", got)
	}
	got := sinkA.For("m")
	if len(got) != 2 {
		t.Fatalf("want the two well-formed entries for path m, got %+v", got)
	}
	if got[0].Code != "task.create.no-source" || got[0].Text != "No source." || got[0].Register == nil ||
		got[0].Register.State != "mapped" || got[0].Register.TaskLoc == nil || *got[0].Register.TaskLoc != "tasks:a" ||
		got[0].Register.TaskURN == nil || *got[0].Register.TaskURN != "hrn:node:acme.com:sys:tasks:a" {
		t.Fatalf("first warning %+v", got[0])
	}
	if got[1].Code != "task.future.unknown" || got[1].Register == nil || got[1].Register.State != "unmapped" ||
		got[1].Register.TaskLoc != nil || got[1].Register.TaskURN != nil {
		t.Fatalf("second warning %+v", got[1])
	}
	if other := sinkA.For("other"); len(other) != 1 {
		t.Fatalf("path filter: %+v", other)
	}
	if err := c962Request(ctxB, client); err != nil {
		t.Fatal(err)
	}
	if n := len(sinkA.For("m")); n != 2 {
		t.Fatalf("request B leaked into sink A: %d", n)
	}
}

func TestWarningSinkIgnoresAFailedResponse(t *testing.T) {
	client := c962Server(t, `{"data":null,"errors":[{"message":"nope","path":["m"]}],`+
		`"extensions":{"warnings":[{"path":"m","code":"task.finish.open-children","text":"Open children."}]}}`)
	ctx, sink := WithWarningSink(context.Background())
	if err := c962Request(ctx, client); err == nil {
		t.Fatal("want the error")
	}
	if got := sink.For("m"); len(got) != 0 {
		t.Fatalf("a failed response filled the sink: %+v", got)
	}
}

func TestWarningSinkToleratesMalformedCarriers(t *testing.T) {
	for _, carrier := range []string{`null`, `"x"`, `{"path":"m"}`, `[null,1,"s",{"path":"m"},{"path":"m","code":"c"}]`} {
		client := c962Server(t, `{"data":{"m":true},"extensions":{"warnings":`+carrier+`}}`)
		ctx, sink := WithWarningSink(context.Background())
		if err := c962Request(ctx, client); err != nil {
			t.Fatalf("%s: %v", carrier, err)
		}
		if got := sink.For("m"); len(got) != 0 {
			t.Fatalf("%s: %+v", carrier, got)
		}
	}
	// A register that is not an object, or has no string state, is dropped
	// while the warning itself is kept.
	client := c962Server(t, `{"data":{"m":true},"extensions":{"warnings":[{"path":"m","code":"c","text":"t","register":"mapped"},{"path":"m","code":"c2","text":"t2","register":{"state":5}}]}}`)
	ctx, sink := WithWarningSink(context.Background())
	if err := c962Request(ctx, client); err != nil {
		t.Fatal(err)
	}
	got := sink.For("m")
	if len(got) != 2 || got[0].Register != nil || got[1].Register != nil {
		t.Fatalf("%+v", got)
	}
}

func TestWarningSinkForFieldsUsesSelectedMutationResponseKeys(t *testing.T) {
	for _, query := range []string{`mutation M { saved: createNode { id } }`, `query M { createNode { id } }`, `mutation M { updateNode { id } }`, `mutation M { ...Root } fragment Root on Mutation { saved: createNode { id } }`, `mutation M { ... on Mutation { saved: createNode { id } } }`, `not graphql`} {
		client := c962Server(t, `{"data":{"saved":{"id":"n"}},"extensions":{"warnings":[{"path":"saved","code":"include.pin.unkept","text":"advice"},{"path":"createNode","code":"wrong","text":"wrong"},{"path":"updateNode","code":"other","text":"other"}]}}`)
		ctx, sink := WithWarningSinkForFields(context.Background(), "createNode")
		var data map[string]any
		if err := client.MakeRequest(ctx, &graphql.Request{Query: query, OpName: "M"}, &graphql.Response{Data: &data}); err != nil {
			t.Fatal(err)
		}
		got := sink.All()
		if query == `mutation M { saved: createNode { id } }` {
			if len(got) != 1 || got[0].Path != "saved" {
				t.Fatalf("alias not isolated: %+v", got)
			}
		} else if len(got) != 0 {
			t.Fatalf("unselected field/query warning: %+v", got)
		}
	}
}
