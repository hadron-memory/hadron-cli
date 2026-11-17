package skill

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/skilldoc"
)

// unrecognizedFingerprint is a multiset of the host-free contract rows. The
// server does not promise serialization order across two calls; duplicate rows
// still count separately. A count map avoids sorting and corpus-sized strings.
type unrecognizedFingerprintRow struct {
	kind, node, rule, severity, message, urn, memory, key string
	keyCount                                              int
}

func unrecognizedFingerprint(entries []*gen.SkillLintPlanSkillPlanUnrecognized, selected map[string]bool) map[unrecognizedFingerprintRow]int {
	rows := map[unrecognizedFingerprintRow]int{}
	for _, entry := range entries {
		if !selected[entry.NodeId] {
			continue
		}
		rows[unrecognizedFingerprintRow{kind: "count", node: entry.NodeId, keyCount: entry.KeyCount}]++
		for _, key := range entry.Keys {
			rows[unrecognizedFingerprintRow{kind: "key", node: entry.NodeId, key: key}]++
		}
		for _, finding := range entry.Findings {
			rows[unrecognizedFingerprintRow{kind: "finding", node: entry.NodeId, rule: finding.Rule, severity: finding.Severity, message: finding.Message, urn: finding.Urn, memory: finding.Memory}]++
		}
	}
	return rows
}

