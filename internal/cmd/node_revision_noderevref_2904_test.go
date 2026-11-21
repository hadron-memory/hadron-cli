package cmd

import (
	"encoding/json"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2904 (server #4766, entity refs S2 #4138): the node-revision family's
// argument is nodeRevRef (a revision URN or a PK), not revisionId. The CLI
// passes what the user typed through unchanged and never builds a URN.

const (
	revURN2904 = "hrn:noderev:acme.com:kb:findings:flaky-ci:3"
	revBody    = `"id":"v2","nodeId":"n1","loc":"findings:flaky-ci","name":"Flaky CI","description":null,"tags":[],` +
		`"createdAt":"2026-06-11T00:00:00Z","editedBy":null,"editedByInfo":null,"editedByUser":null,"revLabel":"x","changes":[]`
)

// Every operation that sends the argument: both nodeRevision reads (the stamped
// one, and the plain one the stamped read falls back to), restore, label and
// delete. Each must send nodeRevRef=<what was typed> and no revisionId.
func TestNodeRevisionOperationsSendNodeRevRef2904(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		args     []string
		responds map[string]string
	}{
		{"get (stamped read)", "NodeRevisionStamped", []string{"get", revURN2904},
			map[string]string{"NodeRevisionStamped": `{"data":{"nodeRevision":{` + revBody + `,"content":"b","authorship":null,"contentValidation":null}}}`}},
		{"get (plain fallback read)", "NodeRevision", []string{"get", revURN2904},
			map[string]string{"NodeRevision": `{"data":{"nodeRevision":{` + revBody + `,"content":"b"}}}`}},
		{"restore", "RestoreNodeRevision", []string{"restore", revURN2904},
			map[string]string{"RestoreNodeRevision": restoreNodeJSON}},
		{"label", "UpdateNodeRevision", []string{"label", revURN2904, "--label", "x"},
			map[string]string{"UpdateNodeRevision": `{"data":{"updateNodeRevision":{` + revBody + `}}}`}},
		{"delete", "DeleteNodeRevision", []string{"delete", revURN2904, "--yes"},
			map[string]string{"DeleteNodeRevision": `{"data":{"deleteNodeRevision":true}}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, tc.responds)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"node", "revision"}, tc.args...), "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			raw, ok := captured[tc.op]
			if !ok {
				t.Fatalf("%s was never sent", tc.op)
			}
			var vars map[string]any
			if err := json.Unmarshal(raw, &vars); err != nil {
				t.Fatalf("%s variables are not JSON: %v (%s)", tc.op, err, raw)
			}
			if vars["nodeRevRef"] != revURN2904 {
				t.Errorf("%s must send nodeRevRef=%q unchanged, got %v", tc.op, revURN2904, vars)
			}
			if _, present := vars["revisionId"]; present {
				t.Errorf("%s must not send the removed argument revisionId: %v", tc.op, vars)
			}
		})
	}
}

// The typed not-found refusal: the server answers NOT_FOUND alike for an
// unknown, malformed or unreadable reference, echoing the argument under the
// renamed extension key nodeRevRef. The CLI keeps today's answer on restore,
// label and delete: exit 4 (NotFound), naming only the reference the caller
// typed, and nothing of the backend's text or extension.
func TestNodeRevisionNotFoundRefusalKeepsTodaysAnswer2904(t *testing.T) {
	const backend = "Node revision not found"
	// The CLI reads no extension key but the code, so a server that still echoes
	// the old key answers the same as one that echoes the renamed one.
	for _, key := range []string{"nodeRevRef", "revisionId"} {
		refusal := `{"errors":[{"message":"` + backend + `","extensions":{"code":"NOT_FOUND","` + key + `":"` + revURN2904 + `"}}],"data":null}`
		notFoundRefusal2904(t, key, refusal)
	}
}

func notFoundRefusal2904(t *testing.T, key, refusal string) {
	t.Helper()
	const backend = "Node revision not found"
	for _, tc := range []struct {
		name, op string
		args     []string
	}{
		{"restore", "RestoreNodeRevision", []string{"restore", revURN2904}},
		{"label", "UpdateNodeRevision", []string{"label", revURN2904, "--label", "x"}},
		{"delete", "DeleteNodeRevision", []string{"delete", revURN2904, "--yes"}},
	} {
		t.Run(key+"/"+tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{tc.op: refusal})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"node", "revision"}, tc.args...), "--server", gql.URL))
			err := root.Execute()
			if err == nil {
				t.Fatal("a NOT_FOUND refusal must be an error")
			}
			if got := exitcode.FromError(err); got != exitcode.NotFound {
				t.Fatalf("want exit %d (NotFound), got %d: %v", exitcode.NotFound, got, err)
			}
			if err.Error() != backend {
				t.Errorf("message = %q, want today's %q", err.Error(), backend)
			}
			if out.Len() != 0 {
				t.Errorf("a refusal prints nothing on stdout: %q", out.String())
			}
		})
	}
}
