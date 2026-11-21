package org

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#2512 — `hadron org github` (a #67 parity slice, gap #60).
//
// Invariants this file keeps (each pinned in internal/cmd/org_github_2512_test.go):
//   - Each verb sends exactly ONE mutation with the org ref as typed (the
//     server resolves id or URN), and never retries: a destructive unlink or a
//     credential-bearing set-app must not be re-sent on a lost answer.
//   - The private key is WRITE-ONLY: file or stdin only (cmdutil.ReadSecretInput,
//     never an inline flag), never in any output, --json, error text or error
//     chain, and redacted out of any metadata a server erroneously reflects.
//     It is multi-line PEM: interior newlines travel byte-exact, and the
//     single-line masked prompt is refused (a pasted PEM would truncate).
//   - unlink is destructive: it prompts on a terminal and refuses
//     non-interactively without --yes, before any request.
//   - No verb looks the org up first: the ref goes to the server as given, and
//     the server's own access check (org ADMIN or platform ADMIN) answers.

// orgGithubDTO is the stable --json shape of all three verbs. It carries the
// server's github fields only; key material is never a field (hasGithubAppPrivateKey
// says whether one is stored).
type orgGithubDTO struct {
	ID                     string  `json:"id"`
	URN                    string  `json:"urn"`
	GithubAppID            *string `json:"githubAppId"`
	GithubInstallationID   *int    `json:"githubInstallationId"`
	HasGithubAppPrivateKey bool    `json:"hasGithubAppPrivateKey"`
}

func orgGithubDTOFrom(fields gen.OrgGithubFields) orgGithubDTO {
	return orgGithubDTO{
		ID: fields.Id, URN: fields.Urn,
		GithubAppID: fields.GithubAppId, GithubInstallationID: fields.GithubInstallationId,
		HasGithubAppPrivateKey: fields.HasGithubAppPrivateKey,
	}
}

func writeOrgGithub(w io.Writer, d orgGithubDTO) error {
	appID, installation := "(unset)", "(unset)"
	if d.GithubAppID != nil {
		appID = output.SafeCell(*d.GithubAppID)
	}
	if d.GithubInstallationID != nil {
		installation = strconv.Itoa(*d.GithubInstallationID)
	}
	_, err := fmt.Fprintf(w, "github app on %s (%s)\n  github app id: %s\n  installation id: %s\n  private key stored: %s\n",
		output.SafeCell(d.URN), output.SafeCell(d.ID), appID, installation, yesNo(d.HasGithubAppPrivateKey))
	return err
}

// githubAppWriteError keeps a write-only credential out of every diagnostic:
// with a key in play the raw error chain is never retained, and even the
// server's own code must not reflect it. Same contract as senderWriteError.
func githubAppWriteError(err error, secret *string) error {
	// An empty secret can appear in no diagnostic, and Contains(x, "") is
	// trivially true — guarding only nil let an empty key withhold every
	// server code. Unreachable via ReadSecretInput (empty is refused); this
	// guards the function's own contract (#2694 D4).
	if secret == nil || *secret == "" {
		return api.MapErrorKeepingCode(err)
	}
	mapped := api.MapError(err)
	safe := exitcode.Newf(exitcode.FromError(mapped), "GitHub App write failed; private key and server diagnostic withheld (check access, org ref, app id and server availability)")
	if strings.Contains(api.ServerCodeOf(err), *secret) {
		return safe
	}
	return api.WithServerCode(safe, err)
}

// redactGithubKey defends against an erroneous server reflecting the
// credential in returned Organization metadata: every string field the DTO
// carries is scrubbed, so the key can never reach an output stream.
func redactGithubKey(d *orgGithubDTO, key string) {
	if key == "" {
		return // unreachable via ReadSecretInput (empty is refused); ReplaceAll with "" would shred every field
	}
	replace := func(s string) string { return strings.ReplaceAll(s, key, "[REDACTED]") }
	d.ID = replace(d.ID)
	d.URN = replace(d.URN)
	if d.GithubAppID != nil {
		s := replace(*d.GithubAppID)
		d.GithubAppID = &s
	}
}

