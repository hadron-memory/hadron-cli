package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSpecReplaceRelintIncomplete1210(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		for _, capError := range []bool{false, true} {
			t.Run(map[bool]string{false: "text", true: "json"}[jsonMode]+map[bool]string{false: "/ordinary", true: "/cap"}[capError], func(t *testing.T) {
				var writes atomic.Int64
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var q struct {
						OperationName string
						Variables     json.RawMessage
					}
					_ = json.NewDecoder(r.Body).Decode(&q)
					body := ""
					switch q.OperationName {
					case "FindNodes":
						body = translateFindNodes(q.OperationName, pageBeyondFirst(q.OperationName, q.Variables, `{"data":{"nodes":[`+specNodeList("msg:010:01", `["spec","p1"]`)+`,`+specNodeList("msg:010:02", `["spec","p1"]`)+`]}}`))
					case "SearchReplaceInNodes":
						var v struct{ Input struct{ DryRun bool } }
						_ = json.Unmarshal(q.Variables, &v)
						if !v.Input.DryRun {
							writes.Add(1)
						}
						body = specReplaceResp(v.Input.DryRun)
					case "NodeBatch":
						if capError {
							body = edgeCap1210
						} else {
							body = `{"errors":[{"message":"SECRET transport detail","extensions":{"code":"BAD_USER_INPUT"}}]}`
						}
					default:
						var ok bool
						body, ok = unstubbedDefault(q.OperationName)
						if !ok {
							t.Errorf("unexpected %s", q.OperationName)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(body))
				}))
				defer s.Close()
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"spec", "replace", "old", "new", "-m", specMem, "--yes", "--server", s.URL}
				if jsonMode {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal("write succeeded, relint cannot refuse it", err)
				}
				if writes.Load() != 1 {
					t.Fatalf("successful mutations=%d", writes.Load())
				}
				stderr := f.IOStreams.ErrOut.(*strings.Builder).String()
				if strings.Contains(stderr, "SECRET") || !strings.Contains(stderr, "incomplete") {
					t.Fatal(stderr)
				}
				if strings.Contains(out.String(), "SECRET") || strings.Contains(out.String(), "re-lint clean") {
					t.Fatal(out.String())
				}
				// cli#1512 items 1+5: the cap refusal's own message — the
				// actionable edge-list guidance — must reach BOTH surfaces
				// (the stderr note and relintError), separately from the
				// ordinary safe message. The branch picks it through
				// IsNodeBatchEdgeCapRefusal's errors.As AFTER MapError has
				// wrapped the refusal, so these end-to-end assertions pin
				// that wrapper traversal at command level: killing the
				// message branch, or IsNodeBatchEdgeCapRefusal itself,
				// degrades the cap subcase to the ordinary message and
				// reddens here.
				var v struct {
					SpecsChanged int
					RelintStatus string
					RelintError  *string
					Lint         []any
				}
				if jsonMode && json.Unmarshal([]byte(out.String()), &v) != nil {
					t.Fatal(out.String())
				}
				if capError {
					for _, m := range []string{"exceeds the nodeBatch selected-edge cap (maximum 2000)", "hadron edge list --source", "hadron edge list --target"} {
						if !strings.Contains(stderr, m) {
							t.Fatalf("cap guidance missing from stderr: %q", stderr)
						}
					}
					for _, raw := range []string{"Read edges in pages", "hadron spec lint"} {
						if strings.Contains(stderr, raw) {
							t.Fatalf("raw provider prose or the ordinary message stood in for the cap guidance: %q", stderr)
						}
					}
					if jsonMode {
						if !strings.Contains(*v.RelintError, "hadron edge list --source") || !strings.Contains(*v.RelintError, "maximum 2000") || strings.Contains(*v.RelintError, "SECRET") {
							t.Fatalf("relintError must carry the sanitized cap guidance: %q", *v.RelintError)
						}
					}
				} else {
					// The ordinary transport failure keeps its own fixed
					// safe message — and must not borrow the cap guidance,
					// so the two branches are pinned separately.
					if !strings.Contains(stderr, "verify the rewritten specs with hadron spec lint") || strings.Contains(stderr, "hadron edge list") || strings.Contains(stderr, "selected-edge cap") {
						t.Fatalf("ordinary-error message changed: %q", stderr)
					}
				}
				if jsonMode {
					if v.SpecsChanged != 2 || v.RelintStatus != "incomplete" || v.RelintError == nil || len(v.Lint) != 0 {
						t.Fatal(out.String())
					}
				} else if !strings.Contains(out.String(), "Replaced") || !strings.Contains(out.String(), "re-lint incomplete") {
					t.Fatal(out.String())
				}
			})
		}
	}
}

func TestSpecReplaceRelintMissingIsIncomplete1210(t *testing.T) {
	for _, body := range []string{
		`{"data":{"nodeBatch":{"nodes":[],"unavailable":[],"omitted":[],"truncated":false}}}`,
		`{"data":{"nodeBatch":{"nodes":[],"unavailable":["id-msg:010:01","id-msg:010:02"],"omitted":[],"truncated":false}}}`,
	} {
		gql, _ := captureGraphQL(t, map[string]string{"FindNodes": `{"data":{"nodes":[` + specNodeList("msg:010:01", `["spec"]`) + `,` + specNodeList("msg:010:02", `["spec"]`) + `]}}`, "SearchReplaceInNodes": specReplaceResp(false), "NodeBatch": body})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"spec", "replace", "old", "new", "-m", specMem, "--yes", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `"relintStatus": "incomplete"`) {
			t.Fatal(out.String())
		}
	}
}
