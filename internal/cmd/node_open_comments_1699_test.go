package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/node"
)

type openCall1699 struct {
	Op    string
	Query string
	Vars  map[string]json.RawMessage
}
type openRun1699 struct {
	Calls []openCall1699
	Out   string
	Code  int
}

var openModes1699 = map[string]struct {
	args     []string
	wantRows int
}{
	"drain":     {nil, 3},
	"page":      {[]string{"--limit", "1", "--offset", "1"}, 1},
	"search":    {[]string{"--search", "needle"}, 3},
	"install":   {[]string{"--install", c7Install}, 3},
	"seq":       {[]string{"--seq-gt", "1", "--sort-seq", "desc", "--limit", "1"}, 1},
	"projected": {[]string{"--with-data", "--with-properties"}, 3},
}

// Stateful clamped-page fake: records the actual query AND variables on each
// call. It does not claim to prove the real provider predicate (independent QA).
func runOpen1699(t *testing.T, summary string, args ...string) openRun1699 {
	t.Helper()
	var mu sync.Mutex
	var calls []openCall1699
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Op    string                     `json:"operationName"`
			Query string                     `json:"query"`
			Vars  map[string]json.RawMessage `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		calls = append(calls, openCall1699{q.Op, q.Query, q.Vars})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if q.Op == "InstallMemory" {
			fmt.Fprint(w, c7MemoryResponse)
			return
		}
		if summary == "old-provider" {
			fmt.Fprint(w, `{"errors":[{"message":"Cannot query field commentSummary","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`)
			return
		}

		if summary == "no-list" {
			fmt.Fprint(w, `{"data":{}}`)
			return
		}
		if summary == "null-hits" {
			fmt.Fprint(w, `{"data":{"findNodes":{"total":3,"hits":null}}}`)
			return
		}
		if summary == "missing-hits" {
			fmt.Fprint(w, `{"data":{"findNodes":{"total":3}}}`)
			return
		}
		if summary == "null-hit" {
			fmt.Fprint(w, `{"data":{"findNodes":{"hits":[null]}}}`)
			return
		}
		if summary == "null-node" {
			fmt.Fprint(w, `{"data":{"findNodes":{"hits":[{"node":null}]}}}`)
			return
		}
		var off int
		_ = json.Unmarshal(q.Vars["offset"], &off)
		hits := []string{}
		if off < 3 && summary != "empty" {
			extra := ""
			if summary != "" {
				current := summary
				if summary == "later-bad" {
					current = `{"openThreads":1,"oldestOpenThreadAt":"2026-11-05T11:00:00Z"}`
					if off > 0 {
						current = "null"
					}
				}
				extra = `,"commentSummary":` + current
			}
			hits = append(hits, fmt.Sprintf(`{"score":null,"node":{"id":"n%d","memoryId":"mem1","loc":"item:%d","name":"Node","nodeType":"info","tags":[],"seq":%d,"isRunnable":true,"role":null,"updatedAt":"2026-11-05T00:00:00Z","properties":{"p":1},"data":{"d":2}%s}}`, off, off, off+1, extra))
		}
		fmt.Fprintf(w, `{"data":{"findNodes":{"total":null,"degraded":null,"reason":null,"hits":[%s]}}}`, strings.Join(hits, ","))
	}))
	defer srv.Close()
	prev := node.CommentListNow
	node.CommentListNow = func() time.Time { return time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC) }
	defer func() { node.CommentListNow = prev }()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"node", "list", "--server", srv.URL}, args...))
	err := root.Execute()
	mu.Lock()
	defer mu.Unlock()
	return openRun1699{append([]openCall1699(nil), calls...), out.String(), exitCodeFor(err)}
}

