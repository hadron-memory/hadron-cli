package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2904 (entity-refs S2, hadron-server#4138 / PR #4766): every node
// revision command names the revision as nodeRevRef, never revisionId. The
// argument is passed through as typed, a primary key or an hrn:noderev: URN
// (cor:urn:010:01; the CLI never builds one). The document-side pin, which
// argument each selection sends, is api.TestS2DocumentsSendNodeRevRefNotRevisionId_2904;
// these pin the variables the commands put on the wire.

const s2RevURN = "hrn:noderev:acme.com:eng:findings:flaky-ci:3"

const s2RevisionJSON = `{"id":"v2","nodeId":"n1","loc":"findings:flaky-ci","name":"Flaky CI",
	"description":null,"tags":[],"createdAt":"2026-06-11T00:00:00Z","editedBy":null,
	"editedByInfo":null,"editedByUser":null,"revLabel":"tagged","changes":[]`

// s2Vars decodes an operation's variables and reports the revision reference
// it carried, failing if the retired name is still on the wire.
func s2Vars(t *testing.T, captured map[string]json.RawMessage, op string) string {
	t.Helper()
	raw, ok := captured[op]
	if !ok {
		t.Fatalf("%s was not sent", op)
	}
	var vars map[string]any
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("%s variables: %v", op, err)
	}
	if _, has := vars["revisionId"]; has {
		t.Errorf("%s still sends the retired revisionId: %s", op, raw)
	}
	got, _ := vars["nodeRevRef"].(string)
	return got
}

func TestNodeRevisionCommandsSendNodeRevRef_2904(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		responses map[string]string
		ops       []string
	}{
		{
			name: "get (stamped)",
			args: []string{"node", "revision", "get", s2RevURN},
			responses: map[string]string{
				"NodeRevisionStamped": `{"data":{"nodeRevision":` + s2RevisionJSON + `,"content":"body","authorship":null,"contentValidation":null}}}`,
			},
			ops: []string{"NodeRevisionStamped"},
		},
		{
			// A server without the stamp fields refuses the stamped read at
			// validation; the plain read is the second nodeRevision document.
			name: "get (plain fallback)",
			args: []string{"node", "revision", "get", s2RevURN},
			responses: map[string]string{
				"NodeRevisionStamped": `{"errors":[{"message":"Cannot query field \"authorship\" on type \"NodeRevision\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
				"NodeRevision":        `{"data":{"nodeRevision":` + s2RevisionJSON + `,"content":"body"}}}`,
			},
			ops: []string{"NodeRevisionStamped", "NodeRevision"},
		},
		{
			name: "restore",
			args: []string{"node", "revision", "restore", s2RevURN},
			responses: map[string]string{"RestoreNodeRevision": `{"data":{"restoreNodeRevision":{"id":"n1","memoryId":"mem1",
				"loc":"findings:flaky-ci","name":"Flaky CI","nodeType":"finding","updatedAt":"2026-06-12T00:00:00Z"}}}`},
			ops: []string{"RestoreNodeRevision"},
		},
		{
			name:      "label",
			args:      []string{"node", "revision", "label", s2RevURN, "--label", "baseline"},
			responses: map[string]string{"UpdateNodeRevision": `{"data":{"updateNodeRevision":` + s2RevisionJSON + `}}}`},
			ops:       []string{"UpdateNodeRevision"},
		},
		{
			name:      "delete",
			args:      []string{"node", "revision", "delete", s2RevURN, "--yes"},
			responses: map[string]string{"DeleteNodeRevision": `{"data":{"deleteNodeRevision":true}}`},
			ops:       []string{"DeleteNodeRevision"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, tc.responses)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			for _, op := range tc.ops {
				if got := s2Vars(t, captured, op); got != s2RevURN {
					t.Errorf("%s: nodeRevRef = %q, want the argument as typed %q", op, got, s2RevURN)
				}
			}
		})
	}
}

// A revision the caller cannot read answers exactly like a missing one and
// names only the caller's own argument (cor:api:140:03, cor:api:040:03). The
// server's NOT_FOUND refusal carries its own message and echoes the reference
// in a nodeRevRef extension; the CLI shows neither. Exit 4 with the same line
// restore and delete already give for a null/false result.
func TestNodeRevisionWritesNotFoundIsTheCLIsOwnLine_2904(t *testing.T) {
	const backend = "Node revision not found BACKEND-DETAIL"
	notFound := `{"data":null,"errors":[{"message":"` + backend + `","extensions":{"code":"NOT_FOUND","nodeRevRef":"hrn:noderev:SERVER-ECHO"}}]}`
	cases := []struct {
		name string
		args []string
		op   string
	}{
		{"restore", []string{"node", "revision", "restore", "gone"}, "RestoreNodeRevision"},
		{"label", []string{"node", "revision", "label", "gone", "--label", "x"}, "UpdateNodeRevision"},
		{"delete", []string{"node", "revision", "delete", "gone", "--yes"}, "DeleteNodeRevision"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{tc.op: notFound})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			err := root.Execute()
			if got := exitCodeFor(err); got != exitcode.NotFound {
				t.Fatalf("exit = %d, want %d (err %v)", got, exitcode.NotFound, err)
			}
			if want := `revision "gone" not found`; err.Error() != want {
				t.Errorf("message = %q, want %q", err.Error(), want)
			}
			for _, leak := range []string{"BACKEND-DETAIL", "SERVER-ECHO"} {
				if strings.Contains(err.Error(), leak) || strings.Contains(out.String(), leak) {
					t.Errorf("backend text %q reached the user: err=%q out=%q", leak, err.Error(), out.String())
				}
			}
		})
	}
}
