package spec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
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

// A successful transport with an unreadable template must not become a clean
// verdict. These are distinct from the denied/transport-error case above.
func TestFrameUnreadableTemplateRefuses2018(t *testing.T) {
	_, _, node := frameFixture2018(t)
	for _, tc := range []struct {
		name     string
		template *gen.GetSpecNodeRawNode
	}{
		{"null-node", nil},
		{"null-content", &gen.GetSpecNodeRawNode{Id: "template", Name: "Rule template"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := draftReadClient(func(req *graphql.Request, resp *graphql.Response) error {
				calls++
				if req.OpName != "GetSpecNodeRaw" {
					t.Fatalf("unexpected operation %s", req.OpName)
				}
				resp.Data.(*gen.GetSpecNodeRawResponse).Node = tc.template
				return nil
			})
			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())
			fs, err := lintFrames(cmd, client, "hrn:mem:hadronmemory.com:specs", []specNode{node})
			if exitcode.FromError(err) != exitcode.Unavailable {
				t.Fatalf("unreadable template must refuse Unavailable: findings=%+v err=%v", fs, err)
			}
			if fs != nil || calls != 1 {
				t.Fatalf("partial findings or extra reads: findings=%+v calls=%d", fs, calls)
			}
		})
	}
}

func TestFrameInvalidRoleDoesNotAbort2018(t *testing.T) {
	source, _, good := frameFixture2018(t)
	for _, role := range []string{"spec.", "spec.Rule", "spec.api-v2"} {
		t.Run(role, func(t *testing.T) {
			bad := good
			bad.Loc = "cor:acl:140:99"
			bad.Role = &role
			goodBody := strings.Replace(*good.Content, "- **Status:**", "Status:", 1)
			next := good
			next.Content = &goodBody
			client := draftReadClient(func(req *graphql.Request, resp *graphql.Response) error {
				resp.Data.(*gen.GetSpecNodeRawResponse).Node = &gen.GetSpecNodeRawNode{Content: &source}
				return nil
			})
			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())
			fs, err := lintFrames(cmd, client, "hrn:mem:hadronmemory.com:specs", []specNode{bad, next})
			if err != nil {
				t.Fatalf("one bad role aborted corpus: %v", err)
			}
			var invalid, continued bool
			for _, f := range fs {
				invalid = invalid || (f.Citation == bad.Loc && f.Rule == "frame-role" && f.Severity == sevError)
				continued = continued || (f.Citation == next.Loc && f.Rule == "frame-status")
			}
			if !invalid || !continued {
				t.Fatalf("bad role or later-node finding missing: %+v", fs)
			}
		})
	}
}

// speccheck's legacy/retired exemption is structural. Link integrity remains
// corpus-wide; the historical body must not become a clean link verdict.
func TestFrameHistoricalLinksStillChecked2018(t *testing.T) {
	for _, retired := range []bool{false, true} {
		t.Run(fmt.Sprintf("retired=%v", retired), func(t *testing.T) {
			role := "spec.rule"
			body := "Legacy body. [[cor:acl:140:05|wrong]] [link](https://example.test/app/u/hrn:node:hadronmemory.com:specs:cor:acl:140:05)"
			n := specNode{Loc: "cor:acl:140:04", Role: &role, Content: &body, UpdatedAt: "2026-10-03T12:00:00Z"}
			if retired {
				n.Tags = []string{supersededTag}
				n.UpdatedAt = "2026-11-14T12:00:00Z"
			}
			target := specNode{Loc: "cor:acl:140:05", Name: "Rule: Other [cor:acl:140:05]"}
			client := draftReadClient(func(*graphql.Request, *graphql.Response) error {
				t.Fatal("historical structure requested a template")
				return nil
			})
			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())
			fs, err := lintFrames(cmd, client, "hrn:mem:hadronmemory.com:specs", []specNode{n, target})
			if err != nil {
				t.Fatal(err)
			}
			if len(fs) != 2 {
				t.Fatalf("historical link checks lost: %+v", fs)
			}
			for _, f := range fs {
				if f.Citation != n.Loc || f.Rule != "frame-link" || f.Severity != sevError {
					t.Fatalf("unexpected historical shape finding: %+v", f)
				}
			}
		})
	}
}