func TestNodeOpenComments1699WireAndModes(t *testing.T) {
	for mode, tc := range openModes1699 {
		for _, flag := range []string{"--has-unresolved-comments", "--has-unresolved-comments=false"} {
			t.Run(mode+flag, func(t *testing.T) {
				summary := `{"openThreads":2,"oldestOpenThreadAt":"2026-11-02T11:59:00Z"}`
				if strings.HasSuffix(flag, "=false") {
					summary = `{"openThreads":0,"oldestOpenThreadAt":null}`
				}
				argv := append(append([]string{}, tc.args...), flag, "--json", "--memory-class", "system", "--role", "boot-prompt", "--prefix", "item:")
				r := runOpen1699(t, summary, argv...)
				if r.Code != 0 {
					t.Fatalf("exit%d: %s", r.Code, r.Out)
				}
				var rows []map[string]json.RawMessage
				if err := json.Unmarshal([]byte(r.Out), &rows); err != nil {
					t.Fatalf("decode --json payload: %v\n%s", err, r.Out)
				}
				if len(rows) != tc.wantRows {
					t.Fatalf("decoded row count: got %d, want %d", len(rows), tc.wantRows)
				}
				finds := 0
				for _, c := range r.Calls {
					if c.Op == "InstallMemory" {
						continue
					}
					finds++
					var f map[string]json.RawMessage
					if err := json.Unmarshal(c.Vars["filter"], &f); err != nil {
						t.Fatal(err)
					}
					want := !strings.HasSuffix(flag, "=false")
					if string(f["hasOpenThreads"]) != fmt.Sprint(want) {
						t.Fatalf("filter on page%d: %s", finds, c.Vars["filter"])
					}
					if string(f["role"]) != `"boot-prompt"` || string(f["locPrefix"]) != `"item:"` || string(f["memoryClasses"]) != `["system"]` {
						t.Fatalf("AND filter: %s", c.Vars["filter"])
					}
					if !strings.Contains(c.Query, "commentSummary") || !strings.Contains(c.Query, "oldestOpenThreadAt") {
						t.Fatal("summary must be selected")
					}
				}
				if mode == "page" && finds != 1 {
					t.Fatalf("explicit page calls%d", finds)
				}
				if mode != "page" && finds != 4 {
					t.Fatalf("clamped drain calls%d", finds)
				}
				for _, row := range rows {
					if compact(t, row["commentSummary"]) != summary {
						t.Fatalf("summary verbatim: %s", row["commentSummary"])
					}
				}
			})
		}
	}
}

func TestNodeOpenComments1699OmittedBaseline(t *testing.T) {
	got := map[string]string{}
	for mode, tc := range openModes1699 {
		for _, format := range []string{"text", "json"} {
			argv := append([]string{}, tc.args...)
			if format == "json" {
				argv = append(argv, "--json")
			}
			r := runOpen1699(t, `{"openThreads":2,"oldestOpenThreadAt":"2026-11-02T11:59:00Z"}`, argv...)
			if r.Code != 0 {
				t.Fatalf("%s/%s exit%d", mode, format, r.Code)
			}
			for _, c := range r.Calls {
				if c.Op == "InstallMemory" {
					continue
				}
				if strings.Contains(c.Query, "commentSummary") {
					t.Error("unrequested query names new field")
				}
				var f map[string]json.RawMessage
				_ = json.Unmarshal(c.Vars["filter"], &f)
				if _, present := f["hasOpenThreads"]; present {
					t.Error("omitted filter key was sent")
				}
			}
			got[mode+"/"+format] = r.Out
		}
	}
	path := filepath.Join("testdata", "node-open-comments-1699-baseline.json")
	if os.Getenv("CAPTURE_NODE_BASELINE_1699") == "1" {
		b, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]string
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	for k, v := range got {
		if v != want[k] {
			t.Errorf("omitted output changed %s: got%q want%q", k, v, want[k])
		}
	}
}

func TestNodeOpenComments1699AgeAndText(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		ago  time.Duration
		want string
	}{{0, "<1m"}, {59 * time.Second, "<1m"}, {time.Minute - time.Nanosecond, "<1m"}, {time.Minute, "1m"}, {59 * time.Minute, "59m"}, {time.Hour - time.Nanosecond, "59m"}, {time.Hour, "1h"}, {23*time.Hour + 59*time.Minute, "23h"}, {24*time.Hour - time.Nanosecond, "23h"}, {24 * time.Hour, "1d"}, {400 * 24 * time.Hour, "400d"}, {-5 * time.Minute, "<1m"}} {
		stamp := now.Add(-tc.ago).Format(time.RFC3339Nano)
		summary := fmt.Sprintf(`{"openThreads":1,"oldestOpenThreadAt":%q}`, stamp)
		r := runOpen1699(t, summary, "--has-unresolved-comments", "--limit", "1")
		if r.Code != 0 || !strings.Contains(r.Out, "OPEN THREADS") || !strings.Contains(r.Out, "OLDEST") || func() bool { cells := strings.Fields(r.Out); return len(cells) == 0 || cells[len(cells)-1] != tc.want }() {
			t.Fatalf("%s: exit%d %q", tc.want, r.Code, r.Out)
		}
	}
}

func TestNodeOpenComments1699MalformedAndOldProvider(t *testing.T) {
	for _, summary := range []string{"", `null`, `{"openThreads":null,"oldestOpenThreadAt":null}`, `{"oldestOpenThreadAt":null}`, `{"openThreads":-1,"oldestOpenThreadAt":null}`, `{"openThreads":-1,"oldestOpenThreadAt":"2026-11-05T11:00:00Z"}`, "later-bad", "no-list", "null-hits", "missing-hits", "null-hit", "null-node", `{"openThreads":1,"oldestOpenThreadAt":null}`, `{"openThreads":0,"oldestOpenThreadAt":"2026-01-01T00:00:00Z"}`, `{"openThreads":1,"oldestOpenThreadAt":"bad"}`, `{"openThreads":1,"oldestOpenThreadAt":"2026-11-05T11:00:00+24:00"}`, `{"openThreads":1,"oldestOpenThreadAt":"2026-11-05T11:00:00,5Z"}`} {
		r := runOpen1699(t, summary, "--has-unresolved-comments", "--json")
		if r.Code != 1 || r.Out != "" {
			t.Fatalf("%s: exit%d output%q", summary, r.Code, r.Out)
		}
	}
	r := runOpen1699(t, "old-provider", "--has-unresolved-comments", "--limit", "1")
	if r.Code != 2 || len(r.Calls) != 1 || r.Out != "" {
		t.Fatalf("old provider: %+v", r)
	}
}

