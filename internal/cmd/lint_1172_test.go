package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestLint1172CommandKeepsSuccessAndReportsSafeClass(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		gql, _ := captureGraphQL(t, map[string]string{"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`, "GetNodeLintWarnings": `{"errors":[{"message":"SECRET private cause","extensions":{"code":"FORBIDDEN"}}]}`})
		f, out := testFactory(t)
		var stderr bytes.Buffer
		f.IOStreams.ErrOut = &stderr
		root := NewRootCmd(f)
		args := []string{"node", "add", "-m", "mem1", "--loc", "x", "--name", "X", "--lint-warnings", "--server", gql.URL}
		if jsonMode {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if stderr.String() != "lint warnings: unavailable (advisory read did not complete)\n" || strings.Contains(out.String()+stderr.String(), "SECRET") {
			t.Fatalf("bad class/output: %q %q", stderr.String(), out.String())
		}
		if jsonMode {
			var dto map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if string(dto["lintWarnings"]) != "null" {
				t.Fatalf("JSON changed %s", out.String())
			}
		} else if !strings.Contains(out.String(), "lint warnings: unavailable (no conclusion about warnings)") {
			t.Fatal("human unavailable receipt lost")
		}
	}
}
