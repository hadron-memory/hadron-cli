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

// A body over the read cap is neither classified nor echoed: the code may sit
// anywhere past the cap (Greta, Joost, Clark on #1212).
func TestOversizedBodyIsNeverEchoedOrClassified(t *testing.T) {
	pad := strings.Repeat("secret stack ", 40000)
	for name, body := range map[string]string{
		"code before the pad":           `{"code":"INTERNAL_SERVER_ERROR","error":"` + pad + `"}`,
		"code after the pad":            `{"error":"` + pad + `","code":"INTERNAL_SERVER_ERROR"}`,
		"typed code quoting the marker": `{"code":"FORBIDDEN","note":"INTERNAL_SERVER_ERROR","error":"` + pad + `"}`,
	} {
		for _, header := range []string{"r-42", ""} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if header != "" {
					w.Header().Set("X-Request-Id", header)
				}
				w.WriteHeader(500)
				_, _ = w.Write([]byte(body))
			}))
			cmd := &cobra.Command{}
			cmd.SetContext(t.Context())
			_, gerr := streamTo(cmd, io.Discard, srv.URL)
			src := filepath.Join(t.TempDir(), "a.txt")
			if werr := os.WriteFile(src, []byte("hi"), 0o600); werr != nil {
				t.Fatal(werr)
			}
			io2, _, _ := output.Test()
			perr := putBytes(cmd, &cmdutil.Factory{IOStreams: io2}, src, 2, &gen.BeginAssetUploadBeginAssetUploadV2BeginAssetUploadResult{PutUrl: srv.URL})
			for _, err := range []error{gerr, perr} {
				if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "INTERNAL_SERVER_ERROR") || strings.Contains(err.Error(), "FORBIDDEN") {
					t.Errorf("%s/%q: raw body leaked or no error: %v", name, header, err)
				} else if !strings.Contains(err.Error(), "too large to show") {
					t.Errorf("%s/%q: want the oversize note: %v", name, header, err)
				}
			}
			srv.Close()
		}
	}
}