// providerLintFindings reports the server's corpus judgment, including S11d's
// resolved-include findings; it never consults source text or the local lint.
// Plan scope is memory-wide, so filter by exact node id before accepting a
// finding for a --node selection.
//
// Order (documented, not sorted — cli#1171): rows come in first-seen order,
// walking skilldoc.Hosts in declaration order, then the provider's entries in
// the order the server returned them, then each entry's findings in server
// order. The Nth identical finding under a later host merges into the Nth row
// (Hosts grows), preserving duplicates within one host. Host-free unknown-key
// findings follow the hosted rows, node-scoped not-judged warnings come next,
// then server-unavailable rows; `skill lint` appends batch-unavailable refs
// last.
func providerLintFindings(ctx context.Context, client graphql.Client, nodes []*batchNode, selectedMemories map[string]*memoryInfo) ([]lintFindingDTO, error) {
	out := []lintFindingDTO{}
	if len(nodes) == 0 {
		return out, nil
	}
	selected := map[string]bool{}
	expectedHosts := map[string]map[string]bool{}
	unknownDeclarations := map[string][]string{}
	memorySet := map[string]bool{}
	for _, n := range nodes {
		selected[n.Id] = true
		props := toSkillNode(n, "").Properties
		for _, h := range skilldoc.Hosts {
			if skilldoc.HasDeclarationFor(props, h) {
				if expectedHosts[n.Id] == nil {
					expectedHosts[n.Id] = map[string]bool{}
				}
				expectedHosts[n.Id][h.Key] = true
			}
		}
		unknownDeclarations[n.Id] = skilldoc.UnknownHostKeys(props)
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
	// Stable node id avoids folding two nodes whose URNs coincide under
	// legacy/compound memory spellings. URN and memory remain part of finding
	// identity so contradictory hosted rows are never coalesced.
	type key struct{ node, rule, severity, message, urn, memory string }
	index := map[key][]int{}
	covered := map[string]map[string]bool{}
	unrecognizedNodes := map[string]bool{}
	unrecognizedKeys := map[string]map[string]bool{}
	unavailableSelected := map[string]bool{}
	unavailableRows := []lintFindingDTO{}
	var unrecognized []*gen.SkillLintPlanSkillPlanUnrecognized
	var unrecognizedBaseline map[unrecognizedFingerprintRow]int
	for hostIndex, h := range skilldoc.Hosts {
		host := h.Key
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
			if entry.KeyCount < len(entry.Keys) {
				return nil, exitcode.Newf(exitcode.Error, "server returned an inconsistent skill unknown-host key count")
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
			unrecognizedBaseline = unrecognizedFingerprint(unrecognized, selected)
		} else if !maps.Equal(unrecognizedBaseline, unrecognizedFingerprint(r.SkillPlan.Unrecognized, selected)) {
			return nil, exitcode.Newf(exitcode.Error, "server returned inconsistent host-free skill lint findings; no clean result can be reported")
		}
		seenInHost := map[key]int{}
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
			if entry.Class != nil && *entry.Class == "unavailable" && len(entry.Findings) > 0 {
				return nil, exitcode.Newf(exitcode.Error, "server returned findings for an unavailable skill node; no clean result can be reported")
			}
			if !selected[entry.NodeId] {
				continue
			}
			if covered[entry.NodeId] == nil {
				covered[entry.NodeId] = map[string]bool{}
			}
			covered[entry.NodeId][host] = true
			if entry.Class != nil && *entry.Class == "unavailable" && !unavailableSelected[entry.NodeId] {
				// A locked encrypted entry has no findings by design: judging its
				// ciphertext would invent a verdict. Report it once when EITHER host
				// returns it; a Codex-only declaration has no Claude entry.
				unavailableSelected[entry.NodeId] = true
				unavailableRows = append(unavailableRows, lintFindingDTO{Node: entry.Urn, Memory: "-", Rule: "skill-node-unavailable", Severity: skilldoc.SevWarning, Message: describeUnavailable(entry.Urn), Hosts: []string{}})
			}
			for _, finding := range entry.Findings {
				k := key{entry.NodeId, finding.Rule, finding.Severity, finding.Message, finding.Urn, finding.Memory}
				occurrence := seenInHost[k]
				seenInHost[k] = occurrence + 1
				if occurrence < len(index[k]) {
					// Merge the Nth identical finding from this host with the
					// Nth from earlier hosts. Equal findings within one host stay
					// distinct, so neutral repeated messages cannot undercount.
					at := index[k][occurrence]
					if !slices.Contains(out[at].Hosts, host) {
						out[at].Hosts = append(out[at].Hosts, host)
					}
					continue
				}
				index[k] = append(index[k], len(out))
				out = append(out, lintFindingDTO{Node: finding.Urn, Memory: finding.Memory, Rule: finding.Rule, Severity: finding.Severity, Message: finding.Message, Hosts: []string{host}})
			}
		}
	}
	// Unknown exports keys are host-free and identical in every host's plan.
	// Read them once, after every hosted finding, never once per host. When the
	// structured key list is untruncated, compare it per key: a newer server may
	// recognize one locally-unknown key while reporting another. A missing or
	// empty judgment produces a non-blocking not-judged warning below. Truncated
	// lists retain their own aggregate warning and are not over-interpreted.
	for _, entry := range unrecognized {
		if !selected[entry.NodeId] {
			continue
		}
		if len(entry.Findings) == 0 {
			// Listed with no findings is no judgment, not a malformed corpus.
			// The not-judged warning below handles it exactly like an omission.
			continue
		}
		unrecognizedNodes[entry.NodeId] = true
		if entry.Keys != nil && entry.KeyCount == len(entry.Keys) {
			if unrecognizedKeys[entry.NodeId] == nil {
				unrecognizedKeys[entry.NodeId] = map[string]bool{}
			}
			for _, key := range entry.Keys {
				unrecognizedKeys[entry.NodeId][key] = true
			}
		}
		for _, finding := range entry.Findings {
			out = append(out, lintFindingDTO{Node: finding.Urn, Memory: finding.Memory, Rule: finding.Rule, Severity: finding.Severity, Message: finding.Message, Hosts: []string{}})
		}
	}
	reportedNotJudged := map[string]bool{}
	for _, n := range nodes {
		ref := n.Urn
		if ref == "" {
			ref = n.Id
		}
		memory := "-"
		if m := selectedMemories[n.MemoryId]; m != nil {
			memory = m.URN
		}
		missing := []string{}
		for _, h := range skilldoc.Hosts {
			if expectedHosts[n.Id][h.Key] && !covered[n.Id][h.Key] {
				missing = append(missing, h.Key)
			}
		}
		for _, host := range missing {
			key := n.Id + "\x00" + host
			if reportedNotJudged[key] {
				continue
			}
			reportedNotJudged[key] = true
			message := fmt.Sprintf("The server returned no lint judgment for this node's %s declaration; the result is not judged rather than clean. Retry once; if it persists, report a server skillPlan defect for this node and host.", host)
			out = append(out, lintFindingDTO{
				Node: ref, Memory: memory, Rule: "skill-node-not-judged", Severity: skilldoc.SevWarning,
				Message: message,
				Hosts:   []string{host},
			})
		}
		unknownNeedsWarning := false
		if !unrecognizedNodes[n.Id] || unrecognizedKeys[n.Id] != nil {
			for _, key := range unknownDeclarations[n.Id] {
				if !unrecognizedKeys[n.Id][key] {
					unknownNeedsWarning = true
					break
				}
			}
		}
		if unknownNeedsWarning && !reportedNotJudged[n.Id+"\x00unknown"] {
			reportedNotJudged[n.Id+"\x00unknown"] = true
			out = append(out, lintFindingDTO{
				Node: ref, Memory: memory, Rule: "skill-node-not-judged", Severity: skilldoc.SevWarning,
				Message: describeNotJudged(ref),
				Hosts:   []string{},
			})
		}
	}
	return append(out, unavailableRows...), nil
}
