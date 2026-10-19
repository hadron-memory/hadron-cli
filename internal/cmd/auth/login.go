package auth

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLogin(f *cmdutil.Factory) *cobra.Command {
	var withToken bool
	var provider string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to a Hadron server",
		Long: `Sign in to a Hadron server.

By default this opens your browser and runs an OAuth flow against the
server's sign-in page, which offers the methods that server supports, such
as GitHub, Google or email. Pass --provider google to go straight to Google.
The resulting token is stored in the OS keychain (or
~/.config/hadron/auth.json when no keychain is available).

If the server requires a passkey for email sign-in, its page asks for one
before the CLI receives anything. Where the server offers passkey
registration, it emails a second link: open it in the same browser.
Otherwise sign in with --provider github or --provider google. An agent
cannot complete a browser passkey step; agents use HADRON_TOKEN or
--with-token.

The browser step must finish within 15 minutes, a limit the CLI sets for
itself. If it does not, nothing is stored; rerun ` + "`hadron auth login`" + `.

The browser flow requests the "account" OAuth scope, because the CLI uses
the server's data API and a key limited to "mcp" is refused there. If the
server refuses "account" or grants a narrower scope, login fails with exit 1
rather than storing a key the CLI cannot use; sign in with --with-token instead.

For CI and scripting, pipe a personal access token to
` + "`hadron auth login --with-token`" + ` or set the HADRON_TOKEN
environment variable (which skips storage entirely).`,
		Example: `  hadron auth login
  hadron auth login --provider google
  echo $TOKEN | hadron auth login --with-token`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			server, err := f.Server()
			if err != nil {
				return err
			}

			var token string
			if withToken {
				if f.IOStreams.IsInputTerminal() {
					fmt.Fprintln(f.IOStreams.ErrOut, "Now paste your key/token here:")
				}
				token, err = readToken(f.IOStreams.In, "--with-token")
				if err != nil {
					return err
				}
			} else {
				result, err := auth.BrowserStrategy{}.Login(cmd.Context(), auth.LoginOptions{
					ServerURL:     server,
					LoginProvider: provider,
					IO:            f.IOStreams,
					HTTPClient:    f.HTTPClient,
				})
				if err != nil {
					return err
				}
				token = result.AccessToken
			}

			st := f.TokenStore()
			warnIfPlaintext(f.IOStreams, st)
			host := auth.Host(server)
			if err := st.Set(host, token); err != nil {
				return fmt.Errorf("storing token: %w", err)
			}
			// Clear the non-selected backend so a token from a prior login in a
			// different environment (keychain vs plaintext file) can't linger as
			// a stale, still-readable duplicate (#116). Warn rather than fail if
			// it couldn't be removed — the new token is already stored, but a
			// stale one may remain in the other store.
			if err := store.ClearExcept(st, host); err != nil {
				fmt.Fprintf(f.IOStreams.ErrOut, "warning: a prior credential in the other store could not be cleared (%v); run `hadron auth logout` if a stale token may linger\n", err)
			}

			dto := loginResult{Server: server, TokenStorage: st.Name()}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Signed in to %s (token stored in %s)\n", server, st.Name())
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&withToken, "with-token", false, "read the token from standard input")
	cmd.Flags().StringVar(&provider, "provider", "github", "browser identity provider: github or google")
	cmd.MarkFlagsMutuallyExclusive("with-token", "provider")
	return cmd
}

type loginResult struct {
	Server       string `json:"server"`
	TokenStorage string `json:"tokenStorage"`
}

// warnIfPlaintext surfaces a downgrade to the plaintext file store: landing
// there means no usable OS keychain was found — which also happens when the
// keychain is merely LOCKED or transiently unavailable, so the token would
// otherwise be written unencrypted without any signal (#122).
func warnIfPlaintext(io *output.IOStreams, st store.Store) {
	if st.Name() == (store.File{}).Name() {
		fmt.Fprintln(io.ErrOut, "warning: no usable OS keychain — storing the token unencrypted on disk (auth.json). If your keychain is just locked, unlock it and re-run `hadron auth login` to use the secure store.")
	}
}

// readToken reads a single token line from stdin. src names the caller so the
// usage error points at the right surface (e.g. "--with-token" for login, the
// command name for `token validate`).
func readToken(in io.Reader, src string) (string, error) {
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		// Distinguish a real read error from plain EOF (no input) — the former
		// shouldn't masquerade as a usage error (matches cmdutil.Confirm).
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", exitcode.Newf(exitcode.Usage, "%s expects a token on standard input", src)
	}
	token := strings.TrimSpace(scanner.Text())
	if token == "" {
		return "", exitcode.Newf(exitcode.Usage, "%s expects a non-empty token on standard input", src)
	}
	return token, nil
}
