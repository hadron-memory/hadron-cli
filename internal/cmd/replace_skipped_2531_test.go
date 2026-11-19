package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #2531: `replace text` reports how many selected nodes the server skipped
// (nodesSelected / nodesSkipped), so "Replaced N across M of K scanned" can no
// longer read as "the old term is gone everywhere" while skipped nodes still
// carry it. Text mode prints "Skipped N node(s) ..." after the unchanged
// totals line only when N > 0; --json always carries both keys.

func skippedBody(dry bool, scanned, selected, skipped, total int) string {
	d := "false"
	if dry {
		d = "true"
	}
	changed := 0
	results := `[]`
	if total > 0 {
		changed = 1
		results = `[{"nodeId":"n1","loc":"kb:a","memoryId":"m1","replacements":` + itoa2531(total) + `,"fields":[{"field":"content","matches":` + itoa2531(total) + `}]}]`
	}
	return `{"data":{"searchReplaceInNodes":{"nodesScanned":` + itoa2531(scanned) +
		`,"nodesSelected":` + itoa2531(selected) + `,"nodesSkipped":` + itoa2531(skipped) +
		`,"nodesChanged":` + itoa2531(changed) + `,"totalReplacements":` + itoa2531(total) +
		`,"dryRun":` + d + `,"results":` + results + `}}}`
}

func itoa2531(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// serveSkipped answers the preview (dryRun true) and the applied request
// (dryRun false) with the same counts.
func serveSkipped(t *testing.T, selected, skipped, total int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Input struct {
					DryRun *bool `json:"dryRun"`
				} `json:"input"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		dry := body.Variables.Input.DryRun != nil && *body.Variables.Input.DryRun
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(skippedBody(dry, 3, selected, skipped, total)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runReplace2531(t *testing.T, srv *httptest.Server, extra ...string) (stdout, stderr string) {
	t.Helper()
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	root := NewRootCmd(f)
	argv := append([]string{"replace", "text", "cat", "dog", "-m", "acme.com::kb", "--field", "content", "--server", srv.URL}, extra...)
	root.SetArgs(argv)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute %v: %v", extra, err)
	}
	return out.String(), errOut.String()
}

func TestReplaceTextShowsSkippedCount2531(t *testing.T) {
	const totals = "across 1 of 3 node(s) scanned"
	for _, c := range []struct {
		name  string
		extra []string
	}{
		{"dry-run", []string{"--dry-run"}},
		{"applied", []string{"--yes"}},
	} {
		t.Run(c.name+"/text", func(t *testing.T) {
			out, _ := runReplace2531(t, serveSkipped(t, 5, 2, 4), c.extra...)
			lines := strings.Split(out, "\n")
			if !strings.Contains(lines[0], totals) {
				t.Fatalf("the totals line must be unchanged: %q", lines[0])
			}
			if !strings.HasPrefix(lines[1], "Skipped 2 node(s)") {
				t.Fatalf("a Skipped line must follow the totals line: %q", out)
			}
		})
		t.Run(c.name+"/json", func(t *testing.T) {
			out, _ := runReplace2531(t, serveSkipped(t, 5, 2, 4), append([]string{"--json"}, c.extra...)...)
			var got map[string]any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("json: %v\n%s", err, out)
			}
			if got["nodesSelected"] != float64(5) || got["nodesSkipped"] != float64(2) {
				t.Fatalf("nodesSelected/nodesSkipped missing or wrong: %v", got)
			}
			for _, k := range []string{"nodesScanned", "nodesChanged", "totalReplacements", "dryRun", "results"} {
				if _, ok := got[k]; !ok {
					t.Fatalf("existing key %q must stay: %v", k, got)
				}
			}
		})
	}
}

// Nothing skipped: no Skipped line in text, but --json still carries both keys (0).
func TestReplaceTextNothingSkipped2531(t *testing.T) {
	for _, extra := range [][]string{{"--dry-run"}, {"--yes"}} {
		out, _ := runReplace2531(t, serveSkipped(t, 3, 0, 4), extra...)
		if strings.Contains(out, "Skipped") {
			t.Fatalf("nodesSkipped 0 must print no Skipped line: %q", out)
		}
		jout, _ := runReplace2531(t, serveSkipped(t, 3, 0, 4), append([]string{"--json"}, extra...)...)
		var got map[string]any
		if err := json.Unmarshal([]byte(jout), &got); err != nil {
			t.Fatal(err)
		}
		if v, ok := got["nodesSkipped"]; !ok || v != float64(0) {
			t.Fatalf("--json must carry nodesSkipped 0: %v", got)
		}
		if v, ok := got["nodesSelected"]; !ok || v != float64(3) {
			t.Fatalf("--json must carry nodesSelected 3: %v", got)
		}
	}
}

// The no-match run writes nothing and says so on stderr; the skipped count
// still shows on stdout when nodes were skipped.
func TestReplaceTextNoMatchStillShowsSkipped2531(t *testing.T) {
	out, errOut := runReplace2531(t, serveSkipped(t, 5, 2, 0), "--yes")
	if !strings.Contains(errOut, "No matches") {
		t.Fatalf("no-match note missing: %q", errOut)
	}
	if !strings.Contains(out, "Skipped 2 node(s)") {
		t.Fatalf("skipped count must show on a no-match run: %q", out)
	}
}
