package cmdutil

import "github.com/hadron-memory/hadron-cli/internal/output"

// SetUnlockPromptForTest replaces the no-echo passphrase prompt and returns a
// restore func (hadron-cli#1234). Red-first stub: not wired yet.
func SetUnlockPromptForTest(fn func(*output.IOStreams, string) (string, error)) func() {
	return func() {}
}
