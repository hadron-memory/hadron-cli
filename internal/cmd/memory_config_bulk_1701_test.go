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
	"sync/atomic"
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
	seen := map[string]bool{}
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
		} else if seen[ref] {
			entry["ok"] = false
			entry["result"] = nil
			entry["error"] = map[string]any{"code": "DUPLICATE_TARGET", "message": "already named earlier"}
		} else {
			seen[ref] = true
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
	out, code := runBulk1701(t, s.URL, "memory", "config", "template", "apply", "t1", "acme.com::kb", "m2", "acme.com::kb", "--json", "--expected-revision", "7", "--yes")
	if code != 1 {
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
	if !strings.Contains(out, "DUPLICATE_TARGET") || report.Succeeded != 2 || report.Results[0].MemoryRef != "hrn:mem:acme.com:kb" {
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
			out, code := runBulk1701(t, s.URL, args...)
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
	out, code := runBulk1701(t, s.URL, "memory", "config", "template", "apply", "t1", "m1", "--memories-file", p, "--dry-run", "--json")
	if code != exitcode.OK || len(calls()) != 1 || strings.Join(calls()[0].Refs, ",") != "m1,m2,m3" {
		t.Fatalf("exit=%d calls=%v %s", code, calls(), out)
	}
}

func TestBulkTemplate1701Refusals(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
	}{{"UNAUTHENTICATED", 3}, {"FORBIDDEN", 8}, {"BAD_USER_INPUT", 2}, {"BULK_TARGETS_TOO_MANY", 2}, {"MEMORY_CONFIG_TEMPLATE_NOT_FOUND", 4}, {"CONFLICT", 5}} {
		t.Run(tc.code, func(t *testing.T) {
			s, calls := bulk1701Server(t, func(c bulk1701Call) string {
				return fmt.Sprintf(`{"errors":[{"message":"refused max 50 count 51","extensions":{"code":%q,"max":50,"count":51}}]}`, tc.code)
			})
			args := []string{"memory", "config", "template", "apply", "t1"}
			for i := 0; i < 51; i++ {
				args = append(args, fmt.Sprintf("m%d", i))
			}
			args = append(args, "--json")
			out, code := runBulk1701(t, s.URL, args...)
			if code != tc.want || len(calls()) != 1 || len(calls()[0].Refs) != 51 {
				t.Fatalf("exit=%d calls=%v out=%s", code, calls(), out)
			}
			if tc.code == "BULK_TARGETS_TOO_MANY" && (!strings.Contains(out, "50") || !strings.Contains(out, "51")) {
				t.Fatal(out)
			}
		})
	}
}
func TestBulkTemplate1701StdinAndTTY(t *testing.T) {
	t.Run("stdin once", func(t *testing.T) {
		s, calls := bulk1701Server(t, func(c bulk1701Call) string { return bulk1701Payload(c, nil, false, false) })
		f, out := testFactory(t)
		f.IOStreams.In = strings.NewReader("# comment\nm2\nm3\n")
		root := NewRootCmd(f)
		root.SetArgs([]string{"memory", "config", "template", "apply", "t1", "m1", "--memories-file", "-", "--server", s.URL, "--dry-run", "--json"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if len(calls()) != 1 || strings.Join(calls()[0].Refs, ",") != "m1,m2,m3" {
			t.Fatalf("calls=%v %s", calls(), out)
		}
	})
	for _, answer := range []string{"y\n", "n\n"} {
		t.Run("TTY"+answer, func(t *testing.T) {
			s, calls := bulk1701Server(t, func(c bulk1701Call) string { return bulk1701Payload(c, nil, false, true) })
			f, _, errOut := testFactoryTTY(t, answer)
			root := NewRootCmd(f)
			root.SetArgs([]string{"memory", "config", "template", "apply", "t1", "m1", "m2", "--server", s.URL})
			err := root.Execute()
			want := 2
			if answer == "n\n" {
				want = 1
				if exitCodeFor(err) != exitcode.Cancelled {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(calls()) != want || strings.Count(errOut.String(), "(y/N)") != 1 || !strings.Contains(errOut.String(), "m1: spec") || !strings.Contains(errOut.String(), "m2: spec") {
				t.Fatalf("calls=%v prompt=%s", calls(), errOut)
			}
		})
	}
}
func TestBulkTemplate1701MixedReport(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonMode), func(t *testing.T) {
			s, calls := bulk1701Server(t, func(c bulk1701Call) string {
				var p map[string]any
				_ = json.Unmarshal([]byte(bulk1701Payload(c, nil, false, false)), &p)
				r := p["data"].(map[string]any)["applyMemoryConfigTemplateToMemories"].(map[string]any)
				entries := r["results"].([]any)
				codes := []string{"MEMORY_NOT_FOUND", "DUPLICATE_TARGET", "CONFLICT", "INTERNAL_SERVER_ERROR", "FUTURE_CODE"}
				for i, code := range codes {
					e := entries[i+1].(map[string]any)
					e["ok"] = false
					e["result"] = nil
					er := map[string]any{"code": code, "message": "failed\u001b[31m"}
					if code == "CONFLICT" {
						er["currentRevision"] = 8
					}
					if code == "INTERNAL_SERVER_ERROR" {
						er["requestId"] = "req-1"
					}
					e["error"] = er
				}
				r["succeeded"] = 1
				r["failed"] = 5
				b, _ := json.Marshal(p)
				return string(b)
			})
			args := []string{"memory", "config", "template", "apply", "t1", "acme.com::kb", "m2", "m3", "m4", "m5", "m6", "--yes"}
			if jsonMode {
				args = append(args, "--json")
			}
			out, code := runBulk1701(t, s.URL, args...)
			if code != 1 || len(calls()) != 2 {
				t.Fatalf("exit=%d calls=%v out=%s", code, calls(), out)
			}
			if jsonMode {
				var r map[string]any
				if err := json.Unmarshal([]byte(out), &r); err != nil {
					t.Fatalf("report plus extra error? %v %s", err, out)
				}
				if r["results"].([]any)[0].(map[string]any)["memoryRef"] != "hrn:mem:acme.com:kb" {
					t.Fatal(out)
				}
			} else {
				for _, want := range []string{"[0] acme.com::kb", "1 succeeded, 5 failed", "currentRevision 8", "requestId req-1", "FUTURE_CODE"} {
					if !strings.Contains(out, want) {
						t.Fatalf("missing %q in %s", want, out)
					}
				}
				if strings.Contains(out, "\x1b") {
					t.Fatal("unsafe terminal bytes")
				}
			}
		})
	}
}

func runBulk1701(t *testing.T, server string, args ...string) (string, int) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args = append(args, "--server", server)
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		return out.String(), 0
	}
	code := renderFailure(f, args, err)
	return out.String(), code
}

