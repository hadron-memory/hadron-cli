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
	if !strings.Contains(err.Error(), "not advancing") || strings.Contains(err.Error(), "retry") {
		t.Errorf("a stuck server must keep the not-advancing diagnosis, got %v", err)
	}
}

// BLIND P2 (#1460): an empty or repeated terminal page while the reported total is
// still unmet is a partial list even when hasMore is false; it must fail.
func TestTickerListUnmetTotalOnTerminalPageFails(t *testing.T) {
	for _, tail := range []string{"empty", "repeat"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				OperationName string `json:"operationName"`
				Variables     struct {
					Offset *int `json:"offset"`
				} `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "application/json")
			if body.OperationName != "ListTickersPage" {
				_, _ = w.Write([]byte(`{"data":{"app":{"id":"app1","urn":"hrn:app:acme.com:ops","name":"Ops"}}}`))
				return
			}
			var items []string
			for i := 0; i < 200; i++ {
				if tail == "empty" && body.Variables.Offset != nil && *body.Variables.Offset > 0 {
					break
				}
				items = append(items, strings.Replace(tickerFixture, "cticker000000000000000000", fmt.Sprintf("ctk%023d", i), 1))
			}
			more := body.Variables.Offset == nil || *body.Variables.Offset == 0
			_, _ = fmt.Fprintf(w, `{"data":{"tickersPage":{"total":250,"hasMore":%v,"items":[%s]}}}`, more, strings.Join(items, ","))
		}))
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"ticker", "list", "--app", "acme.com:ops", "--json", "--server", srv.URL})
		err := root.Execute()
		srv.Close()
		if err == nil || exitCodeFor(err) != 7 || out.String() != "" {
			t.Fatalf("%s terminal page: want exit 7, no partial list; got %v out %.80q", tail, err, out.String())
		}
	}
}

// A total lowered by deletions mid-read is satisfied by the rows already seen.
func TestTickerListTotalLoweredMidReadSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Offset *int `json:"offset"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.OperationName == "TickerSubscribers" {
			_, _ = w.Write([]byte(`{"data":{"ticker":{"subscribers":{"items":[],"hasNextPage":false,"endCursor":null}}}}`))
			return
		}
		if body.OperationName != "ListTickersPage" {
			_, _ = w.Write([]byte(`{"data":{"app":{"id":"app1","urn":"hrn:app:acme.com:ops","name":"Ops"}}}`))
			return
		}
		if body.Variables.Offset != nil && *body.Variables.Offset > 0 {
			_, _ = w.Write([]byte(`{"data":{"tickersPage":{"total":200,"hasMore":false,"items":[]}}}`))
			return
		}
		var items []string
		for i := 0; i < 200; i++ {
			items = append(items, strings.Replace(tickerFixture, "cticker000000000000000000", fmt.Sprintf("ctk%023d", i), 1))
		}
		_, _ = fmt.Fprintf(w, `{"data":{"tickersPage":{"total":250,"hasMore":true,"items":[%s]}}}`, strings.Join(items, ","))
	}))
	defer srv.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ticker", "list", "--app", "acme.com:ops", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil || len(rows) != 200 {
		t.Fatalf("want 200 rows, got %d (%v)", len(rows), err)
	}
}

