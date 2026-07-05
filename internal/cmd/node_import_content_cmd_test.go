package cmd

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// importNodeResp builds an ImportNode GraphQL response (sync v1: STORED + the
// stored node, no jobId).
func importNodeResp(loc, name, nodeType string) string {
	return `{"data":{"importNode":{"status":"STORED","jobId":null,"node":{` +
		`"id":"n1","memoryId":"mem1","loc":"` + loc + `","name":"` + name + `",` +
		`"nodeType":"` + nodeType + `","updatedAt":"2026-06-11T00:00:00Z"}}}}`
}

// ingestVars is the decoded request-variable shape of an ImportNode call.
type ingestVars struct {
	Input struct {
		MemoryId    *string `json:"memoryId"`
		Loc         *string `json:"loc"`
		NodeUrn     *string `json:"nodeUrn"`
		Url         *string `json:"url"`
		Content     *string `json:"content"`
		ContentType *string `json:"contentType"`
		Name        *string `json:"name"`
		NodeType    *string `json:"nodeType"`
	} `json:"input"`
}

func decodeIngestVars(t *testing.T, raw json.RawMessage) ingestVars {
	t.Helper()
	var v ingestVars
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode ImportNode vars: %v\n%s", err, raw)
	}
	return v
}

// A .pdf routes to content mode automatically (it can never be an export doc).
// A PDF is binary and reaches the server base64-encoded; the CLI does the
// encoding, so the caller just points at the file.
func TestNodeImportPDFBase64EncodesAndSetsContentType(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ImportNode": importNodeResp("papers:attention", "Attention Is All You Need", "info"),
	})
	f, out := testFactory(t)
	pdfBytes := []byte("%PDF-1.4\n\x00\x01binary\xff body")
	file := filepath.Join(t.TempDir(), "paper.pdf")
	if err := os.WriteFile(file, pdfBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", file, "-m", "acme.com:kb", "--loc", "papers:attention", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	vars := decodeIngestVars(t, captured["ImportNode"])
	if vars.Input.ContentType == nil || *vars.Input.ContentType != "application/pdf" {
		t.Errorf("contentType = %v, want application/pdf", vars.Input.ContentType)
	}
	want := base64.StdEncoding.EncodeToString(pdfBytes)
	if vars.Input.Content == nil || *vars.Input.Content != want {
		t.Errorf("content must be base64 of the PDF bytes:\n got: %v\nwant: %s", vars.Input.Content, want)
	}
	if vars.Input.MemoryId == nil || *vars.Input.MemoryId != "acme.com:kb" || vars.Input.Loc == nil || *vars.Input.Loc != "papers:attention" {
		t.Errorf("target = memory %v loc %v", vars.Input.MemoryId, vars.Input.Loc)
	}
	if vars.Input.Url != nil {
		t.Errorf("url must be omitted on the inline path, got %v", *vars.Input.Url)
	}

	var summary struct {
		Mode     string `json:"mode"`
		Status   string `json:"status"`
		Memory   string `json:"memory"`
		Loc      string `json:"loc"`
		NodeType string `json:"nodeType"`
	}
	if err := json.Unmarshal([]byte(out.String()), &summary); err != nil {
		t.Fatalf("summary not JSON: %v\n%s", err, out.String())
	}
	if summary.Mode != "content" || summary.Status != "STORED" || summary.NodeType != "info" || summary.Loc != "papers:attention" || summary.Memory != "acme.com:kb" {
		t.Errorf("summary = %+v", summary)
	}
}

// An ambiguous .md defaults to restore; --as-content forces the content path and
// infers text/markdown, passing the body through verbatim (no base64).
func TestNodeImportContentFlagInfersMarkdown(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ImportNode": importNodeResp("notes:today", "Today", "webpage"),
	})
	f, _ := testFactory(t)
	const body = "# Today\n\nSome notes.\n"
	file := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", file, "--as-content", "-m", "acme.com:kb", "--loc", "notes:today", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := decodeIngestVars(t, captured["ImportNode"])
	if vars.Input.ContentType == nil || *vars.Input.ContentType != "text/markdown" {
		t.Errorf("contentType = %v, want text/markdown", vars.Input.ContentType)
	}
	if vars.Input.Content == nil || *vars.Input.Content != body {
		t.Errorf("content = %v, want verbatim %q", vars.Input.Content, body)
	}
}

