package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

// What an old server that still has nodeExport(id: ID!) answers to the new document: GraphQL
// validation reports EVERY error of one document in one response, so the unknown `ref`
// argument arrives with the required-`id` error (F4, #3147). Neither is a missing FIELD.
const oldServerBothErrors2899 = `{"errors":[` +
	`{"message":"Unknown argument \"ref\" on field \"Query.nodeExport\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}},` +
	`{"message":"Field \"nodeExport\" argument \"id\" of type \"ID!\" is required, but it was not provided.","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`

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
			// The VALUE flows from the resolve step, not from the typed ref:
			// an empty or echoed-typed-URN value must fail (Jonas's F3 on #2918).
			if got, ok := sent[0].Vars["ref"].(string); !ok || got != "n1" {
				t.Errorf("the ref variable carries the resolved node id n1, got %v", sent[0].Vars["ref"])
			}
			// The sectioned document carries the mapped section values; the
			// canonical one sends none (the canonical render is full: true).
			if name == "sectioned" {
				got, _ := sent[0].Vars["sections"].([]any)
				if len(got) != 1 || got[0] != "CONTENT" {
					t.Errorf("the sections variable carries the mapped values, got %v", sent[0].Vars["sections"])
				}
			} else if _, has := sent[0].Vars["sections"]; has {
				t.Errorf("the canonical document sends no sections: %v", sent[0].Vars)
			}
		})
	}
}

