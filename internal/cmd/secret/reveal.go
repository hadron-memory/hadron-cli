package secret

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/hadron-memory/hadron-cli/internal/secretfile"
)

// cli#2664 slice 2: `secret exec` and `secret reveal`. A read client is revealed
// by the worker it names, in that worker's session (cor:sec:030:01); a person
// reveals on the server's passkey page, never through an API token
// (cor:sec:060:02). The server owns who may read, the client's state and every
// refusal. This file owns only what the CLI must get right around ONE call that
// spends a read:
//
//   - every local check runs BEFORE the call, because a successful reveal spends
//     a read;
//   - the call is sent once per process and never retried;
//   - the value is a secret: it lives only in the revealed-response struct, the
//     child's environment block or the --out file, never in an argument, an
//     error, a log or a receipt.

var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// notRevealedHere is the one sentence a refused reveal adds when the server says
// NOT_FOUND. The server answers an unknown id, another holder's client, an App
// key, a run, an impersonation and a person on an API token identically, so the
// CLI says what is true of all of them and never which cause applied.
// revealLookupMaxPages bounds the link lookup: it reads at most this many pages of 200 clients, so a
// person with a very long list is not made to wait for a link that is an extra; a client beyond them
// simply gets the sentence alone (the full list is `secret client list --all`).
const revealLookupMaxPages = 10

// uniformNotFoundCode is the one code the server answers an unknown id, another holder's client, an
// App key, a run, an impersonation and a person on an API token with.
const uniformNotFoundCode = "SECRET_READ_CLIENT_NOT_FOUND"

const notRevealedHere = "a read client is revealed by the worker it names, in that worker's session; a person reveals on the server's passkey page"

const lostAnswerNote = "the read may have been spent: check `hadron secret client list` before trying again"

// valueText turns the server's value (JSON text) into what a consumer gets: a
// JSON string becomes its content, any other JSON is passed as its compact text
// (gate call Q8), and text that is not JSON at all is passed as it came.
func valueText(jsonText string) string {
	// JSON null decodes into a string without error and leaves it empty, which
	// would lose the documented compact text "null": handle it first.
	if strings.TrimSpace(jsonText) == "null" {
		return "null"
	}
	var s string
	if err := json.Unmarshal([]byte(jsonText), &s); err == nil {
		return s
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(jsonText)); err == nil {
		return buf.String()
	}
	return jsonText
}

// revealError maps a failed reveal. The mapped error is used for its exit CODE
// and the server's extension code only: its MESSAGE is not forwarded, because a
// non-200 answer can carry a success-shaped body (data holding the value) that the
// generic mapper would print. The message is built from the server's own
// errors[].message sentences (accepted as the server's text) or from a fixed class
// sentence, never from raw response data.
//
// A lost answer (an error with no server code: transport, gateway, a missing
// answer) says the read may be spent and names `client list`; a refusal with a
// server code does not, because the server refused before any read. NOT_FOUND
// adds the one uniform sentence.
func revealError(err error) error {
	mapped := api.MapErrorKeepingCode(err)
	// An internal fault is already safe (its text is written by the CLI, never from
	// the server's) and carries the request id the --json envelope adds: keep it.
	if _, ok := api.InternalFaultOf(mapped); ok {
		return mapped
	}
	code := exitcode.FromError(mapped)
	msg := safeRevealMessage(err, code)
	switch {
	case api.ServerCodeOf(err) == "" && (code == exitcode.Unavailable || code == exitcode.Error):
		msg += "; " + lostAnswerNote
	case code == exitcode.NotFound:
		msg += "; " + notRevealedHere
	}
	return api.WithServerCode(exitcode.Newf(code, "%s", msg), err)
}

