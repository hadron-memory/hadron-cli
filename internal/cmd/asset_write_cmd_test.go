package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// uploadFixture stands up a fake object store plus the begin/complete GraphQL
// pair, and records what the PUT actually received.
type uploadFixture struct {
	gqlURL   string
	putBody  string
	putCT    string
	putAuth  string
	putLen   int64
	putCalls int
}

func newUploadFixture(t *testing.T, putStatus int) *uploadFixture {
	t.Helper()
	fx := &uploadFixture{}
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fx.putCalls++
		fx.putCT = r.Header.Get("Content-Type")
		fx.putAuth = r.Header.Get("Authorization")
		fx.putLen = r.ContentLength
		b, _ := io.ReadAll(r.Body)
		fx.putBody = string(b)
		w.WriteHeader(putStatus)
	}))
	t.Cleanup(store.Close)

	begin := `{"data":{"beginAssetUploadV2":{"uploadId":"up1","putUrl":"` + store.URL +
		`","putHeaders":[{"name":"Content-Type","value":"image/png"}],"storageKey":"k","maxSizeBytes":10485760,` +
		`"allowedMimeType":"image/png","expiresAt":"2026-08-06T01:00:00Z"}}}`
	complete := `{"data":{"completeAssetUpload":{"id":"a1","urn":"hrn:asset:acme.com:kb:assets:a1","filename":"logo.png",` +
		`"mimeType":"image/png","sizeBytes":7,"scanStatus":"PENDING","uploadedAt":"2026-08-06T00:00:00Z",` +
		`"memoryId":"mem1","publicUrl":null}}}`
	gql := fakeGraphQL(t, map[string]string{
		"GetMemory":           assetMemoryResp,
		"BeginAssetUpload":    begin,
		"CompleteAssetUpload": complete,
	})
	fx.gqlURL = gql.URL
	return fx
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAssetUploadSendsBytesWithServerHeadersAndNoCredentials(t *testing.T) {
	fx := newUploadFixture(t, http.StatusOK)
	src := writeTempFile(t, "logo.png", "PNGDATA")

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "upload", src, "-m", "acme.com::kb", "--server", fx.gqlURL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fx.putBody != "PNGDATA" {
		t.Errorf("PUT body = %q, want the file's bytes", fx.putBody)
	}
	// The presigned URL is its own authorization; the Hadron token must never
	// reach object storage.
	if fx.putAuth != "" {
		t.Errorf("the bearer token must not be sent to object storage; got %q", fx.putAuth)
	}
	// Only the headers the server handed back — inventing or dropping one
	// breaks the presigned signature.
	if fx.putCT != "image/png" {
		t.Errorf("the server's putHeaders must be sent verbatim; Content-Type = %q", fx.putCT)
	}
	// A chunked PUT breaks the signature, so the length must be explicit.
	if fx.putLen != 7 {
		t.Errorf("ContentLength = %d, want 7 (a chunked PUT breaks the signature)", fx.putLen)
	}
	var dto struct {
		ID         string `json:"id"`
		ScanStatus string `json:"scanStatus"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if dto.ID != "a1" || dto.ScanStatus != "PENDING" {
		t.Errorf("unexpected DTO: %+v", dto)
	}
}

func TestAssetUploadDoesNotCompleteWhenThePutFails(t *testing.T) {
	// Completing after a failed PUT would publish an asset whose bytes never
	// landed — a listable, downloadable, empty file.
	fx := newUploadFixture(t, http.StatusForbidden)
	src := writeTempFile(t, "logo.png", "PNGDATA")

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "upload", src, "-m", "acme.com::kb", "--server", fx.gqlURL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a rejected PUT must fail the upload")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("a 403 should hint at expiry; got %v", err)
	}
}

func TestAssetUploadRejectsADirectory(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "upload", t.TempDir(), "-m", "acme.com::kb"})
	err := root.Execute()
	if err == nil {
		t.Fatal("uploading a directory must be a usage error")
	}
	if got := exitcode.FromError(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", got, exitcode.Usage)
	}
}

func TestAssetUploadRequiresMemory(t *testing.T) {
	src := writeTempFile(t, "logo.png", "x")
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "upload", src})
	err := root.Execute()
	if err == nil {
		t.Fatal("upload without -m must be a usage error")
	}
	if got := exitcode.FromError(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", got, exitcode.Usage)
	}
}

func TestAssetUploadDeclaresMimeAndSizeBeforeSendingBytes(t *testing.T) {
	// The whole point of the three-step flow: the allowlist and size cap are
	// enforced on the reservation, so a rejected upload costs one round-trip.
	fx := newUploadFixture(t, http.StatusOK)
	_ = fx
	gql, reqs := captureGraphQL(t, map[string]string{
		"GetMemory":        assetMemoryResp,
		"BeginAssetUpload": `{"errors":[{"message":"MIME_NOT_ALLOWED: text/plain is not accepted by this memory"}]}`,
	})
	src := writeTempFile(t, "notes.txt", "hello")

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "upload", src, "-m", "acme.com::kb", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a rejected reservation must fail the upload")
	}
	if !strings.Contains(err.Error(), "MIME_NOT_ALLOWED") {
		t.Errorf("the server's typed rejection should surface verbatim; got %v", err)
	}
	vars := decodeVars(t, reqs, "BeginAssetUpload")
	if vars["mimeType"] != "text/plain" {
		t.Errorf("mimeType should be derived from the extension; got %v", vars["mimeType"])
	}
	if got := vars["sizeBytes"].(float64); int(got) != 5 {
		t.Errorf("sizeBytes should be the file's size; got %v", got)
	}
}

func TestAssetUploadMimeFlagOverridesTheExtension(t *testing.T) {
	gql, reqs := captureGraphQL(t, map[string]string{
		"GetMemory":        assetMemoryResp,
		"BeginAssetUpload": `{"errors":[{"message":"stop here"}]}`,
	})
	src := writeTempFile(t, "data", "{}")
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "upload", src, "-m", "acme.com::kb", "--mime", "application/json", "--name", "data.json", "--server", gql.URL})
	_ = root.Execute()
	vars := decodeVars(t, reqs, "BeginAssetUpload")
	if vars["mimeType"] != "application/json" {
		t.Errorf("--mime must win; got %v", vars["mimeType"])
	}
	if vars["filename"] != "data.json" {
		t.Errorf("--name must set the stored filename; got %v", vars["filename"])
	}
}

func TestAssetRmRequiresConfirmation(t *testing.T) {
	// Non-interactive without --yes must refuse rather than delete.
	gql := fakeGraphQL(t, map[string]string{
		"SoftDeleteAsset": `{"data":{"softDeleteAsset":{"id":"a1","urn":"u","filename":"logo.png","deletedAt":"2026-08-06T00:00:00Z"}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "rm", "a1", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("rm without --yes must not delete non-interactively")
	}
	if got := exitcode.FromError(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", got, exitcode.Usage)
	}
}

func TestAssetRmWithYesReportsHowToRestore(t *testing.T) {
	// The delete is reversible, so the output must say so — an operator who
	// reads "permanent" on a soft delete learns to distrust the prompts.
	gql := fakeGraphQL(t, map[string]string{
		"SoftDeleteAsset": `{"data":{"softDeleteAsset":{"id":"a1","urn":"u","filename":"logo.png","deletedAt":"2026-08-06T00:00:00Z"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "rm", "a1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "asset restore a1") {
		t.Errorf("the output should name the restore command; got %q", out.String())
	}
}

func TestAssetRestoreClearsDeletedAt(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"RestoreAsset": `{"data":{"restoreAsset":{"id":"a1","urn":"u","filename":"logo.png","deletedAt":null}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "restore", "a1", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		DeletedAt *string `json:"deletedAt"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("json: %v", err)
	}
	if dto.DeletedAt != nil {
		t.Errorf("a restored asset should report deletedAt null; got %v", *dto.DeletedAt)
	}
}

func TestAssetLinkForwardsTheRefVerbatim(t *testing.T) {
	// The server accepts an id or a URN; forwarding the URN keeps its memory
	// qualification, which matters when the reference lands in another memory.
	gql, reqs := captureGraphQL(t, map[string]string{
		"CreateAssetReferenceNode": `{"data":{"createAssetReferenceNode":{"id":"n1","urn":"hrn:node:acme.com:kb:designs:logo",` +
			`"loc":"designs:logo","name":"logo.png","nodeType":"reference","memoryId":"mem1"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	const ref = "hrn:asset:acme.com:kb:assets:a1"
	root.SetArgs([]string{"asset", "link", ref, "--node", "hrn:node:acme.com:kb:designs", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := decodeVars(t, reqs, "CreateAssetReferenceNode")
	if vars["assetRef"] != ref {
		t.Errorf("assetRef should be forwarded verbatim; got %v", vars["assetRef"])
	}
	var dto struct {
		NodeType string `json:"nodeType"`
		AssetID  string `json:"assetId"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("json: %v", err)
	}
	if dto.NodeType != "reference" || dto.AssetID != "a1" {
		t.Errorf("unexpected DTO: %+v", dto)
	}
}

func TestAssetLinkTargetsTheNodeToCreateNotAParent(t *testing.T) {
	// Review on #363: CreateAssetReferenceNodeInput.nodeUrn is the URN of the
	// node being CREATED. The first version documented it as a parent to
	// append under, so following the help produced a loc conflict instead of a
	// reference node. The flag must forward the caller's URN unchanged, and
	// the help must not describe it as a parent.
	gql, reqs := captureGraphQL(t, map[string]string{
		"CreateAssetReferenceNode": `{"data":{"createAssetReferenceNode":{"id":"n1","urn":"hrn:node:acme.com:kb:designs:logo-v3",` +
			`"loc":"designs:logo-v3","name":"logo.png","nodeType":"reference","memoryId":"mem1"}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	const target = "hrn:node:acme.com:kb:designs:logo-v3"
	root.SetArgs([]string{"asset", "link", "a1", "--node", target, "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := decodeVars(t, reqs, "CreateAssetReferenceNode")["nodeUrn"]; got != target {
		t.Errorf("nodeUrn should be the caller's target verbatim; got %v", got)
	}

	// The help must not tell a reader to pass a parent — that was the bug.
	help := &strings.Builder{}
	f2, _ := testFactory(t)
	h := NewRootCmd(f2)
	h.SetOut(help)
	h.SetArgs([]string{"asset", "link", "--help"})
	if err := h.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	if !strings.Contains(help.String(), "not of a parent") && !strings.Contains(help.String(), "not a parent") {
		t.Errorf("help must say --node is the node to create, not a parent; got:\n%s", help.String())
	}
}

func TestAssetRmPromptDoesNotClaimIrreversibility(t *testing.T) {
	// Review on #363: ConfirmDeletion wraps its argument in
	// "Delete …? This cannot be undone." — garbled here, and false, since the
	// delete is restorable. Non-interactively the refusal must still fire.
	gql := fakeGraphQL(t, map[string]string{
		"SoftDeleteAsset": `{"data":{"softDeleteAsset":{"id":"a1","urn":"u","filename":"logo.png","deletedAt":"2026-08-06T00:00:00Z"}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "rm", "a1", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("rm without --yes must refuse non-interactively")
	}
	if strings.Contains(err.Error(), "cannot be undone") {
		t.Errorf("a soft delete must not claim irreversibility; got %v", err)
	}
	stderr, ok := f.IOStreams.ErrOut.(*strings.Builder)
	if ok && strings.Contains(stderr.String(), "cannot be undone") {
		t.Errorf("prompt must not claim irreversibility; got %q", stderr.String())
	}
}

func TestAssetLinkRequiresNode(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "link", "a1"})
	err := root.Execute()
	if err == nil {
		t.Fatal("link without --node must be a usage error")
	}
	if got := exitcode.FromError(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", got, exitcode.Usage)
	}
}

// The malware verdict lands on the LAST step, after the bytes are already in
// storage, so the message has to say what happened to them. The fake returns
// the exact envelope prd sends (captured from an EICAR upload): a message and
// no extensions.code — which is why the CLI matches on text too (#364).
func TestAssetUploadMalwareBlockedIsExplained(t *testing.T) {
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(store.Close)
	begin := `{"data":{"beginAssetUploadV2":{"uploadId":"up1","putUrl":"` + store.URL +
		`","putHeaders":[],"storageKey":"k","maxSizeBytes":10485760,` +
		`"allowedMimeType":"text/plain","expiresAt":"2026-08-09T01:00:00Z"}}}`
	gql := fakeGraphQL(t, map[string]string{
		"GetMemory":        assetMemoryResp,
		"BeginAssetUpload": begin,
		"CompleteAssetUpload": `{"errors":[{"message":` +
			`"input:3: completeAssetUpload upload rejected: file failed the malware scan"}]}`,
	})
	src := writeTempFile(t, "eicar-test.txt", "harmless test bytes")

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "upload", src, "-m", "acme.com::kb", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a blocked upload must fail")
	}
	msg := err.Error()
	for _, want := range []string{"eicar-test.txt", "malware scan", "audit"} {
		if !strings.Contains(msg, want) {
			t.Errorf("want %q in the message, got %q", want, msg)
		}
	}
	// Retrying identical bytes always fails the same way.
	if strings.Contains(strings.ToLower(msg), "try again") {
		t.Errorf("must not suggest a retry: %q", msg)
	}
	// Non-zero, and stable across the server typing the code later.
	if got := exitcode.FromError(err); got != exitcode.Error {
		t.Errorf("exit code = %d, want %d", got, exitcode.Error)
	}
}

// A PENDING asset is not a dead end — the server's sweep settles it — so the
// download refusal has to say "not yet", not "no".
func TestAssetGetPendingScanSaysRetryShortly(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"AssetDownloadUrl": `{"errors":[{"message":` +
			`"input:2: assetDownloadUrl asset has not been scanned yet"}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "get", "a1", "-o", filepath.Join(t.TempDir(), "x"), "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a PENDING asset cannot be downloaded")
	}
	msg := err.Error()
	if !strings.Contains(msg, "try again") || !strings.Contains(msg, "malware scan") {
		t.Errorf("expected a retry-shortly hint, got %q", msg)
	}
	if strings.Contains(msg, "deleted") {
		t.Errorf("PENDING keeps its bytes — must not read like BLOCKED: %q", msg)
	}
}

func TestAssetGetBlockedScanExplainsTheTombstone(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"AssetDownloadUrl": `{"errors":[{"message":"input:2: assetDownloadUrl asset blocked by scan"}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "get", "a1", "-o", filepath.Join(t.TempDir(), "x"), "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a BLOCKED asset cannot be downloaded")
	}
	if msg := err.Error(); !strings.Contains(msg, "failed the malware scan") || !strings.Contains(msg, "audit") {
		t.Errorf("expected the blocked explanation, got %q", msg)
	}
}
