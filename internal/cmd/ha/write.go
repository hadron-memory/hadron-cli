package ha

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
// before trying again. token is the access token the write sent ("" if none):
// a server message that echoes it is redacted, so it reaches no output.
func writeError(err error, check, token string) error {
	mapped := api.MapErrorKeepingCode(err)
	if exitcode.FromError(mapped) == exitcode.Unavailable {
		mapped = exitcode.New(exitcode.Unavailable, fmt.Errorf("outcome unknown (sent once, not retried; run `%s` to see whether it applied before retrying): %w", check, mapped))
	}
	if token == "" {
		return mapped
	}
	return &redactedError{err: mapped, secret: token}
}

// redactedError removes a secret from an error's text. It unwraps to the
// original, so the exit code and serverCode still reach the root handler; it
// also answers HumanMessage, which the root handler prefers for a terminal.
type redactedError struct {
	err    error
	secret string
}

func (e *redactedError) Error() string { return e.redact(e.err.Error()) }
func (e *redactedError) Unwrap() error { return e.err }

func (e *redactedError) HumanMessage() string {
	var h interface{ HumanMessage() string }
	if errors.As(e.err, &h) {
		return e.redact(h.HumanMessage())
	}
	return e.Error()
}

// redact replaces the secret raw and JSON-escaped, the form an HTTP error's
// body takes.
func (e *redactedError) redact(s string) string {
	s = strings.ReplaceAll(s, e.secret, "[redacted]")
	if q, err := json.Marshal(e.secret); err == nil {
		if escaped := string(q[1 : len(q)-1]); escaped != e.secret {
			s = strings.ReplaceAll(s, escaped, "[redacted]")
		}
	}
	return s
}
