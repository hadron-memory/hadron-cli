package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestRawAPINullEntryKeepsEnvelopeAndTypedExit(t *testing.T) {
	const body = `{"errors":[null,{"message":"no such node","extensions":{"code":"NOT_FOUND"}}]}`
	gql := graphQLAlways(t, http.StatusOK, body)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"api", "query Q { __typename }", "--json", "--server", gql.URL})
	if got := exitCodeFor(root.Execute()); got != exitcode.NotFound {
		t.Fatalf("raw API exit = %d, want %d", got, exitcode.NotFound)
	}
	var envelope struct {
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal([]byte(out.String()), &envelope); err != nil {
		t.Fatalf("stdout must remain the original GraphQL envelope: %v\n%s", err, out.String())
	}
	var second bytes.Buffer
	if len(envelope.Errors) == 2 {
		if err := json.Compact(&second, envelope.Errors[1]); err != nil {
			t.Fatal(err)
		}
	}
	if len(envelope.Errors) != 2 || string(envelope.Errors[0]) != "null" ||
		second.String() != `{"message":"no such node","extensions":{"code":"NOT_FOUND"}}` {
		t.Fatalf("raw API changed the error entries: %s", out.String())
	}
}
