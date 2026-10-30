package ha

import (
	"fmt"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// readToken reads the write-only access token (cmdutil.ReadSecretInput). It
// never comes from argv, and it never appears in an error. given is whether
// --token-file was passed: a passed-but-empty source (--token-file="$F" with F
// unset) is refused rather than read as "piped stdin", which would store
// whatever happened to be piped in. Only an ABSENT flag reaches the reader
// with "" — and only on create, since update reads no token without the flag.
func readToken(io *output.IOStreams, source string, given bool) (string, error) {
	if given && source == "" {
		return "", exitcode.Newf(exitcode.Usage, `--token-file is empty; pass "-" for stdin, @path or a path`)
	}
	return cmdutil.ReadSecretInput(io, source, "token-file", "Home Assistant access token", "access token")
}

// writeError maps a failed registry write. A server answer keeps its exit code
// and serverCode; a lost answer (exit 7) is outcome unknown: the write was sent
// once and is not retried, so the caller reads the registry back with check
// before trying again.
func writeError(err error, check string) error {
	mapped := api.MapErrorKeepingCode(err)
	if exitcode.FromError(mapped) == exitcode.Unavailable {
		return exitcode.New(exitcode.Unavailable, fmt.Errorf("outcome unknown (sent once, not retried; run `%s` to see whether it applied before retrying): %w", check, mapped))
	}
	return mapped
}
