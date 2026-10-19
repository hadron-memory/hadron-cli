package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestNodeClearRoleDryRunAndAtomicMergeGuard(t *testing.T) {
	out, _, captured, err := runNode(t, bodyOnlyStubs(delimiterBody), "",
		"node", "update", testNodeURN, "--role", " \t", "--name", "new", "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for op := range captured {
		if op != "ResolveUrn" && op != "GetNodeRaw" {
			t.Fatalf("dry-run sent %s", op)
		}
	}
	if !strings.Contains(out, `"role"`) || !strings.Contains(out, `"dryRun": true`) {
		t.Fatal(out)
	}
	_, _, captured, err = runNode(t, bodyOnlyStubs(delimiterBody), "",
		"node", "update", testNodeURN, "--role", "", "--data-merge", `{"x":1}`)
	if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "one mutation") || len(captured) != 0 {
		t.Fatalf("clear+merge must refuse before requests: %v %v", err, captured)
	}
}

func TestNodeClearRoleConflictAndOldServerNeverRetry(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
	}{
		{"NODE_WRITE_CONFLICT", exitcode.Conflict},
		{"GRAPHQL_VALIDATION_FAILED", exitcode.Usage},
		{"ROLE_GOVERNED", exitcode.Usage},
	} {
		t.Run(tc.code, func(t *testing.T) {
			calls := 0
			gql, captured := captureGraphQLFunc(t, func(op string) string {
				switch op {
				case "ResolveUrn":
					return resolveNodeJSON
				case "GetNode":
					return `{"data":{"node":` + nodeDetailJSON + `}}`
				case "UpdateNodeClearingRole":
					calls++
					return fmt.Sprintf(`{"errors":[{"message":"clear refused","extensions":{"code":%q}}]}`, tc.code)
				default:
					return ""
				}
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"node", "update", nodeURN, "--role", "", "--name", "simultaneous", "--server", gql.URL})
			err := root.Execute()
			if exitCodeFor(err) != tc.want || calls != 1 {
				t.Fatalf("calls %d code %d: %v", calls, exitCodeFor(err), err)
			}
			if _, ok := captured["UpdateNode"]; ok {
				t.Fatal("generic retry")
			}
			var vars map[string]any
			_ = json.Unmarshal(captured["UpdateNodeClearingRole"], &vars)
			if vars["name"] != "simultaneous" {
				t.Fatalf("edit split/lost: %v", vars)
			}
		})
	}
}

func TestNodeClearRoleUnreadableKindDoesNotWrite(t *testing.T) {
	for _, response := range []string{`{"data":{"node":null}}`, `{"errors":[{"message":"unreadable","extensions":{"code":"FORBIDDEN"}}]}`} {
		gql, captured := captureGraphQL(t, map[string]string{"ResolveUrn": resolveNodeJSON, "GetNode": response})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"node", "update", nodeURN, "--role", "", "--server", gql.URL})
		if err := root.Execute(); err == nil {
			t.Fatal("kind lookup must succeed before clear")
		}
		for op := range captured {
			if op != "ResolveUrn" && op != "GetNode" {
				t.Fatalf("failed kind read sent %s", op)
			}
		}
	}
}
