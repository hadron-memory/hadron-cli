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

var openModes1699 = map[string][]string{
	"drain": {}, "page": {"--limit", "1", "--offset", "1"}, "search": {"--search", "needle"},
	"install": {"--install", c7Install}, "seq": {"--seq-gt", "1", "--sort-seq", "desc", "--limit", "1"},
	"projected": {"--with-data", "--with-properties"},
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
		var off int
		_ = json.Unmarshal(q.Vars["offset"], &off)
		hits := []string{}
		if off < 3 {
			extra := ""
			if summary != "" {
				extra = `,"commentSummary":` + summary
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
	for mode, args := range openModes1699 {
		for _, flag := range []string{"--has-unresolved-comments", "--has-unresolved-comments=false"} {
			t.Run(mode+flag, func(t *testing.T) {
				summary := `{"openThreads":2,"oldestOpenThreadAt":"2026-11-02T11:59:00Z"}`
				if strings.HasSuffix(flag, "=false") {
					summary = `{"openThreads":0,"oldestOpenThreadAt":null}`
				}
				argv := append(append([]string{}, args...), flag, "--json", "--memory-class", "system", "--role", "boot-prompt", "--prefix", "item:")
				r := runOpen1699(t, summary, argv...)
				if r.Code != 0 {
					t.Fatalf("exit%d: %s", r.Code, r.Out)
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
					if string(f["role"]) != `"boot-prompt"` || string(f["locPrefix"]) != `"item:"` || string(f["memoryClasses"]) != `["SYSTEM"]` {
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
				rows := decodeRows(t, r.Out)
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
	for mode, args := range openModes1699 {
		for _, format := range []string{"text", "json"} {
			argv := append([]string{}, args...)
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
	}{{0, "<1m"}, {59 * time.Second, "<1m"}, {time.Minute, "1m"}, {59 * time.Minute, "59m"}, {time.Hour, "1h"}, {23*time.Hour + 59*time.Minute, "23h"}, {24 * time.Hour, "1d"}, {400 * 24 * time.Hour, "400d"}, {-5 * time.Minute, "<1m"}} {
		stamp := now.Add(-tc.ago).Format(time.RFC3339Nano)
		summary := fmt.Sprintf(`{"openThreads":1,"oldestOpenThreadAt":%q}`, stamp)
		r := runOpen1699(t, summary, "--has-unresolved-comments", "--limit", "1")
		if r.Code != 0 || !strings.Contains(r.Out, "OPEN THREADS") || !strings.Contains(r.Out, "OLDEST") || !strings.Contains(r.Out, tc.want) {
			t.Fatalf("%s: exit%d %q", tc.want, r.Code, r.Out)
		}
	}
}

func TestNodeOpenComments1699MalformedAndOldProvider(t *testing.T) {
	for _, summary := range []string{"", `null`, `{"openThreads":1,"oldestOpenThreadAt":null}`, `{"openThreads":0,"oldestOpenThreadAt":"2026-01-01T00:00:00Z"}`, `{"openThreads":1,"oldestOpenThreadAt":"bad"}`} {
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
