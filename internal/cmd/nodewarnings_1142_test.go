package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pinWarningText = "A pinned include revision carries no recorded approval or signature and may be automatically pruned, breaking the include."

func TestNodeWriteResponseWarnings1142(t *testing.T) {
	for _, kind := range []struct {
		name, op, key string
		flags         []string
	}{
		{"generic", "CreateNode", "createNode", nil},
		{"task", "CreateTaskNode", "createTaskNode", []string{"--runnable"}},
		{"spec", "CreateSpecNode", "createSpecNode", []string{"--role", "spec.rule"}},
		{"review", "CreateReviewNode", "createReviewNode", []string{"--role", "review"}},
	} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(kind.name+map[bool]string{false: "/text", true: "/json"}[jsonMode], func(t *testing.T) {
				run := func(warnings string, failed bool) (string, string, error) {
					body := `{"data":{"` + kind.key + `":` + nodeJSON + `},"extensions":{"warnings":` + warnings + `}}`
					if failed {
						body = `{"errors":[{"message":"refused"}],"extensions":{"warnings":` + warnings + `}}`
					}
					srv := fakeGraphQL(t, map[string]string{kind.op: body})
					f, out := testFactory(t)
					teamGitDir(t)
					errOut := &strings.Builder{}
					f.IOStreams.ErrOut = errOut
					root := NewRootCmd(f)
					args := []string{"node", "add", "-m", "acme.com::kb", "--loc", "test:pin", "--name", "pin", "--content", "body", "--server", srv.URL}
					args = append(args, kind.flags...)
					if jsonMode {
						args = append(args, "--json")
					}
					root.SetArgs(args)
					err := root.Execute()
					return out.String(), errOut.String(), err
				}
				plain, _, err := run(`[]`, false)
				if err != nil {
					t.Fatal(err)
				}
				warnings := `[{"path":"` + kind.key + `","code":"include.pin.unkept","text":` + quote1142(pinWarningText) + `},{"path":"updateNode","code":"wrong.root","text":"must stay silent"},{"path":"` + kind.key + `","code":"future.code","text":"Future advice"},null,{"path":"` + kind.key + `","code":7,"text":"bad"}]`
				out, stderr, err := run(warnings, false)
				if err != nil {
					t.Fatal(err)
				}
				if out != plain {
					t.Fatalf("stdout changed:\n%s\nvs\n%s", out, plain)
				}
				if !strings.Contains(stderr, "warning: [include.pin.unkept] "+pinWarningText) || !strings.Contains(stderr, "[future.code] Future advice") {
					t.Fatalf("missing advice: %q", stderr)
				}
				if strings.Contains(stderr, "wrong.root") || strings.Contains(stderr, "bad") {
					t.Fatalf("foreign/malformed advice: %q", stderr)
				}
				if jsonMode {
					var dto map[string]any
					if json.Unmarshal([]byte(out), &dto) != nil {
						t.Fatalf("invalid JSON %q", out)
					}
					if _, ok := dto["warnings"]; ok {
						t.Fatal("JSON shape changed")
					}
				}
				_, stderr, err = run(warnings, true)
				if err == nil || strings.Contains(stderr, "warning:") {
					t.Fatalf("failed request: err=%v stderr=%q", err, stderr)
				}
			})
		}
	}
}
func quote1142(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestNodeWriteWarningOtherDoors1142(t *testing.T) {
	for _, tc := range []struct {
		name, op, key, body string
		args                []string
		extra               map[string]string
	}{
		{"update", "UpdateNode", "updateNode", `{"data":{"updateNode":` + nodeJSON + `}}`, []string{"node", "update", "hrn:node:acme.com:kb:test:pin", "--content", "body"}, map[string]string{"ResolveUrn": resolveNodeJSON, "GetNode": `{"data":{"node":` + nodeDetailJSON + `}}`, "GetNodeRaw": `{"data":{"node":` + nodeDetailJSON + `}}`}},
		{"restore", "RestoreNode", "restoreNode", `{"data":{"restoreNode":` + recRestoredJSON + `}}`, []string{"node", "restore", recNodeURN}, nil},
		{"revision restore", "RestoreNodeRevision", "restoreNodeRevision", restoreNodeJSON, []string{"node", "revision", "restore", "v2"}, nil},
		{"replace", "SearchReplaceInNodes", "searchReplaceInNodes", searchReplaceRealJSON, []string{"replace", "text", "cat", "dog", "-m", "acme.com::kb", "--field", "content", "--yes"}, nil},
		{"import", "ImportNode", "importNode", importNodeResp("papers:a", "A", "info"), []string{"node", "import", "__FILE__", "-m", "acme.com:kb", "--loc", "papers:a"}, map[string]string{"ResolveUrn": resolveNullJSON}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, jsonMode := range []bool{false, true} {
				body := tc.body[:len(tc.body)-1] + `,"extensions":{"warnings":[{"path":"` + tc.key + `","code":"include.pin.unkept","text":` + quote1142(pinWarningText) + `}]}}`
				responses := map[string]string{tc.op: body}
				for k, v := range tc.extra {
					responses[k] = v
				}
				srv := fakeGraphQL(t, responses)
				if tc.name == "replace" {
					srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var req struct {
							Variables struct {
								Input struct {
									DryRun bool `json:"dryRun"`
								} `json:"input"`
							} `json:"variables"`
						}
						_ = json.NewDecoder(r.Body).Decode(&req)
						w.Header().Set("Content-Type", "application/json")
						if req.Variables.Input.DryRun {
							_, _ = w.Write([]byte(searchReplaceDryJSON))
						} else {
							_, _ = w.Write([]byte(body))
						}
					}))
					t.Cleanup(srv.Close)
				}
				f, out := testFactory(t)
				teamGitDir(t)
				stderr := &strings.Builder{}
				f.IOStreams.ErrOut = stderr
				root := NewRootCmd(f)
				args := append([]string{}, tc.args...)
				for i, arg := range args {
					if arg == "__FILE__" {
						path := filepath.Join(t.TempDir(), "a.pdf")
						if err := os.WriteFile(path, []byte("%PDF-1.4"), 0600); err != nil {
							t.Fatal(err)
						}
						args[i] = path
					}
				}

				args = append(args, "--server", srv.URL)
				if jsonMode {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if strings.Count(stderr.String(), "[include.pin.unkept]") != 1 {
					t.Fatalf("warning missing/duplicated: %q", stderr.String())
				}
				if strings.Contains(out.String(), pinWarningText) {
					t.Fatal("warning polluted stdout")
				}
			}
		})
	}
}
