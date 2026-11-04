package orca

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
)

// cli#1459 (hadron-server#2970): `orca status` lists the App's workers to name
// its targets. It needs id, urn, name and retiredAt; it read every worker's
// boot briefing too. The fake answers the way the server does: a worker
// carries its multi-KB prompt only when the document selects it.
func TestOrcaWorkersReadStaysUnderBudget1459(t *testing.T) {
	const workers, promptSize, budget = 100, 40 * 1024, 50 * 1024
	total, queries := 0, []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
			Query         string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.OperationName != "Workers" {
			t.Errorf("unexpected operation %q", body.OperationName)
		}
		queries = append(queries, body.Query)
		items := []string{}
		for i := 0; i < workers; i++ {
			item := map[string]any{"id": fmt.Sprintf("w%d", i), "urn": fmt.Sprintf("hrn:worker:acme.com:eng-team:w%d", i),
				"name": fmt.Sprintf("W%d", i), "retiredAt": nil}
			for _, f := range []string{"prompt", "signature", "trailer"} {
				if strings.Contains(body.Query, f) {
					item[f] = strings.Repeat("x", promptSize)
				}
			}
			raw, _ := json.Marshal(item)
			items = append(items, string(raw))
		}
		resp := fmt.Sprintf(`{"data":{"workers":{"total":%d,"items":[%s]}}}`, workers, strings.Join(items, ","))
		total += len(resp)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	c, err := api.NewClient(srv.URL, "hdr_user_test", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	got, err := graphReads{client: c, app: "app1"}.Workers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != workers || got[7].ID != "w7" || got[7].URN != "hrn:worker:acme.com:eng-team:w7" || got[7].Name != "W7" {
		t.Fatalf("workers: %d %+v", len(got), got)
	}
	if total > budget {
		t.Errorf("orca's worker read was %d bytes for %d workers, over %d; document:\n%s", total, workers, budget, queries[0])
	}
}
