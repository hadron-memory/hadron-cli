//go:build !unix

package settingscheck

// Claude Code reads no repository-root local file on Windows.
func pathOwnedByUser(string) bool { return false }
