package spec

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/spf13/cobra"
	"os"
	"strings"
	"testing"
)

func TestLintFrameBasics2018(t *testing.T) {
	role := "spec.rule"
	body := "# Rule Spec: A rule\n\n- **Status:** Proposed\n"
	abs := "A rule."
	n := specNode{Loc: "cor:acl:140:04", NodeType: "info", Name: "Rule: A rule", Role: &role, Tags: []string{"spec"}, Content: &body, Abstract: &abs}
	fs := lintNode(n, "hadronmemory.com::specs")
	for _, rule := range []string{"frame-name", "frame-tags"} {
		if !hasRule(fs, rule) {
			t.Errorf("missing %s: %+v", rule, fs)
		}
	}
}

func frameFixture2018(t *testing.T) (string, frameTemplate, specNode) {
	t.Helper()
	raw, err := os.ReadFile("testdata/rule-template-2018.md")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	tmpl, err := parseFrameTemplate(source)
	if err != nil {
		t.Fatal(err)
	}
	_, body, _ := strings.Cut(source, "```markdown\n")
	body, _, _ = strings.Cut(body, "```")
	body = strings.ReplaceAll(body, "<plain title>", "A rule")
	role, abs := "spec.rule", "The rule's meaning."
	return source, tmpl, specNode{Loc: "cor:acl:140:04", Name: "Rule: A rule [cor:acl:140:04]", NodeType: "info", Role: &role, Content: &body, Abstract: &abs}
}

func TestFrameTemplateDriven2018(t *testing.T) {
	source, tmpl, node := frameFixture2018(t)
	if fs := lintFrame(node, tmpl); len(fs) != 0 {
		t.Fatalf("clean fixture: %+v", fs)
	}
	changed := strings.Replace(source, "## Why We Need This Rule", "## Newly Added\n\n<New section>\n\n## Why We Need This Rule", 1)
	newer, err := parseFrameTemplate(changed)
	if err != nil {
		t.Fatal(err)
	}
	fs := lintFrame(node, newer)
	if !hasRule(fs, "frame-required") {
		t.Fatalf("template edit did not change lint: %+v", fs)
	}
	for _, tc := range []struct{ name, old, new, rule string }{
		{"extra-section", "## Exceptions", "## App Secret Grants", "frame-section"},
		{"caps", tmpl.bottom, "", "frame-caps"},
		{"status", "- **Status:**", "Status:", "frame-status"},
		{"built", "- **Built:**", "Built:", "frame-built"},
		{"divider", "\n---\n", "\n---\n\n---\n", "frame-divider"},
		{"order", "## Exceptions", "## What Can Change", "frame-section-order"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := node
			body := strings.Replace(*n.Content, tc.old, tc.new, 1)
			n.Content = &body
			if !hasRule(lintFrame(n, tmpl), tc.rule) {
				t.Fatalf("missing %s", tc.rule)
			}
		})
	}
	abs := strings.Repeat("x", 1601)
	node.Abstract = &abs
	if !hasRule(lintFrame(node, tmpl), "frame-abstract") {
		t.Fatal("abstract ceiling not checked")
	}
	if _, err := parseFrameTemplate("not a template"); err == nil {
		t.Fatal("malformed template accepted")
	}
}

func TestFrameReadsRawTemplatesAndLinkLabels2018(t *testing.T) {
	source, _, node := frameFixture2018(t)
	body := *node.Content + "\n[[cor:acl:140:05|cor:acl:140:05]]\n[[hrn:node:hadronmemory.com:specs:cor:acl:140:05|Rule: Other]]\n"
	node.Content = &body
	calls := []string{}
	client := draftReadClient(func(req *graphql.Request, resp *graphql.Response) error {
		if req.OpName != "GetSpecNodeRaw" || !strings.Contains(req.Query, "raw: true") {
			t.Fatalf("unexpected non-raw operation: %s", req.OpName)
		}
		b, _ := json.Marshal(req.Variables)
		var v struct{ Ref string }
		_ = json.Unmarshal(b, &v)
		calls = append(calls, v.Ref)
		n := &gen.GetSpecNodeRawNode{}
		switch v.Ref {
		case "hrn:node:hadronmemory.com:specs-engineer-system:authoring:rule:template":
			n.Content = &source
		case "hrn:node:hadronmemory.com:specs:cor:acl:140:05":
			n.Name = "Rule: Other [cor:acl:140:05]"
		default:
			t.Fatalf("unexpected reference %s", v.Ref)
		}
		resp.Data.(*gen.GetSpecNodeRawResponse).Node = n
		return nil
	})
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	fs, err := lintFrames(cmd, client, "hrn:mem:hadronmemory.com:specs", []specNode{node})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Rule != "frame-link" || !strings.Contains(fs[0].Message, `"Rule: Other"`) {
		t.Fatalf("findings: %+v", fs)
	}
	if len(calls) != 2 {
		t.Fatalf("uncached template/target reads: %v", calls)
	}
	// A denied template read must be an error, never an empty/clean report.
	denied := draftReadClient(func(*graphql.Request, *graphql.Response) error { return errors.New("fixture template denied") })
	if _, err := lintFrames(cmd, denied, "hrn:mem:hadronmemory.com:specs", []specNode{node}); err == nil {
		t.Fatal("failed template returned clean")
	}
}
