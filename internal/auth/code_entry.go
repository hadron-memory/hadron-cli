package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// codeDeliveryPage is the authorize request's code_delivery value that asks
// the server to SHOW the authorization code on a page instead of redirecting
// it to the loopback address (hadron-server#5125, cor:aut:030:01). The CLI
// consumes the name; it never chooses it, and the server never infers it.
const (
	codeDeliveryParam = "code_delivery"
	codeDeliveryPage  = "page"
)

// codeRefusal is the ONE answer to a pasted code the server did not accept.
// Wrong, expired, already used, dead after too many entries and mismatched
// are the server's states and the server answers all of them alike; the CLI
// adds no text that tells them apart and never repeats the code.
const (
	codeRefusedReason = "the code was not accepted (it may be wrong, expired or already used)"
	codeRefusal       = codeRefusedReason + "; run `hadron auth login` again to get a new one"
)

// noCodeInputHint is the remedy when a pasted code cannot be read at all.
const noCodeInputHint = "sign in with a token instead: echo $TOKEN | hadron auth login --with-token, or set HADRON_TOKEN"

// CodeReader reads the code a person pastes, returning when it arrives or
// ctx ends. It must not echo what it reads.
type CodeReader func(ctx context.Context) (string, error)

// errNoCodeInput is the refusal for a host that has nowhere to paste a code:
// standard input is not a terminal (an agent, a pipe, CI), so waiting would
// hang until the login timeout.
func errNoCodeInput() error {
	return exitcode.Newf(exitcode.Usage,
		"code entry needs a terminal to paste the code into, and standard input is not one; %s", noCodeInputHint)
}

// pasteReader is the reader used when none is injected: a no-echo read of one
// line from the terminal on standard input. A pasted code is credential-adjacent
// material, so the terminal never shows it and it is never logged.
func pasteReader(streams *output.IOStreams) CodeReader {
	return func(ctx context.Context) (string, error) {
		if !streams.IsInputTerminal() {
			return "", errNoCodeInput()
		}
		in, ok := streams.In.(*os.File)
		if !ok {
			return "", errNoCodeInput()
		}
		line, err := readHiddenLine(ctx, int(in.Fd()))
		fmt.Fprintln(streams.ErrOut)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", exitcode.Newf(exitcode.Error, "reading the code failed")
		}
		return line, nil
	}
}

// cleanCode trims the whitespace a paste drags along. The code itself stays
// opaque: nothing is parsed, split or validated (its shape is the server's).
func cleanCode(raw string) string { return strings.TrimSpace(raw) }

// reserveRedirectURI returns a loopback redirect URI for the client to
// register without starting anything on it. In code-entry mode the server
// sends nothing to the address; it only has to be a valid registered one for
// the exchange to present.
//
// The port is free when named and could be taken by another local process
// before the exchange; that only matters to a server that redirects anyway
// (one without the code page), and PKCE keeps a code taken that way unusable.
func reserveRedirectURI() (string, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("choosing a redirect address: %w", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return fmt.Sprintf("http://%s/callback", addr), nil
}

// waitForCode waits for a pasted code and returns it cleaned, or the same
// Cancelled answers the loopback wait gives when ctx ends.
func waitForCode(ctx context.Context, read CodeReader, bound time.Duration) (string, error) {
	code, err := read(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctxDoneError(ctx, bound)
		}
		return "", err
	}
	code = cleanCode(code)
	if code == "" {
		return "", exitcode.Newf(exitcode.Usage, "no code was entered; run `hadron auth login` again")
	}
	return code, nil
}

// errCodeRefused marks a token-endpoint refusal of a pasted code.
var errCodeRefused = errors.New("code refused")

// isCodeRefusal reports whether err is the fixed refusal of a pasted code.
func isCodeRefusal(err error) bool {
	var coded *exitcode.CodedError
	return errors.As(err, &coded) && coded.Error() == codeRefusal
}