// safeRevealMessage is the message of a failed reveal: the server's own sentences
// when it sent any, otherwise a fixed sentence for the HTTP status or the exit class.
func safeRevealMessage(err error, code int) string {
	// Only a GENUINELY structured GraphQL error is forwarded: one that carries an
	// extensions.code. For an unstructured non-200 (plain text, HTML, malformed
	// JSON) genqlient synthesises an errors[].message out of the raw body, which is
	// not a server sentence and may hold anything, the value included.
	if api.ServerCodeOf(err) != "" {
		if msgs := api.ServerMessages(err); len(msgs) > 0 {
			return output.SafeLine(strings.Join(msgs, "; "), 500)
		}
	}
	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) {
		return fmt.Sprintf("the server answered HTTP %d", httpErr.StatusCode)
	}
	switch code {
	case exitcode.Unavailable:
		return "the server could not be reached, or its answer was lost"
	case exitcode.AuthRequired:
		return "the server did not accept the credential"
	case exitcode.NotFound:
		return "not found"
	case exitcode.Forbidden:
		return "not permitted"
	case exitcode.Usage:
		return "the server rejected the request"
	case exitcode.Conflict:
		return "the read client is not usable now"
	}
	return "the server returned an error"
}

// revealedValue is what one successful reveal returns. It is deliberately tiny
// and has no String or Format method: nothing should be able to print it by
// accident.
type revealedValue struct {
	secretName string
	json       string // the server's value: JSON text
	readsLeft  *int
}

// revealOnce resolves the session, sends ONE revealSecret and returns the value.
// An unbound worktree is not an error: the call is sent without a session (so a
// person gets the server's uniform answer) with one stderr note. The binding is
// re-verified immediately before the call.
func revealOnce(cmd *cobra.Command, f *cmdutil.Factory, clientID string) (*revealedValue, error) {
	wctx, err := f.ResolveWorkerSessionIfBound(cmd.Context())
	if err != nil {
		return nil, err
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return nil, err
	}
	ctx := cmd.Context()
	if wctx != nil {
		if ctx, err = wctx(ctx); err != nil {
			return nil, err
		}
	} else {
		_, _ = fmt.Fprintln(f.IOStreams.ErrOut, "no worker binding in this worktree; sent without a worker session")
	}
	resp, err := gen.RevealSecret(ctx, client, clientID)
	if err != nil {
		return nil, revealError(err)
	}
	if resp == nil || resp.RevealSecret == nil {
		return nil, exitcode.Newf(exitcode.Error, "server returned no value; %s", lostAnswerNote)
	}
	r := resp.RevealSecret
	return &revealedValue{secretName: r.SecretName, json: r.Value, readsLeft: r.ReadsLeft}, nil
}

// revealLinkError is a refused reveal that also carries the server's own link to its passkey
// page (SecretReadClient.revealUrl, returned only to the person who holds the client). The text is
// what the terminal prints; the URL travels exactly as the server sent it for the --json envelope
// (the root renderer reads RevealURL).
type revealLinkError struct {
	err error
	msg string
	url string
}

func (e *revealLinkError) Error() string     { return e.msg }
func (e *revealLinkError) Unwrap() error     { return e.err }
func (e *revealLinkError) RevealURL() string { return e.url }

// withRevealLink adds the server's link to the uniform NOT_FOUND refusal when the viewer's own
// client list carries one for this client. It never composes a URL, never decides who a person
// holder is (the server's field is the answer for the viewer), and changes nothing else: a refusal
// of any other kind, a lookup that fails or a client with no link comes back exactly as it was.
func withRevealLink(cmd *cobra.Command, f *cmdutil.Factory, clientID string, err error) error {
	// Only the server's own uniform answer: a not-found the CLI raised before sending (a binding
	// check, say) carries no server code and never triggers the read.
	var coded interface{ ServerCode() string }
	if exitcode.FromError(err) != exitcode.NotFound || !errors.As(err, &coded) || coded.ServerCode() != uniformNotFoundCode {
		return err
	}
	client, cerr := f.GraphQLClient()
	if cerr != nil {
		return err
	}
	url := findRevealURL(cmd.Context(), client, clientID)
	if url == "" {
		return err
	}
	if f.JSON {
		return &revealLinkError{err: err, msg: err.Error(), url: url}
	}
	if cmdutil.UnsafeForTerminal(url) {
		return &revealLinkError{err: err, url: url, msg: err.Error() +
			"\nthe server gave a link to its passkey page, but it contains control or format characters and is not printed as text; use --json to read it exactly"}
	}
	return &revealLinkError{err: err, url: url, msg: err.Error() + "\nits passkey page: " + url}
}

