package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// pagedSource serves one operation as a window over rows, honouring the
// request's limit/offset but never more than serve rows per page (a server
// clamp below the asked-for size, cli#967). wrap renders one page.
type pagedSource struct {
	rows  []string
	serve int
	// wrap builds the full response body from the page's rows and the
	// number of rows in the whole list.
	wrap func(page []string, total int) string
	// failAtCall (1-based) answers that call with a GraphQL error.
	failAtCall int
}

// pagedRecord is what the fake saw for one operation.
type pagedRecord struct {
	mu    sync.Mutex
	Calls []map[string]any
}

func (p *pagedRecord) add(v map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Calls = append(p.Calls, v)
}

// pagedGraphQL routes by operationName: sources are paged, others answers are
// static. The returned map holds the recorded variables per operation.
func pagedGraphQL(t *testing.T, sources map[string]*pagedSource, static map[string]string) (*httptest.Server, map[string]*pagedRecord) {
	t.Helper()
	records := map[string]*pagedRecord{}
	for op := range sources {
		records[op] = &pagedRecord{}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if src, ok := sources[body.OperationName]; ok {
			rec := records[body.OperationName]
			rec.add(body.Variables)
			if src.failAtCall > 0 && len(rec.Calls) == src.failAtCall {
				_, _ = w.Write([]byte(`{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`))
				return
			}
			limit, offset := len(src.rows), 0
			if v, ok := body.Variables["limit"].(float64); ok {
				limit = int(v)
			}
			if v, ok := body.Variables["offset"].(float64); ok {
				offset = int(v)
			}
			if src.serve > 0 && limit > src.serve {
				limit = src.serve
			}
			end := offset + limit
			if end > len(src.rows) {
				end = len(src.rows)
			}
			page := []string{}
			if offset < len(src.rows) {
				page = src.rows[offset:end]
			}
			_, _ = w.Write([]byte(src.wrap(page, len(src.rows))))
			return
		}
		resp, ok := static[body.OperationName]
		if !ok {
			resp, ok = unstubbedDefault(body.OperationName)
		}
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(server.Close)
	return server, records
}

// itemsEnvelope wraps a page as {"data":{field:{"total":N,"items":[...]}}}.
func itemsEnvelope(field string) func([]string, int) string {
	return func(page []string, total int) string {
		return fmt.Sprintf(`{"data":{"%s":{"total":%d,"items":[%s]}}}`, field, total, strings.Join(page, ","))
	}
}
