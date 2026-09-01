package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const assetMemoryResp = `{"data":{"memory":{"id":"mem1","urn":"hrn:mem:acme.com:kb","name":"KB","shortDescription":null,"class":"shared","visibility":"private","organizationId":"org1","isEncrypted":false,"maxRevCount":null,"updatedAt":"2026-08-06T00:00:00Z"}}}`

// assetJSON builds one asset row. publicUrl/description are raw so a test can
// pass null.
func assetJSON(id, name, mime string, size int, scan, publicURL string) string {
	// Only a BLOCKED row carries a signature; every other row is null.
	sig := "null"
	if scan == "BLOCKED" {
		sig = `"Eicar-Signature"`
	}
	return `{"id":"` + id + `","urn":"hrn:asset:acme.com:kb:assets:` + id + `","filename":"` + name +
		`","mimeType":"` + mime + `","sizeBytes":` + itoaT(size) + `,"description":null,"scanStatus":"` + scan +
		`","scanSignature":` + sig +
		`,"uploadedAt":"2026-08-06T00:00:00Z","uploadedBy":"u1","deletedAt":null,"memoryId":"mem1","publicUrl":` + publicURL + `}`
}

func itoaT(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func assetListResp(total int, hasMore bool, rows ...string) string {
	hm := "false"
	if hasMore {
		hm = "true"
	}
	return `{"data":{"memoryAssets":{"total":` + itoaT(total) + `,"hasMore":` + hm + `,"assets":[` + strings.Join(rows, ",") + `]}}}`
}

func TestAssetListRequiresMemory(t *testing.T) {
	// memoryAssets is memory-addressed server-side; guessing a scope would be
	// worse than saying so.
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "list"})
	err := root.Execute()
	if err == nil {
		t.Fatal("asset list without -m must be a usage error")
	}
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", got, exitcode.Usage)
	}
}

