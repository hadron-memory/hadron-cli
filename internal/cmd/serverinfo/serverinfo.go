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
		Long: `Report the identity of the server this CLI is configured to reach.

Works SIGNED OUT: the server answers this query publicly, so it doubles as a
reachability probe — a failure reaching the server is the server or the
network, not a missing login. Credentials are still sent when present, and a
credential STORE that cannot be read is still reported (it fails loud rather
than silently querying anonymously).

'version' is the server's API-surface CONTRACT version, bumped when the
tool/query surface changes in a caller-visible way. It is NOT the server's
release version, so don't compare it against a release tag; use it to decide
whether a surface you depend on exists.

'url' is where this invocation sent the query; 'baseUrl' is what the server
reports as its own public base URL. They should agree — a mismatch means the
server is behind a proxy without its public base URL configured, which makes
every absolute URL it emits (OAuth discovery, webhooks) point somewhere wrong.

` + "`hadron version`" + ` reports the CLI build instead, and needs no network.`,
		Example: `  hadron server-info
  hadron server-info --json
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