// The --url path sends the url and no content; contentType is server-side (HTML).
func TestNodeImportURL(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ImportNode": importNodeResp("clips:post", "A Post", "webpage"),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "--url", "https://example.com/post", "-m", "acme.com:kb", "--loc", "clips:post", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := decodeIngestVars(t, captured["ImportNode"])
	if vars.Input.Url == nil || *vars.Input.Url != "https://example.com/post" {
		t.Errorf("url = %v", vars.Input.Url)
	}
	if vars.Input.Content != nil || vars.Input.ContentType != nil {
		t.Errorf("url path must omit content/contentType: content=%v contentType=%v", vars.Input.Content, vars.Input.ContentType)
	}
}

// --content-type selects content mode and overrides inference — e.g. a PDF
// piped over stdin, where nothing is inferable from a pipe.
func TestNodeImportStdinPDFViaContentTypeFlag(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ImportNode": importNodeResp("papers:x", "X", "info"),
	})
	f, _ := testFactory(t)
	pdfBytes := []byte("%PDF-1.7\x00rawbytes")
	f.IOStreams.In = strings.NewReader(string(pdfBytes))
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "-", "-m", "acme.com:kb", "--loc", "papers:x", "--content-type", "application/pdf", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := decodeIngestVars(t, captured["ImportNode"])
	if vars.Input.Content == nil || *vars.Input.Content != base64.StdEncoding.EncodeToString(pdfBytes) {
		t.Errorf("stdin PDF must be base64-encoded, got %v", vars.Input.Content)
	}
}

// --node targets by URN (content mode); a .html file routes to content and
// infers text/html.
func TestNodeImportContentNodeURNTarget(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ImportNode": importNodeResp("clips:example", "Example", "webpage"),
	})
	f, _ := testFactory(t)
	file := filepath.Join(t.TempDir(), "capture.html")
	if err := os.WriteFile(file, []byte("<p>hi</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", file, "--node", nodeURN, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := decodeIngestVars(t, captured["ImportNode"])
	if vars.Input.NodeUrn == nil || *vars.Input.NodeUrn != nodeURN {
		t.Errorf("nodeUrn = %v", vars.Input.NodeUrn)
	}
	if vars.Input.MemoryId != nil || vars.Input.Loc != nil {
		t.Errorf("--node must not send memoryId/loc: %v %v", vars.Input.MemoryId, vars.Input.Loc)
	}
	if vars.Input.ContentType == nil || *vars.Input.ContentType != "text/html" {
		t.Errorf("contentType = %v, want text/html", vars.Input.ContentType)
	}
}

func TestNodeImportContentSourceXORIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	// Both a file and --url.
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "some.pdf", "--url", "https://x", "-m", "acme.com:kb", "--loc", "x", "--server", "http://127.0.0.1:1"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "exactly one source") {
		t.Fatalf("both sources should be a usage error, got %v", err)
	}
	// --as-content forces content mode but no source given.
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"node", "import", "--as-content", "-m", "acme.com:kb", "--loc", "x", "--server", "http://127.0.0.1:1"})
	if err := root2.Execute(); err == nil || !strings.Contains(err.Error(), "exactly one source") {
		t.Fatalf("no source should be a usage error, got %v", err)
	}
}

func TestNodeImportContentTargetXORIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	// --node together with -m.
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "--url", "https://x", "--node", nodeURN, "-m", "acme.com:kb", "--server", "http://127.0.0.1:1"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("--node with -m should be a usage error, got %v", err)
	}
	// Neither target form.
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"node", "import", "--url", "https://x", "--server", "http://127.0.0.1:1"})
	if err := root2.Execute(); err == nil || !strings.Contains(err.Error(), "no target") {
		t.Fatalf("missing target should be a usage error, got %v", err)
	}
}

// A restore-only flag in content mode fails loudly rather than being ignored.
func TestNodeImportContentRejectsRestoreFlags(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "paper.pdf", "--with-edges", "-m", "acme.com:kb", "--loc", "x", "--server", "http://127.0.0.1:1"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "with-edges") {
		t.Fatalf("--with-edges in content mode should be a usage error, got %v", err)
	}
}

// A restore-mode import (an export file with no content type) must NOT put
// contentType on the wire: the server reads an explicit null as "clear", so an
// unset field has to be omitted, not serialized as null (Codex PR-139 P2).
func TestNodeImportRestoreOmitsContentType(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	file := filepath.Join(t.TempDir(), "flaky.md")
	if err := os.WriteFile(file, []byte("---\nname: Flaky CI\nloc: findings:flaky-ci\nmemory: acme.com:kb\n---\n\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", file, "--create-only", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(captured["CreateNode"], &vars); err != nil {
		t.Fatalf("decode CreateNode vars: %v\n%s", err, captured["CreateNode"])
	}
	if _, present := vars.Input["contentType"]; present {
		t.Errorf("restore write must omit contentType from the wire, got input %v", vars.Input)
	}
}