func TestAssetListRendersAndPagesToExhaustion(t *testing.T) {
	// The whole-scope contract is "every asset in the memory", so a listing
	// larger than one page must not silently stop at the first (#23).
	page1 := assetListResp(3, true, assetJSON("a1", "one.png", "image/png", 2048, "CLEAN", `"https://cdn/one"`))
	calls := 0
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "GetMemory":
			_, _ = w.Write([]byte(assetMemoryResp))
		case "MemoryAssets":
			calls++
			if calls == 1 {
				_, _ = w.Write([]byte(page1))
				return
			}
			_, _ = w.Write([]byte(assetListResp(3, false,
				assetJSON("a2", "two.pdf", "application/pdf", 1048576, "CLEAN", "null"))))
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
		}
	}))
	defer gql.Close()

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "list", "-m", "acme.com::kb", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Total  int `json:"total"`
		Assets []struct {
			ID        string  `json:"id"`
			PublicURL *string `json:"publicUrl"`
		} `json:"assets"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(dto.Assets) != 2 {
		t.Fatalf("expected both pages collected, got %d assets", len(dto.Assets))
	}
	if dto.Assets[1].PublicURL != nil {
		t.Errorf("a null publicUrl must stay null, not become \"\"; got %v", *dto.Assets[1].PublicURL)
	}
	if dto.Total != 3 {
		t.Errorf("total should be the server's, got %d", dto.Total)
	}
}

func TestAssetListSingleExplicitPageWithLimit(t *testing.T) {
	gql, reqs := captureGraphQL(t, map[string]string{
		"GetMemory": assetMemoryResp,
		"MemoryAssets": assetListResp(50, true,
			assetJSON("a1", "one.png", "image/png", 10, "CLEAN", "null")),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "list", "-m", "acme.com::kb", "--limit", "1", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// hasMore is true, but --limit means one explicit page — it must not page on.
	vars := decodeVars(t, reqs, "MemoryAssets")
	if got := vars["count"].(float64); int(got) != 1 {
		t.Errorf("--limit should set the page size; got %v", got)
	}
}

func TestAssetGetWritesFileAndRefusesToClobber(t *testing.T) {
	bytesSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The presigned URL must NOT receive Hadron credentials.
		if r.Header.Get("Authorization") != "" {
			t.Errorf("the bearer token must not be sent to object storage; got %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte("PNGDATA"))
	}))
	defer bytesSrv.Close()

	dl := `{"data":{"assetDownloadUrl":{"url":"` + bytesSrv.URL + `","filename":"one.png","mimeType":"image/png","sizeBytes":7,"expiresAt":"2026-08-06T00:05:00Z"}}}`
	gql := fakeGraphQL(t, map[string]string{"AssetDownloadUrl": dl})

	dir := t.TempDir()
	dest := filepath.Join(dir, "out.png")

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "get", "a1", "-o", dest, "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "PNGDATA" {
		t.Fatalf("file contents = %q, %v", got, err)
	}

	// Second run must refuse rather than silently overwrite.
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"asset", "get", "a1", "-o", dest, "--server", gql.URL, "--json"})
	err = root2.Execute()
	if err == nil {
		t.Fatal("an existing output file must not be clobbered without --force")
	}
	if code := exitCodeFor(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d", code, exitcode.Conflict)
	}

	// --force overwrites.
	f3, _ := testFactory(t)
	root3 := NewRootCmd(f3)
	root3.SetArgs([]string{"asset", "get", "a1", "-o", dest, "--force", "--server", gql.URL, "--json"})
	if err := root3.Execute(); err != nil {
		t.Fatalf("--force should overwrite: %v", err)
	}
}

func TestAssetGetRemovesPartialFileOnFailure(t *testing.T) {
	// A half-written file looks like a successful download to everything
	// downstream, which is worse than no file at all.
	bytesSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("expired"))
	}))
	defer bytesSrv.Close()

	dl := `{"data":{"assetDownloadUrl":{"url":"` + bytesSrv.URL + `","filename":"one.png","mimeType":"image/png","sizeBytes":7,"expiresAt":"2026-08-06T00:05:00Z"}}}`
	gql := fakeGraphQL(t, map[string]string{"AssetDownloadUrl": dl})

	dest := filepath.Join(t.TempDir(), "out.png")
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "get", "a1", "-o", dest, "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a non-200 from the presigned URL must fail")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("a 403 should hint at expiry; got %v", err)
	}
	if _, serr := os.Stat(dest); !os.IsNotExist(serr) {
		t.Errorf("the partial file must be removed; stat err = %v", serr)
	}
}

func TestAssetURLPrintsHotlinkAndWarns(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetMemory": assetMemoryResp,
		"MemoryAssets": assetListResp(1, false,
			assetJSON("a1", "one.png", "image/png", 10, "CLEAN", `"https://cdn/one.png"`)),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// The URN carries its memory, so no -m is needed.
	root.SetArgs([]string{"asset", "url", "hrn:asset:acme.com:kb:assets:a1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "https://cdn/one.png") {
		t.Errorf("the hotlink should be on stdout; got %q", out.String())
	}
}

func TestAssetURLAbsentIsAnErrorWithAReason(t *testing.T) {
	// A missing hotlink must not print an empty line that a script would
	// happily consume as a URL.
	gql := fakeGraphQL(t, map[string]string{
		"GetMemory": assetMemoryResp,
		"MemoryAssets": assetListResp(1, false,
			assetJSON("a1", "one.png", "image/png", 10, "PENDING", "null")),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "url", "hrn:asset:acme.com:kb:assets:a1", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("an absent hotlink must be an error, not empty output")
	}
	if code := exitCodeFor(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d", code, exitcode.Conflict)
	}
	// stdout must stay empty so a `$(hadron asset url …)` capture yields
	// nothing rather than a blank line that reads as a URL.
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("stdout should be empty when there is no hotlink; got %q", out.String())
	}
	stderr, ok := f.IOStreams.ErrOut.(*strings.Builder)
	if !ok {
		t.Fatal("expected a capturable stderr")
	}
	if !strings.Contains(stderr.String(), "scan") {
		t.Errorf("the reason should be on stderr; got %q", stderr.String())
	}
}

func TestAssetURLBareIDNeedsMemory(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "url", "a1"})
	err := root.Execute()
	if err == nil {
		t.Fatal("a bare id with no -m must be a usage error")
	}
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", got, exitcode.Usage)
	}
}

func TestAssetGetForcedFailureLeavesOriginalIntact(t *testing.T) {
	// Copilot P1 on #361: writing straight to the destination truncates it
	// before the transfer, so a mid-download failure destroyed the very file
	// --force was replacing. The download must be atomic — the destination is
	// either untouched or fully replaced.
	bytesSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("expired"))
	}))
	defer bytesSrv.Close()
	dl := `{"data":{"assetDownloadUrl":{"url":"` + bytesSrv.URL + `","filename":"one.png","mimeType":"image/png","sizeBytes":7,"expiresAt":"2026-08-06T00:05:00Z"}}}`
	gql := fakeGraphQL(t, map[string]string{"AssetDownloadUrl": dl})

	dir := t.TempDir()
	dest := filepath.Join(dir, "precious.png")
	if err := os.WriteFile(dest, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "get", "a1", "-o", dest, "--force", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("a 403 must fail the download")
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("the original file must survive a failed --force download: %v", err)
	}
	if string(got) != "ORIGINAL" {
		t.Errorf("original contents clobbered: got %q, want %q", got, "ORIGINAL")
	}
	// And no .part-* debris left beside it.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".part-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestAssetURLAbsentHotlinkExitsNonZeroInJSONMode(t *testing.T) {
	// Codex/Copilot on #361: the exit code lived in the human-render callback,
	// which --json never invokes — so automation saw exit 0 and could treat a
	// non-hotlinkable asset as a successful URL lookup.
	gql := fakeGraphQL(t, map[string]string{
		"GetMemory": assetMemoryResp,
		"MemoryAssets": assetListResp(1, false,
			assetJSON("a1", "one.png", "image/png", 10, "PENDING", "null")),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "url", "hrn:asset:acme.com:kb:assets:a1", "--server", gql.URL, "--json"})
	err := root.Execute()
	if err == nil {
		t.Fatal("--json must not exit 0 for an absent hotlink")
	}
	if code := exitCodeFor(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d", code, exitcode.Conflict)
	}
	// The machine-readable reason still has to reach the caller.
	var dto struct {
		PublicURL *string `json:"publicUrl"`
		Reason    string  `json:"reason"`
	}
	if jerr := json.Unmarshal([]byte(out.String()), &dto); jerr != nil {
		t.Fatalf("--json should still emit the DTO: %v\n%s", jerr, out.String())
	}
	if dto.PublicURL != nil || !strings.Contains(dto.Reason, "scan") {
		t.Errorf("DTO should carry a null url and the reason; got %+v", dto)
	}
}

func TestAssetURLDoesNotSearchDeletedAssets(t *testing.T) {
	// A soft-deleted asset has no hotlink; finding it would turn a clean
	// "no such asset" into a misleading "no hotlink because its scan…".
	gql, reqs := captureGraphQL(t, map[string]string{
		"GetMemory":    assetMemoryResp,
		"MemoryAssets": assetListResp(0, false),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "url", "hrn:asset:acme.com:kb:assets:a1", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("a missing asset should be a not-found error")
	}
	vars := decodeVars(t, reqs, "MemoryAssets")
	if v, present := vars["includeDeleted"]; present && v == true {
		t.Errorf("url must not search soft-deleted assets; includeDeleted = %v", v)
	}
}

func TestAssetListRejectsZeroLimit(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "list", "-m", "acme.com::kb", "--limit", "0"})
	err := root.Execute()
	if err == nil {
		t.Fatal("--limit 0 asks for a zero-row page and must be rejected")
	}
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", code, exitcode.Usage)
	}
}

func TestAssetListOffsetAloneIsASinglePage(t *testing.T) {
	// --offset is deliberate user-driven pagination, like `spec list`; paging
	// on from it would return far more than the caller asked to page through.
	calls := 0
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.OperationName == "GetMemory" {
			_, _ = w.Write([]byte(assetMemoryResp))
			return
		}
		calls++
		_, _ = w.Write([]byte(assetListResp(99, true,
			assetJSON("a1", "one.png", "image/png", 10, "CLEAN", "null"))))
	}))
	defer gql.Close()

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "list", "-m", "acme.com::kb", "--offset", "20", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if calls != 1 {
		t.Errorf("--offset should fetch exactly one page despite hasMore; made %d calls", calls)
	}
}

func TestAssetRefAcceptsTheMemPrefixedSpelling(t *testing.T) {
	// The server emits hrn:asset:…, but the schema documents the shape as
	// "<memory.urn>:assets:<asset.id>", which reads as though the memory's own
	// hrn:mem: prefix is carried through. Accept both (#239 is liberal on input).
	gql := fakeGraphQL(t, map[string]string{
		"GetMemory": assetMemoryResp,
		"MemoryAssets": assetListResp(1, false,
			assetJSON("a1", "one.png", "image/png", 10, "CLEAN", `"https://cdn/one.png"`)),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "url", "hrn:mem:acme.com:kb:assets:a1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("the mem:-prefixed spelling should resolve: %v", err)
	}
	if !strings.Contains(out.String(), "https://cdn/one.png") {
		t.Errorf("expected the hotlink; got %q", out.String())
	}
}

// A BLOCKED row's bytes are gone, so the engine signature is the only thing
// left that says WHY — it belongs in both the table and the --json contract
// (#364). Every other row has none, and must not grow an empty one.
func TestAssetListShowsScanSignatureOnBlockedRows(t *testing.T) {
	page := `{"data":{"memoryAssets":{"total":2,"hasMore":false,"assets":[` +
		assetJSON("a1", "logo.png", "image/png", 7, "CLEAN", `"https://cdn/x"`) + `,` +
		assetJSON("a2", "eicar.txt", "text/plain", 68, "BLOCKED", "null") + `]}}}`
	gql := fakeGraphQL(t, map[string]string{"GetMemory": assetMemoryResp, "MemoryAssets": page})

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "list", "-m", "acme.com::kb", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("list should succeed: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "BLOCKED (Eicar-Signature)") {
		t.Errorf("a BLOCKED row should name the engine signature, got %q", s)
	}
	if strings.Contains(s, "CLEAN (") {
		t.Errorf("a CLEAN row has no signature and must not gain empty parentheses: %q", s)
	}

	// --json carries it as a nullable field: null is "not blocked", which "" would
	// blur into "blocked, matched nothing named".
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	gql2 := fakeGraphQL(t, map[string]string{"GetMemory": assetMemoryResp, "MemoryAssets": page})
	root2.SetArgs([]string{"asset", "list", "-m", "acme.com::kb", "--json", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("list --json should succeed: %v", err)
	}
	var dto struct {
		Assets []struct {
			ID            string  `json:"id"`
			ScanStatus    string  `json:"scanStatus"`
			ScanSignature *string `json:"scanSignature"`
		} `json:"assets"`
	}
	if err := json.Unmarshal([]byte(out2.String()), &dto); err != nil {
		t.Fatalf("decoding --json: %v (%q)", err, out2.String())
	}
	if len(dto.Assets) != 2 {
		t.Fatalf("want 2 rows, got %d", len(dto.Assets))
	}
	for _, a := range dto.Assets {
		switch a.ScanStatus {
		case "BLOCKED":
			if a.ScanSignature == nil || *a.ScanSignature != "Eicar-Signature" {
				t.Errorf("BLOCKED row must carry the signature, got %v", a.ScanSignature)
			}
		default:
			if a.ScanSignature != nil {
				t.Errorf("%s row must carry null, got %q", a.ScanStatus, *a.ScanSignature)
			}
		}
	}
}
