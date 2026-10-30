package connection

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const (
	connectDefaultWait = 5 * time.Minute
	connectPollEvery   = 3 * time.Second
)

// Hooks are the process seams `connection connect` touches: the browser
// launcher, the clock and the wait between polls. Production values are the
// real ones; tests swap them so a poll loop runs without real time or a real
// browser.
var Hooks = struct {
	Open  func(string) error
	Sleep func(context.Context, time.Duration) error
	Now   func() time.Time
}{
	Open: auth.OpenInBrowser,
	Sleep: func(ctx context.Context, d time.Duration) error {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	},
	Now: time.Now,
}

// connectProviders maps the spellings a person types to the server's
// ConnectionProvider enum, and each enum value to the string a connection row
// carries in its `provider` field (the two differ: GOOGLE_CAL vs google-cal).
var connectProviders = map[string]struct{ enum, rowKey string }{
	"ms_exchange":  {"MS_EXCHANGE", "ms-exchange"},
	"microsoft":    {"MS_EXCHANGE", "ms-exchange"},
	"exchange":     {"MS_EXCHANGE", "ms-exchange"},
	"gmail":        {"GMAIL", "gmail"},
	"google_cal":   {"GOOGLE_CAL", "google-cal"},
	"google_drive": {"GOOGLE_DRIVE", "google-drive"},
}

const connectProviderList = "ms-exchange, gmail, google-cal, google-drive"

func parseConnectProvider(s string) (enum, rowKey string, err error) {
	key := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "-", "_")
	p, ok := connectProviders[key]
	if !ok {
		return "", "", exitcode.Newf(exitcode.Usage, "unknown provider %q (valid: %s)", s, connectProviderList)
	}
	return p.enum, p.rowKey, nil
}

// connectedDTO is the connection the wait found.
type connectedDTO struct {
	ID           string  `json:"id"`
	Provider     string  `json:"provider"`
	MailboxEmail string  `json:"mailboxEmail"`
	DisplayName  *string `json:"displayName"`
}

// connectDTO is the stable --json shape. authorizeUrl is the ONLY place the
// credential-bearing URL appears in any output (cor:api:240:02).
type connectDTO struct {
	Provider      string        `json:"provider"`
	OrgID         string        `json:"orgId"`
	AuthorizeURL  string        `json:"authorizeUrl"`
	ExpiresAt     string        `json:"expiresAt"`
	BrowserOpened bool          `json:"browserOpened"`
	Status        string        `json:"status"`
	Connection    *connectedDTO `json:"connection"`
}

// codedError carries the server's code to the --json error envelope (the root
// handler reads it through ServerCode); the message is the mapped error's.
type codedError struct {
	error
	code string
}

func (c *codedError) Unwrap() error      { return c.error }
func (c *codedError) ServerCode() string { return c.code }

func withServerCode(mapped, raw error) error {
	if code := api.ServerCodeOf(raw); code != "" {
		return &codedError{error: mapped, code: code}
	}
	return mapped
}

// redactor scrubs the authorize URL and the nonce inside it from any text
// about to leave the process (error and notice output).
func redactor(authorizeURL string) func(string) string {
	secrets := []string{authorizeURL}
	if u, err := url.Parse(authorizeURL); err == nil {
		for _, vs := range u.Query() {
			for _, v := range vs {
				if len(v) >= 8 {
					secrets = append(secrets, v)
				}
			}
		}
		if i := strings.LastIndex(u.Path, "/"); i >= 0 && len(u.Path[i+1:]) >= 8 {
			secrets = append(secrets, u.Path[i+1:])
		}
	}
	// Longest first, so the full URL goes before the pieces inside it.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return func(s string) string {
		for _, sec := range secrets {
			s = strings.ReplaceAll(s, sec, "<redacted>")
		}
		return s
	}
}