func TestBulkTemplate1701MalformedPreview(t *testing.T) {
	for _, realPhase := range []bool{false, true} {
		for _, mutation := range []string{"missing result", "wrong index", "wrong echo", "wrong count", "missing entry", "wrong dry run", "wrong entry dry run", "both result and error"} {
			t.Run(fmt.Sprintf("real-%v/%s", realPhase, mutation), func(t *testing.T) {
				s, calls := bulk1701Server(t, func(c bulk1701Call) string {
					if c.DryRun == realPhase {
						return bulk1701Payload(c, nil, false, false)
					}
					var p map[string]any
					_ = json.Unmarshal([]byte(bulk1701Payload(c, nil, false, false)), &p)
					r := p["data"].(map[string]any)["applyMemoryConfigTemplateToMemories"].(map[string]any)
					es := r["results"].([]any)
					e := es[0].(map[string]any)
					switch mutation {
					case "missing result":
						e["result"] = nil
					case "wrong index":
						e["index"] = 1
					case "wrong echo":
						e["memoryRef"] = "other"
					case "wrong count":
						r["succeeded"] = 0
					case "missing entry":
						r["results"] = es[:1]
					case "wrong dry run":
						r["dryRun"] = !c.DryRun
					case "wrong entry dry run":
						e["result"].(map[string]any)["dryRun"] = !c.DryRun
					case "both result and error":
						e["error"] = map[string]any{"code": "INTERNAL_SERVER_ERROR", "message": "bad"}
					}
					b, _ := json.Marshal(p)
					return string(b)
				})
				out, code := runBulk1701(t, s.URL, "memory", "config", "template", "apply", "t1", "m1", "m2", "--json")
				wantCalls := 1
				if realPhase {
					wantCalls = 2
				}
				if code != 1 || len(calls()) != wantCalls || !strings.Contains(out, "verify memory configs before retrying") {
					t.Fatalf("exit=%d calls=%v out=%s", code, calls(), out)
				}
			})
		}
	}
}

