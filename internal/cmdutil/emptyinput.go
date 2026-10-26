package cmdutil

import (
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// RefuseEmptyFileInput is the one guard for a text flag that read a file (or
// piped stdin) and got ZERO bytes (#1035). An empty string is a real "clear" on
// the wire, and a zero-byte file is the signature of an upstream failure
// (`generate > body.md` that died) — so without this the write silently blanks
// the stored field. source names what was read ("file x.md", "stdin") so the
// refusal says which input was empty. allowFlag is the opt-in that says the
// empty value is deliberate. Only zero bytes trips it: whitespace-only input
// and an explicit inline "" keep their existing behaviour.
func RefuseEmptyFileInput(data string, source, flag, allowFlag string, allow bool) error {
	if data != "" || allow {
		return nil
	}
	return exitcode.Newf(exitcode.Usage,
		"%s is empty (%s read zero bytes); refusing to write an empty value — a failed upstream command often leaves a zero-byte file. Pass %s if an empty value is intended",
		flag, source, allowFlag)
}
