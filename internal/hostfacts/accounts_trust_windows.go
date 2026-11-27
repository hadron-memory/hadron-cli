//go:build windows

package hostfacts

import "os"

// checkAccountsTrust is not enforced on Windows: file ownership and mode do not carry the meaning the Unix check
// relies on (as the poller's reload trust check). The symlink and regular-file refusals still apply.
func checkAccountsTrust(string, os.FileInfo) error { return nil }

func openAccountsNoFollow(path string) (*os.File, error) { return os.Open(path) }
