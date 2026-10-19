package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#916 C3a: every read that feeds a stored-text consumer (export, the
// abstract fingerprint, a body written back) says content(raw: true) on the
// wire. Since hadron-server#2118 a plain node read RESOLVES include tags
// (cor:dmo:150:03), and only nodeBatch defaults to stored text (R21 A); the
// explicit argument is what keeps these callers stored-text regardless of
// which default a surface has.

const rawArg = "content(raw: true)"

// The include tag as the server parses it ({{> ref}}), and what a reader
// would see once it resolved.
const (
	c3aStored   = "Intro {{> hrn:node:acme.com:kb:shared}} body."
	c3aResolved = "Intro Shared text, spliced in. body."
)

// rawAwareGraphQL answers like a server whose default for `content` is the
// RESOLVED reader view: it returns stored text only to a query that asks for
// it explicitly. respond builds the body for an operation given the content
// the query earned. It records each operation's query text.
func rawAwareGraphQL(t *testing.T, respond func(op, content string) (string, bool)) (*httptest.Server, map[string]string) {
	t.Helper()
	var mu sync.Mutex
	queries := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string `json:"operationName"`
			Query         string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		queries[body.OperationName] = body.Query
		mu.Unlock()
		content := c3aResolved
		if strings.Contains(body.Query, rawArg) {
			content = c3aStored
		}
		resp, ok := respond(body.OperationName, content)
		if !ok {
			resp, ok = unstubbedDefault(body.OperationName)
		}
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(translateFindNodes(body.OperationName, resp)))
	}))
	t.Cleanup(server.Close)
	return server, queries
}

func c3aJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Pin 1, by operation: the documents genqlient sends. Every caller of an
// operation sends this same document, so this covers each caller family —
// NodeBatch (memory export, spec get/grep/lint/replace/citations/checktools,
// node getbatch, coding review, skill lint), GetNodeRaw (node get --raw,
// node update, coding preflight) and the spec raw reads (spec edit, and
// withRawBody under spec supersede).
func TestC3aStoredReadOperationsStateRawContent(t *testing.T) {
	ops := map[string]string{
		"NodeBatch":          gen.NodeBatch_Operation,
		"GetNodeRaw":         gen.GetNodeRaw_Operation,
		"GetSpecNodeRaw":     gen.GetSpecNodeRaw_Operation,
		"GetSpecNodeForEdit": gen.GetSpecNodeForEdit_Operation,
	}
	for name, doc := range ops {
		if !strings.Contains(doc, rawArg) {
			t.Errorf("%s must select %s explicitly; document:\n%s", name, rawArg, doc)
		}
	}
	// The reader view stays the reader view: plain GetNode is the display
	// read and must keep resolving.
	if strings.Contains(gen.GetNode_Operation, "content(raw") {
		t.Errorf("GetNode is the resolved reader view; it must not pin raw:\n%s", gen.GetNode_Operation)
	}
}

