package auth

// cli#878 revoke-page handoff: `hadron auth passkey revoke-page`, the address
// of the server's interactive revoke page (ServerInfo.passkeyRevokeUrl,
// hadron-server#4063; cor:aut:040:08, cor:api:020:01). The CLI prints it, or
// on an explicit --open in a terminal hands it to the platform launcher. It
// never revokes, and it never builds the URL: the value is used exactly as
// the server returned it, after validation (author contract
// issuecomment-6835152900, gate 6835278968).

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	authpkg "github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// PasskeyOpenBrowser is the launcher `revoke-page --open` calls: argv, no
// shell, a leading '-' refused (auth.OpenInBrowser). Tests swap it so no test
// ever navigates.
var PasskeyOpenBrowser = authpkg.OpenInBrowser

// PasskeyStdoutIsTerminal reports whether stdout is a terminal, the one
// condition under which --open may launch. A seam because the test streams
// can fake an input terminal but not an output one.
var PasskeyStdoutIsTerminal = func(streams *output.IOStreams) bool { return streams.IsTerminal() }

// The one field this read selects. A server before #4063 lacks it, and a
// switched-off server drops it from the schema; either way the query fails
// validation on this name alone, and the two cannot be told apart.
var passkeyRevokeSchemaNames = []string{"passkeyRevokeUrl"}

const passkeyRevokeNotAdvertised = "this server does not provide a passkey revoke page (it may be an older server, or passkey registration was switched off since the check)"

const passkeyRevokeInvalid = "the server returned an address that is not a valid, secure web address, so it was not printed or opened; tell the server operator"

// passkeyRevokePageDTO is the stable --json shape, named like the SDL field.
type passkeyRevokePageDTO struct {
	PasskeyRevokeURL string `json:"passkeyRevokeUrl"`
}

func newCmdPasskeyRevokePage(f *cmdutil.Factory) *cobra.Command {
	var open bool
	cmd := &cobra.Command{
		Use:   "revoke-page [--open]",
		Short: "Print the address of the server's passkey revoke page",
		Long: `Print the address of the server's passkey revoke page. On that page you prove
it's you, with another passkey or a linked identity provider, and choose which
passkey to remove. The address is the same for everyone. It names no passkey
and grants nothing, and this CLI removes nothing itself.

--open also asks your default browser to open it. It is refused (exit 2) with --json
or when output is not a terminal. Without --open, nothing is launched. A
launcher that starts is not proof that the page loaded.

The address is printed alone on one line; --json gives an object with one
key, passkeyRevokeUrl. No sign-in is needed. A server that does not offer
passkeys exits 2 before any query, and so does one whose passkey support
cannot be confirmed. A server that offers passkeys but does not advertise the
page also exits 2 (it may be an older server, or passkey registration was
switched off since the check). Both printing and --open refuse an insecure
server address (exit 2): use https, loopback for local development, or
HADRON_ALLOW_HTTP=1 for a trusted self-hosted server. If the server returns no
address, exit 1 says so. An address that is not a valid, secure web address
exits 1 and is never printed or opened.`,
		Example: `  hadron auth passkey revoke-page
  hadron auth passkey revoke-page --open
  hadron auth passkey revoke-page --json | jq -r .passkeyRevokeUrl`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPasskeyRevokePage(cmd.Context(), f, open, PasskeyStdoutIsTerminal(f.IOStreams))
		},
	}
	cmd.Flags().BoolVar(&open, "open", false, "also ask your default browser to open the page (terminal only, not with --json)")
	return cmd
}

func runPasskeyRevokePage(ctx context.Context, f *cmdutil.Factory, open, terminal bool) error {
	// --open is honoured only for text to a terminal, and refused loudly
	// otherwise — before any request, discovery included.
	if open && f.JSON {
		return exitcode.Newf(exitcode.Usage, "--open cannot be used with --json: --json only prints the address")
	}
	if open && !terminal {
		return exitcode.Newf(exitcode.Usage, "--open needs output to go to a terminal; nothing was launched. Drop --open to print the address")
	}
	server, err := f.Server()
	if err != nil {
		return err
	}
	u, parseErr := url.Parse(server)
	if parseErr != nil || !api.URLIsSecure(u) {
		return exitcode.Newf(exitcode.Usage, "refusing an insecure server address; use https, loopback for local development, or HADRON_ALLOW_HTTP=1 for a trusted self-hosted server; nothing was printed or opened")
	}
	caps, err := PasskeyCapabilities(ctx, server)
	if err != nil {
		return exitcode.Newf(exitcode.Usage, "could not confirm that this server offers passkeys (%s); no query was sent",
			oneLineTail(err.Error(), discoveryCauseMaxRunes))
	}
	if !caps[passkeyCapability] {
		return exitcode.Newf(exitcode.Usage, passkeysUnavailable)
	}
	// The server answers this field without a credential, and the URL grants
	// nothing: the public client requires none and keeps the existing
	// transport rules when one exists.
	client, _, err := f.PublicGraphQLClient()
	if err != nil {
		return err
	}
	resp, err := gen.PasskeyRevokeUrl(ctx, client)
	if err != nil {
		if api.IsGraphQLValidationOnlyFor(err, passkeyRevokeSchemaNames...) {
			return exitcode.Newf(exitcode.Usage, passkeyRevokeNotAdvertised)
		}
		return api.MapErrorKeepingCode(err)
	}
	if resp == nil || resp.ServerInfo == nil {
		return exitcode.Newf(exitcode.Error, "the server did not return a passkey revoke page address")
	}
	target := resp.ServerInfo.PasskeyRevokeUrl
	if target == "" {
		return exitcode.Newf(exitcode.Error, "the server did not return a passkey revoke page address")
	}
	if !validRevokeURL(target) {
		return exitcode.Newf(exitcode.Error, passkeyRevokeInvalid)
	}
	dto := passkeyRevokePageDTO{PasskeyRevokeURL: target}
	if open {
		if err := PasskeyOpenBrowser(target); err != nil {
			return exitcode.Newf(exitcode.Error, "browser launcher failed: %s; revoke page: %s",
				output.SafeLine(err.Error(), discoveryCauseMaxRunes), target)
		}
	}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		_, err := fmt.Fprintln(w, target)
		return err
	})
}

// validRevokeURL is the whole acceptance rule for the server's value; nothing
// is repaired or joined. An absolute http(s) URL with a host, no userinfo and
// no opaque part (node open's writeNodePortal rule), not starting with '-'
// (a launcher would read it as a flag; no http(s) URL can, so this only
// backs up OpenInBrowser's own refusal), with no control or format character
// (it is printed to a terminal verbatim; url.Parse refuses ASCII controls
// only before the '#', so an ESC in the fragment, a C1 control or a bidi
// override would pass it), and passing the CLI's own transport rule:
// https, http to a loopback host, or HADRON_ALLOW_HTTP=1. No same-origin check
// against --server: the value is the deployment's canonical baseUrl, which
// may differ from the API host the CLI dials (gate 6835278968).
func validRevokeURL(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") || output.SafeLine(s, len(s)+1) != s {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" || u.Hostname() == "" {
		return false
	}
	// Explicit even though URLIsSecure allow-lists the same two schemes today:
	// that rule is about where a credential may ride and could grow; this
	// page must stay a web page.
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return api.URLIsSecure(u)
}
