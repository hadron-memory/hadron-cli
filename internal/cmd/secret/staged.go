package secret

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// codeNeedsPasskey is the server's answer to a change of a protected secret:
// the change is STAGED, not applied, and a person completes it on a ceremony
// page (hadron-server#3999 S4, cor:sec:070:02). It carries extensions.requestId
// and extensions.ceremonyUrl, except where signing is off or the caller is not
// a person acting as themselves, which get the bare code.
const codeNeedsPasskey = "SECRET_PROTECTED_NEEDS_PASSKEY"

// noProtectedChanges is the sentence for a server that offers no protected-secret
// changes (signing off): the absent operations and a refusal with no request id.
const noProtectedChanges = "this server does not offer protected-secret changes"

// stagedChange is a change a protected secret staged instead of applying. It is
// an exit-5 refusal (the secret's state, which no retry changes) that also hands
// root the two values a script needs, so --json carries them exactly.
type stagedChange struct {
	error
	requestID, ceremonyURL string
}

func (s *stagedChange) Unwrap() error { return s.error }

// StagedSecretChange is read by the root error renderer: the request id and
// the ceremony URL exactly as the server sent them (the text is neutralised,
// these are not).
func (s *stagedChange) StagedSecretChange() (requestID, ceremonyURL string) {
	return s.requestID, s.ceremonyURL
}

// mapChange maps the error of a door that can stage a change (create, update,
// set, rm, protect). A staged refusal becomes stagedChange (exit 5); the same
// code without a request id is not "a request is open" (cor:sec:070:02: the code
// alone never means that), so it says nothing was staged (exit 2); everything
// else maps as before.
func mapChange(err error, what string) error {
	// Only a refusal that is the WHOLE answer, at the mutation's own level and
	// not inside a non-200 envelope, is claimed as staged. A mixed envelope (an
	// internal error riding beside the code), a result field failing beneath the
	// mutation, or a gateway's 5xx is not proof of what the server did, so it
	// maps as any other error and keeps its own request id.
	if !api.OnlyErrorCodesAtRoot(err, codeNeedsPasskey) {
		return api.MapErrorKeepingCode(err)
	}
	id, _ := api.ErrorCodeExtension(err, codeNeedsPasskey, "requestId")
	if strings.TrimSpace(id) == "" {
		return api.WithServerCode(exitcode.Newf(exitcode.Usage,
			"%s: the secret is protected but the server returned no change request, so nothing was staged by this command: %s, or this caller is not a person acting as themselves",
			what, noProtectedChanges), err)
	}
	url, _ := api.ErrorCodeExtension(err, codeNeedsPasskey, "ceremonyUrl")
	return api.WithServerCode(&stagedChange{
		error:       exitcode.New(exitcode.Conflict, errors.New(stagedText(what, id, url))),
		requestID:   id,
		ceremonyURL: url,
	}, err)
}

// stagedText is the text-mode refusal. The URL is printed exactly as returned,
// or withheld when it could act on a terminal (--json then carries it).
func stagedText(what, id, url string) string {
	safeID := output.SafeCell(id)
	var b strings.Builder
	fmt.Fprintf(&b, "staged, not applied: %s waits until a person signs it with a passkey\n", what)
	fmt.Fprintf(&b, "request id: %s\n", safeID)
	switch {
	case strings.TrimSpace(url) == "":
		b.WriteString("ceremony URL: the server returned none; read the request with the command below\n")
	case cmdutil.UnsafeForTerminal(url):
		b.WriteString("ceremony URL: contains control or format characters and is not printed as text; use --json to read it exactly\n")
	default:
		fmt.Fprintf(&b, "ceremony URL: %s\n", url)
	}
	b.WriteString("Give the URL to the person who must sign; hadron never completes the ceremony itself.\n")
	q := cmdutil.ShellQuote(safeID)
	fmt.Fprintf(&b, "Check it: hadron secret change get %s --wait\nWithdraw it: hadron secret change withdraw %s", q, q)
	return b.String()
}
