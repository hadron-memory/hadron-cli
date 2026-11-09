package skill

import (
	"context"
	"reflect"
	"slices"
	"sort"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/skilldoc"
)

// providerLintFindings reports the server's corpus judgment, including S11d's
// resolved-include findings; it never consults source text or the local lint.
// Plan scope is memory-wide, so filter by exact node id before accepting a
// finding for a --node selection.
//
// Order (documented, not sorted — cli#1171): rows come in first-seen order,
// walking skilldoc.Hosts in declaration order, then the provider's entries in
// the order the server returned them, then each entry's findings in server
// order. A finding repeated under a later host merges into its first row
// (Hosts grows) rather than adding one. Host-free unknown-key findings follow
// the hosted rows, then `skill lint` appends unavailable-node rows.
func providerLintFindings(ctx context.Context, client graphql.Client, nodes []*batchNode) ([]lintFindingDTO, error) {
	out := []lintFindingDTO{}
	if len(nodes) == 0 {
		return out, nil
	}
	selected := map[string]bool{}
	properties := map[string]map[string]any{}
	memorySet := map[string]bool{}
	expectedUnrecognized := map[string]bool{}
	for _, n := range nodes {
		selected[n.Id] = true
		properties[n.Id] = toSkillNode(n, "").Properties
		if len(skilldoc.UnknownHostKeys(properties[n.Id])) > 0 {
			expectedUnrecognized[n.Id] = true
		}
		if n.MemoryId == "" {
			return nil, exitcode.Newf(exitcode.Error, "cannot lint skills: selected node has no memory id")
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
	unavailableSelected := map[string]bool{}
	unrecognizedSelected := map[string]bool{}
	var unrecognized []*gen.SkillLintPlanSkillPlanUnrecognized
	for hostIndex, h := range skilldoc.Hosts {
		host := h.Key
		expected := map[string]bool{}
		for _, n := range nodes {
			_, declared := skilldoc.DeclaredFor(properties[n.Id], h)
			if declared || len(skilldoc.MalformedFor(properties[n.Id], h)) > 0 {
				expected[n.Id] = true
			}
		}
		judgedSelected := map[string]bool{}
		r, err := gen.SkillLintPlan(ctx, client, &gen.SkillPlanInput{Intent: gen.SkillPlanIntentLint, Memories: memories, Host: &host})
		if err != nil {
			return nil, api.MapError(err)
		}
		if r == nil || r.SkillPlan == nil || r.SkillPlan.Entries == nil || r.SkillPlan.Unrecognized == nil {
			return nil, exitcode.Newf(exitcode.Error, "server returned an incomplete skill lint plan; no clean result can be reported")
		}
		if r.SkillPlan.Judged != len(r.SkillPlan.Entries) {
			return nil, exitcode.Newf(exitcode.Error, "server returned an inconsistent skill lint plan; no clean result can be reported")
		}
		for _, entry := range r.SkillPlan.Unrecognized {
			if entry == nil {
				return nil, exitcode.Newf(exitcode.Error, "server returned a null skill unknown-host entry")
			}
			if entry.Findings == nil {
				return nil, exitcode.Newf(exitcode.Error, "server returned no skill unknown-host findings")
			}
			for _, finding := range entry.Findings {
				if finding == nil {
					return nil, exitcode.Newf(exitcode.Error, "server returned a null skill unknown-host finding")
				}
			}
		}
		if hostIndex == 0 {
			// Host-free rows are identical in every host's plan. Retain only
			// those rows, not the whole first response, while fetching the next.
			unrecognized = r.SkillPlan.Unrecognized
		} else if !reflect.DeepEqual(unrecognized, r.SkillPlan.Unrecognized) {
			return nil, exitcode.Newf(exitcode.Error, "server returned inconsistent host-free skill lint findings; no clean result can be reported")
		}
		for _, entry := range r.SkillPlan.Entries {
			if entry == nil {
				return nil, exitcode.Newf(exitcode.Error, "server returned a null skill lint entry")
			}
			if entry.Findings == nil {
				return nil, exitcode.Newf(exitcode.Error, "server returned no skill lint findings")
			}
			for _, finding := range entry.Findings {
				if finding == nil {
					return nil, exitcode.Newf(exitcode.Error, "server returned a null skill lint finding")
				}
			}
			if !selected[entry.NodeId] {
				continue
			}
			judgedSelected[entry.NodeId] = true
			if entry.Class != nil && *entry.Class == "unavailable" && !unavailableSelected[entry.NodeId] {
				// A locked encrypted entry has no findings by design: judging its
				// ciphertext would invent a verdict. Report it once when EITHER host
				// returns it; a Codex-only declaration has no Claude entry.
				unavailableSelected[entry.NodeId] = true
				out = append(out, lintFindingDTO{Node: entry.Urn, Memory: "-", Rule: "skill-node-unavailable", Severity: skilldoc.SevWarning, Message: describeUnavailable(entry.Urn), Hosts: []string{}})
			}
			for _, finding := range entry.Findings {
				k := key{entry.NodeId, finding.Rule, finding.Severity, finding.Message}
				if at, ok := index[k]; ok {
					// Repeated identical findings within one host are one row;
					// merging hosts must not print that host twice.
					if !slices.Contains(out[at].Hosts, host) {
						out[at].Hosts = append(out[at].Hosts, host)
					}
					continue
				}
				index[k] = len(out)
				out = append(out, lintFindingDTO{Node: finding.Urn, Memory: finding.Memory, Rule: finding.Rule, Severity: finding.Severity, Message: finding.Message, Hosts: []string{host}})
			}
		}
		for id := range expected {
			if !judgedSelected[id] {
				return nil, exitcode.Newf(exitcode.Error, "server omitted a selected declaration from its skill lint plan; no clean result can be reported")
			}
		}
	}
	// Unknown exports keys are host-free and identical in every host's plan.
	// Read them once, after every hosted finding, never once per host.
	for _, entry := range unrecognized {
		if !selected[entry.NodeId] {
			continue
		}
		unrecognizedSelected[entry.NodeId] = true
		if len(entry.Findings) == 0 {
			return nil, exitcode.Newf(exitcode.Error, "server returned no findings for a selected unknown-host declaration; no clean result can be reported")
		}
		for _, finding := range entry.Findings {
			out = append(out, lintFindingDTO{Node: finding.Urn, Memory: finding.Memory, Rule: finding.Rule, Severity: finding.Severity, Message: finding.Message, Hosts: []string{}})
		}
	}
	for id := range expectedUnrecognized {
		if !unrecognizedSelected[id] {
			return nil, exitcode.Newf(exitcode.Error, "server omitted a selected unknown-host declaration from its skill lint plan; no clean result can be reported")
		}
	}
	return out, nil
}
