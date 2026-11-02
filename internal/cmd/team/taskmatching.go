package team

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Only syntax is checked here. Role existence, case matching, limits and the
// eligible team pool remain server decisions.
func taskRoleFlags(values []string, supplied bool) ([]string, error) {
	var roles []string
	for _, v := range values {
		for _, r := range strings.Split(v, ",") {
			r = strings.TrimSpace(r)
			if r == "" {
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
