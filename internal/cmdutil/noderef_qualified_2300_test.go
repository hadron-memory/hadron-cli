package cmdutil

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// hadron-cli#2300: a qualified node ref names its own memory (cor:urn:010:02),
// so -m/--memory never recomposes it; -m composes only a bare loc. Every
// caller of ResolveNodeRef and BatchNodeRef, reads and writes alike, inherits
// this from the two helpers.

var qualifiedRefs2300 = []string{
	"hrn:node:example.test:source:demo",
	"urn:node:example.test:source:demo",
	"example.test::source::demo",
	"example.test::juno:app-mem:ops::demo",
}

var memories2300 = []string{"example.test:other", "example.test::other", "example.test::juno:app-mem:other"}

// resolveSent2300 runs ResolveNodeRef against a fake server and returns the urn
// it sent to resolveUrn.
func resolveSent2300(t *testing.T, memory, ref string) string {
	t.Helper()
	var sent string
	var seen bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Urn string `json:"urn"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.OperationName != "ResolveUrn" {
			t.Errorf("unexpected operation %q", body.OperationName)
		}
		sent, seen = body.Variables.Urn, true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`))
	}))
	defer srv.Close()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if _, err := ResolveNodeRef(cmd, graphql.NewClient(srv.URL, srv.Client()), memory, ref); err != nil {
		t.Fatalf("ResolveNodeRef(%q, %q): %v", memory, ref, err)
	}
	if !seen {
		// An empty capture would compare "" with "" and pass with nothing sent.
		t.Fatalf("ResolveNodeRef(%q, %q) never sent ResolveUrn", memory, ref)
	}
	return sent
}

func TestResolveNodeRef2300QualifiedIgnoresMemory(t *testing.T) {
	for _, ref := range qualifiedRefs2300 {
		want := resolveSent2300(t, "", ref)
		for _, mem := range memories2300 {
			if got := resolveSent2300(t, mem, ref); got != want {
				t.Errorf("-m %q recomposed %q: sent %q, want %q (as without -m)", mem, ref, got, want)
			}
		}
	}
}

func TestResolveNodeRef2300BareLocStillComposes(t *testing.T) {
	if got := resolveSent2300(t, "example.test:other", "demo"); got != "hrn:node:example.test:other:demo" {
		t.Errorf("bare loc + -m sent %q", got)
	}
}

func TestBatchNodeRef2300QualifiedIgnoresMemory(t *testing.T) {
	for _, ref := range qualifiedRefs2300 {
		want, err := BatchNodeRef("", ref)
		if err != nil {
			t.Fatalf("BatchNodeRef(\"\", %q): %v", ref, err)
		}
		for _, mem := range memories2300 {
			got, err := BatchNodeRef(mem, ref)
			if err != nil || got != want {
				t.Errorf("-m %q recomposed %q: got %q, %v; want %q (as without -m)", mem, ref, got, err, want)
			}
		}
	}
	if got, err := BatchNodeRef("example.test:other", "demo"); err != nil || got != "hrn:node:example.test:other:demo" {
		t.Errorf("bare loc + -m: got %q, %v", got, err)
	}
}
