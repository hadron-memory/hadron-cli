package team

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

func controlsClient(cmd *cobra.Command, f *cmdutil.Factory) (appScope, graphql.Client, error) {
	b, err := readBindingOrNilWithApp(cmd.Context(), f)
	if err != nil {
		return appScope{}, nil, err
	}
	s, err := resolveTeamAppScope(cmd.Context(), f, b)
	if err != nil {
		return s, nil, err
	}
	c, err := f.GraphQLClient()
	return s, c, err
}

type appGrantDTO struct {
	ID        string  `json:"id"`
	AppID     string  `json:"appId"`
	WorkerID  string  `json:"workerId"`
	Name      string  `json:"name"`
	GrantedAt string  `json:"grantedAt"`
	RevokedAt *string `json:"revokedAt"`
}

const appGrantHelp = `Manage named App grants, separate from worker action-grant requests and
organization action grants. The server checks who may manage the App.
add is person-only; these commands use your ordinary authenticated caller,
not this worktree's worker session. No instruction is sent.

Names follow the server's closed list:
TICKER_SUBSCRIBE_WORKERS: subscribe and unsubscribe other workers of the App,
at any cadence. WORKERS_STAFF: named permission for headless casting,
retiring and deleting workers; the holder's own staffing rights still apply.
APP_MEMBERS: named permission for headless App-member add/remove; the
holder's own member-management rights still apply. These last two grants
are provisioned before the separate server#3308 enforcement rollout; this
CLI claims no additional gating effect. add is idempotent; revocation takes
effect at the grantee's next call. Worker references are passed to the server
as given (use a worker id or URN). --app selects the App; otherwise the
configured App or binding supplies it. --json prints the returned grant's
id, appId, workerId, name, grantedAt and nullable revokedAt.`

func newCmdAppGrant(f *cmdutil.Factory, remove bool) *cobra.Command {
	verb, short := "add", "Give a worker a named App grant"
	if remove {
		verb, short = "rm", "Revoke a worker's named App grant"
	}
	var yes bool
	c := &cobra.Command{Use: verb + " <worker-ref> <name>", Short: short, Long: appGrantHelp, Example: "  hadron team grant " + verb + " hrn:worker:acme.com:eng:iris WORKERS_STAFF --app acme.com:eng", Args: cobra.ExactArgs(2)}
	if remove {
		c.Long += "\n\nrm prompts on a terminal; otherwise pass --yes."
		c.Flags().BoolVar(&yes, "yes", false, "skip the revocation confirmation")
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(args[0]) == "" {
			return exitcode.Newf(exitcode.Usage, "worker reference must not be empty")
		}
		name := gen.AppGrantName(args[1])
		if !slices.Contains(gen.AllAppGrantName, name) {
			known := make([]string, 0, len(gen.AllAppGrantName))
			for _, n := range gen.AllAppGrantName {
				known = append(known, string(n))
			}
			return exitcode.Newf(exitcode.Usage, "name must be one of %s", strings.Join(known, ", "))
		}
		if remove && !yes && !f.IOStreams.IsInputTerminal() {
			return cmdutil.Confirm(f.IOStreams, false, "")
		}
		s, client, err := controlsClient(cmd, f)
		if err != nil {
			return err
		}
		label := lazyAppLabel(cmd.Context(), f, s)
		if remove && !yes {
			if e := cmdutil.Confirm(f.IOStreams, false, fmt.Sprintf("Revoke grant %s from worker %s in App %s?", output.SafeCell(args[1]), output.SafeCell(args[0]), label())); e != nil {
				return e
			}
		}
		var g *gen.TeamAppGrantFields
		if remove {
			r, e := gen.TeamAppGrantRm(cmd.Context(), client, s.Ref, args[0], name)
			if e != nil {
				return api.MapError(e)
			}
			if r != nil && r.RevokeAppGrant != nil {
				g = &r.RevokeAppGrant.TeamAppGrantFields
			}
		} else {
			r, e := gen.TeamAppGrantAdd(cmd.Context(), client, s.Ref, args[0], name)
			if e != nil {
				return api.MapError(e)
			}
			if r != nil && r.GrantApp != nil {
				g = &r.GrantApp.TeamAppGrantFields
			}
		}
		if g == nil || g.App == nil || g.Worker == nil {
			return exitcode.Newf(exitcode.Error, "server returned no App grant")
		}
		d := appGrantDTO{ID: g.Id, AppID: g.App.Id, WorkerID: g.Worker.Id, Name: string(g.Name), GrantedAt: g.GrantedAt, RevokedAt: g.RevokedAt}
		return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
			if _, e := fmt.Fprintf(w, "app: %s\n", label()); e != nil {
				return e
			}
			_, e := fmt.Fprintf(w, "App grant %s: %s for worker %s in App %s; granted %s; revoked %s\n", output.SafeCell(d.ID), output.SafeCell(d.Name), output.SafeCell(d.WorkerID), output.SafeCell(d.AppID), output.SafeCell(d.GrantedAt), output.SafeCell(strOr(d.RevokedAt)))
			return e
		})
	}
	return c
}

