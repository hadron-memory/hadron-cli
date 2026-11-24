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
	"github.com/hadron-memory/hadron-cli/internal/instruction"
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

// localGrantPromptScope resolves the App before an instructed interactive
// revocation asks for consent. It makes no request: a legacy memory-only binding
// needs an explicit/configured App (ruling #116489). The returned scope is also
// the mutation target; a later binding/config change must not retarget consent.
func localGrantPromptScope(cmd *cobra.Command, f *cmdutil.Factory) (appScope, error) {
	ref, err := f.App()
	if err != nil {
		return appScope{}, err
	}
	if ref != "" {
		if f.AppFlag != "" {
			return appScope{Ref: ref, Source: "from --app"}, nil
		}
		return appScope{Ref: ref, Source: "from the App context", Ambient: true}, nil
	}
	b, _, err := readBindingFile(cmd.Context())
	if err != nil {
		return appScope{}, exitcode.Newf(exitcode.Usage, "cannot determine the target App locally; pass --app <ref> or set a configured App: %v", err)
	}
	if b != nil && b.AppID != "" {
		return appScope{Ref: b.AppID, Source: "from the worktree binding", Ambient: true}, nil
	}
	return appScope{}, exitcode.Newf(exitcode.Usage, "instructed interactive grant rm needs --app <ref>, a configured App, or a binding with an AppID; a legacy team-memory-only binding cannot resolve the App before consent")
}

type appGrantDTO struct {
	ID        string  `json:"id"`
	AppID     string  `json:"appId"`
	WorkerID  string  `json:"workerId"`
	Name      string  `json:"name"`
	GrantedAt string  `json:"grantedAt"`
	RevokedAt *string `json:"revokedAt"`
}

// appGrantHelpIntro opens both add and rm; the verb-specific paragraph follows.
const appGrantHelpIntro = `A named App grant is one permission, from a short fixed list, held by one
worker in an App. It is separate from worker action grants ('hadron team grant
request') and from organization action grants ('hadron grant').`

const appGrantAddHelp = `

add is person-only: the CLI always sends it as the signed-in person, never as a
worker session, so a worker cannot give itself a grant. The server refuses a
caller who does not manage the App (FORBIDDEN, exit 8), so a person who manages
the App must run it. Giving a grant the worker already holds changes nothing.`

const appGrantRmHelp = `

rm takes a permission away; it takes effect at the worker's next call. By
default it is sent as the signed-in person, so a person who manages the App must
run it (the server refuses anyone else with FORBIDDEN, exit 8). Only rm accepts
--instruction, for an AI worker acting on its holder's say-so (described below).`

const appGrantHelp = `

The names are a closed list:
  TICKER_SUBSCRIBE_WORKERS  subscribe and unsubscribe other workers of the App,
                            at any cadence.
  WORKERS_STAFF             headless casting, retiring and deleting of workers;
                            the holder's own staffing rights still apply.
  APP_MEMBERS               headless App-member add and remove; the holder's own
                            member-management rights still apply.
The last two are recorded now; the server may not yet enforce them against a
headless action, and this CLI adds no gating of its own.

Pass the worker as an id or URN; it goes to the server as given. --app selects
the App; otherwise the configured App or the binding supplies it. --json prints
the returned grant: id, appId, workerId, name, grantedAt and revokedAt (null
until revoked). That returned grant is the only confirmation: no command lists
named App grants yet.`

