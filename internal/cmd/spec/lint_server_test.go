package spec

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

type serverLintBatchNode = gen.SpecLintBatchNodeBatchNodeBatchResultNodesNode

func TestCollectServerLintPagesAndRequeuesTruncation(t *testing.T) {
	var nodes []specNode
	for i := 0; i < api.NodeBatchCap+5; i++ {
		nodes = append(nodes, specNode{ID: fmt.Sprintf("id%d", i), Loc: fmt.Sprintf("x:%d", i)})
	}
	nodes = append(nodes, specNode{ID: "gone", Loc: "x:gone", Unavailable: true})
	var sizes []int
	first := true
	got, err := collectServerLint(nodes, func(refs []string) (*gen.SpecLintBatchNodeBatchNodeBatchResult, error) {
		sizes = append(sizes, len(refs))
		res := &gen.SpecLintBatchNodeBatchNodeBatchResult{}
		serve := refs
		if first && len(refs) > 3 {
			// byte-cap spillover: serve three, omit the rest
			first = false
			serve, res.Truncated, res.Omitted = refs[:3], true, refs[3:]
		}
		for _, id := range serve {
			res.Nodes = append(res.Nodes, &serverLintBatchNode{Id: id, Loc: "loc-" + id, SpecLint: []*gen.SpecLintBatchNodeBatchNodeBatchResultNodesNodeSpecLintSpecLintFinding{}})
		}
		return res, nil
	})
	if err != nil || len(got) != 0 {
		t.Fatalf("err=%v findings=%v", err, got)
	}
	total := 0
	for _, s := range sizes {
		total += s
	}
	if total != api.NodeBatchCap+5+(api.NodeBatchCap-3) { // every omitted ref asked again once
		t.Errorf("omitted refs must be re-queued exactly once; asked %v (sum %d)", sizes, total)
	}
	if sizes[0] != api.NodeBatchCap {
		t.Errorf("chunks must respect the cap, got %v", sizes)
	}
}

func TestServerLintRuleAndSeverityMapping(t *testing.T) {
	for in, want := range map[gen.SpecLintRule]string{
		gen.SpecLintRuleRuleWhyMissing:     "rule-why-missing",
		gen.SpecLintRuleRuleWhyEmpty:       "rule-why-empty",
		gen.SpecLintRuleLimitWithoutReason: "limit-without-reason",
	} {
		if got := serverLintRule(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
	if serverLintSeverity(gen.SpecLintSeverityError) != sevError || serverLintSeverity(gen.SpecLintSeverityWarning) != sevWarning || serverLintSeverity("SOMETHING_NEW") != sevWarning {
		t.Error("only ERROR is an error; anything else is a warning")
	}
}

func okLintNode(id string, fs ...*gen.SpecLintBatchNodeBatchNodeBatchResultNodesNodeSpecLintSpecLintFinding) *serverLintBatchNode {
	if fs == nil {
		fs = []*gen.SpecLintBatchNodeBatchNodeBatchResultNodesNodeSpecLintSpecLintFinding{}
	}
	return &serverLintBatchNode{Id: id, Loc: "loc-" + id, SpecLint: fs}
}

func TestCollectServerLintAggregatesAcrossNodes(t *testing.T) {
	nodes := []specNode{{ID: "a", Loc: "loc-a"}, {ID: "b", Loc: "loc-b"}}
	got, err := collectServerLint(nodes, func([]string) (*gen.SpecLintBatchNodeBatchNodeBatchResult, error) {
		return &gen.SpecLintBatchNodeBatchNodeBatchResult{Nodes: []*serverLintBatchNode{
			okLintNode("a", &gen.SpecLintBatchNodeBatchNodeBatchResultNodesNodeSpecLintSpecLintFinding{Citation: "loc-a", Rule: gen.SpecLintRuleRuleWhyMissing, Severity: gen.SpecLintSeverityError, Message: "m"}),
			okLintNode("b", &gen.SpecLintBatchNodeBatchNodeBatchResultNodesNodeSpecLintSpecLintFinding{Rule: gen.SpecLintRuleLimitWithoutReason, Severity: gen.SpecLintSeverityWarning, Message: "m"}),
		}}, nil
	})
	if err != nil || len(got) != 2 {
		t.Fatalf("err=%v got=%v", err, got)
	}
	if got[0].Citation != "loc-a" || got[1].Citation != "loc-b" {
		t.Errorf("an empty server citation falls back to the node's loc: %v", got)
	}
}

func TestCollectServerLintNamesUnavailableAndEmptyIDNodes(t *testing.T) {
	nodes := []specNode{{ID: "a", Loc: "loc-a"}, {ID: "gone", Loc: "loc-gone"}, {Loc: "loc-noid"}, {ID: "x", Loc: "loc-unavail", Unavailable: true}}
	got, err := collectServerLint(nodes, func(refs []string) (*gen.SpecLintBatchNodeBatchNodeBatchResult, error) {
		return &gen.SpecLintBatchNodeBatchNodeBatchResult{Nodes: []*serverLintBatchNode{okLintNode("a")}, Unavailable: []string{"gone"}}, nil
	})
	if err != nil || len(got) != 1 || got[0].Rule != ruleServerLintUnread {
		t.Fatalf("err=%v got=%v", err, got)
	}
	m := got[0].Message
	if !strings.Contains(m, "loc-gone") || !strings.Contains(m, "loc-noid") || strings.Contains(m, "loc-unavail") {
		t.Errorf("must name the vanished and the id-less spec, not the one lintNode already reports: %s", m)
	}
}

func TestCollectServerLintCapsTheNotReadList(t *testing.T) {
	var nodes []specNode
	for i := 0; i < unreadShown+5; i++ {
		nodes = append(nodes, specNode{Loc: fmt.Sprintf("loc-%02d", i)})
	}
	got, _ := collectServerLint(nodes, func([]string) (*gen.SpecLintBatchNodeBatchNodeBatchResult, error) { return nil, nil })
	if len(got) != 1 || !strings.Contains(got[0].Message, "and 5 more") || strings.Contains(got[0].Message, "loc-14") {
		t.Errorf("the list must be capped: %v", got)
	}
}

func TestCollectServerLintTruncatedWithoutNodesIsAnError(t *testing.T) {
	_, err := collectServerLint([]specNode{{ID: "a", Loc: "x"}}, func([]string) (*gen.SpecLintBatchNodeBatchNodeBatchResult, error) {
		return &gen.SpecLintBatchNodeBatchNodeBatchResult{Truncated: true, Omitted: []string{"a"}}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "first: a") {
		t.Errorf("a truncation that serves nothing must fail and name refs: %v", err)
	}
}
