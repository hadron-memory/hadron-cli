package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#714 — a node file carries the governed signals (role, isRunnable), and
// `node import` writes through the door the node's kind requires. The server
// gate reads the kind the node is now AND the kind it will be (before ∪ after),
// so the door has to own every kind a write touches.

const notFoundJSON = `{"errors":[{"message":"node not found","extensions":{"code":"NODE_NOT_FOUND"}}]}`

// kindDetail is a GetNode response for a node of the given kind.
func kindDetail(role, runnable string) string {
	return `{"data":{"node":{"id":"n1","memoryId":"mem1","loc":"findings:flaky-ci","name":"X","description":null,
		"abstract":null,"nodeType":"info","tags":[],"role":` + role + `,"isRunnable":` + runnable + `,
		"content":"b","seq":null,"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z",
		"outgoingEdges":[],"incomingEdges":[]}}}`
}

func importFile(t *testing.T, name, body string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// governedMd is a standalone node file with extra frontmatter lines.
func governedMd(extra string) string {
	return "---\nname: X\nid: n1\nloc: findings:flaky-ci\nmemory: acme.com:kb\n" + extra + "---\n\nbody\n"
}

func doorInput(t *testing.T, captured map[string]json.RawMessage, op string) map[string]any {
	t.Helper()
	raw, ok := captured[op]
	if !ok {
		keys := make([]string, 0, len(captured))
		for k := range captured {
			keys = append(keys, k)
		}
		t.Fatalf("expected the write to go through %s; operations sent: %v", op, keys)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatal(err)
	}
	return vars.Input
}

func runImport(t *testing.T, responses map[string]string, args ...string) (map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"node", "import"}, args...), "--yes", "--json", "--server", gql.URL))
	err := root.Execute()
	return captured, err
}

// The live break: re-importing a task onto its own loc from a file that says
// nothing about the kind was sent to the generic updateNode, which the server
// refuses because the node IS a task. It must take the task door, and must not
// send the fields the file omits (absent = preserve).
func TestNodeImportOfAnOldFileOntoATaskUsesTheTaskDoorAndPreserves(t *testing.T) {
	captured, err := runImport(t, map[string]string{
		"ResolveUrn":     resolveNodeJSON,
		"GetNode":        kindDetail("null", "true"),
		"UpdateTaskNode": `{"data":{"updateTaskNode":` + nodeJSON + `}}`,
	}, importFile(t, "old.md", governedMd("")))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	in := doorInput(t, captured, "UpdateTaskNode")
	for _, k := range []string{"role", "isRunnable"} {
		if _, present := in[k]; present {
			t.Errorf("a file without %s must not send it (it would stop preserving the stored value): %v", k, in[k])
		}
	}
}

