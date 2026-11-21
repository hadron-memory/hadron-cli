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
		return withholdServerText(err, what)
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
	q := cmdutil.ShellQuote(safeID)
	switch {
	case strings.TrimSpace(url) == "":
		// A request with no link cannot be signed by anyone from here, and it still
		// counts against the secret's open-request cap: say so, and name the way out.
		b.WriteString("ceremony URL: none was returned, so no person can sign this request\n")
		fmt.Fprintf(&b, "Withdraw it, then try again: hadron secret change withdraw %s\nCheck it: hadron secret change get %s", q, q)
		return b.String()
	case cmdutil.UnsafeForTerminal(url):
		b.WriteString("ceremony URL: contains control or format characters and is not printed as text; use --json to read it exactly\n")
	default:
		fmt.Fprintf(&b, "ceremony URL: %s\n", url)
	}
	b.WriteString("Give the URL to the person who must sign; hadron never completes the ceremony itself.\n")
	fmt.Fprintf(&b, "Check it: hadron secret change get %s --wait\nWithdraw it: hadron secret change withdraw %s", q, q)
	return b.String()
}

// codeChangeLimit is the server's refusal of one more open change request on a
// secret (hadron-server#3999 S4): too many are already waiting for a person.
const codeChangeLimit = "SECRET_CHANGE_REQUEST_LIMIT"

// withheld is an error whose text is fixed here, not the server's: Error() is the
// fixed sentence, while Unwrap() keeps the mapped error underneath so the exit
// code, the server code and an internal fault's request id still reach the root
// renderer.
type withheld struct {
	msg string
	err error
}

func (w *withheld) Error() string { return w.msg }
func (w *withheld) Unwrap() error { return w.err }

// withholdServerText maps an error of a door that can stage a change when it is
// NOT a clean staged refusal. For the two protected-change codes the server's
// message can repeat a value the caller submitted (LOCAL review of cli#2738), and
// the answer may be a limit refusal, a mixed envelope, an error below the
// mutation or an HTTP 5xx, so the message is replaced by a fixed sentence while
// the exit class and the server code are kept. Any other error maps as before.
func withholdServerText(err error, what string) error {
	mapped := api.MapErrorKeepingCode(err)
	switch {
	case api.HasErrorCode(err, codeChangeLimit) && !api.HasErrorCode(err, codeNeedsPasskey):
		return &withheld{"too many change requests are already open on this secret, so " + what + " was not staged; withdraw one (hadron secret change withdraw <request-id>) or wait for a person to sign or decline them", mapped}
	case api.HasErrorCode(err, codeNeedsPasskey) || api.HasErrorCode(err, codeChangeLimit):
		return &withheld{what + ": the server answered with a protected-secret refusal inside an answer that does not prove what was staged (several errors, an error below the mutation, or an HTTP 5xx), so a change request may or may not have been opened. The server's message is withheld because it can repeat a submitted value; read --json for the server code, and check any request id you were given with hadron secret change get", mapped}
	}
	return mapped
}
