package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#916 C5a — the server's include write refusals (hadron-server#2226 S2b,
// #2165) exit by their class, not the generic 1. The envelopes are rebuilt
// from the SDL's createNode/updateNode/updateEdge prose, not captured live.
// Each message names only the authored tag; it must reach the caller whole,
// and the write is sent exactly once (no retry, no target lookup).

func includeRefusalJSON(code, message string) string {
	return `{"errors":[{"message":"` + message + `","extensions":{"code":"` + code + `"}}]}`
}

func TestIncludeRefusalExits(t *testing.T) {
	cases := []struct {
		name    string
		op      string
		code    string
		message string
		args    []string
		want    int
	}{
		{
			name:    "target unavailable (node add)",
			op:      "CreateNode",
			code:    "INCLUDE_TARGET_UNAVAILABLE",
			message: `Include target unavailable: {{include node=\"guides:missing\"}}`,
			args:    []string{"node", "add", "-m", "acme.com::kb", "--loc", "a:b", "--name", "X", "--content", `{{include node="guides:missing"}}`},
			want:    exitcode.NotFound,
		},
		{
			name:    "cycle (node update)",
			op:      "UpdateNode",
			code:    "INCLUDE_CYCLE",
			message: `Include cycle: {{include node=\"a:b\"}} would make this node include itself`,
			args:    []string{"node", "update", nodeURN, "--content", `{{include node="a:b"}}`},
			want:    exitcode.Usage,
		},
		{
			name:    "derived edge (edge rm)",
			op:      "DeleteEdge",
			code:    "INCLUDE_EDGE_DERIVED",
			message: `This INCLUDE edge is derived from the node's content; edit the include tag instead.`,
			args:    []string{"edge", "rm", "e1", "--yes"},
			want:    exitcode.Usage,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			gql, _ := captureGraphQLFunc(t, func(op string) string {
				switch op {
				case tc.op:
					calls++
					return includeRefusalJSON(tc.code, tc.message)
				case "ResolveUrn":
					return resolveNodeJSON
				case "GetNode":
					return `{"data":{"node":` + nodeDetailJSON + `}}`
				}
				return `{"errors":[{"message":"unexpected operation ` + op + `"}]}`
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			err := root.Execute()
			if got := exitCodeFor(err); got != tc.want {
				t.Fatalf("%s must exit %d, got %d (%v)", tc.code, tc.want, got, err)
			}
			want := strings.ReplaceAll(tc.message, `\"`, `"`)
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the server's sentence must survive whole (want %q): %v", want, err)
			}
			if calls != 1 {
				t.Errorf("%s must be sent exactly once, got %d", tc.op, calls)
			}
		})
	}
}
