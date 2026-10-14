package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#873: the edit round-trip. `node get --body-only` prints the STORED body
// and nothing else, byte for byte; `node update` and `spec edit` refuse a body
// carrying the text-mode get header unless --force, and warn when the new body
// drops a {{…}} tag the stored one has.

// delimiterBody is a stored body whose first line changes the Mustache
// delimiters, so its {{name}} is literal text: the 050:01 shape. A rendered
// read drops the first line, and saving that turns {{name}} into a placeholder.
// No trailing newline, so a reader that appends one is caught.
const delimiterBody = "{{=<% %>=}}\nWrite {{name}} literally; <%who%> is a placeholder."

func bodyOnlyStubs(stored string) map[string]string {
	return map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"GetNode":    templatedNodeJSON("Write  literally; ada is a placeholder."),
		"GetNodeRaw": templatedNodeJSON(stored),
		"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
	}
}

func runNode(t *testing.T, stubs map[string]string, stdin string, args ...string) (string, string, error, map[string]json.RawMessage) {
	t.Helper()
	gql, captured := captureGraphQL(t, stubs)
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	if stdin != "" {
		f.IOStreams.In = strings.NewReader(stdin)
	}
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", gql.URL))
	err := root.Execute()
	if err != nil {
		renderError(f, err)
	}
	return out.String(), errOut.String(), err, captured
}

