package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2899 (companion to hadron-server#4763, entity refs #4139): the server renames
// Query.nodeExport(id:) to nodeExport(ref:) with no alias, and answers a missing,
// soft-deleted, unreadable or concealed node with ONE code, NODE_NOT_FOUND. The CLI
// sends ref: on both export documents, and the miss is one answer and one exit.

func exportCalls2899(calls *[]attnCall) []attnCall {
	var out []attnCall
	for _, c := range *calls {
		if c.Op == "NodeExport" || c.Op == "NodeExportSections" {
			out = append(out, c)
		}
	}
	return out
}

// Both the canonical and the presentation (sectioned) export send nodeExport(ref:) and a
// ref variable, never nodeExport(id:).
func TestNodeExportSendsRefNotID2899(t *testing.T) {
	for name, args := range map[string][]string{
		"canonical": {"node", "export", nodeURN},
		"sectioned": {"node", "export", nodeURN, "--section", "content"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{
				"ResolveUrn":         resolveNodeJSON,
				"NodeExport":         nodeExportResp("MD", "text/markdown", "f.md", "# x\n"),
				"NodeExportSections": nodeExportResp("MD", "text/markdown", "f.md", "# x\n"),
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(args, "--server", srv.URL))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			sent := exportCalls2899(calls)
			if len(sent) != 1 {
				t.Fatalf("one export operation expected, got %d", len(sent))
			}
			q := strings.Join(strings.Fields(sent[0].Query), " ")
			if !strings.Contains(q, "nodeExport(ref: $ref") || strings.Contains(q, "nodeExport(id:") {
				t.Errorf("the export must call nodeExport(ref: …) and never nodeExport(id: …): %s", q)
			}
			if _, has := sent[0].Vars["ref"]; !has {
				t.Errorf("the ref variable is sent: %v", sent[0].Vars)
			}
			if _, has := sent[0].Vars["id"]; has {
				t.Errorf("no id variable: %v", sent[0].Vars)
			}
		})
	}
}

// The server's one refusal for a missing, soft-deleted, unreadable or concealed node
// (NODE_NOT_FOUND, one uniform message from the server) is exit 4 with the message the
// old NOT_FOUND code gave, the CLI adds no text of its own, and it makes no follow-up
// read to tell the cases apart (cor:api:040:03, :140:03). The null-answer path
// ("node is not readable") keeps the same exit.
func TestNodeExportNodeNotFoundIsOneAnswer2899(t *testing.T) {
	refusal := func(code string) string {
		return `{"errors":[{"message":"node not found","extensions":{"code":"` + code + `"}}]}`
	}
	run := func(t *testing.T, resp string) (*[]attnCall, error) {
		srv, calls := attnServer(t, map[string]string{"ResolveUrn": resolveNodeJSON, "NodeExport": resp})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"node", "export", nodeURN, "--server", srv.URL})
		err := root.Execute()
		return calls, err
	}
	_, oldErr := run(t, refusal("NOT_FOUND"))
	calls, newErr := run(t, refusal("NODE_NOT_FOUND"))
	if oldErr == nil || newErr == nil {
		t.Fatalf("a missing node refuses: old=%v new=%v", oldErr, newErr)
	}
	if exitCodeFor(newErr) != exitcode.NotFound || exitCodeFor(newErr) != exitCodeFor(oldErr) {
		t.Errorf("NODE_NOT_FOUND exits %d, want %d (what NOT_FOUND gave)", exitCodeFor(newErr), exitCodeFor(oldErr))
	}
	if newErr.Error() != oldErr.Error() {
		t.Errorf("one message: NODE_NOT_FOUND %q vs NOT_FOUND %q", newErr, oldErr)
	}
	if !strings.Contains(newErr.Error(), "node not found") || strings.Contains(newErr.Error(), "soft-deleted") || strings.Contains(newErr.Error(), "unreadable") {
		t.Errorf("the server's one message, and no cause the CLI invents: %v", newErr)
	}
	for _, c := range *calls {
		if c.Op == "NodeExportMeta" || c.Op == "GetNode" {
			t.Errorf("no follow-up read to tell the cases apart: %s", c.Op)
		}
	}
	// A null answer (today's other not-found shape) keeps the same exit.
	_, nullErr := run(t, `{"data":{"nodeExport":null}}`)
	if nullErr == nil || exitCodeFor(nullErr) != exitcode.NotFound {
		t.Errorf("a null answer exits %d, want %d: %v", exitCodeFor(nullErr), exitcode.NotFound, nullErr)
	}
}

// Against a server that still has nodeExport(id:), the new CLI fails loud: the server
// rejects the unknown ref argument at validation, nothing executes, and the CLI says the
// two are out of sync (exit 2). It does NOT retry with the old name: there is no
// fallback, so there is no silent success and no guessing at support (cor:api:220:01).
// The release order (the server first, then the CLI in step) is the release hold's.
func TestNodeExportOnAServerThatStillHasIDFailsLoudWithoutAFallback2899(t *testing.T) {
	srv, calls := attnServer(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"NodeExport": `{"errors":[{"message":"Unknown argument \"ref\" on field \"Query.nodeExport\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "export", nodeURN, "--server", srv.URL})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "out of sync") {
		t.Fatalf("an old server fails loud as a version mismatch (exit 2): exit=%d err=%v", exitCodeFor(err), err)
	}
	sent := exportCalls2899(calls)
	if len(sent) != 1 {
		t.Fatalf("one attempt and no retry, got %d", len(sent))
	}
	if strings.Contains(sent[0].Query, "nodeExport(id:") {
		t.Errorf("never falls back to the old argument name: %s", sent[0].Query)
	}
}
