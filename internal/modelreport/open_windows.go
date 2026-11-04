//go:build windows

package modelreport

import "os"

// Windows has no O_NOFOLLOW; the canonical path and the fd identity check
// still apply.
func openNoFollow(path string) (*os.File, error) {
	return os.Open(path) // #nosec G304 — canonical, allowlisted, identity-checked path
}
