package connection

import (
	"context"
	"encoding/json"
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
	// fillTimeout bounds the one read that fills a finished connection's
	// details; the flow already succeeded, so it never decides the exit code.
	fillTimeout = 10 * time.Second
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
	FlowID        string        `json:"flowId"`
	BrowserOpened bool          `json:"browserOpened"`
	Status        string        `json:"status"`
	FailureCode   *string       `json:"failureCode"`
	Connection    *connectedDTO `json:"connection"`
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
	// The form an HTTP error keeps a response body in: JSON-escaped.
	for _, sec := range secrets {
		if q, err := json.Marshal(sec); err == nil {
			if esc := string(q[1 : len(q)-1]); esc != sec {
				secrets = append(secrets, esc)
			}
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
authorize URL in your browser and waits for that flow to finish. Finish consent
in the browser; the page it ends on tells you to return to the terminal. The
link is single-use, expires (the command says when) and is a credential until
opened once: treat it like a password.

The URL is shown exactly once. When the browser opens, only a notice is
printed; the URL itself is printed with --no-browser, or when the browser could
not be opened. With --json it is the "authorizeUrl" field of the one result
document and appears nowhere else; because that document is written when the
command finishes, --json with --no-browser needs --no-wait. Error output never
contains the URL or its nonce, and nothing is written to disk.

The wait follows this flow by its flow id (the result's "flowId"), so two flows
you start at once for the same provider each end with their own connection, and
reconnecting an account ends with the connection it already had. --wait
<duration> bounds the wait (default 5m, never past the link's expiry). If the
flow ends without a connection the command exits 1 and reports the flow's
failure code (for example access_denied). If nothing finishes in time it exits
13 with "not found yet": the consent may still be completing, so run
"hadron connection flow <flow-id>" (the id is in the start notice and in the result,
which is printed on a timeout too) or check "hadron email connections --org <org>" before
starting again. With --json, if the browser cannot be opened the command does
not wait: it returns the result (status "started") at once. --no-wait returns
right after the start; follow that flow with "hadron connection flow <flow-id>".`,
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
				return api.WithServerCode(api.MapError(err), err)
			}
			if resp == nil || resp.StartConnection == nil || resp.StartConnection.AuthorizeUrl == "" {
				return exitcode.Newf(exitcode.Unavailable, "server returned no connection start")
			}
			start := resp.StartConnection
			redact := redactor(start.AuthorizeUrl)
			ioStreams := f.IOStreams

			// An unreadable expiry would silently lift the "never past the link's
			// expiry" bound, so it is refused before the link is shown.
			expiry, perr := time.Parse(time.RFC3339, start.ExpiresAt)
			if perr != nil {
				return exitcode.Newf(exitcode.Unavailable, "server returned an unreadable link expiry; the link was not shown")
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
				ExpiresAt: start.ExpiresAt, FlowID: start.FlowId, BrowserOpened: opened, Status: "started"}
			switch {
			case opened:
				fmt.Fprintf(ioStreams.ErrOut, "Opened your browser to connect %s (flow %s). Finish consent there; the link expires %s.\n", output.SafeCell(rowKey), output.SafeCell(start.FlowId), output.SafeCell(start.ExpiresAt))
			case f.JSON:
				// The result document is the only place the URL may appear, and
				// it is written when the command ends: waiting would hold it
				// back until the link is useless. Hand it over now instead.
				fmt.Fprintln(ioStreams.ErrOut, "The browser could not be opened: not waiting; the link is in the result's authorizeUrl field.")
				noWait = true
			default:
				fmt.Fprintf(ioStreams.ErrOut, "Open this link in a browser to connect %s (flow %s; single-use, expires %s; treat it like a password):\n", output.SafeCell(rowKey), output.SafeCell(start.FlowId), output.SafeCell(start.ExpiresAt))
				fmt.Fprintf(ioStreams.Out, "%s\n", start.AuthorizeUrl)
			}

			if !noWait {
				deadline := Hooks.Now().Add(wait)
				if expiry.Before(deadline) {
					deadline = expiry
					fmt.Fprintf(ioStreams.ErrOut, "Waiting only until the link expires (%s), not the full --wait.\n", output.SafeCell(start.ExpiresAt))
				}
				res, err := awaitFlow(cmd.Context(), client, start.FlowId, start.OrgId, rowKey, deadline)
				if err != nil {
					var ce *exitcode.CodedError
					if errors.As(err, &ce) {
						if ce.Code == exitcode.WaitTimeout {
							// The flow was started and may still finish: hand over the
							// record (its flowId) before the error, so a --json caller
							// can follow it with "connection flow".
							dto.Status = "pending"
							if werr := writeConnect(ioStreams, f.JSON, dto, rowKey); werr != nil {
								return werr
							}
						}
						return scrub(err, redact)
					}
					return scrub(api.WithServerCode(api.MapError(err), err), redact)
				}
				if res.failureCode != "" {
					// The flow's own outcome is the result, not a transport error.
					code := res.failureCode
					dto.Status, dto.FailureCode = "failed", &code
					if err := writeConnect(ioStreams, f.JSON, dto, rowKey); err != nil {
						return err
					}
					return scrub(exitcode.Newf(exitcode.Error, "the connection flow failed: %s", output.SafeCell(code)), redact)
				}
				dto.Status = "connected"
				dto.Connection = res.connection
			}

			return writeConnect(ioStreams, f.JSON, dto, rowKey)
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
func listConnections(ctx context.Context, client graphql.Client, orgID string) ([]connectionRow, error) {
	r, err := gen.EmailConnections(ctx, client, orgID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, exitcode.Newf(exitcode.Unavailable, "server returned no connection list")
	}
	rows := make([]connectionRow, 0, len(r.ExchangeConnections))
	for _, v := range r.ExchangeConnections {
		if v == nil {
			continue
		}
		rows = append(rows, connectionRow{connectedDTO: connectedDTO{ID: v.Id, Provider: v.Provider, MailboxEmail: v.MailboxEmail, DisplayName: v.DisplayName}, userID: v.UserId})
	}
	return rows, nil
}

// connectionRow is a listed connection plus its owner, which only the match
// uses (the owner is not part of the --json result).
type connectionRow struct {
	connectedDTO
	userID string
}

// flowResult is where the wait ended: a connection (possibly with only its id
// when the follow-up read could not fill the rest) or the flow's failure code.
type flowResult struct {
	connection  *connectedDTO
	failureCode string
}

// awaitFlow polls connectionFlow(flowId) until it is CONNECTED or FAILED, or the
// deadline passes (exit 13, "not found yet"). PENDING is an answer, so only the
// deadline ends it. Each read is bounded by what is left of the wait at the
// moment it starts; it reads first and sleeps after. A refusal ends the wait at
// once, as the one refusal, and is not retried.
func awaitFlow(ctx context.Context, client graphql.Client, flowID, orgID, rowKey string, deadline time.Time) (*flowResult, error) {
	for {
		remaining := deadline.Sub(Hooks.Now())
		if remaining <= 0 {
			return nil, notFoundYet(rowKey, orgID, flowID)
		}
		pctx, cancel := context.WithTimeout(ctx, remaining)
		fl, err := readFlow(pctx, client, flowID)
		expired := pctx.Err() != nil && ctx.Err() == nil
		cancel()
		if err != nil {
			if expired {
				return nil, notFoundYet(rowKey, orgID, flowID)
			}
			return nil, err
		}
		switch fl.Status {
		case gen.ConnectionFlowStatusFailed:
			code := ""
			if fl.FailureCode != nil {
				code = *fl.FailureCode
			}
			if code == "" {
				code = "unknown"
			}
			return &flowResult{failureCode: code}, nil
		case gen.ConnectionFlowStatusConnected:
			if fl.ConnectionId == nil || *fl.ConnectionId == "" {
				return nil, exitcode.Newf(exitcode.Unavailable, "server reported the flow connected without a connection id")
			}
			return &flowResult{connection: fillConnection(ctx, client, orgID, *fl.ConnectionId, rowKey)}, nil
		}
		remaining = deadline.Sub(Hooks.Now())
		if remaining <= 0 {
			return nil, notFoundYet(rowKey, orgID, flowID)
		}
		if err := Hooks.Sleep(ctx, min(connectPollEvery, remaining)); err != nil {
			return nil, exitcode.Newf(exitcode.Error, "interrupted while waiting for the connection: %v", err)
		}
	}
}

// readFlow is one connectionFlow read. A FORBIDDEN is mapped to the single
// refusal whatever the server's message says, so an unknown, an expired and
// another user's flow id cannot be told apart (cor:dmo:080:04).
func readFlow(ctx context.Context, client graphql.Client, flowID string) (*gen.ConnectionFlowConnectionFlow, error) {
	r, err := gen.ConnectionFlow(ctx, client, flowID)
	if err != nil {
		if api.ServerCodeOf(err) == "FORBIDDEN" {
			return nil, flowRefusal()
		}
		return nil, err
	}
	if r == nil {
		return nil, exitcode.Newf(exitcode.Unavailable, "server returned no connection flow")
	}
	return r.ConnectionFlow, nil
}

func flowRefusal() error {
	return api.WithCode(exitcode.Newf(exitcode.Forbidden,
		"no such connection flow for you: the id is unknown, expired (flows last one hour) or not yours to read"), "FORBIDDEN")
}

// fillConnection reads the org's connection list for the one row the flow made
// (the flow answer carries only its id), within fillTimeout. If that read fails or the row is not
// listed, the result still reports the id: the established success is never lost.
func fillConnection(ctx context.Context, client graphql.Client, orgID, id, rowKey string) *connectedDTO {
	bare := &connectedDTO{ID: id, Provider: rowKey}
	rctx, cancel := context.WithTimeout(ctx, fillTimeout)
	defer cancel()
	rows, err := listConnections(rctx, client, orgID)
	if err != nil {
		return bare
	}
	for _, r := range rows {
		if r.ID == id {
			c := r.connectedDTO
			return &c
		}
	}
	return bare
}

// writeConnect renders the connect result (human or --json).
func writeConnect(ioStreams *output.IOStreams, asJSON bool, dto connectDTO, rowKey string) error {
	return output.Write(ioStreams, asJSON, dto, func(w io.Writer) error {
		switch {
		case dto.FailureCode != nil:
			_, err := fmt.Fprintf(w, "✗ %s connection failed: %s (flow %s)\n", output.SafeCell(rowKey), output.SafeCell(*dto.FailureCode), output.SafeCell(dto.FlowID))
			return err
		case dto.Status == "pending":
			_, err := fmt.Fprintf(w, "%s connection flow %s has not finished (link expires %s); follow it with: hadron connection flow %s\n",
				output.SafeCell(rowKey), output.SafeCell(dto.FlowID), output.SafeCell(dto.ExpiresAt), output.SafeCell(dto.FlowID))
			return err
		case dto.Connection == nil:
			_, err := fmt.Fprintf(w, "started %s connection for org %s (flow %s, link expires %s); follow it with: hadron connection flow %s\n",
				output.SafeCell(rowKey), output.SafeCell(dto.OrgID), output.SafeCell(dto.FlowID), output.SafeCell(dto.ExpiresAt), output.SafeCell(dto.FlowID))
			return err
		}
		_, err := fmt.Fprintf(w, "✓ connected %s (%s) as %s\n",
			output.SafeCell(dto.Connection.Provider), output.SafeCell(dto.Connection.MailboxEmail), output.SafeCell(dto.Connection.ID))
		return err
	})
}

// scrub makes sure nothing raised after the link was received can carry it.
// It always detaches: an outer message that looks safe can sit over a cause
// (a response body, an extension field, a joined branch) that holds the link,
// so redacting only a changed message would leave that graph reachable. The
// result keeps the redacted text, exit code and server code, and no raw error.
func scrub(err error, redact func(string) string) error {
	return api.Detached(err, redact)
}

func notFoundYet(rowKey, orgID, flowID string) error {
	return exitcode.Newf(exitcode.WaitTimeout,
		"not found yet: the %s connection flow has not finished; consent may still be completing — check \"hadron connection flow %s\" or \"hadron email connections --org %s\" before starting again", rowKey, flowID, orgID)
}
