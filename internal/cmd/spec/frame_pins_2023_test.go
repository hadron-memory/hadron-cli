package spec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func frameCmd2023() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	return cmd
}

func frameRef2023(t *testing.T, req *graphql.Request) string {
	t.Helper()
	if req.OpName != "GetSpecNodeRaw" || !strings.Contains(req.Query, "raw: true") {
		t.Fatalf("unexpected operation: %s %s", req.OpName, req.Query)
	}
	b, err := json.Marshal(req.Variables)
	if err != nil {
		t.Fatal(err)
	}
	var vars struct{ Ref string }
	if err := json.Unmarshal(b, &vars); err != nil {
		t.Fatal(err)
	}
	return vars.Ref
}

// Two same-type nodes share a read, but a second invocation reads afresh.
// This discriminates both an absent cache and an incorrectly global cache.
func TestFrameTemplateCacheIsPerInvocation2023(t *testing.T) {
	source, _, first := frameFixture2018(t)
	second := first
	second.Loc = "cor:acl:140:06"
	reads := 0
	client := draftReadClient(func(req *graphql.Request, resp *graphql.Response) error {
		if ref := frameRef2023(t, req); ref != "hrn:node:hadronmemory.com:specs-engineer-system:authoring:rule:template" {
			t.Fatalf("unexpected ref %s", ref)
		}
		reads++
		resp.Data.(*gen.GetSpecNodeRawResponse).Node = &gen.GetSpecNodeRawNode{Content: &source}
		return nil
	})
	for invocation := 1; invocation <= 2; invocation++ {
		fs, err := lintFrames(frameCmd2023(), client, "hrn:mem:hadronmemory.com:specs", []specNode{first, second})
		if err != nil || len(fs) != 0 || reads != invocation {
			t.Fatalf("invocation %d: reads=%d findings=%+v err=%v", invocation, reads, fs, err)
		}
	}
}

// A missing target is cached as missing, not silently treated as a match.
// A readable target is the positive control; neither cache crosses invocations.
func TestFrameTargetCacheKeepsMissingEvidence2023(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(fmt.Sprintf("missing=%v", missing), func(t *testing.T) {
			body := "[[cor:acl:140:99|Rule: Other]] [[cor:acl:140:99|Rule: Other]]"
			nodes := []specNode{{Loc: "cor:acl:140:04", Content: &body}, {Loc: "cor:acl:140:06", Content: &body}}
			reads := 0
			client := draftReadClient(func(req *graphql.Request, resp *graphql.Response) error {
				if ref := frameRef2023(t, req); ref != "hrn:node:hadronmemory.com:specs:cor:acl:140:99" {
					t.Fatalf("unexpected ref %s", ref)
				}
				reads++
				if !missing {
					resp.Data.(*gen.GetSpecNodeRawResponse).Node = &gen.GetSpecNodeRawNode{Name: "Rule: Other [cor:acl:140:99]"}
				}
				return nil
			})
			for invocation := 1; invocation <= 2; invocation++ {
				fs, err := lintFrames(frameCmd2023(), client, "hrn:mem:hadronmemory.com:specs", nodes)
				want := 0
				if missing {
					want = 4
				}
				if err != nil || len(fs) != want || reads != invocation {
					t.Fatalf("invocation %d: reads=%d findings=%+v err=%v", invocation, reads, fs, err)
				}
				for _, f := range fs {
					if f.Rule != "frame-link" || f.Severity != sevError || !strings.Contains(f.Message, "unresolved link: cor:acl:140:99") {
						t.Fatalf("missing target became another finding: %+v", f)
					}
				}
			}
		})
	}
}

func TestFrameTemplateKindMustMatchRole2023(t *testing.T) {
	source, _, node := frameFixture2018(t)
	// A syntactically valid template of the wrong type must refuse, rather
	// than merely lint the node against the wrong section requirements.
	source = strings.Replace(source, "# Rule Spec:", "# Flow Spec:", 1)
	reads := 0
	client := draftReadClient(func(req *graphql.Request, resp *graphql.Response) error {
		if ref := frameRef2023(t, req); ref != "hrn:node:hadronmemory.com:specs-engineer-system:authoring:rule:template" {
			t.Fatalf("unexpected ref %s", ref)
		}
		reads++
		resp.Data.(*gen.GetSpecNodeRawResponse).Node = &gen.GetSpecNodeRawNode{Content: &source}
		return nil
	})
	fs, err := lintFrames(frameCmd2023(), client, "hrn:mem:hadronmemory.com:specs", []specNode{node})
	if exitcode.FromError(err) != exitcode.Error || !strings.Contains(err.Error(), "describes Flow") || fs != nil || reads != 1 {
		t.Fatalf("wrong-type template did not refuse: findings=%+v err=%v reads=%d", fs, err, reads)
	}
}