// The server's one refusal for a missing, soft-deleted, unreadable or concealed node
// (NODE_NOT_FOUND, one uniform message from the server) is exit 4 with the message the
// old NOT_FOUND code gave, the CLI adds no text of its own, and it makes no follow-up
// read to tell the cases apart (cor:api:040:03, :140:03): the whole conversation is the
// resolve and the one export document. Same on every export path. A null export answer
// (not a shape a conforming server sends: nodeExport is NodeExportResult!, so this is a
// defensive path) keeps the same exit.
func TestNodeExportNodeNotFoundIsOneAnswer2899(t *testing.T) {
	refusal := func(code string) string {
		return `{"errors":[{"message":"node not found","extensions":{"code":"` + code + `"}}]}`
	}
	for name, tc := range map[string]struct {
		args []string
		op   string
	}{
		"canonical":      {[]string{"node", "export", nodeURN}, "NodeExport"},
		"sectioned":      {[]string{"node", "export", nodeURN, "--section", "content"}, "NodeExportSections"},
		"to a file (-o)": {[]string{"node", "export", nodeURN, "-o", "OUT"}, "NodeExport"},
	} {
		t.Run(name, func(t *testing.T) {
			// existing: the -o target already holds a file the refusal must leave alone (F5).
			run := func(resp string, existing bool) (*[]attnCall, error) {
				srv, calls := attnServer(t, map[string]string{"ResolveUrn": resolveNodeJSON, "NodeExport": resp, "NodeExportSections": resp})
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				args := append([]string{}, tc.args...)
				var outPath string
				for i, a := range args {
					if a == "OUT" {
						outPath = filepath.Join(t.TempDir(), "out.md")
						args[i] = outPath
					}
				}
				if outPath != "" && existing {
					if err := os.WriteFile(outPath, []byte("keep me\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				root.SetArgs(append(args, "--server", srv.URL))
				err := root.Execute()
				if outPath != "" {
					got, rerr := os.ReadFile(outPath)
					switch {
					case existing && (rerr != nil || string(got) != "keep me\n"):
						t.Errorf("a refusal must leave an existing -o file as it was: %q, %v", got, rerr)
					case !existing && rerr == nil:
						t.Errorf("a refusal must leave no -o file behind, found %q", got)
					}
				}
				return calls, err
			}
			// The old code runs against an existing target, the new one against none: both
			// halves of the F5 claim (preserved / not created) ride along on the one pair.
			_, oldErr := run(refusal("NOT_FOUND"), true)
			calls, newErr := run(refusal("NODE_NOT_FOUND"), false)
			if oldErr == nil || newErr == nil {
				t.Fatalf("a missing node refuses: old=%v new=%v", oldErr, newErr)
			}
			if exitCodeFor(newErr) != exitcode.NotFound || exitCodeFor(newErr) != exitCodeFor(oldErr) {
				t.Errorf("NODE_NOT_FOUND exits %d, want %d (what NOT_FOUND gave)", exitCodeFor(newErr), exitCodeFor(oldErr))
			}
			if newErr.Error() != oldErr.Error() {
				t.Errorf("one message: NODE_NOT_FOUND %q vs NOT_FOUND %q", newErr, oldErr)
			}
			// The same negative set as the resolve-step test, `concealed` included (F7).
			for _, cause := range []string{"soft-deleted", "unreadable", "concealed"} {
				if strings.Contains(newErr.Error(), cause) {
					t.Errorf("no cause the CLI invents (%s): %v", cause, newErr)
				}
			}
			if !strings.Contains(newErr.Error(), "node not found") {
				t.Errorf("the server's one message: %v", newErr)
			}
			var ops []string
			for _, c := range *calls {
				ops = append(ops, c.Op)
			}
			if strings.Join(ops, ",") != "ResolveUrn,"+tc.op {
				t.Errorf("no follow-up read to tell the cases apart: the conversation is %v, want [ResolveUrn %s]", ops, tc.op)
			}
		})
	}
	srv, _ := attnServer(t, map[string]string{"ResolveUrn": resolveNodeJSON, "NodeExport": `{"data":{"nodeExport":null}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "export", nodeURN, "--server", srv.URL})
	if err := root.Execute(); err == nil || exitCodeFor(err) != exitcode.NotFound {
		t.Errorf("a null answer exits %d, want %d: %v", exitCodeFor(err), exitcode.NotFound, err)
	}
}

// A node the resolve step cannot find (the node is missing, soft-deleted or concealed before
// nodeExport is ever sent) ends the same way: exit 4, no export request, no cause named, and
// the same lead message as the export refusal, so "one answer" holds in words and not only in
// the exit code (F7, #3147). The CLI still resolves the ref first (cmdutil.ResolveNodeRef);
// passing a URN straight to nodeExport(ref:) is a possible follow-up, not this companion.
func TestNodeExportANodeTheResolveStepCannotFindIsTheSameExit2899(t *testing.T) {
	srv, calls := attnServer(t, map[string]string{"ResolveUrn": resolveNullJSON, "NodeExport": nodeExportResp("MD", "text/markdown", "f.md", "# x\n")})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "export", nodeURN, "--server", srv.URL})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("a node the resolve step cannot find exits %d, want %d: %v", exitCodeFor(err), exitcode.NotFound, err)
	}
	if strings.Contains(err.Error(), "soft-deleted") || strings.Contains(err.Error(), "unreadable") || strings.Contains(err.Error(), "concealed") {
		t.Errorf("no cause is named: %v", err)
	}
	if len(exportCalls2899(calls)) != 0 {
		t.Errorf("no export request is sent for a node that does not resolve")
	}

	// The export step's refusal for the same node, on a server that does resolve it.
	srv2, _ := attnServer(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"NodeExport": `{"errors":[{"message":"node not found","extensions":{"code":"NODE_NOT_FOUND"}}]}`,
	})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"node", "export", nodeURN, "--server", srv2.URL})
	refusal := root2.Execute()
	if refusal == nil || exitCodeFor(refusal) != exitcode.NotFound {
		t.Fatalf("the export refusal exits %d, want %d: %v", exitCodeFor(refusal), exitcode.NotFound, refusal)
	}
	// The resolve miss is the CLI's own answer and adds a how-to-verify hint after the
	// same lead ("node not found"); the export refusal is the server's bare message. One
	// answer in words: the same lead text, and neither names a cause.
	if !strings.HasPrefix(err.Error(), refusal.Error()) {
		t.Errorf("one answer in text too: resolve miss %q must lead with the export refusal %q", err, refusal)
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
		"NodeExport": oldServerBothErrors2899,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "export", nodeURN, "--server", srv.URL})
	err := root.Execute()
	// The response carries BOTH the unknown `ref` argument and the required-`id` error; the
	// answer is the one out-of-sync refusal, never "too old" (the field exists, F4 #3147).
	if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "out of sync") || strings.Contains(err.Error(), "too old") {
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

// exportOverHTTP400Server2899 serves validation failures the way Apollo does, HTTP 400 with the
// error envelope, for the NodeExport and NodeExportSections operations ONLY (it counts the
// attempts atomically: the handler runs on the server's goroutines). ResolveUrn gets the
// resolve response; any OTHER operation is refused, so a request the test did not plan for
// cannot be quietly answered with a resolve response (F6, #3147).
func exportOverHTTP400Server2899(t *testing.T, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var exportAttempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.OperationName {
		case "NodeExport", "NodeExportSections":
			exportAttempts.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(body))
		case "ResolveUrn":
			_, _ = w.Write([]byte(resolveNodeJSON))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unplanned operation","extensions":{"code":"BAD_USER_INPUT"}}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &exportAttempts
}

// The catch-all above answers only its planned operations: an operation it was not given is
// refused, not answered with the resolve response (F6, #3147).
func TestExportOverHTTP400ServerRefusesAnUnplannedOperation2899(t *testing.T) {
	srv, attempts := exportOverHTTP400Server2899(t, oldServerBothErrors2899)
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{"operationName":"SomethingElse","query":"{ x }"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an unplanned operation is refused (400), got %d", resp.StatusCode)
	}
	if attempts.Load() != 0 {
		t.Errorf("an unplanned operation is not an export attempt: %d", attempts.Load())
	}
}

// Apollo answers a validation error with HTTP 400. An old server's "Unknown argument ref on
// field Query.nodeExport" is then NOT "the server has no nodeExport field" (it has one, under
// the old argument name): the CLI must say the two are out of sync, not that the server is too
// old to render exports and needs hadron-server #386 (Cooper's P3 on #2918). The server also
// reports the required `id` it was not given (F4), and a lone unknown `sections` argument on
// the sectioned document takes the same generic answer (F2 remainder, #3147).
func TestNodeExportUnknownRefArgumentOverHTTP400IsOutOfSyncNotTooOld2899(t *testing.T) {
	loneSections := `{"errors":[{"message":"Unknown argument \"sections\" on field \"Query.nodeExport\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	for name, tc := range map[string]struct {
		args []string
		body string
	}{
		"canonical":                    {[]string{"node", "export", nodeURN}, oldServerBothErrors2899},
		"sectioned":                    {[]string{"node", "export", nodeURN, "--section", "content"}, oldServerBothErrors2899},
		"sectioned, lone sections arg": {[]string{"node", "export", nodeURN, "--section", "content"}, loneSections},
	} {
		t.Run(name, func(t *testing.T) {
			srv, exportAttempts := exportOverHTTP400Server2899(t, tc.body)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", srv.URL))
			err := root.Execute()
			if err == nil || exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("an old server fails loud (exit 2): exit=%d err=%v", exitCodeFor(err), err)
			}
			if !strings.Contains(err.Error(), "out of sync") {
				t.Errorf("an unknown ARGUMENT is a version mismatch: %v", err)
			}
			if strings.Contains(err.Error(), "too old") || strings.Contains(err.Error(), "#386") || strings.Contains(err.Error(), "no nodeExport field") {
				t.Errorf("the server HAS nodeExport; it must not be called too old to have it: %v", err)
			}
			if got := exportAttempts.Load(); got != 1 {
				t.Errorf("one attempt, no retry with the old name: %d", got)
			}
		})
	}
}

// A validation response that batches the unknown `ref` argument WITH a real "Cannot query
// field" (GraphQL validation reports every error of one document in a single response —
// here an intermediate server that has nodeExport(id:) and lacks the nodeExport field the
// document selects) still takes the too-old branch: a missing FIELD is the discriminator, and
// the unknown-argument exclusion must not mask it (Jonas's F2 on #2918). The fixture's missing
// field is nodeExport itself, so the match is keyed to the field error, not to the
// unknown-argument message that also names Query.nodeExport (Delta F1, #3147).
func TestNodeExportBatchedValidationKeepsTheTooOldAnswer2899(t *testing.T) {
	srv, calls := attnServer(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"NodeExport": `{"errors":[{"message":"Unknown argument \"ref\" on field \"Query.nodeExport\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}},{"message":"Cannot query field \"nodeExport\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "export", nodeURN, "--server", srv.URL})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "too old to render exports server-side") {
		t.Fatalf("a batched response carrying a real missing field is the too-old answer (exit 2): exit=%d err=%v", exitCodeFor(err), err)
	}
	if len(exportCalls2899(calls)) != 1 {
		t.Fatalf("one attempt, no retry: %d", len(exportCalls2899(calls)))
	}
}
