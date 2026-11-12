package auth

// cli#878 part 1: `hadron auth passkey list`, the caller's own passkey records
// over Query.myPasskeys (hadron-server#1631 PR3, cor:aut:040:05). List only:
// removing a passkey is an interactive proof on the server's own page
// (cor:aut:040:08), and no surface advertises that page's URL, so the CLI
// offers no revoke and builds no URL (#878 gate 6831823066; the URL companion
// is hadron-server#4051).

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// passkeysUnavailable is the one answer for a server that does not offer
// passkeys: the switch is off (the capability is omitted), discovery failed,
// or the schema dropped the read between discovery and the query.
const passkeysUnavailable = "this server does not offer passkeys"

// passkeyCapability is the /capabilities token for the registration switch
// (hadron-server lib/signing/surface.ts). myPasskeys and its types are in the
// schema only while it is on.
const passkeyCapability = "passkey-registration"

// PasskeyCapabilities reads GET /capabilities. Package-level so tests can
// count or fail it (TestPasskeyListDiscoveryErrorIsOneLine injects an error);
// the default is the cached, unauthenticated api.Capabilities.
var PasskeyCapabilities = api.Capabilities

// PasskeyKeyMaterialNames are the fields the server withholds over a bearer
// (hadron-server resolvers.passkeys.ts, #1631 §10): this read never selects
// or prints them. It is the ONE list both guards read, the operation-selection
// unit test here and the command-level --json key walk, so they cannot drift.
var PasskeyKeyMaterialNames = []string{"credentialId", "publicKey", "ip", "ipAddress", "userAgent"}

// discoveryCauseMaxRunes bounds the discovery error quoted in the refusal.
// Real causes are client-composed (the --server URL, then the reason last:
// "connection refused", a timeout, an HTTP status), so the bound is wide
// enough to keep the reason behind a long URL.
const discoveryCauseMaxRunes = 1024

// passkeyNow is the clock the state derivation reads. Injected by tests.
var passkeyNow = time.Now

// The field a switched-off server drops (deploymentSchema.ts removes it with
// its types). A query on a missing root field fails validation on that field
// alone ("Cannot query field \"myPasskeys\" on type \"Query\""), so an
// envelope naming only it maps to "does not offer passkeys". The type names
// are deliberately absent: a validation error on one of THEIR fields (a field
// this build selects that the server lacks) is real schema skew, and its
// message names the type.
var passkeySchemaNames = []string{"myPasskeys"}

// Passkey states. Derived on the CLI's clock (see derivePasskeyState).
const (
	passkeyActive     = "active"
	passkeyCoolingOff = "cooling-off"
	passkeyRevoked    = "revoked"
)

// passkeyDTO is the stable --json shape. It mirrors the server's
// PasskeyCredential field names and adds the derived state. It carries no
// credential id, public key or cancel IP/user agent: the server never sends
// them on this read, and the operation does not select them.
type passkeyDTO struct {
	ID             string            `json:"id"`
	Label          string            `json:"label"`
	State          string            `json:"state"`
	RegisteredAt   string            `json:"registeredAt"`
	LastUsedAt     *string           `json:"lastUsedAt"`
	RevokedAt      *string           `json:"revokedAt"`
	Window         *passkeyWindowDTO `json:"window"`
	Cancel         *passkeyCancelDTO `json:"cancel"`
	Transports     []string          `json:"transports"`
	BackupEligible bool              `json:"backupEligible"`
	BackedUp       bool              `json:"backedUp"`
}

type passkeyWindowDTO struct {
	EndsAt             string  `json:"endsAt"`
	EndedAt            *string `json:"endedAt"`
	Outcome            *string `json:"outcome"`
	RegistrationMethod *string `json:"registrationMethod"`
}

type passkeyCancelDTO struct {
	At string `json:"at"`
}

type passkeyRow = gen.MyPasskeysMyPasskeysPasskeyCredential

// toPasskeyDTO maps one server row at now. transports is normalized to [] so
// the contract never renders null, even from a server that broke its own
// non-null SDL (a robustness guard, not a shape the server sends).
func toPasskeyDTO(r *passkeyRow, now time.Time) passkeyDTO {
	d := passkeyDTO{
		ID:             r.Id,
		Label:          r.Label,
		RegisteredAt:   r.RegisteredAt,
		LastUsedAt:     r.LastUsedAt,
		RevokedAt:      r.RevokedAt,
		Transports:     []string{},
		BackupEligible: r.BackupEligible,
		BackedUp:       r.BackedUp,
	}
	if r.Transports != nil {
		d.Transports = r.Transports
	}
	if w := r.Window; w != nil {
		d.Window = &passkeyWindowDTO{EndsAt: w.EndsAt, EndedAt: w.EndedAt, Outcome: w.Outcome, RegistrationMethod: w.RegistrationMethod}
	}
	if c := r.Cancel; c != nil {
		d.Cancel = &passkeyCancelDTO{At: c.At}
	}
	d.State = derivePasskeyState(d, now)
	return d
}

// derivePasskeyState is presentation, not server authority. Rule 2 is the
// server's own open-window test (hadron-server lib/signing/state.ts:45,
// windowOpen = windowEndedAt === null && now < windowEndsAt, at 2755d34ad):
//  1. revokedAt set: revoked (it wins over any window, a cancel included);
//  2. a window that has not ended (endedAt null) and whose endsAt is after
//     now: cooling-off. An endsAt the CLI cannot parse also reads cooling-off,
//     because claiming the window is over would be the stronger, unproved
//     statement;
//  3. otherwise active, including a window past its endsAt whose outcome is
//     still null (the SDL: "a window that ran out reads by its end time").
//
// The window length is a server setting, so nothing here assumes one.
func derivePasskeyState(d passkeyDTO, now time.Time) string {
	if d.RevokedAt != nil {
		return passkeyRevoked
	}
	if w := d.Window; w != nil && w.EndedAt == nil {
		ends, err := time.Parse(time.RFC3339Nano, w.EndsAt)
		if err != nil || ends.After(now) {
			return passkeyCoolingOff
		}
	}
	return passkeyActive
}