// findRevealURL reads the viewer's own read clients (the same read as `client list`, no worker
// session) and returns the revealUrl of the one whose full id is clientID, or "". A short page is
// the end of the list, so the lookup stops there; a failed read is not an error here.
func findRevealURL(ctx context.Context, client graphql.Client, clientID string) string {
	noLink := false
	for off, pages := 0, 0; pages < revealLookupMaxPages; pages++ {
		l, o := clientsPageSize, off
		page, err := fetchClientPage(ctx, client, &noLink, &l, &o)
		if err != nil {
			return ""
		}
		for _, c := range page {
			if c.ClientID != nil && *c.ClientID == clientID {
				if c.RevealURL != nil {
					return *c.RevealURL
				}
				return ""
			}
		}
		if len(page) < clientsPageSize {
			return ""
		}
		off += len(page)
	}
	return ""
}

func requireClient(v string) (string, error) {
	if strings.TrimSpace(v) == "" {
		return "", exitcode.Newf(exitcode.Usage, "the read client id is empty")
	}
	return v, nil
}

func newCmdExec(f *cmdutil.Factory) *cobra.Command {
	var client, env string
	cmd := &cobra.Command{
		Use:   "exec --client <client-id> --env <NAME> -- <command> [args...]",
		Short: "Run a command with a read client's secret in its environment",
		Long: `Reveal a secret through a read client and run a command with the value in one
environment variable. The value is never an argument and hadron prints nothing of
its own: the child gets your environment plus NAME=<value>, and its stdin, stdout
and stderr pass through. SIGINT and SIGTERM are forwarded to the child.

This is a worker session's act: it sends this worktree's bound session, and the
read client must name that worker. Run unbound it still asks, so the server gives
its one answer, with a stderr note. A person cannot reveal here and reveals on the
server's passkey page. The server decides everything else: who may read, whether
the client is live and the secret's kind.

Every check runs before the call, because a successful reveal spends one read: a
bad --env name (letters, digits and underscores, not starting with a digit), a
missing command after --, a blank client id, the command not being found, and
--json (refused: the child owns stdout). The reveal is sent once per process and
never retried; if its answer is lost the read may be spent, so check 'hadron
secret client list' before trying again. A JSON string value is passed as its
content; any other JSON value as its compact text. A value containing a NUL byte
cannot be placed in an environment variable: the read is spent and nothing is run.

Exit codes. Once the child has started, hadron exits with the CHILD's own status
(128 plus the signal number if a signal killed it), like env, nice and sudo.
Every hadron refusal happens before the child starts and keeps hadron's codes: 2
usage, 4 not found, 5 the client is not live now (spent, expired, revoked, not yet
started, pending, its issuer gone) or its secret was deleted, 8 not permitted. A
child that exits with one of those numbers looks the same, so a script must tell
the two apart by whether the child ran (it prints its own output), never by the
number. If the child cannot start after the read was spent, exit 1 says so.`,
		Example: `  hadron secret exec --client <client-id> --env API_TOKEN -- ./deploy.sh
  hadron secret exec --client <client-id> --env TOKEN -- sh -c 'curl -H "Authorization: Bearer $TOKEN" https://example.test'`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.JSON {
				return exitcode.Newf(exitcode.Usage, "exec cannot be combined with --json: the command owns stdout, and hadron prints no receipt that could carry the value; nothing was sent")
			}
			clientID, err := requireClient(client)
			if err != nil {
				return err
			}
			if !envNameRE.MatchString(env) {
				return exitcode.Newf(exitcode.Usage, "--env must be a variable name (letters, digits and underscores, not starting with a digit); nothing was sent")
			}
			dash := cmd.ArgsLenAtDash()
			if dash < 0 || len(args) == dash {
				return exitcode.Newf(exitcode.Usage, "name the command to run after --: hadron secret exec --client <id> --env <NAME> -- <command>; nothing was sent")
			}
			if dash > 0 {
				return exitcode.Newf(exitcode.Usage, "unexpected argument %q before --; nothing was sent", args[0])
			}
			argv := args
			path, err := osexec.LookPath(argv[0])
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "cannot run %q: %v; nothing was sent, so no read was spent", argv[0], errors.Unwrap(err))
			}

			rv, err := revealOnce(cmd, f, clientID)
			if err != nil {
				return err
			}
			val := valueText(rv.json)
			if strings.IndexByte(val, 0) >= 0 {
				return exitcode.Newf(exitcode.Error, "one read was spent, but the value cannot be placed in an environment variable (it contains a NUL byte); nothing was run")
			}
			return runChild(f, path, argv, env+"="+val)
		},
	}
	cmd.Flags().StringVar(&client, "client", "", "the read client id (required)")
	cmd.Flags().StringVar(&env, "env", "", "the environment variable that receives the value (required)")
	_ = cmd.MarkFlagRequired("client")
	_ = cmd.MarkFlagRequired("env")
	return cmd
}

