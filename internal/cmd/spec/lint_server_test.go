package spec

import (
	"fmt"
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
	nodes = append(nodes, specNode{ID: "gone", Loc: "x:gone", Unavailable: true}, specNode{Loc: "x:noid"})
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
