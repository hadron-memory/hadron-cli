package spec

import (
	"context"
	"fmt"
	"strings"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// Server content lint (hadron-cli#3127, server half hadron-server#5109).
//
// Two checks about what a spec must SAY — a Rule spec owes a Why section, and a
// sentence that limits something owes a reason — are business rules, so they
// live on the server (cor:api:240:01) and every client gets the same findings.
// The CLI carries no copy of them: it asks Node.specLint, relabels each finding
// into its own report and lets the existing severity / --strict / exit-code
// logic decide. The CLI's local checks (abstract length, wiring, links) stay
// local, so one report mixes the two sources.
//
// An older server has no such field. That is not "no findings": the report says
// the two checks did not run, rather than letting a clean result imply they did.
const (
	ruleServerLintUnavailable = "spec-lint-unavailable"
	ruleServerLintUnread      = "spec-lint-not-read"
)

// serverLintFetch reads one chunk of Node.specLint. Injected so the paging and
// the older-server handling are unit-testable without a server.
type serverLintFetch func(refs []string) (*gen.SpecLintBatchNodeBatchNodeBatchResult, error)

// serverLintFindings lints the given nodes through the server. Unreadable nodes
// are skipped here (lintNode already reports them as "unavailable"). The
// returned findings never include a spec the server could not lint without
// saying so: a null specLint, or a server without the field, is reported once.
func serverLintFindings(ctx context.Context, client graphql.Client, nodes []specNode) ([]lintFindingDTO, error) {
	return collectServerLint(nodes, func(refs []string) (*gen.SpecLintBatchNodeBatchNodeBatchResult, error) {
		resp, err := gen.SpecLintBatch(ctx, client, refs)
		if err != nil {
			return nil, err
		}
		if resp == nil {
			return nil, nil
		}
		return resp.NodeBatch, nil
	})
}

func collectServerLint(nodes []specNode, fetch serverLintFetch) ([]lintFindingDTO, error) {
	findings := []lintFindingDTO{}
	queue := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.Unavailable || n.ID == "" {
			continue
		}
		queue = append(queue, n.ID)
	}
	var unread []string
	for len(queue) > 0 {
		n := api.NodeBatchCap
		if n > len(queue) {
			n = len(queue)
		}
		chunk := queue[:n]
		queue = queue[n:]

		res, err := fetch(chunk)
		if err != nil {
			if api.IsFrameworkValidationOnlyFor(err, "specLint") {
				return []lintFindingDTO{{
					Rule:     ruleServerLintUnavailable,
					Severity: sevInfo,
					Message:  "this server does not offer Node.specLint, so the Rule-needs-a-Why and limiting-sentence checks did NOT run; the other checks ran",
				}}, nil
			}
			return nil, api.MapError(err)
		}
		if res == nil {
			return nil, fmt.Errorf("nodeBatch returned no result for %d ref(s)", len(chunk))
		}
		for _, sn := range res.Nodes {
			if sn == nil {
				continue
			}
			if sn.SpecLint == nil {
				unread = append(unread, sn.Loc)
				continue
			}
			for _, f := range sn.SpecLint {
				if f == nil {
					continue
				}
				findings = append(findings, serverLintFinding(sn.Loc, f))
			}
		}
		if res.Truncated {
			if len(res.Nodes) == 0 {
				return nil, fmt.Errorf("nodeBatch truncated without returning any node (%d omitted)", len(res.Omitted))
			}
			queue = append(queue, res.Omitted...)
		}
	}
	if len(unread) > 0 {
		findings = append(findings, lintFindingDTO{
			Rule:     ruleServerLintUnread,
			Severity: sevInfo,
			Message:  fmt.Sprintf("the server did not load the text of %d spec(s), so the content checks did NOT run on them: %s", len(unread), strings.Join(unread, ", ")),
		})
	}
	return findings, nil
}

func serverLintFinding(loc string, f *gen.SpecLintBatchNodeBatchNodeBatchResultNodesNodeSpecLintSpecLintFinding) lintFindingDTO {
	citation := f.Citation
	if citation == "" {
		citation = loc
	}
	out := lintFindingDTO{
		Citation: citation,
		Rule:     serverLintRule(f.Rule),
		Severity: serverLintSeverity(f.Severity),
		Message:  f.Message,
		Line:     f.Line,
	}
	if f.Sentence != nil {
		out.Sentence = *f.Sentence
	}
	return out
}

// serverLintRule names a server rule the way the CLI's own rules are named:
// lowercase, hyphenated. The server's SpecLintRule value is the stable source.
func serverLintRule(r gen.SpecLintRule) string {
	return strings.ToLower(strings.ReplaceAll(string(r), "_", "-"))
}

// serverLintSeverity maps the server's two severities. Only ERROR fails a run:
// the sentence check is a WARNING by construction (4,444 of them over 603 of
// 704 live specs at its first scan), and --strict is the caller's way to change
// that. An unknown future severity is reported as a warning, never an error.
func serverLintSeverity(s gen.SpecLintSeverity) string {
	if s == gen.SpecLintSeverityError {
		return sevError
	}
	return sevWarning
}
