package auth

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLogin(f *cmdutil.Factory) *cobra.Command {
	var withToken bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to a Hadron server",
		Long: `Sign in to a Hadron server.

By default this opens your browser and runs an OAuth flow against the
server's consent screen. The resulting token is stored in the OS
keychain (or ~/.config/hadron/auth.json when no keychain is available).

For CI and scripting, pipe a personal access token to
` + "`hadron auth login --with-token`" + ` or set the HADRON_TOKEN
environment variable (which skips storage entirely).`,
		Example: `  hadron auth login
  echo $TOKEN | hadron auth login --with-token`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			server, err := f.Server()
			if err != nil {
				return err
			}

			var token string
			if withToken {
				token, err = readToken(f.IOStreams.In)
				if err != nil {
					return err
				}
			} else {
				result, err := auth.BrowserStrategy{}.Login(cmd.Context(), auth.LoginOptions{
					ServerURL:  server,
					IO:         f.IOStreams,
					HTTPClient: f.HTTPClient,
				})
				if err != nil {
					return err
				}
				token = result.AccessToken
			}

			st := f.TokenStore()
			if err := st.Set(auth.Host(server), token); err != nil {
				return fmt.Errorf("storing token: %w", err)
			}

			dto := loginResult{Server: server, TokenStorage: st.Name()}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Signed in to %s (token stored in %s)\n", server, st.Name())
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&withToken, "with-token", false, "read the token from standard input")
	return cmd
}

type loginResult struct {
	Server       string `json:"server"`
	TokenStorage string `json:"tokenStorage"`
}

func readToken(in io.Reader) (string, error) {
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return "", exitcode.Newf(exitcode.Usage, "--with-token expects a token on standard input")
	}
	token := strings.TrimSpace(scanner.Text())
	if token == "" {
		return "", exitcode.Newf(exitcode.Usage, "--with-token expects a non-empty token on standard input")
	}
	return token, nil
}
