//go:build windows

package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// On Windows a file's permissions cannot be checked, so the file form is refused
// before any request with a message that names the way out, while "-" (stdin)
// keeps working.
func TestDataKeyFileOnWindowsIsRefusedButStdinWorks1657(t *testing.T) {
	for _, c := range dataKeyCases1657() {
		t.Run(c.name, func(t *testing.T) {
			r := runDataKey1657(t, c, "", "--data-key-file", `C:\keys\data.key`)
			if r.err == nil || exitcode.FromError(r.err) != exitcode.Usage || len(r.reqs) != 0 ||
				!strings.Contains(r.err.Error(), "--data-key-file is not supported on Windows") ||
				!strings.Contains(r.err.Error(), "--data-key-file -") {
				t.Fatalf("want the Windows refusal naming '--data-key-file -': err=%v reqs=%d", r.err, len(r.reqs))
			}
			ok := runDataKey1657(t, c, linkUserDataKey+"\n", "--data-key-file", "-")
			if ok.err != nil || len(ok.reqs) == 0 {
				t.Fatalf("--data-key-file - must still work: err=%v reqs=%d", ok.err, len(ok.reqs))
			}
		})
	}
}
