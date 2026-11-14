package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSpecEditTypedTitle2018(t *testing.T) {
	m := guardMocks(guardWriteOK)
	m["GetSpecNodeForEdit"][0] = strings.Replace(m["GetSpecNodeForEdit"][0], `"role":null`, `"role":"spec.rule"`, 1)
	title := "Rule: Who may issue a grant [msg:010:02]"
	out, _, err := runSpecEdit(t, m, "", "--title", title, "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Changes []struct{ Field, Diff string }
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) != 1 || !strings.Contains(result.Changes[0].Diff, "+"+title) || strings.Contains(result.Changes[0].Diff, "+msg:010:02 — ") {
		t.Fatalf("typed title not literal: %s", out)
	}
}

func TestSpecRescindAuthoredClaim2018(t *testing.T) {
	_, s, err := runRescind(t, &rescindServer{}, "", "--reason", "draft dropped", "--summary", "Former contract.", "--yes", "--authored-with", "hrn:node:acme.com:tasks:author", "--authored-revision", "4")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Input struct {
			AuthoredWith struct {
				Task     string
				Revision int
			}
		}
	}
	if err := json.Unmarshal(s.vars["UpdateSpecNode"][0], &v); err != nil {
		t.Fatal(err)
	}
	if v.Input.AuthoredWith.Task != "hrn:node:acme.com:tasks:author" || v.Input.AuthoredWith.Revision != 4 {
		t.Fatalf("claim dropped: %+v", v)
	}
}

func TestSpecEditDryRunAuthoringLimit2018(t *testing.T) {
	for _, args := range [][]string{
		{"--title", "New title", "--dry-run"},
		{"--title", "New title", "--dry-run", "--authored-with", "hrn:node:acme.com:tasks:does-not-exist", "--authored-revision", "4"},
	} {
		out, calls, err := runSpecEdit(t, guardMocks(""), "", args...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "authoring check runs only at write time") {
			t.Fatalf("misleading preview: %s", out)
		}
		if len(calls["UpdateSpecNode"]) != 0 {
			t.Fatal("dry run attempted write")
		}
	}
}

func TestSpecEditDryRunAuthoringLimitJSON2018(t *testing.T) {
	out, _, err := runSpecEdit(t, guardMocks(""), "", "--title", "New title", "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var r struct{ AuthoringCheck string }
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.AuthoringCheck, "write-only") {
		t.Fatalf("JSON preview omitted authoring limit: %s", out)
	}
}

func TestSpecLintFrameCommand2018(t *testing.T) {
	raw, err := os.ReadFile("spec/testdata/rule-template-2018.md")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	_, body, _ := strings.Cut(source, "```markdown\n")
	body, _, _ = strings.Cut(body, "```")
	oldBody, _ := json.Marshal(specBatchContent)
	newBody, _ := json.Marshal(body)
	for _, tc := range []struct {
		name, memory string
		flag         bool
	}{
		{"platform-default", "hrn:mem:hadronmemory.com:specs", false},
		{"explicit-framework", specMem, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			responses := unwiredCorpus(t)
			responses["NodeBatch"] = strings.ReplaceAll(responses["NodeBatch"], string(oldBody), string(newBody))
			responses["NodeBatch"] = strings.ReplaceAll(responses["NodeBatch"], `"role":"spec"`, `"role":"spec.rule"`)
			template, _ := json.Marshal(source)
			responses["GetSpecNodeRaw"] = `{"data":{"node":{"id":"template","content":` + string(template) + `}}}`
			srv, calls := captureGraphQL(t, responses)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"spec", "lint", "--prefix", "msg:010", "-m", tc.memory, "--json", "--server", srv.URL}
			if tc.flag {
				args = append(args, "--frame")
			}
			root.SetArgs(args)
			err := root.Execute()
			if exitCodeFor(err) != 5 {
				t.Fatalf("exit=%d err=%v output=%s", exitCodeFor(err), err, out.String())
			}
			if !strings.Contains(out.String(), `"frame-name"`) || !strings.Contains(out.String(), `"frame-tags"`) {
				t.Fatalf("missing frame mechanics: %s", out.String())
			}
			if _, ok := calls["GetSpecNodeRaw"]; !ok {
				t.Fatal("template never read")
			}
		})
	}
}
