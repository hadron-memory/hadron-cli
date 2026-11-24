package cmdutil

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

// cli#3000: ComposesWithMemory re-states ResolveNodeRef's branch order for callers that resolve
// several refs against one -m (edge add reads a memory ID only when an endpoint needs it). Nothing
// but this test ties the two together, so it drives the real ResolveNodeRef with a memory ID over
// one table of refs and requires that the memory is read exactly when ComposesWithMemory says the
// ref composes: a new branch in ResolveNodeRef that composes (or stops composing) a ref, without
// the helper following, fails here.
func TestComposesWithMemoryAgreesWithResolveNodeRef3000(t *testing.T) {
	const memID = "01a1d9c2453a782189ceeafb18c37d61"
	for _, ref := range []string{
		"sheds:rolling-checkpoint",
		"start-here",
		"a:b:c:d",
		"01a1d9c2453a782189ceeafb18c37d62",
		"hrn:node:example.test:source:demo",
		"urn:node:example.test:source:demo",
		"example.test::source::demo",
		"example.test::juno:app-mem:ops::demo",
		"hrn:action:example.test:agent:task.finish.open-children",
		"urn:action:example.test:agent:task.finish.open-children",
	} {
		var reads atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				OperationName string `json:"operationName"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "application/json")
			if body.OperationName == "GetMemory" {
				reads.Add(1)
				_, _ = w.Write([]byte(`{"data":{"memory":{"id":"` + memID + `","urn":"hrn:mem:example.test:other","name":"o","isEncrypted":false}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`))
		}))
		cmd := &cobra.Command{}
		cmd.SetContext(context.Background())
		_, _ = ResolveNodeRef(cmd, graphql.NewClient(srv.URL, srv.Client()), memID, ref)
		srv.Close()
		read := reads.Load()
		if want := ComposesWithMemory(ref); (read == 1) != want || read > 1 {
			t.Errorf("ref %q: ResolveNodeRef read the -m memory %d time(s) but ComposesWithMemory = %v", ref, read, want)
		}
	}
}
