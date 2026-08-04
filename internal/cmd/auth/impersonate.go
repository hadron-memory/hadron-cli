package auth

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	authpkg "github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type impersonateResult struct {
	Status    string `json:"status"`
	SessionID string `json:"sessionId,omitempty"`
	Target    string `json:"target,omitempty"`
	Org       string `json:"org,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// newCmdImpersonate implements `hadron auth impersonate` — start or stop a
// read-only admin impersonation session (support/diagnostics). The returned
// short-TTL token is filed under a host-derived impersonation key and
// preferred by Factory.Token(), so every subsequent command runs read-only as
// the target until `--stop` (or the token expires).
func newCmdImpersonate(f *cmdutil.Factory) *cobra.Command {
	var org, reason string
	var stop, yes bool

	cmd := &cobra.Command{
		Use:   "impersonate <user>",
		Short: "View Hadron read-only as another member of your org (admin support)",
		Long: "Start a read-only impersonation session as another member of an\n" +
			"organization you administer (ADMIN/OWNER), for support and diagnostics.\n" +
			"The session is scoped to that one org, cannot mutate anything, and\n" +
			"expires automatically. Run with --stop to end it early.",
		Args: func(cmd *cobra.Command, args []string) error {
			if stop {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if stop {
				return runStopImpersonation(cmd, f)
			}
			if org == "" {
				return exitcode.Newf(exitcode.Usage, "--org is required to start impersonation")
			}
			return runStartImpersonation(cmd, f, args[0], org, reason, yes)
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "Organization to scope the session to (id or URN)")
	cmd.Flags().StringVar(&reason, "reason", "", "Optional support/diagnostic reason (audited)")
	cmd.Flags().BoolVar(&stop, "stop", false, "End the current impersonation session")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

func runStartImpersonation(cmd *cobra.Command, f *cmdutil.Factory, userRef, org, reason string, yes bool) error {
	// HADRON_TOKEN outranks the stored impersonation token in Factory.Token(),
	// so starting a session while it is exported would report a READ-ONLY
	// session that isn't: every later command would silently run with the
	// admin credential, mutations included. Refuse rather than mislead
	// (PR #345 review, P1).
	if os.Getenv(store.EnvToken) != "" {
		return exitcode.Newf(exitcode.Usage,
			"%s is set and takes precedence over an impersonation session — unset it before impersonating, "+
				"otherwise later commands would run as you, not the target", store.EnvToken)
	}

	// An impersonation session must not stack on another — Factory.Token()
	// would already be handing out the existing impersonation token.
	server, err := f.Server()
	if err != nil {
		return err
	}
	if authpkg.ResolveImpersonationToken(f.TokenStore(), server) != "" {
		return exitcode.Newf(exitcode.Usage,
			"already impersonating — run `hadron auth impersonate --stop` first")
	}

	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	// Resolve the target EXACTLY (a partial match is a usage error, not a
	// silent retarget) so the confirmation names precisely who will be viewed.
	user, found, err := cmdutil.ResolveUserExactly(cmd, client, userRef)
	if err != nil {
		return err
	}
	if !found {
		return exitcode.Newf(exitcode.NotFound, "no user matched %q", userRef)
	}
	if err := cmdutil.Confirm(f.IOStreams, yes,
		fmt.Sprintf("Start a read-only impersonation session as %s in org %s?",
			cmdutil.DescribeUser(user), org)); err != nil {
		return err
	}

	var reasonArg *string
	if reason != "" {
		reasonArg = &reason
	}
	resp, err := gen.StartImpersonation(cmd.Context(), client, org, user.Id, reasonArg)
	if err != nil {
		return api.MapError(err)
	}
	result := resp.StartImpersonation
	// File the token under the impersonation key (beside, not over, the admin's
	// own credential) so Factory.Token() prefers it until stop/expiry.
	impKey := authpkg.ImpersonationHostKey(server)
	if err := f.TokenStore().Set(impKey, result.Token); err != nil {
		return fmt.Errorf("storing the impersonation token: %w", err)
	}
	// Clear the key from the OTHER backend (the auth login precedent): if
	// keychain availability flips between runs, a stale copy left in the
	// backend we didn't write could resurrect a dead session. Non-fatal — the
	// token we just wrote is the live one either way.
	if err := store.ClearExcept(f.TokenStore(), impKey); err != nil {
		fmt.Fprintf(f.IOStreams.ErrOut,
			"warning: could not clear a stale impersonation token from the other credential store: %v\n", err)
	}

	targetLabel := user.Id
	if result.Session != nil && result.Session.TargetUser != nil {
		targetLabel = cmdutil.DescribeUser(user)
	}
	orgLabel := org
	if result.Session != nil && result.Session.Organization != nil {
		orgLabel = result.Session.Organization.Name
	}
	dto := impersonateResult{Status: "started"}
	if result.Session != nil {
		dto.SessionID = result.Session.Id
		dto.ExpiresAt = result.Session.ExpiresAt
	}
	dto.Target = targetLabel
	dto.Org = orgLabel
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		_, err := fmt.Fprintf(w,
			"Impersonating %s in %s (read-only). Expires %s.\nRun `hadron auth impersonate --stop` to end.\n",
			targetLabel, orgLabel, dto.ExpiresAt)
		return err
	})
}

func runStopImpersonation(cmd *cobra.Command, f *cmdutil.Factory) error {
	server, err := f.Server()
	if err != nil {
		return err
	}
	key := authpkg.ImpersonationHostKey(server)
	token, err := f.TokenStore().Get(key)
	// Distinguish "nothing filed" from "the store is unreadable" — reporting a
	// corrupt auth.json as "no active session" would leave a live session the
	// user believes is stopped (PR #345 review).
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("reading the stored impersonation token: %w", err)
	}
	if token == "" {
		dto := impersonateResult{Status: "no_active_session"}
		return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
			_, err := fmt.Fprintln(w, "No active impersonation session.")
			return err
		})
	}

	// Best-effort server-side stop (the session row is the revocation source of
	// truth; an expired token just means the session already lapsed). The live
	// impersonation token is still filed, so f.GraphQLClient() resolves it and
	// the stop self-terminates that session. Either way we clear the local
	// token below.
	if authpkg.ImpersonationTokenLive(token) {
		server, err := f.Server()
		if err == nil {
			if client, err := api.NewClient(server, token, f.HTTPClient); err == nil {
				if _, err := gen.StopImpersonation(cmd.Context(), client, nil); err != nil {
					fmt.Fprintf(f.IOStreams.ErrOut,
						"warning: server stop failed (%v); the session will lapse by TTL\n", api.MapError(err))
				}
			}
		}
	}
	// Purge from EVERY backend, not just the one resolved now — the same
	// cross-backend rule `auth logout` follows. Deleting only the selected
	// store can leave a live token that reappears when availability flips.
	if _, err := store.Purge(key); err != nil {
		return fmt.Errorf("clearing the impersonation token: %w", err)
	}

	dto := impersonateResult{Status: "stopped"}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		_, err := fmt.Fprintln(w, "Impersonation ended — restored your own session.")
		return err
	})
}