func newCmdGithub(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github <command>",
		Short: "Manage an organization's GitHub App link and credentials",
		Long: `Link the organization's GitHub App installation, set its credentials, or
remove the whole GitHub App configuration. These commands are for an
organization's ADMINs and the platform's ADMINs — that check is the server's,
made on every call (an unknown org and a caller without access both get one
refusal).

The CLI does not run the browser-mediated GitHub App installation: installing
the App on a GitHub account or organization happens on GitHub, and the
installation id it produces is what ` + "`link`" + ` takes. Setting credentials
(` + "`set-app`" + `) stores the App id and its private key on the server,
encrypted at rest; the key is write-only input (a file or stdin — never an
inline flag, and never a masked prompt: the key is multi-line PEM, so on a
terminal the file is required) and is never shown, logged or returned — only
whether one is stored.
Removing the link (` + "`unlink`" + `) removes ALL GitHub App config from the
organization.

  link      connect an installation id to the org
  unlink    remove the org's GitHub App configuration (destructive)
  set-app   set the GitHub App id and private key

The org is an id or URN passed to the server as typed. Every command prints
the resulting githubAppId, githubInstallationId and hasGithubAppPrivateKey;
--json returns {id, urn, githubAppId, githubInstallationId,
hasGithubAppPrivateKey}.`,
		Example: `  hadron org github link acme.com --installation-id 998877
  hadron org github set-app acme.com --app-id 12345 --private-key-file ./app.key
  hadron org github unlink acme.com`,
	}
	cmd.AddCommand(newCmdGithubLink(f))
	cmd.AddCommand(newCmdGithubUnlink(f))
	cmd.AddCommand(newCmdGithubSetApp(f))
	return cmd
}