func newCmdAppGrant(f *cmdutil.Factory, remove bool) *cobra.Command {
	verb, short := "add", "Give a worker a named App grant"
	if remove {
		verb, short = "rm", "Revoke a worker's named App grant"
	}
	var yes bool
	lead := appGrantAddHelp
	if remove {
		lead = appGrantRmHelp
	}
	c := &cobra.Command{Use: verb + " <worker-ref> <name>", Short: short, Long: appGrantHelpIntro + lead + appGrantHelp, Example: "  hadron team grant " + verb + " hrn:worker:acme.com:eng:iris WORKERS_STAFF --app acme.com:eng", Args: cobra.ExactArgs(2)}
	if remove {
		c.Long += "\n\nrm prompts on a terminal; otherwise pass --yes.\nWith --instruction and a prompt, the App is selected once from --app, the\nconfigured App, or the binding AppID; that exact App is confirmed and sent.\nA legacy binding with only teamMemory needs --app or a configured App for\nthis instructed interactive path (exit 2 before any prompt or request).\nThe plain path and instructed --yes path retain their existing scope resolution."
		c.Flags().BoolVar(&yes, "yes", false, "skip the revocation confirmation")
		// The shared body says "Needs server 2026-11-04 or later", so the flag
		// states the same version (cli#2607 item 4), not the commit.
		instruction.AddManagerFlag(c, managerInstructionMinimum)
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		var claim *gen.ActInstructionInput
		var instructed InstructedWrite
		if remove {
			var err error
			claim, instructed, err = managerInstruction(cmd, f)
			if err != nil {
				return err
			}
		}
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
		var s appScope
		var client graphql.Client
		var err error
		if remove && claim != nil && !yes {
			s, err = localGrantPromptScope(cmd, f)
			if err != nil {
				return err
			}
			if err := cmdutil.Confirm(f.IOStreams, false, fmt.Sprintf("Revoke grant %s from worker %s in App %s?", output.SafeCell(args[1]), output.SafeCell(args[0]), output.SafeCell(s.Ref))); err != nil {
				return err
			}
			// Keep the existing post-consent ended-session advisory, but never
			// use this fresh binding read to resolve the confirmed target again.
			// Local binding errors belong to the attribution recheck: the known
			// target still permits the instructedAsPerson fallback.
			_, _, _ = readBinding(cmd.Context())
			client, err = f.GraphQLClient()
		} else {
			s, client, err = controlsClient(cmd, f)
		}
		if err != nil {
			return err
		}
		label := lazyAppLabel(cmd.Context(), f, s)
		if remove && !yes && claim == nil {
			if e := cmdutil.Confirm(f.IOStreams, false, fmt.Sprintf("Revoke grant %s from worker %s in App %s?", output.SafeCell(args[1]), output.SafeCell(args[0]), label())); e != nil {
				return e
			}
		}
		var g *gen.TeamAppGrantFields
		if remove {
			ctx, e := managerWriteContext(cmd, s.Ref, claim, instructed)
			if e != nil {
				return e
			}
			r, e := revokeManagerAppGrant(ctx, client, s.Ref, args[0], name, claim)
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
	root := &cobra.Command{Use: "ticker <command>", Short: "App ticker controls", Long: `A ticker is a recurring wake: a worker subscribed to one is woken on its
cadence to take a turn — the platform's own alternative to an external cron
poking an agent. This group holds the App's ticker settings. Around it:
` + "`hadron team wakes`" + ` lists and acknowledges the wakes due now;
` + "`hadron team wake-settings`" + ` shows a worker's subscriptions and pause;
` + "`hadron team pause`" + ` pauses the App's regular wakes.`}
	settings := &cobra.Command{Use: "settings <command>", Short: "Show or change the App's ticker settings"}
	settings.AddCommand(newCmdTeamTickerSettingsGet(f))
	c := &cobra.Command{Use: "set", Short: "Change only the ticker setting fields supplied", Long: `Change App ticker settings: --min-cadence-seconds (the floor no ticker in
this App may tick faster than), --live-cap (how many tickers may be live at
once), --self-subscribe-cadence-seconds (the cadence bounds for a worker
subscribing itself), --wake-daily-budget (the wakes the App may spend per
day). The App comes from --app, the App context or the worktree binding (the
examples show --app first, then the App context). Without --instruction, this uses your ordinary
authenticated caller with no worker session or instruction. With the flag,
the verified target-App worker session and holder's claim are used as described
below; otherwise the claim goes as the person. The server decides admission.
Omitted fields preserve their values; null is not accepted. The server checks
bounds and conflicts, never the CLI: minimum cadence floor 60 seconds,
live cap 1–100, and a nonnegative daily wake budget. The self-subscription
cadence must be between the shared floor and 86400 seconds; raising the
floor above an existing ticker's cadence is refused. One more check names a
value this command has no flag for, because it lives elsewhere: delivery's stuck
window (` + "`hadron team delivery-settings`" + `) must exceed its busy delay plus
this floor. These are current server limits, not client-side authorization or
validation.

--clear-wake-daily-budget returns to a roster-derived budget; =false means
"do not clear", the same as omitting it, and is not sent. A set that names no
field (including one carrying only =false) is refused before any request,
because it would change nothing.
Standby is not a ticker setting; there is no standby switch to configure
here. To read the current values, use
` + "`hadron team ticker settings get`" + `.
On success the output is the App line and one settings line ending
` + "`(derived: true|false)`" + ` (see ` + "`settings get`" + ` for the fields' meaning).
--json prints minCadenceSeconds, liveCap, selfSubscribeCadenceSeconds,
dailyWakeBudget and dailyWakeBudgetDerived.`, Example: "  hadron team ticker settings set --live-cap 10 --app acme.com:eng\n  hadron team ticker settings set --clear-wake-daily-budget", Args: cobra.NoArgs}
	var raw [4]string
	names := []string{"min-cadence-seconds", "live-cap", "self-subscribe-cadence-seconds", "wake-daily-budget"}
	var clear bool
	for i, name := range names {
		c.Flags().StringVar(&raw[i], name, "", "whole number; omitted preserves, server checks limits")
	}
	c.Flags().BoolVar(&clear, "clear-wake-daily-budget", false, "return to derived budget (=false is the same as omitting it)")
	addManagerInstruction(c)
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		claim, instructed, err := managerInstruction(cmd, f)
		if err != nil {
			return err
		}
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
		// The server reads clearWakeDailyBudget:false as "don't clear", the same
		// as omitting it, so an explicit false names nothing: it is treated as
		// absent and a set carrying only it is refused below (cli#1986).
		if cmd.Flags().Changed("clear-wake-daily-budget") && clear {
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
		ctx, e := managerWriteContext(cmd, s.Ref, claim, instructed)
		if e != nil {
			return e
		}
		r, e := setManagerTickerSettings(ctx, client, s.Ref, vals[0], vals[1], vals[2], vals[3], cp, claim)
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

// tickerSettingsText renders the settings for the human branch. The daily
// wake budget says only whether the server derived it (cli#1985, cor:api:240:01):
// no formula or default is named, because the CLI never computes it.
func tickerSettingsText(w io.Writer, d tickerSettingsDTO) error {
	origin := "set"
	if d.DailyWakeBudgetDerived {
		origin = "derived"
	}
	_, e := fmt.Fprintf(w, "ticker settings: minimum cadence %ds; live cap %d; self-subscribe cadence %ds; daily wake budget %d (%s)\n", d.MinCadenceSeconds, d.LiveCap, d.SelfSubscribeCadenceSeconds, d.DailyWakeBudget, origin)
	return e
}

func newCmdTeamTickerSettingsGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the App's ticker settings",
		Long: `Show the App's ticker settings as the server returns them, with what each
number governs: the minimum cadence floor (no ticker in this App may tick
faster than it) and the self-subscription cadence (the cadence bounds for a
worker subscribing itself), in seconds; the live-ticker cap (how many tickers
may be live at once); and the daily wake budget (the wakes the App may spend
per day), marked "derived" when the server computed it from the roster rather
than the App setting it; the CLI never works it out itself. Any App
participant may read. Output is one line, for example
  ticker settings: minimum cadence 60s; live cap 20; self-subscribe cadence 300s; daily wake budget 500 (derived)
To change a value, use
` + "`hadron team ticker settings set`" + `.
--json prints minCadenceSeconds, liveCap, selfSubscribeCadenceSeconds,
dailyWakeBudget and dailyWakeBudgetDerived.`,
		Example: "  hadron team ticker settings get --app acme.com:eng\n  hadron team ticker settings get --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, client, e := controlsClient(cmd, f)
			if e != nil {
				return e
			}
			r, e := gen.TeamTickerSettingsGet(cmd.Context(), client, s.Ref)
			if e != nil {
				return api.MapError(e)
			}
			if r == nil || r.AppTickerSettings == nil {
				return exitcode.Newf(exitcode.Error, "server returned no ticker settings")
			}
			v := r.AppTickerSettings
			label := lazyAppLabel(cmd.Context(), f, s)
			d := tickerSettingsDTO{
				MinCadenceSeconds:           v.MinCadenceSeconds,
				LiveCap:                     v.LiveCap,
				SelfSubscribeCadenceSeconds: v.SelfSubscribeCadenceSeconds,
				DailyWakeBudget:             v.DailyWakeBudget,
				DailyWakeBudgetDerived:      v.DailyWakeBudgetDerived,
			}
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				if _, e := fmt.Fprintf(w, "app: %s\n", label()); e != nil {
					return e
				}
				return tickerSettingsText(w, d)
			})
		},
	}
}
