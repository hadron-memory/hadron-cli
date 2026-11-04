package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// offsetIgnoringServer answers `op` with the same body for EVERY offset (a
// provider that does not honour paging); other ops come from static/defaults.
func offsetIgnoringServer(t *testing.T, op, body string, static map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.OperationName == op {
			_, _ = w.Write([]byte(body))
			return
		}
		resp, ok := static[req.OperationName]
		if !ok {
			resp, _ = unstubbedDefault(req.OperationName)
		}
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// cli#967 (Gene's P2-1): a provider that returns the same non-empty page for
// every offset yields a FAILED read, never a successful partial list.
func TestChatReadFailsWhenThePageDoesNotAdvance(t *testing.T) {
	srv := offsetIgnoringServer(t, "ChatMessages", chatMessagesResp(chatHits(3)...), nil)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "read", "--node", "hrn:node:acme.com:tc:chats:api:messages", "--since", "0", "--json", "--server", srv.URL})
	err := root.Execute()
	if err == nil {
		t.Fatalf("a non-advancing page must fail the read, got success: %.200s", out.String())
	}
	if exitCodeFor(err) != exitcode.Unavailable {
		t.Errorf("exit code = %d, want %d (Unavailable)", exitCodeFor(err), exitcode.Unavailable)
	}
}

func TestSpecDescribePlaceholderScanFailsWhenThePageDoesNotAdvance(t *testing.T) {
	body := `{"data":{"findNodes":{"hits":[{"node":{"loc":"msg:010:001","isPlaceholder":false}},{"node":{"loc":"msg:010:002","isPlaceholder":true}}]}}}`
	srv := offsetIgnoringServer(t, "SpecPlaceholderScan", body, map[string]string{
		"Memories":        memListJSON,
		"GetMemory":       memGetJSON(`null`),
		"FindNodes":       `{"data":{"nodes":[` + specNodeList("msg:010:002", `["spec"]`) + `]}}`,
		"SpecCorpusState": draftStateJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "describe", "-m", specMem, "--json", "--server", srv.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not advancing") {
		t.Fatalf("a non-advancing placeholder scan must fail, got %v", err)
	}
}
