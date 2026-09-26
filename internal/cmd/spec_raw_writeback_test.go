package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#742: `spec supersede` and `spec extract --strip-source` WRITE a body they
// read. GetNode renders Mustache, so each fixture serves GetNode the RENDERED
// body and GetSpecNodeRaw the STORED one: whichever the command writes from is
// what reaches the mutation payload.

const (
	renderedLine = "Hello Jane, the cli-engineer.\n"
	storedLine   = "Hello {{name}}, the {{role}}.\n"
)

// withContent returns a GetNode/GetSpecNodeRaw payload for cleanSpecDetail with
// its body replaced.
func withContent(body string) string {
	oldBody, _ := json.Marshal(cleanSpecDetailContent)
	newBody, _ := json.Marshal(body)
	return `{"data":{"node":` + strings.Replace(cleanSpecDetail, string(oldBody), string(newBody), 1) + `}}`
}

func supersedeRawMocks(rendered, stored string) map[string]string {
	return map[string]string{
		"ResolveUrn":     resolveSpecJSON,
		"GetNode":        withContent(rendered),
		"GetSpecNodeRaw": withContent(stored),
		"NodeBatch":      specLintRawBodyStub(cleanSpecDetail),
		"FindNodes":      `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `,` + specNodeList("msg:010:00", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
		"CreateSpecNode": `{"data":{"createSpecNode":{"id":"new1","memoryId":"mem1","loc":"msg:010:03","name":"msg:010:03 — W2 v2","nodeType":"info","tags":["spec","p1"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"UpdateSpecNode": `{"data":{"updateSpecNode":{"id":"sp1","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — W2","nodeType":"info","tags":["spec","p1","superseded"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
		"CreateEdge":     `{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"sp1","loc":"msg:010:02"},"target":{"id":"new1","loc":"msg:010:03"}}}}`,
	}
}

type contentInput struct {
	Input struct {
		Loc     string  `json:"loc"`
		Content *string `json:"content"`
	} `json:"input"`
}

func runSupersede(t *testing.T, mocks map[string]string, extra ...string) map[string]json.RawMessage {
	t.Helper()
	gql, captured := captureSupersedeGraphQL(t, "msg:010:03", mocks)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"spec", "supersede", "msg:010:02", "-m", specMem, "--title", "W2 v2", "--yes", "--json"}, extra...), "--server", gql.URL))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out.String())
	}
	return captured
}

func sentContent(t *testing.T, captured map[string]json.RawMessage, op string) string {
	t.Helper()
	var in contentInput
	if err := json.Unmarshal(captured[op], &in); err != nil || in.Input.Content == nil {
		t.Fatalf("%s sent no content (err %v): %s", op, err, captured[op])
	}
	return *in.Input.Content
}

// Retiring appends a note to the old spec's body and writes it back: the
// stored body, placeholders intact, not the rendered one.
func TestSpecSupersedeRetiresTheStoredBody(t *testing.T) {
	stored := cleanSpecDetailContent + "\n" + storedLine
	captured := runSupersede(t, supersedeRawMocks(cleanSpecDetailContent+"\n"+renderedLine, stored))
	got := sentContent(t, captured, "UpdateSpecNode")
	if want := stored + "\n\n> Superseded by msg:010:03."; got != want {
		t.Errorf("retirement wrote:\n%q\nwant the stored body plus the note:\n%q", got, want)
	}
}

// Control: a body with no template syntax retires exactly as before.
func TestSpecSupersedeRetiresAPlainBodyUnchanged(t *testing.T) {
	captured := runSupersede(t, supersedeRawMocks(cleanSpecDetailContent, cleanSpecDetailContent))
	if got, want := sentContent(t, captured, "UpdateSpecNode"), cleanSpecDetailContent+"\n\n> Superseded by msg:010:03."; got != want {
		t.Errorf("retirement wrote %q, want %q", got, want)
	}
}

// --copy-body seeds the successor with the old body: the stored one.
func TestSpecSupersedeCopyBodyCopiesTheStoredBody(t *testing.T) {
	stored := cleanSpecDetailContent + "\n" + storedLine
	captured := runSupersede(t, supersedeRawMocks(cleanSpecDetailContent+"\n"+renderedLine, stored), "--copy-body")
	got := sentContent(t, captured, "CreateSpecNode")
	if !strings.Contains(got, storedLine) || strings.Contains(got, renderedLine) {
		t.Errorf("the successor must get the stored body, placeholders intact; sent:\n%s", got)
	}
}

// Control: without --copy-body the successor is the scaffold, not the old body.
func TestSpecSupersedeWithoutCopyBodyDoesNotCopy(t *testing.T) {
	captured := runSupersede(t, supersedeRawMocks(cleanSpecDetailContent+"\n"+renderedLine, cleanSpecDetailContent+"\n"+storedLine))
	if got := sentContent(t, captured, "CreateSpecNode"); strings.Contains(got, "Hello") {
		t.Errorf("without --copy-body the successor must not carry the old body; sent:\n%s", got)
	}
}

const (
	extractStoredBody   = "# cor:dmo:060:02 — Node\n\nIntro {{name}} para.\n\n## Node type\n\nThe nodeType chunk.\n\n## Tail\n\nend {{> footer}}.\n"
	extractRenderedBody = "# cor:dmo:060:02 — Node\n\nIntro Jane para.\n\n## Node type\n\nThe nodeType chunk.\n\n## Tail\n\nend .\n"
)

func extractRawMocks() map[string]string {
	m := extractMocks()
	oldBody, _ := json.Marshal("# cor:dmo:060:02 — Node\n\nIntro para.\n\n## Node type\n\nThe nodeType chunk.\n\n## Tail\n\nend.\n")
	node := func(body string) string {
		b, _ := json.Marshal(body)
		return strings.Replace(extractSrcDetail, string(oldBody), string(b), 1)
	}
	m["GetNode"] = node(extractRenderedBody)
	m["GetSpecNodeRaw"] = node(extractStoredBody)
	return m
}

func runExtract(t *testing.T, extra ...string) map[string]json.RawMessage {
	t.Helper()
	gql, captured := captureGraphQL(t, extractRawMocks())
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader(extractChunk)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"spec", "extract", "cor:dmo:060:02", "-m", specMem,
		"--to-feature", "020", "--title", "Node type", "--content", "-", "--json"}, extra...), "--server", gql.URL))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out.String())
	}
	return captured
}

// --strip-source writes the source back without the moved chunk: computed from
// the stored body, so every other placeholder survives.
func TestSpecExtractStripSourceKeepsTheStoredBody(t *testing.T) {
	got := sentContent(t, runExtract(t, "--strip-source"), "UpdateSpecNode")
	if strings.Contains(got, "nodeType chunk") {
		t.Errorf("the chunk was not stripped:\n%s", got)
	}
	if !strings.Contains(got, "Intro {{name}} para.") || !strings.Contains(got, "end {{> footer}}.") || strings.Contains(got, "Jane") {
		t.Errorf("the source must be written from its stored body, placeholders intact; sent:\n%s", got)
	}
}

// Control: without --strip-source the source is not written at all.
func TestSpecExtractWithoutStripDoesNotWriteTheSource(t *testing.T) {
	if captured := runExtract(t); captured["UpdateSpecNode"] != nil {
		t.Errorf("without --strip-source the source must not be written, sent %s", captured["UpdateSpecNode"])
	}
}
