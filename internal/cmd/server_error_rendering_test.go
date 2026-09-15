package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// #566 measured at the surface a person actually reads.
//
// The unit tests pin api.MapError's message. This pins what `renderError`
// PRINTS, which is the thing the issue was written about — and it covers both
// branches, because the human line and the `--json` error envelope render the
// SAME string, so a fix that reached only one of them would be invisible here
// if only one were asserted.
//
// The refusal below is the issue's own example: the server names the field in
// its message, and genqlient prepends the field again from the path, so the
// unfixed rendering says "workers workers".
const wireRefusal = `{"errors":[{"message":"workers URN \"hadron-dev-team\" is not fully qualified.",` +
	`"locations":[{"line":3,"column":5}],"path":["workers"]}]}`

func TestServerRefusalPrintsTheServerSentenceOnly(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{"WorkersRoster": wireRefusal})

	f, _ := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut

	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "ls", "--app", "hrn:app:hadronmemory.com:hadron-dev-team", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a refusal must fail")
	}
	renderError(f, err)

	got := errOut.String()
	if !strings.Contains(got, `is not fully qualified`) {
		t.Fatalf("the server's own sentence must reach the user: %q", got)
	}
	// The three things the issue is about, each asserted separately so a
	// failure names which one came back.
	if strings.Contains(got, "input:") {
		t.Errorf("document location leaked: %q", got)
	}
	if strings.Contains(got, "workers workers") {
		t.Errorf("the GraphQL path was prepended to a message that already named the field: %q", got)
	}
	if strings.Contains(got, ":3:") {
		t.Errorf("a line number from a query the user never wrote leaked: %q", got)
	}
}

func TestServerRefusalJSONEnvelopeCarriesTheSameCleanMessage(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{"WorkersRoster": wireRefusal})

	// #334 moved the envelope to STDOUT, so this reads `out` — the same stream
	// a `--json` consumer parses. The point of the test is unchanged: whatever
	// stream carries the envelope must carry the server's sentence and not
	// genqlient's scaffolding.
	f, out := testFactory(t)
	f.JSON = true

	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "ls", "--app", "hrn:app:hadronmemory.com:hadron-dev-team", "--server", gql.URL, "--json"})
	err := root.Execute()
	if err == nil {
		t.Fatal("a refusal must fail")
	}
	f.JSON = true
	renderError(f, err)
	errOut := out

	var env struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(errOut.String()), &env); jerr != nil {
		t.Fatalf("the --json failure envelope must be JSON: %v (%q)", jerr, errOut.String())
	}
	if strings.Contains(env.Error.Message, "input:") {
		t.Errorf("--json carried the leak an agent would then parse: %q", env.Error.Message)
	}
	if !strings.Contains(env.Error.Message, "is not fully qualified") {
		t.Errorf("--json lost the server's sentence: %q", env.Error.Message)
	}
}