func newCmdPasskey(f *cmdutil.Factory) *cobra.Command {
	// The help points at "the server's own pages" without a URL on purpose:
	// no surface advertises the revoke page's address yet (hadron-server#4051),
	// and the CLI builds none (#878 gate 6831823066).
	cmd := &cobra.Command{
		Use:   "passkey <command>",
		Short: "Inspect your passkeys",
		Long: `Inspect the passkeys registered to your account, on servers that offer
passkeys. The CLI reads them only. Registering and removing a passkey are
interactive ceremonies on the server's own pages, not CLI commands.`,
	}
	cmd.AddCommand(newCmdPasskeyList(f))
	return cmd
}

func newCmdPasskeyList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List your passkeys",
		Long: `List the passkeys registered to your account, newest first, revoked ones
included. Shows labels and dates only, never key material: no credential id,
no public key.

A first passkey (one registered when you had no other) starts in a
cooling-off window, at least 72 hours long and set by the server. Until it
ends, signatures the passkey makes are kept as pending and don't count; the
passkey can still sign you in. The window runs out, or a fresh sign-in by a
different method, on the server's own page, ends it early; if the passkey is
revoked first, its pending signatures never count. A later passkey has no
window: WINDOW ENDS shows -.

STATE is revoked for a revoked passkey, cooling-off while a first passkey's
cooling-off window is still open (it has not ended and WINDOW ENDS is ahead),
and active otherwise. It is worked out on this machine's clock and grants
nothing: if this machine's clock is wrong, so is STATE.

--json gives an array of id (the record's id, not the credential id), label,
state, registeredAt, lastUsedAt, revokedAt, window (endsAt, endedAt, outcome,
registrationMethod; null for a later passkey), cancel (at; null if never
cancelled), transports, backupEligible and backedUp. An empty list is [].

Requires your own user login: an App key exits 3, an impersonation session
exits 8. A server that does not offer passkeys exits 2 before any query, and
so does one whose passkey support cannot be confirmed (its message says so).

Removing a passkey happens on the server's interactive revoke page, where you
prove it's you with another passkey or a linked identity provider and choose
the passkey there. This CLI cannot remove a passkey.`,
		Example: `  hadron auth passkey list
  hadron auth passkey list --json | jq -r '.[] | select(.state == "active") | .label'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPasskeyList(cmd.Context(), f)
		},
	}
}

func runPasskeyList(ctx context.Context, f *cmdutil.Factory) error {
	// Discovery first, unauthenticated: a switched-off server, or one whose
	// discovery fails (unreachable, a non-200 such as an older server's 404, a
	// bad body), answers 2 with zero GraphQL requests, credential or not.
	server, err := f.Server()
	if err != nil {
		return err
	}
	caps, err := PasskeyCapabilities(ctx, server)
	if err != nil {
		// Unknown is not "no" (review:assert-the-value-the-user-gets): same
		// exit and the same zero queries, as the gate rules, but the sentence
		// says discovery failed rather than that the feature is absent.
		// The cause is flattened to one bounded line. api.Capabilities never
		// quotes the response body (an HTTP status, a parse error or a
		// transport error), so this is defence in depth: whatever the cause
		// text becomes, the refusal stays one line with its tail.
		return exitcode.Newf(exitcode.Usage, "could not confirm that this server offers passkeys (%s); no query was sent",
			output.SafeLine(err.Error(), discoveryCauseMaxRunes))
	}
	if !caps[passkeyCapability] {
		return exitcode.Newf(exitcode.Usage, passkeysUnavailable)
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	resp, err := gen.MyPasskeys(ctx, client)
	if err != nil {
		// The switch went off after discovery: the schema no longer has the
		// read. Only an envelope of validation errors on exactly these names
		// is that case; anything else keeps its own mapping and server code.
		if api.IsGraphQLValidationOnlyFor(err, passkeySchemaNames...) {
			return exitcode.Newf(exitcode.Usage, passkeysUnavailable)
		}
		return api.MapErrorKeepingCode(err)
	}
	now := passkeyNow()
	passkeys := []passkeyDTO{}
	if resp != nil {
		for _, r := range resp.MyPasskeys {
			if r == nil {
				continue
			}
			passkeys = append(passkeys, toPasskeyDTO(r, now))
		}
	}
	return output.Write(f.IOStreams, f.JSON, passkeys, func(w io.Writer) error {
		if len(passkeys) == 0 {
			_, err := fmt.Fprintln(w, "No passkeys.")
			return err
		}
		t := output.NewTable(w, "LABEL", "STATE", "REGISTERED", "LAST USED", "WINDOW ENDS", "ID")
		for _, p := range passkeys {
			windowEnds := "-"
			if p.Window != nil {
				windowEnds = p.Window.EndsAt
			}
			t.Row(output.SafeCell(p.Label), p.State, output.SafeCell(p.RegisteredAt),
				output.SafeCell(orText(p.LastUsedAt, "never")), output.SafeCell(windowEnds), output.SafeCell(p.ID))
		}
		return t.Flush()
	})
}