// runChild runs the command with the extra environment entry and returns the
// child's own status as the exit code. It prints nothing and carries no value in
// any error it returns.
func runChild(f *cmdutil.Factory, path string, argv []string, envEntry string) error {
	c := osexec.Command(path, argv[1:]...)
	c.Env = append(os.Environ(), envEntry)
	c.Stdin, c.Stdout, c.Stderr = f.IOStreams.In, f.IOStreams.Out, f.IOStreams.ErrOut
	if err := c.Start(); err != nil {
		return exitcode.Newf(exitcode.Error, "one read was spent, but the command could not be started (%s); the value was not delivered", startFailure(err))
	}
	sigc := make(chan os.Signal, 2)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigc:
				_ = c.Process.Signal(s)
			case <-done:
				return
			}
		}
	}()
	err := c.Wait()
	signal.Stop(sigc)
	close(done)
	if err == nil {
		return nil
	}
	var ee *osexec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return exitcode.Silent(128 + int(ws.Signal()))
		}
		return exitcode.Silent(ee.ExitCode())
	}
	return exitcode.Newf(exitcode.Error, "the command's input or output failed after it ran")
}

// startFailure names a failed start without anything but the OS reason.
func startFailure(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return "the operating system refused it"
}

// revealDTO is the --json shape of `secret reveal`. value is the JSON text the
// server sent, exactly. readsLeft is null when the grant has no budget.
type revealDTO struct {
	SecretName string `json:"secretName"`
	Value      string `json:"value"`
	ReadsLeft  *int   `json:"readsLeft"`
}

