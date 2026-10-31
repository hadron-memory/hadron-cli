package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// tickerPageServer serves tickersPage from `total` distinct tickers, honouring
// limit/offset like the server (limit capped at 200), and records each offset.
// stuck, when set, makes every page repeat the first rows with hasMore true: a
// server that ignores the offset.
func tickerPageServer(t *testing.T, total int, stuck bool) (*httptest.Server, *[]int) {
	t.Helper()
	var offsets []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Limit  *int `json:"limit"`
				Offset *int `json:"offset"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "TickerSubscribers":
			_, _ = w.Write([]byte(`{"data":{"ticker":{"subscribers":{"items":[],"hasNextPage":false,"endCursor":null}}}}`))
		case "ListTickersPage":
			off, lim := 0, 20
			if body.Variables.Offset != nil {
				off = *body.Variables.Offset
			}
			if body.Variables.Limit != nil {
				lim = *body.Variables.Limit
			}
			if lim > 200 {
				lim = 200
			}
			offsets = append(offsets, off)
			start := off
			if stuck {
				start = 0
			}
			var items []string
			for i := start; i < total && len(items) < lim; i++ {
				items = append(items, strings.Replace(tickerFixture, "cticker000000000000000000", fmt.Sprintf("ctk%023d", i), 1))
			}
			_, _ = fmt.Fprintf(w, `{"data":{"tickersPage":{"total":%d,"hasMore":%v,"items":[%s]}}}`, total, start+len(items) < total, strings.Join(items, ","))
		default:
			_, _ = w.Write([]byte(`{"data":{"app":{"id":"app1","urn":"hrn:app:acme.com:ops","name":"Ops"}}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &offsets
}

// cli#1339: ticker list reads tickersPage to the server's own total.
func TestTickerListPagesToExhaustion(t *testing.T) {
	srv, offsets := tickerPageServer(t, 250, false)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ticker", "list", "--app", "acme.com:ops", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil || len(rows) != 250 {
		t.Fatalf("want a bare array of 250 tickers, got %d (%v)", len(rows), err)
	}
	if len(*offsets) != 2 || (*offsets)[0] != 0 || (*offsets)[1] != 200 {
		t.Fatalf("offsets %v, want [0 200]", *offsets)
	}
}

func TestTickerListNonAdvancingServerFailsNotPartial(t *testing.T) {
	srv, _ := tickerPageServer(t, 250, true)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ticker", "list", "--app", "acme.com:ops", "--json", "--server", srv.URL})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != 7 || out.String() != "" {
		t.Fatalf("want exit 7 and no partial list, got %v out %q", err, out.String())
	}
}