func TestBulkTemplate1701NoInput(t *testing.T) {
	for _, args := range [][]string{{"t1"}, {"t1", "m1", "--memories-file", ""}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			s, calls := bulk1701Server(t, func(c bulk1701Call) string { return bulk1701Payload(c, nil, false, false) })
			out, code := runBulk1701(t, s.URL, append([]string{"memory", "config", "template", "apply"}, args...)...)
			if code != 2 || len(calls()) != 0 {
				t.Fatalf("exit=%d calls=%v %s", code, calls(), out)
			}
		})
	}
}
func TestBulkTemplate1701ApplyConflict(t *testing.T) {
	s, calls := bulk1701Server(t, func(c bulk1701Call) string {
		if c.DryRun {
			return bulk1701Payload(c, nil, false, false)
		}
		var p map[string]any
		_ = json.Unmarshal([]byte(bulk1701Payload(c, nil, true, false)), &p)
		r := p["data"].(map[string]any)["applyMemoryConfigTemplateToMemories"].(map[string]any)
		for _, e := range r["results"].([]any) {
			e.(map[string]any)["error"] = map[string]any{"code": "CONFLICT", "message": "template changed", "currentRevision": 8}
		}
		b, _ := json.Marshal(p)
		return string(b)
	})
	out, code := runBulk1701(t, s.URL, "memory", "config", "template", "apply", "t1", "m1", "m2", "--json")
	if code != 1 || len(calls()) != 2 || !strings.Contains(out, `"currentRevision": 8`) {
		t.Fatalf("exit=%d calls=%v %s", code, calls(), out)
	}
}

func TestBulkTemplate1701OmittedRevision(t *testing.T) {
	var calls atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct{ Variables map[string]json.RawMessage }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body.Variables["expectedTemplateRevision"]; ok {
			t.Error("unset revision serialized")
		}
		fmt.Fprint(w, bulk1701Payload(bulk1701Call{DryRun: true, Refs: []string{"m1", "m2"}}, nil, false, false))
	}))
	defer s.Close()
	_, code := runBulk1701(t, s.URL, "memory", "config", "template", "apply", "t1", "m1", "m2", "--dry-run")
	if code != 0 || calls.Load() != 1 {
		t.Fatalf("exit=%d calls=%d", code, calls.Load())
	}
}