func TestNodeGetBodyOnlyPrintsTheStoredBodyExactly(t *testing.T) {
	for _, args := range [][]string{
		{"node", "get", testNodeURN, "--raw", "--body-only"},
		// --body-only is the edit read, so it is the stored body without --raw
		// too: a rendered body-only read is the lossy path this flag replaces.
		{"node", "get", testNodeURN, "--body-only"},
		{"node", "get", testNodeURN, "--content-only"},
	} {
		out, _, err, captured := runNode(t, bodyOnlyStubs(delimiterBody), "", args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if out != delimiterBody {
			t.Errorf("%v: output must be the stored body byte for byte\n got %q\nwant %q", args, out, delimiterBody)
		}
		if _, rendered := captured["GetNode"]; rendered {
			t.Errorf("%v: a body-only read must not read the rendered body", args)
		}
	}
}

func TestNodeGetBodyOnlyJSON(t *testing.T) {
	out, _, err, _ := runNode(t, bodyOnlyStubs(delimiterBody), "", "node", "get", testNodeURN, "--body-only", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("--json must parse: %v (%s)", err, out)
	}
	if dto["content"] != delimiterBody || dto["rendered"] != false || dto["id"] != "n1" || dto["loc"] != "findings:flaky" {
		t.Errorf("body-only JSON = %v", dto)
	}
	for _, k := range []string{"outgoingEdges", "name", "abstract"} {
		if _, ok := dto[k]; ok {
			t.Errorf("body-only JSON must carry the body, not the node: has %q", k)
		}
	}
}

// A node with no body prints nothing, and its JSON content is "".
func TestNodeGetBodyOnlyEmptyBody(t *testing.T) {
	stubs := bodyOnlyStubs("")
	stubs["GetNodeRaw"] = strings.Replace(templatedNodeJSON(""), `"content":""`, `"content":null`, 1)
	out, _, err, _ := runNode(t, stubs, "", "node", "get", testNodeURN, "--body-only")
	if err != nil || out != "" {
		t.Errorf("an empty body prints nothing, got %q (%v)", out, err)
	}
}

func TestNodeGetBodyOnlyRefusesBatchAndPrefix(t *testing.T) {
	for _, args := range [][]string{
		{"node", "get", testNodeURN, "hrn:node:acme.com:kb:other", "--body-only"},
		{"node", "get", "--prefix", "findings:", "-m", "hrn:mem:acme.com:kb", "--body-only"},
	} {
		_, _, err, captured := runNode(t, bodyOnlyStubs(delimiterBody), "", args...)
		if exitcode.FromError(err) != exitcode.Usage {
			t.Errorf("%v: want exit 2, got %v", args, err)
		}
		if len(captured) != 0 {
			t.Errorf("%v: refused before any request, sent %d", args, len(captured))
		}
	}
}

// The acceptance round trip: get --body-only | update --content-file - writes
// exactly the stored body.
func TestNodeBodyOnlyRoundTripIsByteIdentical(t *testing.T) {
	body, _, err, _ := runNode(t, bodyOnlyStubs(delimiterBody), "", "node", "get", testNodeURN, "--body-only")
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, err, captured := runNode(t, bodyOnlyStubs(delimiterBody), body, "node", "update", testNodeURN, "--content-file", "-")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := updateSentContent(t, captured); got != delimiterBody {
		t.Errorf("round trip changed the body\n got %q\nwant %q", got, delimiterBody)
	}
	if stderr != "" {
		t.Errorf("an unchanged body warns about nothing, got %q", stderr)
	}
}

func updateSentContent(t *testing.T, captured map[string]json.RawMessage) string {
	t.Helper()
	raw, ok := captured["UpdateNode"]
	if !ok {
		t.Fatal("no UpdateNode was sent")
	}
	var vars struct {
		Input struct {
			Content *string `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatal(err)
	}
	if vars.Input.Content == nil {
		t.Fatal("UpdateNode carried no content")
	}
	return *vars.Input.Content
}

// What `node get --raw` (text mode) prints: header, then the body.
const headerPrefixed = "flaky\n  loc: findings:flaky\n  urn: " + testNodeURN + "\n  type: info\n  content: stored body ({{…}} placeholders not compiled)\n\n" + delimiterBody + "\n"

func TestNodeUpdateRefusesAGetHeaderPrefix(t *testing.T) {
	for _, src := range []struct {
		name  string
		stdin string
		args  []string
	}{
		{"content-file", "", []string{"--content-file", writeTemp(t, "b.md", headerPrefixed)}},
		{"content -", headerPrefixed, []string{"--content", "-"}},
		{"content inline", "", []string{"--content", headerPrefixed}},
	} {
		_, _, err, captured := runNode(t, bodyOnlyStubs(delimiterBody), src.stdin,
			append([]string{"node", "update", testNodeURN}, src.args...)...)
		if exitcode.FromError(err) != exitcode.Usage {
			t.Errorf("%s: want exit 2, got %v", src.name, err)
		}
		if err == nil || !strings.Contains(err.Error(), "--body-only") {
			t.Errorf("%s: the refusal must name --body-only: %v", src.name, err)
		}
		if _, wrote := captured["UpdateNode"]; wrote {
			t.Errorf("%s: a refused body must not be written", src.name)
		}
	}
}

func TestNodeUpdateForceWritesAHeaderPrefix(t *testing.T) {
	_, _, err, captured := runNode(t, bodyOnlyStubs(delimiterBody), "",
		"node", "update", testNodeURN, "--content-file", writeTemp(t, "b.md", headerPrefixed), "--force")
	if err != nil {
		t.Fatalf("--force must write: %v", err)
	}
	if got := updateSentContent(t, captured); got != headerPrefixed {
		t.Errorf("--force writes the body as given, sent %q", got)
	}
}

func TestNodeUpdateWarnsOnDroppedTemplateTags(t *testing.T) {
	rendered := "Write {{name}} literally; <%who%> is a placeholder."
	_, stderr, err, captured := runNode(t, bodyOnlyStubs(delimiterBody), "",
		"node", "update", testNodeURN, "--content-file", writeTemp(t, "b.md", rendered))
	if err != nil {
		t.Fatalf("a drop warns, it does not refuse: %v", err)
	}
	if got := updateSentContent(t, captured); got != rendered {
		t.Errorf("sent %q", got)
	}
	if !strings.Contains(stderr, "{{=<% %>=}}") || !strings.Contains(stderr, "warning") {
		t.Errorf("stderr must warn about the dropped delimiter tag, got %q", stderr)
	}
	if _, raw := captured["GetNodeRaw"]; !raw {
		t.Error("the comparison must be against the STORED body (GetNodeRaw)")
	}
}

func TestNodeUpdateDryRunWritesNothing(t *testing.T) {
	proposed := "Write {{name}} literally; <%who%> is a placeholder."
	out, _, err, captured := runNode(t, bodyOnlyStubs(delimiterBody), "",
		"node", "update", testNodeURN, "--content-file", writeTemp(t, "b.md", proposed), "--name", "renamed", "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for op := range captured {
		if op != "ResolveUrn" && op != "GetNodeRaw" {
			t.Errorf("a dry run may only read, sent %s", op)
		}
	}
	var dto struct {
		DryRun   bool     `json:"dryRun"`
		ID       string   `json:"id"`
		Loc      string   `json:"loc"`
		Fields   []string `json:"fields"`
		Warnings []string `json:"warnings"`
		Content  *struct {
			Before string `json:"before"`
			After  string `json:"after"`
			Diff   string `json:"diff"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("--json must parse: %v (%s)", err, out)
	}
	if !dto.DryRun || dto.ID != "n1" || strings.Join(dto.Fields, ",") != "name,content" {
		t.Errorf("dry run = %+v", dto)
	}
	if dto.Content == nil || dto.Content.Before != delimiterBody || dto.Content.After != proposed ||
		!strings.Contains(dto.Content.Diff, "-{{=<% %>=}}") {
		t.Errorf("content preview = %+v", dto.Content)
	}
	if len(dto.Warnings) != 1 || !strings.Contains(dto.Warnings[0], "{{=<% %>=}}") {
		t.Errorf("warnings = %q", dto.Warnings)
	}
}

func TestNodeUpdateDryRunText(t *testing.T) {
	out, _, err, captured := runNode(t, bodyOnlyStubs(delimiterBody), "",
		"node", "update", testNodeURN, "--content", delimiterBody+"\nmore", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if _, wrote := captured["UpdateNode"]; wrote {
		t.Error("a dry run must not write")
	}
	for _, want := range []string{"dry run", "+more", "content"} {
		if !strings.Contains(out, want) {
			t.Errorf("text preview must contain %q:\n%s", want, out)
		}
	}
}

// The header check runs on a dry run too: the preview must not bless a body
// the write would refuse.
func TestNodeUpdateDryRunRefusesAHeaderPrefix(t *testing.T) {
	_, _, err, _ := runNode(t, bodyOnlyStubs(delimiterBody), "",
		"node", "update", testNodeURN, "--content", headerPrefixed, "--dry-run")
	if exitcode.FromError(err) != exitcode.Usage {
		t.Errorf("want exit 2, got %v", err)
	}
}

func TestNodeUpdateDryRunNeedsAField(t *testing.T) {
	_, _, err, _ := runNode(t, bodyOnlyStubs(delimiterBody), "", "node", "update", testNodeURN, "--dry-run")
	if exitcode.FromError(err) != exitcode.Usage {
		t.Errorf("want exit 2, got %v", err)
	}
}

// --dry-run previews the field update only; a data merge is a separate
// mutation it does not preview, so the combination is refused, not half-run.
func TestNodeUpdateDryRunRefusesDataMerge(t *testing.T) {
	_, _, err, captured := runNode(t, bodyOnlyStubs(delimiterBody), "",
		"node", "update", testNodeURN, "--data-merge", `{"a":1}`, "--dry-run")
	if exitcode.FromError(err) != exitcode.Usage {
		t.Errorf("want exit 2, got %v", err)
	}
	if _, wrote := captured["UpdateNodeData"]; wrote {
		t.Error("must not merge")
	}
}

// spec get --body-only is the STORED body too: it used to print the
// rendered one, which is how 050:01 lost its delimiter line.
func TestSpecGetBodyOnlyIsTheStoredBody(t *testing.T) {
	b, _ := json.Marshal(delimiterBody)
	rawDetail := strings.Replace(cleanSpecDetail, `"content":`, `"content":`+string(b)+`,"x":`, 1)
	out, _, err, _ := runNode(t, map[string]string{
		"ResolveUrn":     resolveSpecJSON,
		"GetNode":        `{"data":{"node":` + cleanSpecDetail + `}}`,
		"GetSpecNodeRaw": `{"data":{"node":` + rawDetail + `}}`,
		"NodeBatch":      specLintRawBodyStub(rawDetail),
	}, "", "spec", "get", "msg:010:02", "-m", specMem, "--body-only")
	if err != nil {
		t.Fatal(err)
	}
	if out != delimiterBody {
		t.Errorf("spec get --body-only must print the stored body exactly\n got %q\nwant %q", out, delimiterBody)
	}
}

func TestSpecEditRefusesAGetHeaderPrefix(t *testing.T) {
	header := "msg:010:02 — W2\nhrn:node:micromentor.org:platform-specs:msg:010:02\nTags: spec\n\n" + placeholderBody
	_, _, err, captured := runNode(t, previewMocks(), "",
		"spec", "edit", "msg:010:02", "-m", specMem, "--content-file", writeTemp(t, "b.md", header))
	if exitcode.FromError(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "--body-only") {
		t.Errorf("want an exit-2 refusal naming --body-only, got %v", err)
	}
	if _, wrote := captured["UpdateSpecNode"]; wrote {
		t.Error("a refused body must not be written")
	}
	_, _, err, captured = runNode(t, previewMocks(), "",
		"spec", "edit", "msg:010:02", "-m", specMem, "--content-file", writeTemp(t, "b.md", header), "--force")
	if err != nil {
		t.Fatalf("--force must write: %v", err)
	}
	if _, wrote := captured["UpdateSpecNode"]; !wrote {
		t.Error("--force must write")
	}
}

func TestSpecEditWarnsOnDroppedTemplateTags(t *testing.T) {
	proposed := strings.Replace(placeholderBody, "{{role}}", "role", 1)
	out, stderr, err, _ := runNode(t, previewMocks(), "",
		"spec", "edit", "msg:010:02", "-m", specMem, "--content-file", writeTemp(t, "b.md", proposed), "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Warnings) != 1 || !strings.Contains(dto.Warnings[0], "{{role}}") {
		t.Errorf("warnings = %q", dto.Warnings)
	}
	if !strings.Contains(stderr, "{{role}}") {
		t.Errorf("stderr must carry the warning too, got %q", stderr)
	}
}