// cli#2030: the twin of the session-scan race. A ticker inserted at the top
// between page reads shifts the offsets, so the first pass ends one short of
// the total; the list restarts and prints the whole, current census.
func TestTickerListSurvivesAnInsertBetweenPages(t *testing.T) {
	mk := func(i int) string {
		return strings.Replace(tickerFixture, "cticker000000000000000000", fmt.Sprintf("ctk%023d", i), 1)
	}
	rows := []string{}
	for i := 0; i < 250; i++ {
		rows = append(rows, mk(i+1))
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Offset *int `json:"offset"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.OperationName == "TickerSubscribers" {
			_, _ = w.Write([]byte(`{"data":{"ticker":{"subscribers":{"items":[],"hasNextPage":false,"endCursor":null}}}}`))
			return
		}
		if body.OperationName != "ListTickersPage" {
			_, _ = w.Write([]byte(`{"data":{"app":{"id":"app1","urn":"hrn:app:acme.com:ops","name":"Ops"}}}`))
			return
		}
		off := 0
		if body.Variables.Offset != nil {
			off = *body.Variables.Offset
		}
		end := min(off+200, len(rows))
		page := []string{}
		if off < len(rows) {
			page = rows[off:end]
		}
		_, _ = fmt.Fprintf(w, `{"data":{"tickersPage":{"total":%d,"hasMore":%v,"items":[%s]}}}`, len(rows), end < len(rows), strings.Join(page, ","))
		if calls++; calls == 1 {
			rows = append([]string{mk(0)}, rows...)
		}
	}))
	t.Cleanup(srv.Close)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ticker", "list", "--app", "acme.com:ops", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("an insert between pages must not fail the list: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil || len(got) != 251 {
		t.Fatalf("want all 251 tickers, got %d (%v)", len(got), err)
	}
	ids := map[any]bool{}
	for _, g := range got {
		ids[g["id"]] = true
	}
	if len(ids) != 251 || !ids[fmt.Sprintf("ctk%023d", 0)] {
		t.Fatalf("want 251 unique tickers including the inserted one, got %d unique", len(ids))
	}
}

// The ticker twin of the session fail-closed pins. A list
// that keeps growing past every restart still fails, saying the list changed;
// a server that ignores the offset keeps the "not advancing" diagnosis.
func TestTickerListFailsClosedWhenRestartsRunOut(t *testing.T) {
	mk := func(i int) string {
		return strings.Replace(tickerFixture, "cticker000000000000000000", fmt.Sprintf("ctk%023d", i), 1)
	}
	rows := []string{}
	for i := 0; i < 250; i++ {
		rows = append(rows, mk(i+1))
	}
	next := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Offset *int `json:"offset"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.OperationName == "TickerSubscribers" {
			_, _ = w.Write([]byte(`{"data":{"ticker":{"subscribers":{"items":[],"hasNextPage":false,"endCursor":null}}}}`))
			return
		}
		if body.OperationName != "ListTickersPage" {
			_, _ = w.Write([]byte(`{"data":{"app":{"id":"app1","urn":"hrn:app:acme.com:ops","name":"Ops"}}}`))
			return
		}
		off := 0
		if body.Variables.Offset != nil {
			off = *body.Variables.Offset
		}
		end := min(off+200, len(rows))
		page := []string{}
		if off < len(rows) {
			page = rows[off:end]
		}
		_, _ = fmt.Fprintf(w, `{"data":{"tickersPage":{"total":%d,"hasMore":%v,"items":[%s]}}}`, len(rows), end < len(rows), strings.Join(page, ","))
		// Every page read is followed by a fresh insert at the top.
		next++
		rows = append([]string{mk(1000 + next)}, rows...)
	}))
	t.Cleanup(srv.Close)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ticker", "list", "--app", "acme.com:ops", "--json", "--server", srv.URL})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != 7 || out.String() != "" {
		t.Fatalf("want exit 7 and no partial list, got %v out %q", err, out.String())
	}
	if !strings.Contains(err.Error(), "changed while it was being read") || strings.Contains(err.Error(), "not advancing") {
		t.Errorf("churn must read as a changing list to retry, got %v", err)
	}
}

// The ticker twin of the session lying-total pin: 250 reported, 249 served on
// honest pages. Nothing changes between passes, so the diagnosis is "not
// advancing" with no retry hint, stdout stays empty and the requests stay bounded.
func TestTickerListLyingTotalFailsAsNotAdvancing(t *testing.T) {
	mk := func(i int) string {
		return strings.Replace(tickerFixture, "cticker000000000000000000", fmt.Sprintf("ctk%023d", i), 1)
	}
	rows := []string{}
	for i := 0; i < 249; i++ {
		rows = append(rows, mk(i+1))
	}
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Offset *int `json:"offset"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "TickerSubscribers":
			_, _ = w.Write([]byte(`{"data":{"ticker":{"subscribers":{"items":[],"hasNextPage":false,"endCursor":null}}}}`))
		case "ListTickersPage":
			pages++
			off := 0
			if body.Variables.Offset != nil {
				off = *body.Variables.Offset
			}
			end := min(off+200, len(rows))
			page := []string{}
			if off < len(rows) {
				page = rows[off:end]
			}
			_, _ = fmt.Fprintf(w, `{"data":{"tickersPage":{"total":250,"hasMore":%v,"items":[%s]}}}`, end < len(rows), strings.Join(page, ","))
		default:
			_, _ = w.Write([]byte(`{"data":{"app":{"id":"app1","urn":"hrn:app:acme.com:ops","name":"Ops"}}}`))
		}
	}))
	t.Cleanup(srv.Close)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ticker", "list", "--app", "acme.com:ops", "--json", "--server", srv.URL})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != 7 || out.String() != "" {
		t.Fatalf("want exit 7 and no partial list, got %v out %q", err, out.String())
	}
	if !strings.Contains(err.Error(), "not advancing") || strings.Contains(err.Error(), "retry") {
		t.Errorf("a lying total must read as not advancing, not as churn to retry: %v", err)
	}
	if pages > 12 {
		t.Errorf("a lying total must stop after one restarted pass, made %d page requests", pages)
	}
}
