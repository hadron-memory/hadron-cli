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
				if jsonMode {
					var v struct {
						SpecsChanged int
						RelintStatus string
						RelintError  *string
						Lint         []any
					}
					if json.Unmarshal([]byte(out.String()), &v) != nil || v.SpecsChanged != 2 || v.RelintStatus != "incomplete" || v.RelintError == nil || len(v.Lint) != 0 {
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
