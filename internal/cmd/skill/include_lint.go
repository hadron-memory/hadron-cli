package skill

import (
	"context"
	"sort"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/skilldoc"
)

// providerIncludeFindings reads S11d's resolved-include judgment, not source
// text or a notice-string heuristic. Existing local lint/selection/collision
// rules remain untouched. Plan scope is memory-wide, so filter by exact node
// id before accepting a finding for a --node selection.
func providerIncludeFindings(ctx context.Context, client graphql.Client, nodes []*batchNode) ([]lintFindingDTO, error) {
	out := []lintFindingDTO{}
	if len(nodes) == 0 {
		return out, nil
	}
	selected := map[string]bool{}
	memorySet := map[string]bool{}
	for _, n := range nodes {
		selected[n.Id] = true
		if n.MemoryId == "" {
			return nil, exitcode.Newf(exitcode.Error, "cannot check includes: selected node has no memory id")
		}
		memorySet[n.MemoryId] = true
	}
	memories := make([]string, 0, len(memorySet))
	for id := range memorySet {
		memories = append(memories, id)
	}
	sort.Strings(memories)
	// Unlike findingSet's legacy display key, stable node id avoids folding
	// two nodes whose URNs coincide under legacy/compound memory spellings.
	type key struct{ node, rule, severity, message string }
	index := map[key]int{}
	for _, h := range skilldoc.Hosts {
		host := h.Key
		r, err := gen.SkillPlan(ctx, client, &gen.SkillPlanInput{Intent: gen.SkillPlanIntentLint, Memories: memories, Host: &host})
		if err != nil {
			return nil, api.MapError(err)
		}
		if r == nil || r.SkillPlan == nil || r.SkillPlan.Entries == nil {
			return nil, exitcode.Newf(exitcode.Error, "server returned no skill include-lint plan; no clean result can be reported")
		}
		for _, entry := range r.SkillPlan.Entries {
			if entry == nil {
				return nil, exitcode.Newf(exitcode.Error, "server returned a null skill include-lint entry")
			}
			if !selected[entry.NodeId] {
				continue
			}
			if entry.Findings == nil {
				return nil, exitcode.Newf(exitcode.Error, "server returned no skill include-lint findings")
			}
			for _, finding := range entry.Findings {
				if finding == nil {
					return nil, exitcode.Newf(exitcode.Error, "server returned a null skill include-lint finding")
				}
				if finding.Rule != "skill-include-broken" {
					continue
				}
				k := key{entry.NodeId, finding.Rule, finding.Severity, finding.Message}
				if at, ok := index[k]; ok {
					out[at].Hosts = append(out[at].Hosts, host)
					continue
				}
				index[k] = len(out)
				out = append(out, lintFindingDTO{Node: finding.Urn, Memory: finding.Memory, Rule: finding.Rule, Severity: finding.Severity, Message: finding.Message, Hosts: []string{host}})
			}
		}
	}
	return out, nil
}