// Pin 1, on the wire, for the two command families a reader runs directly.
func TestC3aRawArgumentOnTheWire(t *testing.T) {
	t.Run("node get --raw", func(t *testing.T) {
		gql, queries := rawAwareGraphQL(t, func(op, content string) (string, bool) {
			if op == "ResolveUrn" {
				return resolveNodeJSON, true
			}
			if op != "GetNodeRaw" {
				return "", false
			}
			return `{"data":{"node":{"id":"n1","urn":"hrn:node:acme.com:kb:intro","portalUrl":null,"memoryId":"m1","loc":"intro","name":"Intro","description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"info","tags":[],"content":` + c3aJSON(content) + `,"data":null,"properties":null,"seq":null,"createdAt":"2026-10-19T00:00:00Z","updatedAt":"2026-10-19T00:00:00Z","outgoingEdges":[],"incomingEdges":[]}}}`, true
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"node", "get", "hrn:node:acme.com:kb:intro", "--raw", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(queries["GetNodeRaw"], rawArg) {
			t.Errorf("node get --raw sent no %s:\n%s", rawArg, queries["GetNodeRaw"])
		}
		if !strings.Contains(out.String(), "{{> hrn:node:acme.com:kb:shared}}") {
			t.Errorf("--raw must print the stored include tag:\n%s", out.String())
		}
	})
}

const c3aMem = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func c3aBatch(content string) string {
	return `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` +
		`{"id":"n-root","memoryId":"` + c3aMem + `","loc":"root","name":"Root","alias":null,"nodeType":"info","description":null,"abstract":null,"abstractOriginHash":null,"tags":[],"seq":null,"data":null,"properties":null,"content":"Root.","outgoingEdges":[]},` +
		`{"id":"n-intro","memoryId":"` + c3aMem + `","loc":"guide:intro","name":"Intro","alias":null,"nodeType":"info","description":null,"abstract":null,"abstractOriginHash":null,"tags":[],"seq":null,"data":null,"properties":null,"content":` + c3aJSON(content) + `,"outgoingEdges":[]}` +
		`]}}}`
}

// Pin 4: a restore export keeps the include tag byte for byte, and importing
// that export writes the same bytes back. (The body has no edge whitespace:
// node import trims it, which is a separate, pre-existing behaviour.)
func TestC3aExportImportRoundTripKeepsIncludeTag(t *testing.T) {
	const nodesResp = `{"data":{"nodes":[` +
		`{"id":"n-root","memoryId":"` + c3aMem + `","loc":"root","name":"Root","nodeType":"info","tags":[],"updatedAt":"2026-10-19T00:00:00Z"},` +
		`{"id":"n-intro","memoryId":"` + c3aMem + `","loc":"guide:intro","name":"Intro","nodeType":"info","tags":[],"updatedAt":"2026-10-19T00:00:00Z"}]}}`
	gql, queries := rawAwareGraphQL(t, func(op, content string) (string, bool) {
		switch op {
		case "FindNodes":
			return nodesResp, true
		case "NodeBatch":
			return c3aBatch(content), true
		}
		return "", false
	})
	f, _ := testFactory(t)
	dir := t.TempDir()
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "export", c3aMem, "--out", dir, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.Contains(queries["NodeBatch"], rawArg) {
		t.Errorf("memory export's NodeBatch sent no %s:\n%s", rawArg, queries["NodeBatch"])
	}
	file := filepath.Join(dir, "guide", "intro.md")
	exported, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(exported), c3aStored) {
		t.Fatalf("export must carry the stored include tag:\n%s", exported)
	}

	// Import the exported file into a target and check the bytes it writes.
	imp, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNullJSON,
		"UpdateNode": `{"errors":[{"message":"node not found","extensions":{"code":"NODE_NOT_FOUND"}}]}`,
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
	})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"node", "import", file, "-m", "acme.com:kb", "--loc", "guide:intro", "--json", "--server", imp.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("import: %v", err)
	}
	var v struct {
		Input struct {
			Content string `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(captured["CreateNode"], &v); err != nil {
		t.Fatalf("CreateNode vars: %v (%s)", err, captured["CreateNode"])
	}
	if v.Input.Content != c3aStored {
		t.Errorf("import wrote %q, want the stored bytes %q", v.Input.Content, c3aStored)
	}
}

// Pin 5: the abstract fingerprint (spec 032; the approval-hash rule hashes
// the stored text) is checked against STORED text. Fed the resolved view, a
// clean spec would read abstract-stale.
func TestC3aAbstractFingerprintIsOverStoredText(t *testing.T) {
	const loc = "msg:010:01"
	node := func(content string) string {
		return fmt.Sprintf(`{"id":"id-%s","memoryId":"mem1","loc":%q,"name":"%s — W2","alias":null,"nodeType":"info",`+
			`"description":null,"abstract":"Win back users who never engaged after signup.","abstractOriginHash":%q,`+
			`"tags":["spec","p1"],"seq":null,"data":{"version":"0.0.1"},"properties":null,"content":%s,`+
			`"updatedAt":"2026-10-19T00:00:00Z","outgoingEdges":[{"label":"p1: W2","priority":0,"condition":null,"target":{"id":"f1","loc":"msg:010","memoryId":"mem1"}}],"incomingEdges":[]}`,
			loc, loc, loc, specOriginHash(c3aSpecStored), c3aJSON(content))
	}
	gql, queries := rawAwareGraphQL(t, func(op, content string) (string, bool) {
		switch op {
		case "FindNodes":
			return `{"data":{"nodes":[` + specNodeList(loc, `["spec","p1"]`) + `]}}`, true
		case "NodeBatch":
			body := c3aSpecResolved
			if content == c3aStored {
				body = c3aSpecStored
			}
			return `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` + node(body) + `]}}}`, true
		}
		return "", false
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "get", "--prefix", "msg:010", "-m", specMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(queries["NodeBatch"], rawArg) {
		t.Errorf("spec get's NodeBatch sent no %s", rawArg)
	}
	if strings.Contains(out.String(), "abstract-stale") {
		t.Errorf("a spec whose fingerprint matches its STORED body must not read stale:\n%s", out.String())

	}
}

const (
	c3aSpecStored   = "# msg:010:01 — W2\n\n## Definition\n{{> hrn:node:acme.com:kb:shared}}\n\n## Rule\nx\n\n## Durable vs tunable\nx\n\n## What invalidates this spec\nChanges.\n"
	c3aSpecResolved = "# msg:010:01 — W2\n\n## Definition\nShared text, spliced in.\n\n## Rule\nx\n\n## Durable vs tunable\nx\n\n## What invalidates this spec\nChanges.\n"
)

// Pin 6, compatibility: a server older than hadron-server#2118 rejects
// content(raw:) at validation. That maps through api.MapError's existing
// schema-skew path (direction-neutral upgrade advice, the server's own
// message kept) with no special casing here.
func TestC3aOlderServerRejectingRawMapsToSchemaSkew(t *testing.T) {
	const nodesResp = `{"data":{"nodes":[` +
		`{"id":"n-root","memoryId":"` + c3aMem + `","loc":"root","name":"Root","nodeType":"info","tags":[],"updatedAt":"2026-10-19T00:00:00Z"}]}}`
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes": nodesResp,
		"NodeBatch": `{"errors":[{"message":"Unknown argument \"raw\" on field \"Node.content\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "export", c3aMem, "--out", t.TempDir(), "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("an older server's validation refusal must fail the export")
	}
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Errorf("exit = %d, want the schema-skew mapping %d", code, exitcode.Usage)
	}
	for _, want := range []string{"out of sync", `Unknown argument "raw" on field "Node.content"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must carry %q:\n%v", want, err)
		}
	}
}
