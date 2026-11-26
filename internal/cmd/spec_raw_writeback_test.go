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
		"ResolveUrn":         resolveSpecJSON,
		"GetNode":            withContent(rendered),
		"GetSpecNodeRaw":     withContent(stored),
		"GetSpecNodeForEdit": forEditFixture(withContent(stored)),
		"NodeBatch":          specLintRawBodyStub(cleanSpecDetail),
		"FindNodes":          `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `,` + specNodeList("msg:010:00", `["spec","p1"]`) + `,` + specNodeList("msg:010:02", `["spec","p1"]`) + `]}}`,
		"CreateSpecNode":     `{"data":{"createSpecNode":{"id":"new1","memoryId":"mem1","loc":"msg:010:03","name":"msg:010:03 — W2 v2","nodeType":"info","tags":["spec","p1"],"updatedAt":"2026-06-14T00:00:00Z"}}}`,
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

// --copy-body copies the body AND the abstract from one snapshot, the raw
// read, never a fresh body beside the first read's stale abstract (#743
// review, Codex).
func TestSpecSupersedeCopyBodyCopiesOneSnapshot(t *testing.T) {
	m := supersedeRawMocks(cleanSpecDetailContent, cleanSpecDetailContent)
	stale := `"abstract":"Win back users who never engaged after signup."`
	m["GetNode"] = strings.Replace(m["GetNode"], stale, `"abstract":"The abstract as first read."`, 1)
	m["GetSpecNodeForEdit"] = strings.Replace(m["GetSpecNodeForEdit"], stale, `"abstract":"The abstract as stored now."`, 1)
	captured := runSupersede(t, m, "--copy-body")
	var in struct {
		Input struct {
			Abstract *string `json:"abstract"`
		} `json:"input"`
	}
	if err := json.Unmarshal(captured["CreateSpecNode"], &in); err != nil || in.Input.Abstract == nil {
		t.Fatalf("CreateSpecNode sent no abstract (err %v): %s", err, captured["CreateSpecNode"])
	}
	if *in.Input.Abstract != "The abstract as stored now." {
		t.Errorf("the successor's abstract = %q, want the one read with the body", *in.Input.Abstract)
	}
}

// A strip miss against a templated source says why: the stored text is what
// is matched, and a chunk copied from `spec get` is rendered. The manual-trim
// reminder names `spec edit`, never the lossy `spec get | node update` (#743
// review, Copilot).
func TestSpecExtractStripMissExplainsPlaceholders(t *testing.T) {
	rendered := "## Tail\n\nend .\n" // what `spec get` shows for "end {{> footer}}."
	for name, dry := range map[string]bool{"executed": false, "dry run": true} {
		t.Run(name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, extractRawMocks())
			f, out := testFactory(t)
			f.IOStreams.In = strings.NewReader(rendered)
			args := []string{"spec", "extract", "cor:dmo:060:02", "-m", specMem,
				"--to-feature", "020", "--title", "Tail", "--content", "-", "--strip-source", "--server", gql.URL}
			if dry {
				args = append(args, "--dry-run")
			}
			root := NewRootCmd(f)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if captured["UpdateSpecNode"] != nil {
				t.Error("a strip miss must not write the source")
			}
			text := out.String() + f.IOStreams.ErrOut.(*strings.Builder).String()
			if !strings.Contains(text, "placeholders and is matched as stored") {
				t.Errorf("a miss on a templated source must explain placeholders:\n%s", text)
			}
			if !dry && (!strings.Contains(text, "spec edit cor:dmo:060:02 -m "+specMem) || strings.Contains(text, "node update --content")) {
				t.Errorf("the manual trim must point at spec edit, never the lossy spec get | node update:\n%s", text)
			}
		})
	}
}

// --copy-body derives the successor's tags from the same raw snapshot as its
// body and abstract (#743 review, Copilot).
func TestSpecSupersedeCopyBodyTagsComeFromTheSnapshot(t *testing.T) {
	m := supersedeRawMocks(cleanSpecDetailContent, cleanSpecDetailContent)
	m["GetSpecNodeForEdit"] = strings.Replace(m["GetSpecNodeForEdit"], `"tags":["spec","p1","messaging"]`, `"tags":["spec","p1","messaging","added-meanwhile"]`, 1)
	captured := runSupersede(t, m, "--copy-body")
	var in struct {
		Input struct {
			Tags []string `json:"tags"`
		} `json:"input"`
	}
	if err := json.Unmarshal(captured["CreateSpecNode"], &in); err != nil {
		t.Fatal(err)
	}
	if !contains(in.Input.Tags, "added-meanwhile") {
		t.Errorf("successor tags = %v, want the raw snapshot's topical tags", in.Input.Tags)
	}
}
