package cmdutil

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// isoDateTime is the accepted shape: two-digit fields, a dot fraction, and Z
// or a colon offset. time.Parse alone also takes +24:00, +00:60 and a comma
// fraction, which the server then refuses.
var isoDateTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-](\d{2}):(\d{2}))$`)

// ParseStrictDateTime parses an RFC 3339 instant in the strict shape above,
// with a numeric offset of at most +-23:59.
func ParseStrictDateTime(v string) (time.Time, bool) {
	m := isoDateTime.FindStringSubmatch(v)
	if m == nil {
		return time.Time{}, false
	}
	if m[1] != "" {
		h, _ := strconv.Atoi(m[1])
		mm, _ := strconv.Atoi(m[2])
		if h > 23 || mm > 59 {
			return time.Time{}, false
		}
	}
	t, err := time.Parse(time.RFC3339, v)
	return t, err == nil
}

// ParseTimeFlag validates a time-bound flag client-side: a strict ISO
// date-time with Z or an explicit offset, nothing else (a date-only or
// offset-less value is a usage error, exit 2, with nothing sent). It is the
// one rule shared by every command that takes a time bound, so the error
// reads the same everywhere. The trimmed value is returned unchanged for the
// wire.
func ParseTimeFlag(name, value string) (*string, error) {
	v := strings.TrimSpace(value)
	if _, ok := ParseStrictDateTime(v); !ok {
		return nil, exitcode.Newf(exitcode.Usage, "--%s takes an ISO date-time with Z or an explicit offset, e.g. 2026-10-22T00:00:00Z, got %q", name, value)
	}
	return &v, nil
}
