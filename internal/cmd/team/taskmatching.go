package team

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Only syntax is checked here. Role existence, case matching, limits and the
// eligible team pool remain server decisions.
func taskRoleFlags(values []string, supplied bool) ([]string, error) {
	var roles []string
	for _, v := range values {
		for _, r := range strings.Split(v, ",") {
			if strings.TrimSpace(r) == "" {
				return nil, exitcode.Newf(exitcode.Usage, "--require-role needs a non-empty role in every comma-separated/repeated item")
			}
			roles = append(roles, r)
		}
	}
	if supplied && len(roles) == 0 {
		return nil, exitcode.Newf(exitcode.Usage, "--require-role needs a role")
	}
	return roles, nil
}

func writeTaskMatchFailure(w io.Writer, failure *taskMatchFailureDTO) error {
	_, err := fmt.Fprintf(w, "  match failed: %s at %s — task created unassigned; automatic matching skips it\n", output.SafeCell(failure.Reason), output.SafeCell(failure.FailedAt))
	return err
}

// matchSkippedDTO projects the create answer's matchSkipped (cli#1848). The
// caller sets it only on a --match answer, where the contract is an array:
// a server null (a match that assigned) becomes [], never null or absent.
func matchSkippedDTO(skipped []*gen.AgentTaskCreateCreateAgentTaskMatchSkippedAgentTaskSkippedCandidate) *[]taskSkippedCandidateDTO {
	out := []taskSkippedCandidateDTO{}
	for _, c := range skipped {
		if c == nil {
			continue
		}
		reasons := make([]string, 0, len(c.Reasons))
		for _, r := range c.Reasons {
			reasons = append(reasons, string(r))
		}
		out = append(out, taskSkippedCandidateDTO{WorkerID: c.WorkerId, Name: c.Name, Reasons: reasons})
	}
	return &out
}

// writeTaskMatchSkipped renders the skipped candidates under the match-failed
// line: one line per candidate, its reasons in the server's order, each value
// printed as itself (an unknown future reason included).
func writeTaskMatchSkipped(w io.Writer, skipped []taskSkippedCandidateDTO) error {
	if len(skipped) == 0 {
		_, err := fmt.Fprintln(w, "  skipped: no candidates")
		return err
	}
	for _, c := range skipped {
		reasons := make([]string, len(c.Reasons))
		for i, r := range c.Reasons {
			reasons[i] = output.SafeCell(r)
		}
		if _, err := fmt.Fprintf(w, "  skipped: %s (%s): %s\n", output.SafeCell(c.Name), output.SafeCell(c.WorkerID), strings.Join(reasons, ", ")); err != nil {
			return err
		}
	}
	return nil
}