func TestNodeImportRoutesByTheKindTheWriteTouches(t *testing.T) {
	cases := []struct {
		name      string
		file      string
		stored    string // GetNode response, or "" when the node does not exist
		responses map[string]string
		door      string
		want      map[string]any // input keys the door must receive
		args      []string
	}{
		{
			name: "runnable false onto a task removes it through the task door",
			file: governedMd("runnable: false\n"), stored: kindDetail("null", "true"),
			responses: map[string]string{"UpdateTaskNode": `{"data":{"updateTaskNode":` + nodeJSON + `}}`},
			door:      "UpdateTaskNode", want: map[string]any{"isRunnable": false},
		},
		{
			name: "a spec file onto a new loc creates through the spec door",
			file: governedMd("role: spec\n"),
			responses: map[string]string{
				"UpdateSpecNode": notFoundJSON,
				"CreateSpecNode": `{"data":{"createSpecNode":` + nodeJSON + `}}`,
			},
			door: "CreateSpecNode", want: map[string]any{"role": "spec"},
		},
		{
			name: "an open role onto a spec leaves it through the spec door",
			file: governedMd("role: weather-widget\n"), stored: kindDetail(`"spec"`, "false"),
			responses: map[string]string{"UpdateSpecNode": `{"data":{"updateSpecNode":` + nodeJSON + `}}`},
			door:      "UpdateSpecNode", want: map[string]any{"role": "weather-widget"},
		},
		{
			name: "runnable true onto an ordinary node goes through the task door",
			file: governedMd("runnable: true\n"), stored: kindDetail("null", "null"),
			responses: map[string]string{"UpdateTaskNode": `{"data":{"updateTaskNode":` + nodeJSON + `}}`},
			door:      "UpdateTaskNode", want: map[string]any{"isRunnable": true},
		},
		{
			name: "--create-only with a task file creates through the task door",
			file: governedMd("runnable: true\n"),
			responses: map[string]string{
				"CreateTaskNode": `{"data":{"createTaskNode":` + nodeJSON + `}}`,
			},
			door: "CreateTaskNode", want: map[string]any{"isRunnable": true}, args: []string{"--create-only"},
		},
		{
			name: "an ordinary file onto an ordinary node keeps the generic door",
			file: governedMd("role: weather-widget\n"), stored: kindDetail("null", "false"),
			responses: map[string]string{"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`},
			door:      "UpdateNode", want: map[string]any{"role": "weather-widget"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			responses := map[string]string{}
			for k, v := range tc.responses {
				responses[k] = v
			}
			if tc.stored != "" {
				responses["ResolveUrn"], responses["GetNode"] = resolveNodeJSON, tc.stored
			} else if len(tc.args) == 0 {
				responses["ResolveUrn"] = resolveNullJSON
			}
			captured, err := runImport(t, responses, append([]string{importFile(t, "n.md", tc.file)}, tc.args...)...)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			in := doorInput(t, captured, tc.door)
			for k, v := range tc.want {
				if in[k] != v {
					t.Errorf("%s = %v, want %v", k, in[k], v)
				}
			}
		})
	}
}

func TestNodeImportJSONCarriesTheSignals(t *testing.T) {
	doc := `{"name":"X","id":"n1","loc":"findings:flaky-ci","memory":"acme.com:kb","isRunnable":true,"role":null,"content":"body","edges":[]}`
	captured, err := runImport(t, map[string]string{
		"ResolveUrn":     resolveNullJSON,
		"UpdateTaskNode": notFoundJSON,
		"CreateTaskNode": `{"data":{"createTaskNode":` + nodeJSON + `}}`,
	}, importFile(t, "n.json", doc))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	in := doorInput(t, captured, "CreateTaskNode")
	if in["isRunnable"] != true {
		t.Errorf("isRunnable = %v, want true", in["isRunnable"])
	}
	if _, present := in["role"]; present {
		t.Errorf("a null role is no opinion and must not be sent: %v", in["role"])
	}
}

// Two governed kinds have no door: each door is exempt from its OWN kind only.
// Refused before any write, whether the file states both or the file's kind
// meets a different stored one.
func TestNodeImportTwoGovernedKindsIsRefusedBeforeAnyWrite(t *testing.T) {
	cases := []struct {
		name, file, stored string
		args               []string
	}{
		{"the file states both", governedMd("role: spec\nrunnable: true\n"), "", nil},
		{"the file states both, --create-only", governedMd("role: spec\nrunnable: true\n"), "", []string{"--create-only"}},
		// Checked straight after parsing, so a dry run reports what the real
		// run would refuse rather than classifying it as a create.
		{"the file states both, --dry-run", governedMd("role: spec\nrunnable: true\n"), "", []string{"--dry-run"}},
		{"a review file onto a task", governedMd("role: review\n"), kindDetail("null", "true"), nil},
		{"a task file onto a spec", governedMd("runnable: true\n"), kindDetail(`"spec"`, "false"), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			responses := map[string]string{"ResolveUrn": resolveNullJSON}
			if tc.stored != "" {
				responses["ResolveUrn"], responses["GetNode"] = resolveNodeJSON, tc.stored
			}
			captured, err := runImport(t, responses, append([]string{importFile(t, "n.md", tc.file)}, tc.args...)...)
			if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "governed kinds") {
				t.Fatalf("want a Usage refusal naming the governed kinds, got %v", err)
			}
			for op := range captured {
				if strings.HasPrefix(op, "Update") || strings.HasPrefix(op, "Create") {
					t.Errorf("nothing may be written, but %s was sent", op)
				}
			}
		})
	}
}

// An empty role is read as absent: the server keeps "" as a role no kind
// recognizes, and a file is not a reason to write that.
func TestNodeImportEmptyRoleIsNotSent(t *testing.T) {
	captured, err := runImport(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    kindDetail("null", "null"),
		"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
	}, importFile(t, "n.md", governedMd("role: \"\"\n")))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if v, present := doorInput(t, captured, "UpdateNode")["role"]; present {
		t.Errorf("an empty role must not reach the wire: %v", v)
	}
}

// The same before ∪ after rule on `node update`: removing a governed kind needs
// that kind's door (the generic surface refuses "removes"), and adding a second
// kind has no door at all.
func TestNodeUpdateRoutesByTheKindsItTouches(t *testing.T) {
	t.Run("--runnable=false on a task takes the task door", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{
			"ResolveUrn":     resolveNodeJSON,
			"GetNode":        kindDetail("null", "true"),
			"UpdateTaskNode": `{"data":{"updateTaskNode":` + nodeJSON + `}}`,
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"node", "update", nodeURN, "--runnable=false", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if in := doorInput(t, captured, "UpdateTaskNode"); in["isRunnable"] != false {
			t.Errorf("isRunnable = %v, want false", in["isRunnable"])
		}
	})
	t.Run("--runnable on a spec is refused before any write", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{
			"ResolveUrn": resolveNodeJSON,
			"GetNode":    kindDetail(`"spec"`, "false"),
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"node", "update", nodeURN, "--runnable", "--server", gql.URL})
		err := root.Execute()
		if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "governed kinds") {
			t.Fatalf("want a Usage refusal naming the governed kinds, got %v", err)
		}
		for op := range captured {
			if strings.HasPrefix(op, "Update") {
				t.Errorf("nothing may be written, but %s was sent", op)
			}
		}
	})
}
