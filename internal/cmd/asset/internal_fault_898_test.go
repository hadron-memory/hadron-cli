package asset

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func faultStorage(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "h-9")
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"secret stack","code":"INTERNAL_SERVER_ERROR","requestId":"r-42"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func assertFault(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	if exitcode.FromError(err) != exitcode.Error || !strings.Contains(err.Error(), "request id: r-42") || strings.Contains(err.Error(), "secret stack") {
		t.Errorf("want exit 1, the id, no raw text; got %v", err)
	}
}

func TestStreamToInternalFaultEnvelope(t *testing.T) {
	srv := faultStorage(t)
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	_, err := streamTo(cmd, io.Discard, srv.URL)
	assertFault(t, err)
}

func TestPutBytesInternalFaultEnvelope(t *testing.T) {
	srv := faultStorage(t)
	src := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(src, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	io2, _, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: io2}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	err := putBytes(cmd, f, src, 2, &gen.BeginAssetUploadBeginAssetUploadV2BeginAssetUploadResult{PutUrl: srv.URL})
	assertFault(t, err)
}