func newCmdGithubLink(f *cmdutil.Factory) *cobra.Command {
	var installationID int
	cmd := &cobra.Command{
		Use:   "link <org> --installation-id <n>",
		Short: "Connect a GitHub App installation to an organization",
		Long: `Link the organization to a GitHub App installation by its installation id.
The CLI does not run the install flow: install the Hadron GitHub App on your
GitHub account or organization first (GitHub → Settings → Applications, or the
portal's guided flow), and pass the installation id that produces. The server
checks access (org ADMIN or platform ADMIN) and records the installation on
the organization.

The org is an id or URN, passed as typed. --installation-id must be a positive
integer. One mutation is sent, never retried. On success the org's
githubAppId, githubInstallationId and hasGithubAppPrivateKey are printed;
--json returns {id, urn, githubAppId, githubInstallationId,
hasGithubAppPrivateKey}.`,
		Example: `  hadron org github link acme.com --installation-id 998877
  hadron org github link hrn:org:acme.com --installation-id 998877 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			org := strings.TrimSpace(args[0])
			if org == "" {
				return exitcode.Newf(exitcode.Usage, "org must not be empty (an id or URN)")
			}
			if installationID < 1 || installationID > math.MaxInt32 {
				return exitcode.Newf(exitcode.Usage, "--installation-id must be a positive integer (a GraphQL Int, at most 2147483647)")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.LinkGithubApp(cmd.Context(), client, org, installationID)
			if err != nil {
				return githubAppWriteError(err, nil)
			}
			if resp == nil || resp.LinkGithubApp == nil {
				return exitcode.Newf(exitcode.Error, "server returned no organization; whether the app was linked is unknown — check with the portal")
			}
			dto := orgGithubDTOFrom(resp.LinkGithubApp.OrgGithubFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeOrgGithub(w, dto)
			})
		},
	}
	cmd.Flags().IntVar(&installationID, "installation-id", 0, "GitHub App installation id to link, a positive integer (required)")
	_ = cmd.MarkFlagRequired("installation-id")
	return cmd
}

func newCmdGithubUnlink(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "unlink <org>",
		Short: "Remove an organization's GitHub App configuration (requires confirmation)",
		Long: `Remove ALL GitHub App config from the organization: the installation link and
the stored App credentials (app id and private key). This is destructive and
cannot be undone from the CLI — linking again is a fresh ` + "`link`" + ` (and
` + "`set-app`" + ` if credentials are needed again). The server checks access
(org ADMIN or platform ADMIN).

Prompts on a terminal; pass --yes to confirm non-interactively. The org is an
id or URN, passed as typed. One mutation is sent, never retried. On success
the org prints with githubAppId, githubInstallationId unset and
hasGithubAppPrivateKey false; --json returns {id, urn, githubAppId,
githubInstallationId, hasGithubAppPrivateKey}.`,
		Example: `  hadron org github unlink acme.com
  hadron org github unlink acme.com --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			org := strings.TrimSpace(args[0])
			if org == "" {
				return exitcode.Newf(exitcode.Usage, "org must not be empty (an id or URN)")
			}
			// SafeCell: the user-typed ref reaches a terminal prompt (signing
			// does the same for its <org>).
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "the GitHub App configuration of org "+output.SafeCell(org)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.UnlinkGithubApp(cmd.Context(), client, org)
			if err != nil {
				return githubAppWriteError(err, nil)
			}
			if resp == nil || resp.UnlinkGithubApp == nil {
				return exitcode.Newf(exitcode.Error, "server returned no organization; the link state is unknown — check with the portal")
			}
			dto := orgGithubDTOFrom(resp.UnlinkGithubApp.OrgGithubFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeOrgGithub(w, dto)
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm removal without prompting")
	return cmd
}

func newCmdGithubSetApp(f *cmdutil.Factory) *cobra.Command {
	var appID, keyFile string
	cmd := &cobra.Command{
		Use:   "set-app <org> --app-id <id> [--private-key-file <path|->]",
		Short: "Set an organization's GitHub App id and private key",
		Long: `Set the GitHub App credentials the organization authenticates with: the App
id and the App's private key. The server stores them encrypted at rest; the
key is WRITE-ONLY — read from --private-key-file (a path or - for stdin), or
from piped stdin when the flag is omitted. There is no masked prompt and no
flag that takes the key as an inline value: the key is multi-line PEM, which
a single-line prompt would silently truncate, so on a terminal
--private-key-file is required. The key
is never shown, logged or returned; only hasGithubAppPrivateKey says one is
stored. The server checks access (org ADMIN or platform ADMIN).

Both the app id and the key are set together on every call (there is no
keyless variant). The org is an id or URN, passed as typed. One mutation is
sent, never retried. On success the org's githubAppId,
githubInstallationId and hasGithubAppPrivateKey are printed; --json returns
{id, urn, githubAppId, githubInstallationId, hasGithubAppPrivateKey}.`,
		Example: `  hadron org github set-app acme.com --app-id 12345 --private-key-file ./github-app.key
  hadron org github set-app acme.com --app-id 12345 --private-key-file - < github-app.key`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			org := strings.TrimSpace(args[0])
			if org == "" {
				return exitcode.Newf(exitcode.Usage, "org must not be empty (an id or URN)")
			}
			appID = strings.TrimSpace(appID)
			if appID == "" {
				return exitcode.Newf(exitcode.Usage, "--app-id must not be empty")
			}
			changed := cmd.Flags().Changed
			if changed("private-key-file") && keyFile == "" {
				return exitcode.Newf(exitcode.Usage, "--private-key-file requires a path or -")
			}
			// A GitHub App private key is multi-line PEM. The masked prompt is
			// single-line (term.ReadPassword returns at the first newline), so a
			// key pasted there would be silently truncated and stored broken:
			// refuse the mode instead of offering it (#2512 BLIND P2-1).
			if keyFile == "" && f.IOStreams.IsInputTerminal() {
				return exitcode.Newf(exitcode.Usage, "--private-key-file is required on a terminal: the private key is multi-line PEM a single-line masked prompt would truncate (a path, or - for stdin)")
			}
			key, err := cmdutil.ReadSecretInput(f.IOStreams, keyFile, "private-key-file", "GitHub App private key", "private key")
			if err != nil {
				return api.MapError(err)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.UpdateOrgGithubApp(cmd.Context(), client, org, appID, key)
			if err != nil {
				return githubAppWriteError(err, &key)
			}
			if resp == nil || resp.UpdateOrgGithubApp == nil {
				return exitcode.Newf(exitcode.Error, "server returned no organization; whether the credentials were stored is unknown — check the org in the portal (the CLI has no github read verb)")
			}
			dto := orgGithubDTOFrom(resp.UpdateOrgGithubApp.OrgGithubFields)
			// Defend against an erroneous server reflecting the credential in
			// metadata.
			redactGithubKey(&dto, key)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeOrgGithub(w, dto)
			})
		},
	}
	cmd.Flags().StringVar(&appID, "app-id", "", "the GitHub App's numeric id (required)")
	_ = cmd.MarkFlagRequired("app-id")
	cmd.Flags().StringVar(&keyFile, "private-key-file", "", "read the write-only private key (multi-line PEM) from a file or - (stdin); omit to read piped stdin — on a terminal the file is required (no masked prompt)")
	return cmd
}