func TestNodeOpenComments1699ProjectedPlacementAndEmpty(t *testing.T) {
	for _, flag := range []string{"--has-unresolved-comments", "--has-unresolved-comments=false"} {
		summary := `{"openThreads":1,"oldestOpenThreadAt":"2026-11-02T12:00:00Z"}`
		if strings.HasSuffix(flag, "=false") {
			summary = `{"openThreads":0,"oldestOpenThreadAt":null}`
		}
		for _, projection := range [][]string{{"--with-data"}, {"--with-properties"}, {"--with-data", "--with-properties"}} {
			args := append(append([]string{}, projection...), flag, "--limit", "1")
			r := runOpen1699(t, summary, args...)
			if r.Code != 0 {
				t.Fatalf("exit%d", r.Code)
			}
			marker := strings.Index(r.Out, "OPEN THREADS")
			oldest := strings.Index(r.Out, "OLDEST")
			if marker < 0 || oldest < marker {
				t.Fatal(r.Out)
			}
			for _, column := range []string{"data:", "properties:"} {
				if idx := strings.Index(r.Out, column); idx >= 0 && idx < oldest {
					t.Fatalf("summary after projection: %s", r.Out)
				}
			}
			if strings.HasSuffix(flag, "=false") && !strings.Contains(r.Out, "OPEN THREADS: 0  OLDEST: -") {
				t.Fatal(r.Out)
			}
			bad := runOpen1699(t, "later-bad", append(append([]string{}, projection...), flag)...)
			if bad.Code != 1 || bad.Out != "" {
				t.Fatalf("partial malformed projected output: %+v", bad)
			}
		}
		for _, args := range [][]string{nil, {"--json"}, {"--with-data"}} {
			baseline := runOpen1699(t, "empty", args...)
			selected := runOpen1699(t, "empty", append(append([]string{}, args...), flag)...)
			if selected.Code != 0 || selected.Out != baseline.Out {
				t.Fatalf("empty output changed: %q vs%q", selected.Out, baseline.Out)
			}
		}
	}
}

func TestNodeOpenComments1699CombinesAllFilters(t *testing.T) {
	r := runOpen1699(t, `{"openThreads":1,"oldestOpenThreadAt":"2026-11-05T11:00:00Z"}`, "--has-unresolved-comments", "-m", "m1", "--children-of", "item", "--type", "info", "--runnable=false", "--tag", "a", "--limit", "1", "--json")
	if r.Code != 0 {
		t.Fatalf("exit%d", r.Code)
	}
	var filter map[string]json.RawMessage
	_ = json.Unmarshal(r.Calls[0].Vars["filter"], &filter)
	for key, want := range map[string]string{"hasOpenThreads": "true", "memoryIds": `["m1"]`, "childrenOf": `"item"`, "nodeType": `"info"`, "isRunnable": "false", "tags": `["a"]`} {
		if string(filter[key]) != want {
			t.Errorf("%s=%s want%s", key, filter[key], want)
		}
	}
}

func TestNodeOpenComments1699Documents(t *testing.T) {
	has := func(document, field string) bool {
		for _, line := range strings.Split(document, "\n") {
			if strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "{")) == field {
				return true
			}
		}
		return false
	}
	for _, field := range []string{"commentSummary", "openThreads", "oldestOpenThreadAt"} {
		if has(gen.FindNodes_Operation, field) {
			t.Errorf("ordinary document names %s", field)
		}
		if !has(gen.FindNodesWithCommentSummary_Operation, field) {
			t.Errorf("opt-in document omits %s", field)
		}
	}
}

func TestNodeOpenComments1699TimestampVerbatim(t *testing.T) {
	summary := `{"openThreads":1,"oldestOpenThreadAt":"2026-11-05T12:30:00.123456789+01:00"}`
	r := runOpen1699(t, summary, "--has-unresolved-comments", "--limit", "1", "--json")
	if r.Code != 0 {
		t.Fatalf("exit%d", r.Code)
	}
	rows := decodeRows(t, r.Out)
	if len(rows) != 1 || compact(t, rows[0]["commentSummary"]) != summary {
		t.Fatalf("timestamp changed: %s", r.Out)
	}
}
