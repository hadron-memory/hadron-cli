package cmdutil

import "github.com/hadron-memory/hadron-cli/internal/output"

// Attribution is the stable --json shape of a node's or edge's author
// (hadron-cli#1513, hadron-server#3092): the provider's {handle, urn}
// projection and nothing else. Both fields are pointers WITHOUT omitempty, so
// the object always carries both keys and a missing half is `null`, never
// omitted and never "".
//
// The provider decides. The CLI never classifies the principal (a system job,
// an App key, a client, a deleted user), never converts or looks one up, and
// never prints a raw created-by/updated-by id: a null author means "not shown
// to you or not known", which is the provider's statement, not the CLI's.
type Attribution struct {
	Handle *string `json:"handle"`
	URN    *string `json:"urn"`
}

// NewAttribution wraps one provider object's halves; callers keep a null
// object as a nil *Attribution and never call this for it.
func NewAttribution(handle, urn *string) *Attribution {
	return &Attribution{Handle: handle, URN: urn}
}

// Display is the human form: "@handle" when the handle is non-empty, else the
// URN when that is non-empty, else "" (nothing printable: the caller prints no
// line or row at all). It deliberately reuses only the handle-then-URN
// preference of the revision editor display, never that renderer's raw-id
// fallbacks or its dash, and the result is terminal-sanitized so a hostile
// handle or URN cannot reach the terminal.
func (a *Attribution) Display(maxRunes int) string {
	if a == nil {
		return ""
	}
	if a.Handle != nil && *a.Handle != "" {
		return output.SafeLine("@"+*a.Handle, maxRunes)
	}
	if a.URN != nil && *a.URN != "" {
		return output.SafeLine(*a.URN, maxRunes)
	}
	return ""
}
