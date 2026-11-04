// Package serverinfo implements `hadron server-info`.
package serverinfo

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// serverInfoDTO is the stable --json shape for `server-info`.
//
// url and baseUrl are deliberately both present and mean different things:
// url is where this invocation SENT the query (flag, env, config, or the
// hosted default), baseUrl is what the server reports as its own public base.
// A disagreement is a real misconfiguration — a server behind a proxy without
// BASE_URL set emits absolute URLs on the wrong scheme or host, which is what
// broke OAuth discovery behind Cloudflare — so the command surfaces it rather
// than showing one and hiding the other.
type serverInfoDTO struct {
	URL           string `json:"url"`
	Version       string `json:"version"`
	BaseURL       string `json:"baseUrl"`
	Authenticated bool   `json:"authenticated"`
}

// sameServer reports whether two base URLs address the same deployment.
//
// The comparison must be normalized, not literal: api.Endpoint already trims a
// trailing slash when building the request URL, so `--server https://x/` and a
// reported `https://x` reach the same place. Warning on that spelling would
// cry proxy-misconfiguration at a correctly configured server, and a warning
// that fires on healthy setups stops being read. Scheme and host are
// case-insensitive per RFC 3986, and an explicit default port is equivalent to
// none.
func sameServer(a, b string) bool {
	return normalizeServerURL(a) == normalizeServerURL(b)
}

func normalizeServerURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		// Unparseable: fall back to a literal compare rather than claiming
		// a match we cannot justify.
		return strings.TrimRight(strings.TrimSpace(raw), "/")
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	// Strip the default port for the scheme (https://x:443 == https://x).
	for s, port := range map[string]string{"http": ":80", "https": ":443"} {
		if scheme == s {
			host = strings.TrimSuffix(host, port)
		}
	}
	return scheme + "://" + host + strings.TrimRight(u.Path, "/")
}

func NewCmdServerInfo(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "server-info",
		Aliases: []string{"serverinfo"},
		Short:   "Show the hadron-server this CLI is talking to",
		Long: `Check that this CLI reaches a Hadron server, and which one. It works signed
out, so a failure here means the server or the network, not your sign-in. To
check you are signed in, use 'hadron auth status'.

The server is chosen by --server, then the HADRON_SERVER environment variable,
then 'hadron config get server', then the hosted default
(https://srv.hadronmemory.com).

Output, one line per field:
  hadron-server <version>   the server's API version (see below)
  url:      <url>           where this command sent its request
  base url: <url>           the address the server says it is reached at
  (queried anonymously ...) shown when no sign-in was sent

The version is the API version, raised when the server's commands or fields
change. It is not the release number, so don't compare it with a release tag;
use it to check that a feature you need exists.

url and base url should name the same server. If they differ, a warning line
follows: the server sits behind a proxy whose public address it was not told,
so the links it hands out (for sign-in and webhooks) point to the wrong place.
Tell whoever runs that server.

If you are signed in but your stored credential cannot be read, this fails
instead of quietly checking signed out. 'hadron version' shows this CLI's own
version, with no network.`,
		Example: `  hadron server-info
  hadron server-info --json      # {url, version, baseUrl, authenticated}
  hadron server-info --server https://hadron.internal.example --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, authenticated, err := f.PublicGraphQLClient()
			if err != nil {
				return err
			}
			server, err := f.Server()
			if err != nil {
				return err
			}
			resp, err := gen.ServerInfo(cmd.Context(), client)
			if err != nil {
				return api.MapError(err)
			}
			// serverInfo is declared ServerInfo! so a conformant server never
			// returns null without an error; guard the deref anyway.
			if resp == nil || resp.ServerInfo == nil {
				return exitcode.Newf(exitcode.Error, "server returned no serverInfo — is %s a hadron-server?", server)
			}
			dto := serverInfoDTO{
				URL:           server,
				Version:       resp.ServerInfo.Version,
				BaseURL:       resp.ServerInfo.BaseUrl,
				Authenticated: authenticated,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "hadron-server %s\n", dto.Version); err != nil {
					return err
				}
				if _, err := fmt.Fprintf(w, "  url:      %s\n", dto.URL); err != nil {
					return err
				}
				if _, err := fmt.Fprintf(w, "  base url: %s\n", dto.BaseURL); err != nil {
					return err
				}
				if dto.BaseURL != "" && !sameServer(dto.BaseURL, dto.URL) {
					if _, err := fmt.Fprintf(w,
						"  ! the server reports a different base url than the one queried —\n"+
							"    absolute URLs it emits (OAuth discovery, webhooks) will point at %s\n", dto.BaseURL); err != nil {
						return err
					}
				}
				if !dto.Authenticated {
					if _, err := fmt.Fprintf(w, "  (queried anonymously — server-info is public)\n"); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}
