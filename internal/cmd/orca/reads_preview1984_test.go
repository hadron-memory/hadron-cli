package orca

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// The fixture models the server's documented budget distinction, not its
// implementation: a spending read reserves one slot, withholds the second
// worker and posts a notice. A monitoring preview must leave all three alone.
func TestDueTicksMonitoringPreviewNoBudgetEffects(t *testing.T) {
	var calls, reservations, withheld, notices, acknowledgements int
	var previews []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string         `json:"operationName"`
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls++
		if req.OperationName != "DueTickWakes" {
			acknowledgements++
			t.Errorf("monitoring sent unexpected operation %q", req.OperationName)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		doc, err := parser.ParseQuery(&ast.Source{Input: req.Query})
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		field := doc.Operations.ForName("DueTickWakes").SelectionSet[0].(*ast.Field)
		arg := field.Arguments.ForName("preview")
		preview := arg != nil && arg.Value.Kind == ast.BooleanValue && arg.Value.Raw == "true"
		previews = append(previews, preview)
		w.Header().Set("Content-Type", "application/json")
		if !preview {
			reservations++
			withheld++
			notices++
			_, _ = w.Write([]byte(`{"data":{"dueWakes":{"status":"WAKE_BUDGET_EXHAUSTED","wakes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`))
			return
		}
		refs, ok := req.Variables["workerRefs"].([]any)
		if !ok || len(refs) != 2 || refs[0] != "w1" || refs[1] != "w2" {
			t.Errorf("worker scope changed: %#v", req.Variables)
		}
		worker, more, cursor := "w1", true, any("next")
		if calls == 1 {
			if _, present := req.Variables["after"]; present {
				t.Error("first page sent a cursor")
			}
		} else {
			if req.Variables["after"] != "next" {
				t.Errorf("next page cursor: %#v", req.Variables)
			}
			worker, more, cursor = "w2", false, nil
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"dueWakes": map[string]any{
			"status": "OK", "wakes": []any{map[string]any{
				"worker": map[string]any{"id": worker},
				"reasons": []any{map[string]any{"kind": "TICK", "tickDueAt": "2026-11-13T10:00:00Z",
					"ticker": map[string]any{"id": "ticker", "name": "Fixture"}}},
			}}, "pageInfo": map[string]any{"hasNextPage": more, "endCursor": cursor},
		}}})
	}))
	defer srv.Close()
	client, err := api.NewClient(srv.URL, "hdr_user_synthetic", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := (graphReads{client: client, app: "app1"}).DueTicks(context.Background(), []string{"w1", "w2"})
	for i, preview := range previews {
		if !preview {
			t.Fatalf("monitoring request %d omitted literal preview:true", i+1)
		}
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	if calls != 2 || len(got["w1"]) != 1 || len(got["w2"]) != 1 {
		t.Fatalf("pages=%d ticks=%+v", calls, got)
	}
	if reservations != 0 || withheld != 0 || notices != 0 || acknowledgements != 0 {
		t.Fatalf("monitoring effects: reservations=%d withheld=%d notices=%d acknowledgements=%d",
			reservations, withheld, notices, acknowledgements)
	}
}

// The delivering host poller must continue using the spending read.
func TestMonitoringPreviewDoesNotChangeDeliveryReader(t *testing.T) {
	for name, query := range map[string]string{"DueWakes": gen.DueWakes_Operation} {
		doc, err := parser.ParseQuery(&ast.Source{Input: query})
		if err != nil {
			t.Fatal(err)
		}
		field := doc.Operations.ForName(name).SelectionSet[0].(*ast.Field)
		if field.Arguments.ForName("preview") != nil {
			t.Fatalf("%s changed with the monitoring-only fix", name)
		}
	}
}

// Older servers must never induce a spending fallback. The existing API mapper
// must name the monitoring prerequisite for both GraphQL HTTP shapes.
func TestDueTicksPreviewValidationRefusalNoSpendingFallback(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{
					"message":    "Unknown argument \"preview\" on field \"Query.dueWakes\".",
					"extensions": map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"},
				}}})
			}))
			defer srv.Close()
			client, err := api.NewClient(srv.URL, "hdr_user_synthetic", srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			got, err := (graphReads{client: client, app: "app1"}).DueTicks(context.Background(), []string{"w1"})
			var coded *exitcode.CodedError
			if !errors.As(err, &coded) || coded.Code != exitcode.Usage {
				t.Fatalf("unclean refusal: %v", err)
			}
			if !strings.Contains(err.Error(), "requires a server with dueWakes(preview)") || !strings.Contains(err.Error(), "upgrade the server") {
				t.Fatalf("not actionable: %v", err)
			}
			if calls != 1 || got != nil {
				t.Fatalf("fallback or success: calls=%d got=%+v", calls, got)
			}
		})
	}
}
