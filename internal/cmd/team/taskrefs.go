package team

// Typed task references (#1520, server#2649, cor:agt:050:01 and :09). The CLI
// checks only the `kind=target[|label]` shape and that the kind is known; the
// form and caps of a target stay with the server, which refuses with
// BAD_USER_INPUT naming the item's position. A target and a label are carried
// as given and never resolved, fetched or interpreted.

import (
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const taskRefKindsDoc = "issue, pull-request, node, task or url"

// taskRefKind maps the flag spelling to the enum; pull_request is accepted
// beside pull-request.
func taskRefKind(flag, s string) (gen.AgentTaskReferenceKind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "issue":
		return gen.AgentTaskReferenceKindIssue, nil
	case "pull-request", "pull_request":
		return gen.AgentTaskReferenceKindPullRequest, nil
	case "node":
		return gen.AgentTaskReferenceKindNode, nil
	case "task":
		return gen.AgentTaskReferenceKindTask, nil
	case "url":
		return gen.AgentTaskReferenceKindUrl, nil
	}
	return "", exitcode.Newf(exitcode.Usage, "%s: unknown kind %q: use one of %s", flag, s, taskRefKindsDoc)
}

// parseTaskRefFlags turns the repeatable --ref values into the input list, in
// order. The label starts at the first `|`.
func parseTaskRefFlags(values []string) ([]*gen.AgentTaskReferenceInput, error) {
	out := make([]*gen.AgentTaskReferenceInput, 0, len(values))
	for _, v := range values {
		kindText, rest, ok := strings.Cut(v, "=")
		if !ok {
			return nil, exitcode.Newf(exitcode.Usage, "--ref takes <kind>=<target>[|<label>] with kind one of %s, got %q", taskRefKindsDoc, v)
		}
		kind, err := taskRefKind("--ref", kindText)
		if err != nil {
			return nil, err
		}
		target, label, hasLabel := strings.Cut(rest, "|")
		if target == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--ref %q has no target: <kind>=<target>[|<label>] with kind one of %s", v, taskRefKindsDoc)
		}
		ref := &gen.AgentTaskReferenceInput{Kind: kind, Target: target}
		if hasLabel {
			if label == "" {
				return nil, exitcode.Newf(exitcode.Usage, "--ref %q has an empty label after |: drop the | or name the label", v)
			}
			ref.Label = &label
		}
		out = append(out, ref)
	}
	return out, nil
}

// taskReferenceDTO is one reference in --json: label is null when absent.
type taskReferenceDTO struct {
	Kind   string  `json:"kind"`
	Target string  `json:"target"`
	Label  *string `json:"label"`
}

func taskReferencesDTOFrom(in []*gen.AgentTaskFieldsReferencesAgentTaskReference) []taskReferenceDTO {
	out := []taskReferenceDTO{}
	for _, r := range in {
		if r == nil {
			continue
		}
		out = append(out, taskReferenceDTO{Kind: string(r.Kind), Target: r.Target, Label: r.Label})
	}
	return out
}
