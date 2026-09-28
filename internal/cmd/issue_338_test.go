package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestSpecEditRejectsOverCapAbstractBeforePreviewOrSave(t *testing.T) {
	// The final newline and astral rune count: the server measures UTF-16
	// code units, not trimmed Unicode code points.
	abstract := strings.Repeat("a", 1998) + "😀\n"
	for _, dryRun := range []bool{true, false} {
		args := []string{"--abstract", abstract}
		if dryRun {
			args = append(args, "--dry-run")
		}
		_, sent, err := runSpecEdit(t, guardMocks(guardWriteOK), "", args...)
		if got := exitCodeFor(err); got != exitcode.Usage {
			t.Fatalf("dryRun=%v: exit %d, want usage (%v)", dryRun, got, err)
		}
		if err == nil || !strings.Contains(err.Error(), "2000-character cap") || !strings.Contains(err.Error(), "2001 characters") {
			t.Fatalf("dryRun=%v: missing cap diagnostic: %v", dryRun, err)
		}
		if len(sent["UpdateSpecNode"]) != 0 {
			t.Fatalf("dryRun=%v: over-cap abstract was sent to the server", dryRun)
		}
	}
}

func TestSpecEditDescriptionUsesGuardedWrite(t *testing.T) {
	const description = "A clearer list/search summary."
	out, sent, err := runSpecEdit(t, guardMocks(guardWriteOK), "", "--description", description, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(sent["UpdateSpecNode"]) != 1 {
		t.Fatalf("expected one guarded write, got %d", len(sent["UpdateSpecNode"]))
	}
	if rev := sentRevision(t, sent["UpdateSpecNode"][0]); rev == nil || *rev != 7 {
		t.Fatalf("description write missing expected revision: %v", rev)
	}
	var input struct {
		Input struct {
			Description *string `json:"description"`
			Content     *string `json:"content"`
			Abstract    *string `json:"abstract"`
		} `json:"input"`
	}
	if err := json.Unmarshal(sent["UpdateSpecNode"][0], &input); err != nil {
		t.Fatal(err)
	}
	if input.Input.Description == nil || *input.Input.Description != description || input.Input.Content != nil || input.Input.Abstract != nil {
		t.Fatalf("unexpected description-only input: %+v", input.Input)
	}
	var raw struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(sent["UpdateSpecNode"][0], &raw); err != nil {
		t.Fatal(err)
	}
	for _, omitted := range []string{"content", "abstract"} {
		if _, present := raw.Input[omitted]; present {
			t.Fatalf("unset %s was sent instead of omitted", omitted)
		}
	}
	var dto struct {
		Changed            bool `json:"changed"`
		DescriptionChanged bool `json:"descriptionChanged"`
		Changes            []struct {
			Field string `json:"field"`
		} `json:"changes"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if !dto.Changed || !dto.DescriptionChanged || len(dto.Changes) != 1 || dto.Changes[0].Field != "description" {
		t.Fatalf("description change absent from result: %+v", dto)
	}
}

func TestSpecEditDescriptionFileAndExplicitClear(t *testing.T) {
	mocks := guardMocks(guardWriteOK)
	mocks["GetSpecNodeForEdit"][0] = strings.Replace(mocks["GetSpecNodeForEdit"][0],
		`"name":"msg:010:02 — W2",`, `"name":"msg:010:02 — W2","description":"Old summary",`, 1)
	path := writeTemp(t, "description.txt", "A summary from file.\n")
	_, sent, err := runSpecEdit(t, mocks, "", "--description-file", path)
	if err != nil {
		t.Fatal(err)
	}
	var fileVars struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(sent["UpdateSpecNode"][0], &fileVars); err != nil {
		t.Fatal(err)
	}
	if string(fileVars.Input["description"]) != `"A summary from file.\n"` {
		t.Fatalf("description file was not sent verbatim: %s", fileVars.Input["description"])
	}
	_, sent, err = runSpecEdit(t, mocks, "", "--description", "")
	if err != nil {
		t.Fatal(err)
	}
	var clearVars struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(sent["UpdateSpecNode"][0], &clearVars); err != nil {
		t.Fatal(err)
	}
	if string(clearVars.Input["description"]) != `""` {
		t.Fatalf("explicit empty description must clear, got %s", clearVars.Input["description"])
	}
}

func TestSpecEditDescriptionDryRunWritesNothing(t *testing.T) {
	out, sent, err := runSpecEdit(t, guardMocks(""), "", "--description", "A new summary", "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(sent["UpdateSpecNode"]) != 0 {
		t.Fatal("dry run wrote description")
	}
	var dto struct {
		DescriptionChanged bool `json:"descriptionChanged"`
		Changes            []struct {
			Field string `json:"field"`
			After string `json:"after"`
		} `json:"changes"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if !dto.DescriptionChanged || len(dto.Changes) != 1 || dto.Changes[0].Field != "description" || dto.Changes[0].After != "A new summary" {
		t.Fatalf("dry run omitted proposed description: %+v", dto)
	}
}

func TestMemoryGetRejectsMalformedReferenceLocally(t *testing.T) {
	for _, ref := range []string{"definitely-not-a-memory", "hrn:memory:acme.com:kb:extra"} {
		t.Run(ref, func(t *testing.T) {
			gql, sent := captureGraphQL(t, map[string]string{})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"memory", "get", ref, "--server", gql.URL})
			err := root.Execute()
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Fatalf("exit %d, want usage (%v)", got, err)
			}
			if err == nil || !strings.Contains(err.Error(), "invalid memory reference") || !strings.Contains(err.Error(), "hrn:mem:<root>:<slug>") {
				t.Fatalf("missing v2 guidance: %v", err)
			}
			if len(sent) != 0 {
				t.Fatalf("malformed ref reached GraphQL: %v", sent)
			}
		})
	}
}

func TestMemoryGetAcceptsLegacyCompoundReference(t *testing.T) {
	const ref = "hrn:memory:micromentor.org::coding-app::coding-agent::app-mem"
	gql, sent := captureGraphQL(t, map[string]string{"GetMemory": `{"data":{"memory":` + memoryJSON + `}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "get", ref, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var vars struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(sent["GetMemory"], &vars); err != nil {
		t.Fatal(err)
	}
	if vars.Ref != ref {
		t.Fatalf("legacy compound ref was changed: %q", vars.Ref)
	}
}
