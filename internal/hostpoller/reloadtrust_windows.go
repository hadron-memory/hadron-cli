//go:build windows

package hostpoller

import "os"

// checkReloadTrust is not enforced on Windows: file ownership and mode do not
// carry the meaning the Unix check relies on. A Windows host that wants the
// stricter rule restarts the poller to apply a configuration change.
func checkReloadTrust(_ trustWalk, c, next *Config, path string, info os.FileInfo, changedWorkers []string) error {
	return nil
}