func TestFrameRetiredUntypedStructureExemption2023(t *testing.T) {
	_, _, node := frameFixture2018(t)
	node.Role = nil
	for _, retired := range []bool{false, true} {
		t.Run(fmt.Sprintf("retired=%v", retired), func(t *testing.T) {
			n := node
			if retired {
				n.Tags = []string{supersededTag}
			}
			client := draftReadClient(func(*graphql.Request, *graphql.Response) error {
				t.Fatal("untyped node requested a template")
				return nil
			})
			fs, err := lintFrames(frameCmd2023(), client, "hrn:mem:hadronmemory.com:specs", []specNode{n})
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if retired {
				want = 0
			}
			if len(fs) != want || (!retired && (fs[0].Rule != "frame-role" || fs[0].Severity != sevError)) {
				t.Fatalf("retired=%v findings=%+v", retired, fs)
			}
		})
	}
}

// Freeze the existing rubric boundary, including its empty-date behavior.
// This does not replace the cutoff or claim timestamp validation.
func TestFrameLegacyBoundaryAndEmptyDate2023(t *testing.T) {
	source, _, node := frameFixture2018(t)
	for _, tc := range []struct {
		name, date string
		framed     bool
		reads      int
	}{
		{"before", "2026-10-03T23:59:59Z", false, 0},
		{"boundary", "2026-10-04T00:00:00Z", false, 1},
		{"empty", "", false, 1},
		{"framed-before", "2026-10-03T23:59:59Z", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := node
			n.UpdatedAt = tc.date
			if !tc.framed {
				body := "An old unframed body."
				n.Content = &body
			}
			reads := 0
			client := draftReadClient(func(req *graphql.Request, resp *graphql.Response) error {
				if ref := frameRef2023(t, req); ref != "hrn:node:hadronmemory.com:specs-engineer-system:authoring:rule:template" {
					t.Fatalf("unexpected ref %s", ref)
				}
				reads++
				resp.Data.(*gen.GetSpecNodeRawResponse).Node = &gen.GetSpecNodeRawNode{Content: &source}
				return nil
			})
			fs, err := lintFrames(frameCmd2023(), client, "hrn:mem:hadronmemory.com:specs", []specNode{n})
			if err != nil || reads != tc.reads {
				t.Fatalf("reads=%d want=%d findings=%+v err=%v", reads, tc.reads, fs, err)
			}
			if tc.reads == 0 || tc.framed {
				if len(fs) != 0 {
					t.Fatalf("exempt or valid frame findings: %+v", fs)
				}
			} else if !hasRule(fs, "frame-heading") {
				t.Fatalf("nonlegacy unframed node became clean: %+v", fs)
			}
		})
	}
}

// The generic platform pass owns frameBasics; explicit custom-memory frame
// lint owns it there. Combining their real call sites must yield one copy.
func TestFrameBasicsExactlyOnceAcrossMemoryKinds2023(t *testing.T) {
	source, _, node := frameFixture2018(t)
	node.Name = "Incorrect name"
	node.Tags = []string{"spec"}
	for _, mem := range []string{"hrn:mem:hadronmemory.com:specs", "hrn:mem:example.test:custom"} {
		t.Run(mem, func(t *testing.T) {
			reads := 0
			client := draftReadClient(func(req *graphql.Request, resp *graphql.Response) error {
				if ref := frameRef2023(t, req); ref != "hrn:node:hadronmemory.com:specs-engineer-system:authoring:rule:template" {
					t.Fatalf("unexpected ref %s", ref)
				}
				reads++
				resp.Data.(*gen.GetSpecNodeRawResponse).Node = &gen.GetSpecNodeRawNode{Content: &source}
				return nil
			})
			fs := lintNode(node, mem)
			extra, err := lintFrames(frameCmd2023(), client, mem, []specNode{node})
			fs = append(fs, extra...)
			if err != nil || reads != 1 {
				t.Fatalf("reads=%d findings=%+v err=%v", reads, fs, err)
			}
			for _, rule := range []string{"frame-name", "frame-tags"} {
				count := 0
				for _, f := range fs {
					if f.Rule == rule {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("%s counted %d times in %s: %+v", rule, count, mem, fs)
				}
			}
		})
	}
}
