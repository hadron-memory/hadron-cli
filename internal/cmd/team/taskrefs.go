package team

// Typed task references (#1520, server#2649, cor:agt:050:01 and :09). The CLI
// checks the `kind=target[|label]` shape, that the kind is known, and (cli#2970)
// that a label is within the server's cap, as a convenience before the request;
// the server stays the authority: the form and the other caps of a target stay
// with it, and it refuses with BAD_USER_INPUT naming the item's position. A
// target and a label are carried as given and never resolved, fetched or
// interpreted.

import (
	"strings"
	"unicode/utf16"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const taskRefKindsDoc = "issue, pull-request, node, task or url"

// The reference caps are the server's (cor:agt:050:01): a label is at most 120 UTF-16 code units
// (the server counts label.length), a target at most 512, and a task takes at most 20 references. They
// are stated once here and in every --ref help; only the label is checked before the request.
const (
	taskRefLabelMax = 120

	// taskRefCapsSentence is the one statement of the caps every --ref help carries, wrapped to 78
	// columns: put it on its own lines in a help paragraph.
	taskRefCapsSentence = "A label is at most 120 characters, a target at most 512, and a task takes at\nmost 20 references; the CLI refuses a longer label before sending, and the\nserver judges the rest."

	// taskRefCapsFlagDoc is the short form for a --ref flag usage line.
	taskRefCapsFlagDoc = "; a label is at most 120 characters, a target 512, a task 20 references"
)

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
	for i, v := range values {
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
			// Count UTF-16 code units like the server's label.length, after trimming: this check
			// can only be more permissive than the server, never refuse what it accepts. Same
			// wording and exit as the server's refusal (cli#2970).
			if n := len(utf16.Encode([]rune(strings.TrimSpace(label)))); n > taskRefLabelMax {
				return nil, exitcode.Newf(exitcode.Usage, "Reference %d: a label is at most %d characters.", i+1, taskRefLabelMax)
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
