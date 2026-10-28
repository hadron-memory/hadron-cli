package cmd

import (
	"encoding/json"
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
