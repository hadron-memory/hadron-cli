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
	return `{"id":"` + id + `","urn":"hrn:asset:acme.com:kb:assets:` + id + `","filename":"` + name +
		`","mimeType":"` + mime + `","sizeBytes":` + itoaT(size) + `,"description":null,"scanStatus":"` + scan +
		`","uploadedAt":"2026-08-06T00:00:00Z","uploadedBy":"u1","deletedAt":null,"memoryId":"mem1","publicUrl":` + publicURL + `}`
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
	if got := exitcode.FromError(err); got != exitcode.Usage {
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
	if code := exitcode.FromError(err); code != exitcode.Conflict {
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
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"asset", "url", "hrn:asset:acme.com:kb:assets:a1", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("an absent hotlink must be an error, not empty output")
	}
	if !strings.Contains(err.Error(), "scan") {
		t.Errorf("the error should name the cause; got %v", err)
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
	if got := exitcode.FromError(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", got, exitcode.Usage)
	}
}