func TestBulkTemplate1701RealFailureRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		status, want int
		warn         bool
	}{
		{"gateway", "gateway down", 502, 7, true},
		{"malformed JSON", "{", 200, 7, true},
		{"null result", `{"data":{"applyMemoryConfigTemplateToMemories":null}}`, 200, 1, true},
		{"nested forbidden", `{"errors":[{"message":"refused","path":["applyMemoryConfigTemplateToMemories","results"],"extensions":{"code":"FORBIDDEN"}}]}`, 200, 8, true},
		{"mixed faults", `{"errors":[{"message":"refused","extensions":{"code":"FORBIDDEN"}},{"message":"fault","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, 200, 1, true},
		{"whole conflict", `{"errors":[{"message":"template changed","extensions":{"code":"CONFLICT"}}]}`, 200, 5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var b struct{ Variables bulk1701Call }
				_ = json.NewDecoder(r.Body).Decode(&b)
				if b.Variables.DryRun {
					fmt.Fprint(w, bulk1701Payload(b.Variables, nil, false, false))
					return
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			out, code := runBulk1701(t, s.URL, "memory", "config", "template", "apply", "t1", "m1", "m2", "--json")
			if code != tc.want || calls.Load() != 2 {
				t.Fatalf("exit=%d calls=%d %s", code, calls.Load(), out)
			}
			if strings.Contains(out, "some memories may already have changed") != tc.warn {
				t.Fatalf("warning=%v want%v: %s", !tc.warn, tc.warn, out)
			}
			if tc.warn && !strings.Contains(out, "verify memory configs before retrying") {
				t.Fatal(out)
			}
		})
	}
}
func TestBulkTemplate1701DuplicateRetryHelp(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "config", "template", "apply", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(strings.Fields(out.String()), " "), "Do not retry DUPLICATE_TARGET") {
		t.Fatal(out.String())
	}
}

func TestBulkTemplate1701FailedPreviewNeedsConfirmation(t *testing.T) {
	s, calls := bulk1701Server(t, func(c bulk1701Call) string {
		var p map[string]any
		_ = json.Unmarshal([]byte(bulk1701Payload(c, nil, false, false)), &p)
		r := p["data"].(map[string]any)["applyMemoryConfigTemplateToMemories"].(map[string]any)
		if c.DryRun {
			e := r["results"].([]any)[1].(map[string]any)
			e["ok"] = false
			e["result"] = nil
			e["error"] = map[string]any{"code": "INTERNAL_SERVER_ERROR", "message": "preview failed", "requestId": "req"}
			r["succeeded"] = 1
			r["failed"] = 1
		}
		b, _ := json.Marshal(p)
		return string(b)
	})
	out, code := runBulk1701(t, s.URL, "memory", "config", "template", "apply", "t1", "m1", "m2", "--json")
	if code != 2 || len(calls()) != 1 {
		t.Fatalf("unpreviewed target written without confirmation: code=%d calls=%v %s", code, calls(), out)
	}
}

func TestBulkTemplate1701FileBOMAndSafeRef(t *testing.T) {
	file := filepath.Join(t.TempDir(), "refs")
	if err := os.WriteFile(file, []byte("\ufeffm1\nevil\x1b]0;x\x07a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, calls := bulk1701Server(t, func(c bulk1701Call) string { return bulk1701Payload(c, nil, false, false) })
	out, code := runBulk1701(t, s.URL, "memory", "config", "template", "apply", "t1", "--memories-file", file, "--dry-run")
	if code != 0 || len(calls()) != 1 || calls()[0].Refs[0] != "m1" || strings.ContainsAny(out, "\x1b\x07") {
		t.Fatalf("exit=%d calls=%v out=%q", code, calls(), out)
	}
}
func TestBulkTemplate1701LongPromptNamesEveryRole(t *testing.T) {
	roles := []string{}
	for i := 0; i < 30; i++ {
		roles = append(roles, fmt.Sprintf("long-replaced-role-%02d", i))
	}
	s, _ := bulk1701Server(t, func(c bulk1701Call) string {
		var p map[string]any
		_ = json.Unmarshal([]byte(bulk1701Payload(c, nil, false, true)), &p)
		r := p["data"].(map[string]any)["applyMemoryConfigTemplateToMemories"].(map[string]any)
		for _, e := range r["results"].([]any) {
			entries := []any{}
			for _, role := range roles {
				entries = append(entries, map[string]any{"role": role, "outcome": "REPLACED", "rule": nil})
			}
			e.(map[string]any)["result"].(map[string]any)["entries"] = entries
		}
		b, _ := json.Marshal(p)
		return string(b)
	})
	f, _, stderr := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "config", "template", "apply", "t1", "m1", "m2", "--server", s.URL})
	_ = root.Execute()
	for _, role := range roles {
		if !strings.Contains(stderr.String(), role) {
			t.Fatalf("prompt omitted role %q: %s", role, stderr)
		}
	}
}

func TestBulkTemplate1701PartialPreviewConsent(t *testing.T) {
	for _, mode := range []string{"yes flag", "combined yes", "combined no"} {
		t.Run(mode, func(t *testing.T) {
			s, calls := bulk1701Server(t, func(c bulk1701Call) string {
				var p map[string]any
				_ = json.Unmarshal([]byte(bulk1701Payload(c, nil, false, true)), &p)
				r := p["data"].(map[string]any)["applyMemoryConfigTemplateToMemories"].(map[string]any)
				if c.DryRun {
					e := r["results"].([]any)[1].(map[string]any)
					e["ok"] = false
					e["result"] = nil
					e["error"] = map[string]any{"code": "CONFLICT", "message": "retry"}
					r["succeeded"] = 1
					r["failed"] = 1
				}
				b, _ := json.Marshal(p)
				return string(b)
			})
			args := []string{"memory", "config", "template", "apply", "t1", "m1", "m2"}
			if mode == "yes flag" {
				args = append(args, "--yes")
				_, code := runBulk1701(t, s.URL, args...)
				if code != 0 || len(calls()) != 2 {
					t.Fatalf("exit=%d calls=%v", code, calls())
				}
			} else {
				answer := "y\n"
				if mode == "combined no" {
					answer = "n\n"
				}
				f, _, stderr := testFactoryTTY(t, answer)
				root := NewRootCmd(f)
				root.SetArgs(append(args, "--server", s.URL))
				code := exitCodeFor(root.Execute())
				wantCalls := 2
				if mode == "combined no" {
					wantCalls = 1
					if code != 6 {
						t.Fatal(code)
					}
				} else if code != 0 {
					t.Fatal(code)
				}
				if len(calls()) != wantCalls || strings.Count(stderr.String(), "(y/N)") != 1 || !strings.Contains(stderr.String(), "m1: spec") || !strings.Contains(stderr.String(), "m2") || !strings.Contains(stderr.String(), "not previewed") {
					t.Fatalf("calls=%v prompt=%s", calls(), stderr)
				}
			}
			for _, c := range calls() {
				if strings.Join(c.Refs, ",") != "m1,m2" {
					t.Fatal(calls())
				}
			}
		})
	}
}

func TestBulkTemplate1701StdinCannotAlsoConfirm(t *testing.T) {
	s, calls := bulk1701Server(t, func(c bulk1701Call) string { return bulk1701Payload(c, nil, false, true) })
	f, _, stderr := testFactoryTTY(t, "m1\nm2\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "config", "template", "apply", "t1", "--memories-file", "-", "--server", s.URL})
	err := root.Execute()
	if exitCodeFor(err) != 2 || !strings.Contains(err.Error(), "stdin was consumed") || len(calls()) != 1 || strings.Contains(stderr.String(), "(y/N)") {
		t.Fatalf("err=%v calls=%v stderr=%s", err, calls(), stderr)
	}
}
func TestBulkTemplate1701UsageBeforeAuth(t *testing.T) {
	for _, args := range [][]string{{"t1"}, {"t1", "--memories-file", ""}} {
		f, _ := testFactory(t)
		t.Setenv("HADRON_TOKEN", "")
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"memory", "config", "template", "apply"}, args...))
		if code := exitCodeFor(root.Execute()); code != 2 {
			t.Fatalf("args=%v exit=%d", args, code)
		}
	}
}
