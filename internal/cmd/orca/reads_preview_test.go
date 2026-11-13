package orca

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
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
// These counters document request-shape consequences in this synthetic model;
// the literal-preview assertion dominates them. They do not independently prove
// persisted server budgets. otherOps counts any operation besides DueTickWakes,
// including acknowledgements, none of which monitoring may send.
func TestDueTicksMonitoringPreviewNoBudgetEffects(t *testing.T) {
	for _, ids := range [][]string{{"w1", "w2"}, {"w2", "w1"}} {
		t.Run(strings.Join(ids, ","), func(t *testing.T) { monitoringPreviewFixture(t, ids) })
	}
}

// Exact count and unique members matter; ordering does not. Used on every page.
func monitoringWorkerScope(got []any, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	remaining := make(map[string]bool, len(want))
	for _, id := range want {
		remaining[id] = true
	}
	for _, value := range got {
		id, ok := value.(string)
		if !ok || !remaining[id] {
			return false
		}
		delete(remaining, id)
	}
	return len(remaining) == 0
}

func TestMonitoringWorkerScopeExactSet(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  []any
		want bool
	}{
		{"ordered", []any{"w1", "w2"}, true},
		{"reordered", []any{"w2", "w1"}, true},
		{"missing", []any{"w1"}, false},
		{"extra", []any{"w1", "w2", "w3"}, false},
		{"duplicate", []any{"w1", "w1"}, false},
		{"duplicate-extra", []any{"w1", "w2", "w2"}, false},
		{"wrong-member", []any{"w1", "w3"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := monitoringWorkerScope(tc.got, []string{"w1", "w2"}); got != tc.want {
				t.Fatalf("scope %v: got %v want %v", tc.got, got, tc.want)
			}
		})
	}
}

func monitoringPreviewFixture(t *testing.T, workerIDs []string) {
	var calls, reservations, withheld, notices, otherOps int
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
			otherOps++
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
		if !ok || !monitoringWorkerScope(refs, []string{"w1", "w2"}) {
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
	got, readErr := (graphReads{client: client, app: "app1"}).DueTicks(context.Background(), workerIDs)
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
	if reservations != 0 || withheld != 0 || notices != 0 || otherOps != 0 {
		t.Fatalf("monitoring effects: reservations=%d withheld=%d notices=%d otherOps=%d",
			reservations, withheld, notices, otherOps)
	}
}

// The delivering host poller must continue using the spending read.
func TestMonitoringPreviewDoesNotChangeDeliveryReader(t *testing.T) {
	doc, err := parser.ParseQuery(&ast.Source{Input: gen.DueWakes_Operation})
	if err != nil {
		t.Fatal(err)
	}
	field := doc.Operations.ForName("DueWakes").SelectionSet[0].(*ast.Field)
	if field.Arguments.ForName("preview") != nil {
		t.Fatal("host delivery DueWakes gained preview")
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
			for _, jsonOut := range []bool{false, true} {
				r := newRig(t)
				r.joshua(idleScreen)
				r.env.Reads = func(context.Context, string) (Reads, error) {
					return wireDue{r.reads, graphReads{client: client, app: "app1"}}, nil
				}
				before := calls
				code, out := r.run(t, jsonOut, "Joshua")
				if code != 0 || calls != before+1 {
					t.Fatalf("status contract or retry changed: exit=%d calls=%d before=%d out=%s", code, calls, before, out)
				}
				if jsonOut {
					m := decode(t, out)
					if m["status"] != "locked" || m["reason"] != "unrecognized" || strings.Contains(out, "requires a server") {
						t.Fatalf("unsafe or leaked-detail JSON: %s", out)
					}
				} else if !strings.Contains(out, "locked (unrecognized)") || !strings.Contains(out, "requires a server with dueWakes(preview)") {
					t.Fatalf("not actionable/closed: %s", out)
				}
			}

		})
	}
}

// Pin the real reader's branch, not just the shared envelope helper. Each
// non-match must retain exactly api.MapError's code/message and make one read.
func TestDueTicksPreviewHintOnlyForKnownRefusal(t *testing.T) {
	refusal := func(message, code string) map[string]any {
		return map[string]any{"message": message, "extensions": map[string]any{"code": code}}
	}
	known := `Unknown argument "preview" on field "Query.dueWakes".`
	for _, tc := range []struct {
		name   string
		errors []any
		hint   bool
	}{
		{"forbidden", []any{refusal("Forbidden", "FORBIDDEN")}, false},
		{"wrong-field", []any{refusal(`Unknown argument "preview" on field "Query.workers".`, "GRAPHQL_VALIDATION_FAILED")}, false},
		{"mixed-errors", []any{refusal(known, "GRAPHQL_VALIDATION_FAILED"), refusal("Forbidden", "FORBIDDEN")}, false},
		{"wrong-code", []any{refusal(known, "INTERNAL_SERVER_ERROR")}, false},
		{"bare-spelling", []any{refusal(`Unknown argument "preview" on field "dueWakes".`, "GRAPHQL_VALIDATION_FAILED")}, true},
	} {
		for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
			t.Run(tc.name+"/"+http.StatusText(status), func(t *testing.T) {
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_ = json.NewEncoder(w).Encode(map[string]any{"errors": tc.errors})
				}))
				defer srv.Close()
				client, err := api.NewClient(srv.URL, "hdr_user_synthetic", srv.Client())
				if err != nil {
					t.Fatal(err)
				}
				// Capture the mapped baseline using the same wire envelope/client.
				_, raw := gen.DueTickWakes(context.Background(), client, "app1", pageSize, []string{"w1"}, nil)
				if raw == nil {
					t.Fatal("fixture did not refuse")
				}
				baseline := api.MapError(raw)
				calls = 0
				got, err := (graphReads{client: client, app: "app1"}).DueTicks(context.Background(), []string{"w1"})
				if calls != 1 || got != nil || err == nil {
					t.Fatalf("calls=%d ticks=%v err=%v", calls, got, err)
				}
				hasHint := strings.Contains(err.Error(), "requires a server with dueWakes(preview)")
				if hasHint != tc.hint {
					t.Fatalf("hint=%v want=%v: %v", hasHint, tc.hint, err)
				}
				if tc.hint {
					var coded *exitcode.CodedError
					if !errors.As(err, &coded) || coded.Code != exitcode.Usage {
						t.Fatalf("unclean refusal: %v", err)
					}
				} else if !reflect.DeepEqual(err, baseline) {
					t.Fatalf("MapError changed: got %#v want %#v", err, baseline)
				}
			})
		}
	}
}
