package skill

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/spf13/cobra"
)

// A findNodes server that serves at most `serve` rows per page.
func clampedFindNodes(t *testing.T, total, serve int, calls *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*calls++
		limit, offset := 200, 0
		if v, ok := body.Variables["limit"].(float64); ok {
			limit = int(v)
		}
		if v, ok := body.Variables["offset"].(float64); ok {
			offset = int(v)
		}
		if limit > serve {
			limit = serve
		}
		hits := []string{}
		for i := offset; i < total && i < offset+limit; i++ {
			hits = append(hits, fmt.Sprintf(`{"score":null,"node":{"id":"n%d","memoryId":"m","loc":"review:%03d","name":"N","nodeType":"info","tags":[],"updatedAt":"2026-06-11T00:00:00Z"}}`, i, i))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"findNodes":{"total":null,"degraded":null,"reason":null,"hits":[` + strings.Join(hits, ",") + `]}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// cli#967: a clamped page below listPageSize must not end the declared-id scan.
func TestListDeclaredIDsPastAClampedPage(t *testing.T) {
	var calls int
	srv := clampedFindNodes(t, 130, 50, &calls)
	client, err := api.NewClient(srv.URL, "tok", nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	got, err := listDeclaredIDs(cmd, client, []string{"m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 130 || calls != 4 {
		t.Fatalf("got %d ids in %d requests, want 130 in 4 (50+50+30+empty)", len(got), calls)
	}
}
