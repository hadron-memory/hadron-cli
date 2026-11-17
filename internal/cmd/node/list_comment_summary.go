package node

import (
	"fmt"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

type listCommentSummaryDTO struct {
	OpenThreads        int     `json:"openThreads"`
	OldestOpenThreadAt *string `json:"oldestOpenThreadAt"`
}

// Validate every selected row before any output or client-side seq filtering.
// A missing summary is an invalid answer, never an invented zero.
func listCommentSummary(n *api.ListNode, now time.Time) (*listCommentSummaryDTO, string, error) {
	malformed := func() (*listCommentSummaryDTO, string, error) {
		return nil, "", exitcode.Newf(exitcode.Error, "server returned malformed commentSummary for a node listing")
	}
	c := n.CommentSummary
	if c == nil || c.OpenThreads == nil || *c.OpenThreads < 0 {
		return malformed()
	}
	count := *c.OpenThreads
	if count == 0 {
		if c.OldestOpenThreadAt != nil {
			return malformed()
		}
		return &listCommentSummaryDTO{OpenThreads: 0}, "-", nil
	}
	if c.OldestOpenThreadAt == nil {
		return malformed()
	}
	oldest, valid := cmdutil.ParseStrictDateTime(*c.OldestOpenThreadAt)
	if !valid {
		return malformed()
	}
	return &listCommentSummaryDTO{OpenThreads: count, OldestOpenThreadAt: c.OldestOpenThreadAt}, oldestThreadAge(now, oldest), nil
}

func oldestThreadAge(now, oldest time.Time) string {
	// Floor seconds without time.Duration overflow for very old timestamps.
	seconds := now.Unix() - oldest.Unix()
	if now.Nanosecond() < oldest.Nanosecond() {
		seconds--
	}
	switch {
	case seconds < 60:
		return "<1m"
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh", seconds/3600)
	default:
		return fmt.Sprintf("%dd", seconds/86400)
	}
}