func newCmdReveal(f *cmdutil.Factory) *cobra.Command {
	var out string
	var allowNonTerminal bool
	cmd := &cobra.Command{
		Use:   "reveal <client-id> [--out <file>] [--allow-non-terminal]",
		Short: "Reveal a read client's secret to your terminal or a new file",
		Long: `Reveal a secret through a read client and print the value alone on stdout with one
trailing newline, but only when stdout is a terminal: on a pipe or a file it
exits 2 before any call (so no read is spent) unless you pass --allow-non-terminal.
To hand the value to a program, prefer 'hadron secret exec'.

--out <file> writes the value, exactly and without a newline, to a NEW file at
mode 0600 opened before the call, and prints nothing. An existing path is exit 2
and sends nothing. --json prints {secretName, value, readsLeft} (value is the JSON
text the server sent) and follows the same terminal rule because it prints the
value; it cannot be combined with --out. In text mode "reads left: N" (or
"unlimited") goes to stderr. A JSON string value is printed as its content, any
other JSON value as its compact text; whatever the value contains is printed as
it is, so a terminal sees it raw.

This is a worker session's act, like exec: the read client must name the worker
whose session is bound here. Unbound it still asks, with a stderr note, and a
person gets the server's uniform answer (a person reveals on the server's passkey
page). When the server's answer is the uniform not-found and your own 'hadron
secret client list' carries a revealUrl for this client, the refusal also prints
that link exactly as the server sent it: text refuses a link with control or format
characters and says to use --json, which carries it in error.revealUrl. The link is
never built here and a client without one keeps the sentence alone, as does a server that does not have the field and a client beyond your first 2,000 (the lookup reads at most ten pages). One call, never retried: if its answer is lost the read may be spent, so
check 'hadron secret client list' before trying again. Exit codes as for exec
before the child: 2 usage, 4 not found, 5 not live (spent, expired, revoked, not
yet started, pending, issuer gone) or the secret deleted, 8 not permitted.`,
		Example: `  hadron secret reveal <client-id>
  hadron secret reveal <client-id> --out ./token`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientID, err := requireClient(args[0])
			if err != nil {
				return err
			}
			if out != "" && f.JSON {
				return exitcode.Newf(exitcode.Usage, "--out writes the value to a file and prints nothing; it cannot be combined with --json; nothing was sent")
			}
			if out == "" && !allowNonTerminal && !f.IOStreams.IsTerminal() {
				return exitcode.Newf(exitcode.Usage, "stdout is not a terminal, so the value would land in a pipe or file; nothing was sent and no read was spent. Use --out <file>, `hadron secret exec`, or --allow-non-terminal")
			}
			var pending *secretfile.Pending
			var abs string
			if out != "" {
				if abs, err = filepath.Abs(out); err != nil {
					return exitcode.Newf(exitcode.Usage, "cannot resolve --out: %v; nothing was sent", err)
				}
				if pending, err = secretfile.Open(abs, false); err != nil {
					switch {
					case errors.Is(err, secretfile.ErrExists):
						return exitcode.Newf(exitcode.Usage, "%s already exists; nothing was sent, no read was spent", abs)
					case errors.Is(err, secretfile.ErrIsDir):
						return exitcode.Newf(exitcode.Usage, "%s is a directory; name a file; nothing was sent", abs)
					case errors.Is(err, os.ErrNotExist):
						return exitcode.Newf(exitcode.Usage, "the directory of %s does not exist; nothing was sent", abs)
					}
					return exitcode.Newf(exitcode.Error, "cannot create %s: %v; nothing was sent", abs, err)
				}
			}
			rv, err := revealOnce(cmd, f, clientID)
			if err != nil {
				if pending != nil {
					_ = pending.Abort()
				}
				return withRevealLink(cmd, f, clientID, err)
			}
			if !f.JSON {
				_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "reads left: %s\n", readsLeftText(rv.readsLeft))
			}
			if pending != nil {
				if err := pending.Commit([]byte(valueText(rv.json))); err != nil {
					var lb *secretfile.LeftBehindError
					if errors.As(err, &lb) {
						// The write failed part-way and the file could not be removed: it may
						// hold part or all of the secret. Fixed wording; the underlying error
						// is never printed.
						return exitcode.Newf(exitcode.Error, "one read was spent, but the value could not be fully written to %s, and that file could not be removed: it may hold part or all of the secret; delete it", abs)
					}
					return exitcode.Newf(exitcode.Error, "one read was spent, but the value could not be written to %s; the value was not delivered", abs)
				}
				return nil
			}
			dto := revealDTO{SecretName: rv.secretName, Value: rv.json, ReadsLeft: rv.readsLeft}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintln(w, valueText(rv.json))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "write the value to this new file (mode 0600) instead of printing it")
	cmd.Flags().BoolVar(&allowNonTerminal, "allow-non-terminal", false, "print the value even though stdout is not a terminal")
	return cmd
}

func readsLeftText(n *int) string {
	if n == nil {
		return "unlimited"
	}
	return fmt.Sprint(*n)
}
