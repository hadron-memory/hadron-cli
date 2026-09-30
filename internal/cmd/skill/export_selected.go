package skill

import (
	"context"
	"errors"
	"strings"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	urnlib "github.com/hadron-memory/urn-lib-go"
)

type selectedPlan = gen.SelectedSkillFilePlanSelectedSkillFilePlanSkillPlan
type selectedResult = gen.SelectedSkillFilePlanSelectedSkillFilePlanSkillPlanSelectionResultsSkillSelectionResult
type legacyEntry = gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry
type legacyEntryPlan = gen.SkillExportPlanSkillPlanEntriesSkillPlanEntryExportPlanSkillExportPlanEntry
type legacyEntryReason = gen.SkillExportPlanSkillPlanEntriesSkillPlanEntryExportPlanSkillExportPlanEntryReasonsSkillExportReason
type legacyEntryFinding = gen.SkillExportPlanSkillPlanEntriesSkillPlanEntryFindingsSkillFinding

// Validate the entire selector before resolving a client or touching HOME.
// The server receives node ids or canonical fully qualified node URNs only.
func exportNodeRefs(refs []string) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--node is empty — pass a node id or fully qualified node URN")
		}
		canon, err := canonicalNodeArg(ref)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "--node %q is not a node id or fully qualified node URN", ref)
		}
		if urnlib.HasSchemePrefix(canon) {
			if _, err := nodeIdentityKey(canon); err != nil {
				return nil, exitcode.Newf(exitcode.Usage, "--node %q must name a task node, without a fragment", ref)
			}
		}
		if !seen[canon] {
			seen[canon] = true
			out = append(out, canon)
		}
	}
	return out, nil
}

// Adapt only the fields the existing disk writer reads. The selected query is
// distinct on the wire; conversion happens after validating its scope marker.
func selectedExportPlan(ctx context.Context, client graphql.Client, nodes []string, host string, files []*gen.SkillFileFactsInput, force *bool) (*gen.SkillExportPlanSkillPlan, []*selectedResult, error) {
	ask := func(files []*gen.SkillFileFactsInput) (*gen.SelectedSkillFilePlanResponse, error) {
		return gen.SelectedSkillFilePlan(ctx, client, &gen.SelectedSkillFilePlanInput{
			Nodes: nodes, Host: &host, Files: files, Force: force,
		})
	}
	resp, err := ask(files)
	if err != nil && hasRevisions(files) && isUnknownRevisionInput(err) {
		resp, err = ask(withoutRevisions(files))
	}
	if err != nil {
		return nil, nil, err
	}
	if resp.SelectedSkillFilePlan == nil {
		return nil, nil, errors.New("selectedSkillFilePlan returned no result")
	}
	p := resp.SelectedSkillFilePlan
	if !p.OrphanAssessmentSkipped || len(p.Orphans) != 0 {
		return nil, nil, errors.New("selectedSkillFilePlan returned an unsafe orphan assessment; nothing was written for this host")
	}
	selectedIDs := make(map[string]bool, len(nodes))
	selectedURNs := make(map[string]bool, len(nodes))
	for _, ref := range nodes {
		if urnlib.HasSchemePrefix(ref) {
			key, err := nodeIdentityKey(ref)
			if err != nil {
				return nil, nil, errors.New("selectedSkillFilePlan received an invalid node selector")
			}
			selectedURNs[key] = true
		} else {
			selectedIDs[ref] = true
		}
	}
	for _, e := range p.Entries {
		if e == nil || selectedIDs[e.NodeId] {
			continue
		}
		if !selectedURNs[e.Urn] {
			return nil, nil, errors.New("selectedSkillFilePlan returned an unselected entry; nothing was written for this host")
		}
	}
	return adaptSelectedPlan(p), p.SelectionResults, nil
}

func nodeIdentityKey(ref string) (string, error) {
	if strings.Contains(ref, "#") {
		return "", errors.New("a task node ref cannot carry a fragment")
	}
	parts, err := urnlib.SplitNodeUrn(ref)
	if err != nil {
		return "", errors.New("invalid task node URN")
	}
	// The server emits flat v2 URNs from the stored memory address. A v1
	// selector, including a compound app-memory address, must match that
	// emitted form without guessing where its memory/loc boundary sits.
	mem := strings.TrimPrefix(strings.ReplaceAll(parts.MemoryURN, "::", ":"), "@")
	return "hrn:node:" + mem + ":" + parts.Loc, nil
}

func adaptSelectedPlan(p *selectedPlan) *gen.SkillExportPlanSkillPlan {
	out := &gen.SkillExportPlanSkillPlan{Scanned: p.Scanned, Judged: p.Judged}
	for _, e := range p.Entries {
		if e == nil {
			continue
		}
		item := &legacyEntry{
			Urn: e.Urn, NodeId: e.NodeId, Name: e.Name, Class: e.Class,
			ParseFailure: e.ParseFailure, OutOfExportReason: e.OutOfExportReason,
			MovedFrom: e.MovedFrom, RenderedBody: e.RenderedBody,
		}
		if e.ExportPlan != nil {
			item.ExportPlan = &legacyEntryPlan{
				Action:                e.ExportPlan.Action,
				PreservesExistingFile: e.ExportPlan.PreservesExistingFile,
			}
			for _, r := range e.ExportPlan.Reasons {
				if r != nil {
					item.ExportPlan.Reasons = append(item.ExportPlan.Reasons, &legacyEntryReason{Code: r.Code, Message: r.Message})
				}
			}
		}
		for _, f := range e.Findings {
			if f != nil {
				item.Findings = append(item.Findings, &legacyEntryFinding{
					Rule: f.Rule, Severity: f.Severity, Message: f.Message, Urn: f.Urn, Memory: f.Memory,
				})
			}
		}
		out.Entries = append(out.Entries, item)
	}
	return out
}