type tickerSettingsDTO struct {
	MinCadenceSeconds           int  `json:"minCadenceSeconds"`
	LiveCap                     int  `json:"liveCap"`
	SelfSubscribeCadenceSeconds int  `json:"selfSubscribeCadenceSeconds"`
	DailyWakeBudget             int  `json:"dailyWakeBudget"`
	DailyWakeBudgetDerived      bool `json:"dailyWakeBudgetDerived"`
}

func newCmdTeamTicker(f *cmdutil.Factory) *cobra.Command {
	root := &cobra.Command{Use: "ticker <command>", Short: "App ticker controls"}
	settings := &cobra.Command{Use: "settings <command>", Short: "Change the App's ticker settings"}
	c := &cobra.Command{Use: "set", Short: "Change only the ticker setting fields supplied", Long: `Change App ticker settings using your ordinary authenticated caller.
The server checks manager permission; no worker session or instruction is sent.
Omitted fields preserve their values; null is not accepted. The server checks
bounds and conflicts, never the CLI: minimum cadence floor 60 seconds,
live cap 1–100, and a nonnegative daily wake budget. The self-subscription
cadence must be between the shared floor and 86400 seconds; raising the
floor above an existing ticker's cadence is refused. Delivery's stuck window
must exceed busy delay plus the cadence floor. These are current server
limits, not client-side authorization or validation.

--clear-wake-daily-budget returns to a roster-derived budget; explicit
=false is sent as false. A set that names no field is refused before any
request, because it would be a manager-attributed write that changes nothing.
No standby switch is available.
--json prints minCadenceSeconds, liveCap, selfSubscribeCadenceSeconds,
dailyWakeBudget and dailyWakeBudgetDerived.`, Example: "  hadron team ticker settings set --live-cap 10 --app acme.com:eng\n  hadron team ticker settings set --clear-wake-daily-budget", Args: cobra.NoArgs}
	var raw [4]string
	names := []string{"min-cadence-seconds", "live-cap", "self-subscribe-cadence-seconds", "wake-daily-budget"}
	var clear bool
	for i, name := range names {
		c.Flags().StringVar(&raw[i], name, "", "whole number; omitted preserves, server checks limits")
	}
	c.Flags().BoolVar(&clear, "clear-wake-daily-budget", false, "return to derived budget (explicit false is preserved)")
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		var vals [4]*int
		for i, name := range names {
			if cmd.Flags().Changed(name) {
				v, e := strconv.ParseInt(raw[i], 10, 32)
				if e != nil {
					if strings.EqualFold(strings.TrimSpace(raw[i]), "null") {
						return exitcode.Newf(exitcode.Usage, "--%s takes a whole number; null is not accepted (omit the flag to preserve the value)", name)
					}
					return exitcode.Newf(exitcode.Usage, "--%s takes a whole number that fits 32 bits, got %q", name, raw[i])
				}
				n := int(v)
				vals[i] = &n
			}
		}
		var cp *bool
		if cmd.Flags().Changed("clear-wake-daily-budget") {
			cp = &clear
		}
		// An update that names nothing is refused rather than sent, as in
		// `team role update`: a bare set would be a manager-attributed write
		// that changes nothing (cli#1980 BLIND).
		if cp == nil && vals == [4]*int{} {
			return exitcode.Newf(exitcode.Usage, "nothing to change — pass at least one of --%s or --clear-wake-daily-budget; nothing was sent", strings.Join(names, ", --"))
		}
		s, client, e := controlsClient(cmd, f)
		if e != nil {
			return e
		}
		r, e := gen.TeamTickerSettingsSet(cmd.Context(), client, s.Ref, vals[0], vals[1], vals[2], vals[3], cp)
		if e != nil {
			return api.MapError(e)
		}
		if r == nil || r.SetAppTickerSettings == nil {
			return exitcode.Newf(exitcode.Error, "server returned no ticker settings")
		}
		v := r.SetAppTickerSettings
		label := lazyAppLabel(cmd.Context(), f, s)
		d := tickerSettingsDTO{v.MinCadenceSeconds, v.LiveCap, v.SelfSubscribeCadenceSeconds, v.DailyWakeBudget, v.DailyWakeBudgetDerived}
		return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
			if _, e := fmt.Fprintf(w, "app: %s\n", label()); e != nil {
				return e
			}
			_, e := fmt.Fprintf(w, "ticker settings: minimum cadence %ds; live cap %d; self-subscribe cadence %ds; daily wake budget %d (derived: %t)\n", d.MinCadenceSeconds, d.LiveCap, d.SelfSubscribeCadenceSeconds, d.DailyWakeBudget, d.DailyWakeBudgetDerived)
			return e
		})
	}
	settings.AddCommand(c)
	root.AddCommand(settings)
	return root
}
