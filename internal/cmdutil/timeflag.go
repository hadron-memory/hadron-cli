package cmdutil

import (
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ParseTimeFlag validates a time-bound flag client-side: RFC 3339 with Z or an
// explicit offset, nothing else (a date-only or offset-less value is a usage
// error, exit 2, with nothing sent). It is the one rule shared by every
// command that takes a time bound, so the error reads the same everywhere.
// The trimmed value is returned unchanged for the wire.
func ParseTimeFlag(name, value string) (*string, error) {
	v := strings.TrimSpace(value)
	if _, err := time.Parse(time.RFC3339, v); err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "--%s takes an ISO date-time with Z or an explicit offset, e.g. 2026-10-22T00:00:00Z, got %q", name, value)
	}
	return &v, nil
}
