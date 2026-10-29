package orca

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
)

// graphFixture serves the three reads the command makes and records the
// variables the wake read sent.
func graphFixture(t *testing.T, responses map[string]string) (graphReads, map[string]json.RawMessage) {
	t.Helper()
	seen := map[string]json.RawMessage{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		seen[body.OperationName] = body.Variables
		resp, ok := responses[body.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	c, err := api.NewClient(srv.URL, "hdr_user_test", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return graphReads{client: c, app: "app1"}, seen
}

func TestDueTicksReadsAnchorNotLastTickAt(t *testing.T) {
	g, seen := graphFixture(t, map[string]string{"DueTickWakes": `{"data":{"dueWakes":{"status":"OK","wakes":[
	  {"worker":{"id":"w_j"},"anchorAt":"2026-10-29T16:10:00Z","reasons":[
	    {"kind":"TICK","ticker":{"id":"tk5","name":"base"}},
	    {"kind":"MENTION","ticker":null},
	    {"kind":"TASK","ticker":null}]}],
	  "pageInfo":{"hasNextPage":false,"endCursor":null}}}}`})
	got, err := g.DueTicks(context.Background(), []string{"w_j"})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 29, 16, 10, 0, 0, time.UTC)
	if len(got["w_j"]) != 1 || got["w_j"][0].TickerID != "tk5" || !got["w_j"][0].DueAt.Equal(want) {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(string(seen["DueTickWakes"]), `"workerRefs":["w_j"]`) {
		t.Fatalf("worker refs not sent: %s", seen["DueTickWakes"])
	}
}

func TestDueTicksRefusalIsAnError(t *testing.T) {
	g, _ := graphFixture(t, map[string]string{"DueTickWakes": `{"errors":[{"message":"no","extensions":{"code":"FORBIDDEN"}}],"data":null}`})
	if _, err := g.DueTicks(context.Background(), []string{"w_j"}); err == nil {
		t.Fatal("a refused wake read must be an error so the command fails closed")
	}
}

func TestDueTicksRepeatedCursorIsRefused(t *testing.T) {
	g, _ := graphFixture(t, map[string]string{"DueTickWakes": `{"data":{"dueWakes":{"status":"OK","wakes":[],"pageInfo":{"hasNextPage":true,"endCursor":"c1"}}}}`})
	if _, err := g.DueTicks(context.Background(), []string{"w_j"}); err == nil {
		t.Fatal("a cursor that never advances must refuse, not loop")
	}
}

func TestSubscriptionsActiveEnabledOnly(t *testing.T) {
	tick := func(id string, enabled bool, cadence int) string {
		return `{"id":"` + id + `","name":"` + id + `","slug":"` + id + `","cadenceSeconds":` + itoa(cadence) + `,"enabled":` + boolStr(enabled) + `,"channels":[],"relevance":"ALWAYS","reasons":[]}`
	}
	g, _ := graphFixture(t, map[string]string{
		"ListTickers": `{"data":{"tickers":[` + tick("tk5", true, 300) + `,` + tick("tkoff", false, 60) + `]}}`,
		"TickerSubscribers": `{"data":{"ticker":{"subscribers":{"items":[
		  {"id":"s1","worker":{"id":"w_j","name":"Joshua","urn":"u"},"viaGrant":false,"createdAt":"2026-10-01T00:00:00Z","endedAt":null},
		  {"id":"s2","worker":{"id":"w_a","name":"Ada","urn":"u"},"viaGrant":false,"createdAt":"2026-10-01T00:00:00Z","endedAt":"2026-10-02T00:00:00Z"}],
		  "hasNextPage":false,"endCursor":null}}}}`,
	})
	got, err := g.Subscriptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got["w_j"]) != 1 || got["w_j"][0].Interval != 5*time.Minute {
		t.Fatalf("got %+v (an ended subscription and a disabled ticker must not count)", got)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// A spent wake budget withholds due wakes: an empty page then proves nothing,
// so the read is an error and the command fails closed rather than reporting
// "nothing due".
func TestDueTicksBudgetExhaustedIsAnErrorNotNothingDue(t *testing.T) {
	g, _ := graphFixture(t, map[string]string{"DueTickWakes": `{"data":{"dueWakes":{"status":"WAKE_BUDGET_EXHAUSTED","wakes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`})
	if _, err := g.DueTicks(context.Background(), []string{"w_j"}); err == nil {
		t.Fatal("an exhausted budget hides due wakes; it must not read as nothing due")
	}
}
