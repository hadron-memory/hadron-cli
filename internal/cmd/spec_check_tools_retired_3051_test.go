package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3051: `spec check-tools` skips RETIRED specs (the `superseded` tag, which every retirement
// writes, supersede and rescind alike: cor:spc:010:02). A retired rule is the record of what the
// contract said, so it may cite a tool the server has since removed. A LIVE spec's finding is never
// swallowed, and the skip is counted where a reader sees it.

type retiredSpec3051 struct {
	loc  string
	tags string
	body string
}

// retiredCorpus3051 serves a listing and a nodeBatch that answers ONLY for the refs it is asked
// about, so a spec the command never asks for can never be read, and records every ref asked.
func retiredCorpus3051(t *testing.T, specs []retiredSpec3051) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var req struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var vars struct {
			Refs []string `json:"refs"`
		}
		_ = json.Unmarshal(req.Variables, &vars)
		switch req.OperationName {
		case "FindNodes":
			var nodes []string
			for _, s := range specs {
				nodes = append(nodes, specNodeList(s.loc, s.tags))
			}
			_, _ = w.Write([]byte(translateFindNodes("FindNodes", pageBeyondFirst("FindNodes", req.Variables, `{"data":{"nodes":[`+strings.Join(nodes, ",")+`]}}`))))
		case "NodeBatch", "NodeBatchContent":
			mu.Lock()
			asked = append(asked, vars.Refs...)
			mu.Unlock()
			var nodes []string
			for _, s := range specs {
				for _, ref := range vars.Refs {
					if ref == "id-"+s.loc {
						nodes = append(nodes, specBatchNodeBody(s.loc, s.body))
					}
				}
			}
			_, _ = w.Write([]byte(`{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` + strings.Join(nodes, ",") + `]}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), asked...)
	}
}

const (
	retiredTags3051 = `["spec","p1","superseded"]`
	liveTags3051    = `["spec","p1"]`
	bogus3051       = "Cites hadron_removed_tool_3051 once."
)

func runCheckTools3051(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"spec", "check-tools", "-m", specMem, "--server", srv.URL}, args...))
	err := root.Execute()
	return out.String(), err
}

// A retired spec citing a removed tool is history: no finding, exit 0, and the OK line says one
// retired spec was skipped. The word is "retired" (a rescinded spec is skipped too), not "replaced".
func TestSpecCheckToolsSkipsARetiredSpec3051(t *testing.T) {
	srv, _ := retiredCorpus3051(t, []retiredSpec3051{
		{"cor:agt:120:03", retiredTags3051, bogus3051},
		{"cor:agt:120:04", liveTags3051, "Uses hadron_get_node."},
	})
	out, err := runCheckTools3051(t, srv)
	if err != nil {
		t.Fatalf("a retired spec's removed-tool citation must not fail the gate: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 retired spec skipped") {
		t.Errorf("the OK line must count the skip (\"1 retired spec skipped\"):\n%s", out)
	}
	if strings.Contains(strings.ToLower(out), "replaced") {
		t.Errorf("say retired, not replaced:\n%s", out)
	}
}

// The skip never swallows a live spec: the live finding stays and exits 5, whatever retired specs
// sit beside it, and --json is still the bare findings array naming only the live citation.
func TestSpecCheckToolsKeepsALiveFindingBesideARetiredOne3051(t *testing.T) {
	srv, _ := retiredCorpus3051(t, []retiredSpec3051{
		{"cor:agt:120:03", retiredTags3051, bogus3051},
		{"cor:agt:050:13", liveTags3051, bogus3051},
	})
	out, err := runCheckTools3051(t, srv, "--json")
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("a live spec's finding must still exit %d, got %d\n%s", exitcode.Conflict, got, out)
	}
	var findings []struct {
		Citation string `json:"citation"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal([]byte(out), &findings); err != nil {
		t.Fatalf("--json must stay the bare findings array: %v\n%s", err, out)
	}
	if len(findings) != 1 || findings[0].Citation != "cor:agt:050:13" || findings[0].Token != "hadron_removed_tool_3051" {
		t.Errorf("findings = %+v, want only the live spec's", findings)
	}
}

// A corpus of only retired specs prints [] in --json, and the retired spec is never even read.
func TestSpecCheckToolsNeverReadsARetiredSpec3051(t *testing.T) {
	srv, asked := retiredCorpus3051(t, []retiredSpec3051{
		{"cor:agt:120:03", retiredTags3051, bogus3051},
	})
	out, err := runCheckTools3051(t, srv, "--json")
	if err != nil {
		t.Fatalf("only a retired spec: want exit 0: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("--json must be [] for a clean corpus, got %q", out)
	}
	if got := asked(); len(got) != 0 {
		t.Errorf("a retired spec must not be read, nodeBatch asked for %v", got)
	}
}

// With findings, the text report still says how many retired specs were skipped, so a mass
// retirement cannot hollow out the gate unnoticed.
func TestSpecCheckToolsCountsTheSkipBesideFindings3051(t *testing.T) {
	srv, _ := retiredCorpus3051(t, []retiredSpec3051{
		{"cor:agt:120:03", retiredTags3051, bogus3051},
		{"cor:agt:050:13", liveTags3051, bogus3051},
	})
	out, err := runCheckTools3051(t, srv)
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("want exit %d, got %d\n%s", exitcode.Conflict, got, out)
	}
	if !strings.Contains(out, "1 retired spec skipped") {
		t.Errorf("the findings report must count the skip:\n%s", out)
	}
}
