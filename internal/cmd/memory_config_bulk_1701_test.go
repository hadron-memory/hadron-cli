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

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

type bulk1701Call struct {
	DryRun   bool     `json:"dryRun"`
	Refs     []string `json:"memoryRefs"`
	Revision *int     `json:"expectedTemplateRevision"`
}

func bulk1701Server(t *testing.T, answer func(bulk1701Call) string) (*httptest.Server, func() []bulk1701Call) {
	t.Helper()
	var mu sync.Mutex
	calls := []bulk1701Call{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string
			Variables     bulk1701Call
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.OperationName != "ApplyMemoryConfigTemplateToMemories" {
			t.Errorf("operation=%q", body.OperationName)
		}
		mu.Lock()
		calls = append(calls, body.Variables)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, answer(body.Variables))
	}))
	t.Cleanup(s.Close)
	return s, func() []bulk1701Call { mu.Lock(); defer mu.Unlock(); return append([]bulk1701Call{}, calls...) }
}
func bulk1701Payload(c bulk1701Call, revisions []int, failed bool, replaced bool) string {
	results := []any{}
	okCount := 0
	for i, ref := range c.Refs {
		entry := map[string]any{"index": i, "memoryRef": ref, "ok": true, "error": nil}
		rev := 7
		if i < len(revisions) {
			rev = revisions[i]
		}
		if failed {
			entry["ok"] = false
			entry["result"] = nil
			entry["error"] = map[string]any{"code": "MEMORY_NOT_FOUND", "message": "memory not found"}
		} else {
			okCount++
			outcome := "APPLIED"
			if replaced {
				outcome = "REPLACED"
			}
			entry["result"] = map[string]any{"memoryId": fmt.Sprintf("m%d", i), "template": map[string]any{"id": "t1", "name": "rules", "deleted": false}, "templateRevision": rev, "required": replaced, "dryRun": c.DryRun, "entries": []any{map[string]any{"role": "spec", "outcome": outcome, "rule": nil}}, "warnings": []any{}}
		}
		results = append(results, entry)
	}
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"applyMemoryConfigTemplateToMemories": map[string]any{"dryRun": c.DryRun, "succeeded": okCount, "failed": len(c.Refs) - okCount, "results": results}}})
	return string(b)
}
func TestBulkTemplate1701Wire(t *testing.T) {
	s, calls := bulk1701Server(t, func(c bulk1701Call) string { return bulk1701Payload(c, nil, false, false) })
	out, code := runConfig(t, s.URL, "memory", "config", "template", "apply", "t1", "acme.com::kb", "m2", "acme.com::kb", "--json", "--expected-revision", "7")
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out)
	}
	got := calls()
	if len(got) != 2 {
		t.Fatalf("calls=%v", got)
	}
	for i, c := range got {
		if c.DryRun != (i == 0) || c.Revision == nil || *c.Revision != 7 || strings.Join(c.Refs, ",") != "hrn:mem:acme.com:kb,m2,hrn:mem:acme.com:kb" {
			t.Fatalf("wire=%+v", got)
		}
	}
	var report struct {
		Succeeded int
		Results   []struct{ MemoryRef string }
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Succeeded != 3 || report.Results[0].MemoryRef != "hrn:mem:acme.com:kb" {
		t.Fatal(out)
	}
}
func TestBulkTemplate1701Guards(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		revisions                  []int
		failed, replaced, dry, yes bool
		code, n                    int
	}{
		{name: "drift", revisions: []int{7, 8}, code: 5, n: 1}, {name: "all failed", failed: true, code: 1, n: 1}, {name: "replace refused", replaced: true, code: 2, n: 1}, {name: "replace yes", replaced: true, yes: true, n: 2}, {name: "dry", dry: true, n: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, calls := bulk1701Server(t, func(c bulk1701Call) string { return bulk1701Payload(c, tc.revisions, tc.failed, tc.replaced) })
			args := []string{"memory", "config", "template", "apply", "t1", "m1", "m2", "--json"}
			if tc.dry {
				args = append(args, "--dry-run")
			}
			if tc.yes {
				args = append(args, "--yes")
			}
			out, code := runConfig(t, s.URL, args...)
			if code != tc.code || len(calls()) != tc.n {
				t.Fatalf("exit=%d calls=%v out=%s", code, calls(), out)
			}
		})
	}
}
func TestBulkTemplate1701File(t *testing.T) {
	p := filepath.Join(t.TempDir(), "memories")
	if err := os.WriteFile(p, []byte("# comment\n\nm2\n m3 \n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, calls := bulk1701Server(t, func(c bulk1701Call) string { return bulk1701Payload(c, nil, false, false) })
	out, code := runConfig(t, s.URL, "memory", "config", "template", "apply", "t1", "m1", "--memories-file", p, "--dry-run", "--json")
	if code != exitcode.OK || len(calls()) != 1 || strings.Join(calls()[0].Refs, ",") != "m1,m2,m3" {
		t.Fatalf("exit=%d calls=%v %s", code, calls(), out)
	}
}
