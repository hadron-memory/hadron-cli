package spec

import (
	"context"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

type draftReadClient func(*graphql.Request, *graphql.Response) error

func (f draftReadClient) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
	return f(req, resp)
}

func TestDraftReadsFallBackAcrossServerSlices(t *testing.T) {
	var ops []string
	client := draftReadClient(func(req *graphql.Request, resp *graphql.Response) error {
		ops = append(ops, req.OpName)
		switch req.OpName {
		case "SpecCorpusState":
			resp.Data.(*gen.SpecCorpusStateResponse).Memory = &gen.SpecCorpusStateMemory{CorpusState: gen.CorpusStateDraft}
			return nil
		case "SpecPlaceholderScan":
			return gqlerror.List{{Message: `Cannot query field "isPlaceholder" on type "Node".`, Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"}}}
		case "SpecUnresolvedReferences":
			return gqlerror.List{{Message: `Cannot query field "specUnresolvedReferences" on type "Query".`, Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"}}}
		default:
			t.Fatalf("unexpected operation %s", req.OpName)
			return nil
		}
	})
	info, err := loadDraftInfo(context.Background(), client, "hrn:mem:example:specs", "")
	if err != nil || info.Draft || len(info.Placeholders) != 0 {
		t.Fatalf("pre-#1452 draft should use generic reads: info=%+v err=%v", info, err)
	}
	if len(ops) != 2 || ops[0] != "SpecCorpusState" || ops[1] != "SpecPlaceholderScan" {
		t.Fatalf("unexpected draft-read operations: %v", ops)
	}
	findings, err := unresolvedFindings(context.Background(), client, "hrn:mem:example:specs", map[string]bool{"pas:010": true})
	if err != nil || len(findings) != 0 {
		t.Fatalf("pre-#1453 lint should keep ordinary findings: findings=%v err=%v", findings, err)
	}
}

func TestDraftReadsDoNotHideOtherErrors(t *testing.T) {
	refusal := gqlerror.List{{Message: `Cannot query field "isPlaceholder" on type "Node".`, Extensions: map[string]any{"code": "FORBIDDEN"}}}
	client := draftReadClient(func(req *graphql.Request, resp *graphql.Response) error {
		if req.OpName == "SpecCorpusState" {
			resp.Data.(*gen.SpecCorpusStateResponse).Memory = &gen.SpecCorpusStateMemory{CorpusState: gen.CorpusStateDraft}
			return nil
		}
		return refusal
	})
	if _, err := loadDraftInfo(context.Background(), client, "hrn:mem:example:specs", ""); err == nil {
		t.Fatal("placeholder scan refusal must propagate")
	}
	if _, err := unresolvedFindings(context.Background(), client, "hrn:mem:example:specs", nil); err == nil {
		t.Fatal("unresolved-reference refusal must propagate")
	}
}

// A placeholder's detail carries the marker and ONE lint finding — the rules
// an empty body would trip are not run on an unwritten spec.
func TestSpecDetailOfAPlaceholder(t *testing.T) {
	n := &gen.GetNodeNode{Loc: "pas:010:04", Name: "pas:010:04", NodeType: "info"}
	d := specDetailFromNode(n, true, nil, "", draftInfo{Draft: true, Placeholders: map[string]bool{"pas:010:04": true}})
	if !d.Placeholder || len(d.Lint) != 1 || d.Lint[0].Rule != "placeholder" {
		t.Errorf("want the placeholder marker and exactly the placeholder finding, got %+v", d)
	}
	d = specDetailFromNode(n, true, nil, "", draftInfo{})
	if d.Placeholder {
		t.Errorf("outside a draft nothing is a placeholder: %+v", d)
	}
}

func TestDraftLintKeepsUnreadablePlaceholderAsError(t *testing.T) {
	loc := "pas:010:04"
	nodes, placeholders, inScope := partitionDraftLintNodes(
		[]specNode{{Loc: loc, Unavailable: true}},
		draftInfo{Draft: true, Placeholders: map[string]bool{loc: true}},
	)
	if !inScope[loc] || len(placeholders) != 0 || len(nodes) != 1 {
		t.Fatalf("unreadable placeholder was dropped: nodes=%v, placeholders=%v, inScope=%v", nodes, placeholders, inScope)
	}
	findings := lintCorpus(nodes, "")
	if len(findings) != 1 || findings[0].Rule != "unavailable" || findings[0].Severity != sevError {
		t.Fatalf("want the unavailable error, got %v", findings)
	}

	// A readable placeholder still gets its single warning instead of the
	// content rules for an unwritten spec.
	nodes, placeholders, _ = partitionDraftLintNodes(
		[]specNode{{Loc: loc}},
		draftInfo{Draft: true, Placeholders: map[string]bool{loc: true}},
	)
	if len(nodes) != 0 || len(placeholders) != 1 || placeholders[0].Rule != "placeholder" {
		t.Fatalf("readable placeholder classification changed: nodes=%v, placeholders=%v", nodes, placeholders)
	}
}

// In a DRAFT a split is available (spec renumber), so the index advice must
// not claim it is impossible; in a minted corpus it still says so.
func TestIndexAdviceIsStateAware(t *testing.T) {
	c, err := ParseCitation("msg:010")
	if err != nil {
		t.Fatal(err)
	}
	minted, draft := indexRemedy(c, false), indexRemedy(c, true)
	if !strings.Contains(minted, "never renumbered") || strings.Contains(minted, "spec renumber") {
		t.Errorf("minted advice: %q", minted)
	}
	if strings.Contains(draft, "never renumbered") || !strings.Contains(draft, "spec renumber") {
		t.Errorf("draft advice: %q", draft)
	}
	// The generic split remedy likewise drops "supersede-level" in a draft.
	rule, _ := ParseCitation("msg:010:02")
	long := abstractHardMax - 10
	if msg := nearCapMessage(long, "T", rule, true, true); !strings.Contains(msg, "nothing is superseded before minting") {
		t.Errorf("draft split advice: %q", msg)
	}
	if msg := nearCapMessage(long, "T", rule, true, false); !strings.Contains(msg, "supersede-level split") {
		t.Errorf("minted split advice: %q", msg)
	}
}
