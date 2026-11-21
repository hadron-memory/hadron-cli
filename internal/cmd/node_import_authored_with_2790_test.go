package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2790: `node import` carries the paired authoring attestation the other node
// writes already carry (--authored-with, --authored-revision; cor:dmo:060:16). The
// claim is the caller's, checked by the server; it widens no authority. Omitted,
// the field is absent (never null); a half pair refuses before any request.

const importDoc2790 = "---\nname: Flaky CI\nloc: findings:flaky-ci\ntype: finding\n---\nThe body.\n"

func authoredInput2790(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var v struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode variables: %v\n%s", err, raw)
	}
	return v.Input
}

func TestNodeImportHelpNamesTheAuthoredFlags2790(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "import", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--authored-with", "--authored-revision"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("node import --help does not name %s", want)
		}
	}
}

// Content mode (importNode): the exact task and revision ride the input when both
// flags are given, and the field is absent otherwise.
func TestNodeImportContentCarriesTheClaimOnlyWhenGiven2790(t *testing.T) {
	for _, claim := range []bool{false, true} {
		name := "omitted"
		if claim {
			name = "given"
		}
		t.Run(name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"ResolveUrn": resolveNullJSON,
				"ImportNode": importNodeResp("clips:p", "P", "webpage"),
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"node", "import", "--url", "https://ex.com/p", "-m", "acme.com:kb", "--loc", "clips:p", "--json", "--server", gql.URL}
			if claim {
				args = append(args, "--authored-with", "hrn:node:acme.com:kb:tasks:distill", "--authored-revision", "7")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			in := authoredInput2790(t, captured["ImportNode"])
			got, present := in["authoredWith"]
			if !claim {
				if present {
					t.Fatalf("no flags: authoredWith must be absent, got %s", got)
				}
				return
			}
			var c struct {
				Task     string `json:"task"`
				Revision int    `json:"revision"`
			}
			if err := json.Unmarshal(got, &c); err != nil || c.Task != "hrn:node:acme.com:kb:tasks:distill" || c.Revision != 7 {
				t.Fatalf("authoredWith = %s (err %v), want the exact task and revision 7", got, err)
			}
		})
	}
}

// Restore mode (a node document): the same claim rides each node write the import
// makes, so a flag is never accepted and then dropped.
func TestNodeImportRestoreCarriesTheClaim2790(t *testing.T) {
	for _, op := range []string{"UpdateNode", "CreateNode"} {
		t.Run(op, func(t *testing.T) {
			responses := map[string]string{"ResolveUrn": resolveNullJSON}
			if op == "UpdateNode" {
				responses["FindNodes"] = `{"data":{"nodes":[{"id":"n1","memoryId":"mem1","loc":"findings:flaky-ci","name":"Flaky CI","nodeType":"finding","updatedAt":"2026-06-11T00:00:00Z"}]}}`
				responses["UpdateNode"] = `{"data":{"updateNode":` + nodeJSON + `}}`
			} else {
				responses["UpdateNode"] = `{"errors":[{"message":"node not found","extensions":{"code":"NODE_NOT_FOUND"}}]}`
				responses["CreateNode"] = `{"data":{"createNode":` + nodeJSON + `}}`
			}
			gql, captured := captureGraphQL(t, responses)
			f, _ := testFactory(t)
			file := filepath.Join(t.TempDir(), "flaky.md")
			if err := os.WriteFile(file, []byte(importDoc2790), 0o600); err != nil {
				t.Fatal(err)
			}
			root := NewRootCmd(f)
			root.SetArgs([]string{"node", "import", file, "--json", "--yes", "-m", "acme.com:kb", "--authored-with", "task1", "--authored-revision", "4", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			raw, ok := captured[op]
			if !ok {
				t.Fatalf("%s never reached the server", op)
			}
			got := authoredInput2790(t, raw)["authoredWith"]
			var c struct {
				Task     string `json:"task"`
				Revision int    `json:"revision"`
			}
			if err := json.Unmarshal(got, &c); err != nil || c.Task != "task1" || c.Revision != 4 {
				t.Fatalf("%s authoredWith = %s, want task1 revision 4", op, got)
			}
		})
	}
}

// A half pair, an empty task and an out-of-range revision refuse as usage errors
// before ANY request: nothing is looked up, nothing is written.
func TestNodeImportRefusesABadClaimBeforeAnyRequest2790(t *testing.T) {
	cases := map[string][]string{
		"task alone":     {"--authored-with", "task1"},
		"revision alone": {"--authored-revision", "3"},
		"blank task":     {"--authored-with", "  ", "--authored-revision", "3"},
		"revision range": {"--authored-with", "task1", "--authored-revision", "99999999999"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := append([]string{"node", "import", "--url", "https://ex.com/p", "-m", "acme.com:kb", "--loc", "clips:p", "--server", gql.URL}, extra...)
			root.SetArgs(args)
			err := root.Execute()
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Fatalf("exit = %d (err %v), want usage", got, err)
			}
			if len(captured) != 0 {
				t.Fatalf("a refused claim must send nothing, but %d operation(s) reached the server", len(captured))
			}
		})
	}
}

// A governed refusal of the claim (a task the caller cannot read, a stale revision)
// stays typed and actionable on the import path and never prints the inaccessible
// task's content (the same concealment the other node writes give).
func TestNodeImportRefusedClaimIsConcealed2790(t *testing.T) {
	refusal := `{"errors":[{"message":"SECRET_TASK","extensions":{"code":"AUTHOR_TASK_BROKEN","taskAccess":"UNAVAILABLE","task":"SECRET_TASK","load":{"cli":"SECRET_CLI"}}}]}`
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[jsonMode], func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{"ResolveUrn": resolveNullJSON, "ImportNode": refusal})
			f, out := testFactory(t)
			var stderr strings.Builder
			f.IOStreams.ErrOut = &stderr
			args := []string{"node", "import", "--url", "https://ex.com/p", "-m", "acme.com:kb", "--loc", "clips:p", "--authored-with", "mytask", "--authored-revision", "2", "--server", gql.URL}
			if jsonMode {
				args = append(args, "--json")
			}
			root := NewRootCmd(f)
			root.SetArgs(args)
			err := root.Execute()
			if err == nil {
				t.Fatal("a refused claim must fail")
			}
			code := renderError(f, err)
			text := out.String() + stderr.String()
			if code == 0 || strings.Contains(text, "SECRET_") {
				t.Fatalf("exit %d, text leaks or is silent: %q", code, text)
			}
			if !strings.Contains(strings.ToLower(text), "task") {
				t.Errorf("the refusal must say what to do about the task: %q", text)
			}
		})
	}
}