func newCmdConnect(f *cmdutil.Factory) *cobra.Command {
	var (
		org               string
		noBrowser, noWait bool
		wait              time.Duration
	)
	cmd := &cobra.Command{
		Use:   "connect <provider> --org <org>",
		Short: "Connect an org's mailbox, calendar or Drive (org ADMIN, signed in as yourself)",
		Long: `Start the OAuth flow that connects a Microsoft 365 mailbox, a Gmail mailbox, a
Google Calendar or a Google Drive to an organization, without the portal.

<provider> is one of: ` + connectProviderList + `.
--org is the organization's ID or hrn:org:<slug> URN.

Who may run it: a signed-in user acting as themselves who is an ADMIN of the
org (a login from "hadron auth login" or a user API key). An App key, a bound
worker session, an impersonated request and an OAuth credential limited to the
MCP surface are refused, and so is a caller who is not an org ADMIN. The server
decides; the CLI enforces nothing of its own.

What happens: the command asks the server to start the flow, opens the
authorize URL in your browser and waits for the new connection to appear in the
org's connection list. Finish consent in the browser; the page it ends on tells
you to return to the terminal. The link is single-use, expires (the command
says when) and is a credential until opened once: treat it like a password.

The URL is shown exactly once. When the browser opens, only a notice is
printed; the URL itself is printed with --no-browser, or when the browser could
not be opened. With --json it is the "authorizeUrl" field of the one result
document and appears nowhere else; because that document is written when the
command finishes, --json with --no-browser needs --no-wait. Error output never
contains the URL or its nonce, and nothing is written to disk.

--wait <duration> bounds the wait for the new connection (default 5m, never
past the link's expiry). A connection that already existed before the start
does not count. --no-wait returns right after the start. If nothing appears in
time the command exits 13 with "not found yet": the consent may still be
completing, so check "hadron email connections --org <org>" before starting
again.`,
		Example: `  hadron connection connect gmail --org hrn:org:acme.com
  hadron connection connect ms-exchange --org acme.com --no-browser
  hadron connection connect google-drive --org hrn:org:acme.com --no-wait --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			enum, rowKey, err := parseConnectProvider(args[0])
			if err != nil {
				return err
			}
			org = strings.TrimSpace(org)
			if org == "" {
				return exitcode.Newf(exitcode.Usage, "give a nonempty --org id or fully-qualified URN (check for an unset variable)")
			}
			if noWait && cmd.Flags().Changed("wait") {
				return exitcode.Newf(exitcode.Usage, "--wait and --no-wait are mutually exclusive")
			}
			if !noWait && wait <= 0 {
				return exitcode.Newf(exitcode.Usage, "--wait must be a positive duration (use --no-wait to skip the wait)")
			}
			// Refused before the start, so no single-use link is minted that
			// the caller could never see.
			if f.JSON && noBrowser && !noWait {
				return exitcode.Newf(exitcode.Usage,
					"--json with --no-browser needs --no-wait: the URL is only in the result document, which is written when the wait ends")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			resp, err := gen.StartConnection(cmd.Context(), client, gen.ConnectionProvider(enum), org)
			if err != nil {
				return withServerCode(api.MapError(err), err)
			}
			if resp == nil || resp.StartConnection == nil || resp.StartConnection.AuthorizeUrl == "" {
				return exitcode.Newf(exitcode.Unavailable, "server returned no connection start")
			}
			start := resp.StartConnection
			redact := redactor(start.AuthorizeUrl)
			ioStreams := f.IOStreams

			// The "before" snapshot is taken BEFORE the link is shown, so a
			// connection that appears cannot predate it by consent timing.
			baseline := map[string]bool{}
			if !noWait {
				rows, err := listConnections(cmd.Context(), client, start.OrgId)
				if err != nil {
					return withServerCode(api.MapError(err), err)
				}
				for _, r := range rows {
					baseline[r.ID] = true
				}
			}

			opened := false
			if !noBrowser {
				if err := Hooks.Open(start.AuthorizeUrl); err != nil {
					fmt.Fprintf(ioStreams.ErrOut, "(could not open the browser: %s)\n", redact(err.Error()))
				} else {
					opened = true
				}
			}
			dto := connectDTO{Provider: string(start.Provider), OrgID: start.OrgId, AuthorizeURL: start.AuthorizeUrl,
				ExpiresAt: start.ExpiresAt, BrowserOpened: opened, Status: "started"}
			switch {
			case opened:
				fmt.Fprintf(ioStreams.ErrOut, "Opened your browser to connect %s. Finish consent there; the link expires %s.\n", output.SafeCell(rowKey), output.SafeCell(start.ExpiresAt))
			case f.JSON:
				fmt.Fprintln(ioStreams.ErrOut, "The link was not opened and is in the result's authorizeUrl field; with --json rerun with --no-wait to receive it before consent.")
			default:
				fmt.Fprintf(ioStreams.ErrOut, "Open this link in a browser to connect %s (single-use, expires %s; treat it like a password):\n", output.SafeCell(rowKey), output.SafeCell(start.ExpiresAt))
				fmt.Fprintf(ioStreams.Out, "%s\n", start.AuthorizeUrl)
			}

			if !noWait {
				deadline := Hooks.Now().Add(wait)
				if exp, perr := time.Parse(time.RFC3339, start.ExpiresAt); perr == nil && exp.Before(deadline) {
					deadline = exp
					fmt.Fprintf(ioStreams.ErrOut, "Waiting only until the link expires (%s), not the full --wait.\n", output.SafeCell(start.ExpiresAt))
				}
				found, err := awaitConnection(cmd.Context(), client, start.OrgId, rowKey, baseline, deadline)
				if err != nil {
					var ce *exitcode.CodedError
					if errors.As(err, &ce) {
						return err
					}
					return withServerCode(api.MapError(err), err)
				}
				dto.Status = "connected"
				dto.Connection = found
			}

			return output.Write(ioStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.Connection == nil {
					_, err := fmt.Fprintf(w, "started %s connection for org %s (link expires %s); confirm with: hadron email connections --org %s\n",
						output.SafeCell(rowKey), output.SafeCell(dto.OrgID), output.SafeCell(dto.ExpiresAt), output.SafeCell(dto.OrgID))
					return err
				}
				_, err := fmt.Fprintf(w, "✓ connected %s (%s) as %s\n",
					output.SafeCell(dto.Connection.Provider), output.SafeCell(dto.Connection.MailboxEmail), output.SafeCell(dto.Connection.ID))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization ID or hrn:org:<slug> URN (required)")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the authorize URL instead of opening a browser")
	cmd.Flags().DurationVar(&wait, "wait", connectDefaultWait, "how long to wait for the new connection to appear (never past the link's expiry)")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "return right after the start without waiting for the connection")
	_ = cmd.MarkFlagRequired("org")
	return cmd
}

// listConnections reads the org's connection metadata (never credentials or
// lastError: the operation does not select them).
func listConnections(ctx context.Context, client graphql.Client, orgID string) ([]connectedDTO, error) {
	r, err := gen.EmailConnections(ctx, client, orgID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, exitcode.Newf(exitcode.Unavailable, "server returned no connection list")
	}
	rows := make([]connectedDTO, 0, len(r.ExchangeConnections))
	for _, v := range r.ExchangeConnections {
		if v == nil {
			continue
		}
		rows = append(rows, connectedDTO{ID: v.Id, Provider: v.Provider, MailboxEmail: v.MailboxEmail, DisplayName: v.DisplayName})
	}
	return rows, nil
}

// awaitConnection polls until a connection for rowKey that is not in baseline
// appears, or the deadline passes (exit 13, "not found yet").
func awaitConnection(ctx context.Context, client graphql.Client, orgID, rowKey string, baseline map[string]bool, deadline time.Time) (*connectedDTO, error) {
	for {
		remaining := deadline.Sub(Hooks.Now())
		if remaining <= 0 {
			return nil, notFoundYet(rowKey, orgID)
		}
		if err := Hooks.Sleep(ctx, min(connectPollEvery, remaining)); err != nil {
			return nil, exitcode.Newf(exitcode.Error, "interrupted while waiting for the connection: %v", err)
		}
		// The wait bounds the in-flight read too, not only the gaps between
		// reads: a hung poll cannot outlast --wait.
		pctx, cancel := context.WithTimeout(ctx, remaining)
		rows, err := listConnections(pctx, client, orgID)
		expired := pctx.Err() != nil && ctx.Err() == nil
		cancel()
		if err != nil {
			if expired {
				return nil, notFoundYet(rowKey, orgID)
			}
			return nil, err
		}
		for _, r := range rows {
			if r.Provider == rowKey && !baseline[r.ID] {
				c := r
				return &c, nil
			}
		}
	}
}

func notFoundYet(rowKey, orgID string) error {
	return exitcode.Newf(exitcode.WaitTimeout,
		"not found yet: no new %s connection appeared; consent may still be completing — check \"hadron email connections --org %s\" before starting again", rowKey, orgID)
}
