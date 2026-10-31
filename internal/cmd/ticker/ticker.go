// Package ticker exposes the server's named ticker schedules. It does not
// compute due wakes or enforce server authorization/cadence policy.
package ticker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	urn "github.com/hadron-memory/urn-lib-go"
	"github.com/spf13/cobra"
)

type identityDTO struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	URN  *string `json:"urn"`
}
type channelDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type subscriptionDTO struct {
	ID          string       `json:"id"`
	Worker      *identityDTO `json:"worker"`
	CreatedBy   *identityDTO `json:"createdBy"`
	ViaGrant    bool         `json:"viaGrant"`
	CreatedAt   string       `json:"createdAt"`
	EndedAt     *string      `json:"endedAt"`
	EndedReason *string      `json:"endedReason"`
}
type tickerDTO struct {
	ID               string           `json:"id"`
	URN              *string          `json:"urn"`
	Name             string           `json:"name"`
	Slug             string           `json:"slug"`
	AppID            string           `json:"appId"`
	CadenceSeconds   int              `json:"cadenceSeconds"`
	AnchorAt         string           `json:"anchorAt"`
	QuietHours       *json.RawMessage `json:"quietHours"`
	ReadableChannels []channelDTO     `json:"readableChannels"`
	Relevance        string           `json:"relevance"`
	Reasons          []string         `json:"reasons"`
	Enabled          bool             `json:"enabled"`
	Owner            *identityDTO     `json:"owner"`
	NextTickAt       *string          `json:"nextTickAt"`
	LastTickAt       *string          `json:"lastTickAt"`
	CreatedAt        string           `json:"createdAt"`
	UpdatedAt        *string          `json:"updatedAt"`
	// TickCode is the server's stored tick code. RegisterTaskLoc is the
	// register entry's loc as returned, unresolved; null means no entry or
	// one the caller can't read (cor:agt:080:01), never "unmapped".
	TickCode        string  `json:"tickCode"`
	RegisterTaskLoc *string `json:"registerTaskLoc"`
}
type readDTO struct {
	tickerDTO
	Subscribers []subscriptionDTO `json:"subscribers"`
}

func tickerFrom(x gen.TickerFields) tickerDTO {
	d := tickerDTO{ID: x.Id, URN: x.Urn, Name: x.Name, Slug: x.Slug, CadenceSeconds: x.CadenceSeconds, AnchorAt: x.AnchorAt, QuietHours: x.QuietHours, ReadableChannels: []channelDTO{}, Relevance: strings.ToLower(string(x.Relevance)), Reasons: []string{}, Enabled: x.Enabled, NextTickAt: x.NextTickAt, LastTickAt: x.LastTickAt, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt, TickCode: x.TickCode, RegisterTaskLoc: x.RegisterTaskLoc}
	if x.App != nil {
		d.AppID = x.App.Id
	}
	if x.Owner != nil {
		d.Owner = &identityDTO{x.Owner.Id, x.Owner.Name, x.Owner.Urn}
	}
	for _, c := range x.Channels {
		if c != nil {
			d.ReadableChannels = append(d.ReadableChannels, channelDTO{c.Id, c.Name})
		}
	}
	for _, r := range x.Reasons {
		d.Reasons = append(d.Reasons, strings.ReplaceAll(strings.ToLower(string(r)), "_", "-"))
	}
	return d
}
func subFrom(x gen.TickerSubscriptionFields) subscriptionDTO {
	d := subscriptionDTO{ID: x.Id, ViaGrant: x.ViaGrant, CreatedAt: x.CreatedAt, EndedAt: x.EndedAt}
	if x.Worker != nil {
		d.Worker = &identityDTO{x.Worker.Id, x.Worker.Name, x.Worker.Urn}
	}
	if x.CreatedBy != nil {
		d.CreatedBy = &identityDTO{x.CreatedBy.Id, x.CreatedBy.Name, x.CreatedBy.Urn}
	}
	if x.EndedReason != nil {
		v := string(*x.EndedReason)
		d.EndedReason = &v
	}
	return d
}
func writeTicker(w io.Writer, d tickerDTO) error {
	ref := d.ID
	if d.URN != nil {
		ref = *d.URN
	}
	owner := "unknown"
	if d.Owner != nil {
		owner = d.Owner.Name
		if owner == "" {
			owner = d.Owner.ID
		}
	}
	state := "disabled"
	if d.Enabled {
		state = "enabled"
	}
	channels := "none visible to you"
	if len(d.ReadableChannels) > 0 {
		channels = fmt.Sprint(len(d.ReadableChannels))
	}
	_, err := fmt.Fprintf(w, "%s · %s · every %s · %s · relevance %s · reasons %s\n  owner %s · anchor %s\n  readable channels (reader's view): %s\n", d.Name, ref, cadenceCell(d.CadenceSeconds), state, d.Relevance, strings.Join(d.Reasons, "+"), owner, d.AnchorAt, channels)
	if err != nil {
		return err
	}
	loc := "no register entry you can read"
	if d.RegisterTaskLoc != nil {
		loc = "register task loc " + output.SafeCell(*d.RegisterTaskLoc)
	}
	if _, err = fmt.Fprintf(w, "  code %s · %s\n", output.SafeCell(d.TickCode), loc); err != nil {
		return err
	}
	for _, channel := range d.ReadableChannels {
		if _, e := fmt.Fprintf(w, "    %s · %s\n", channel.ID, channel.Name); e != nil {
			return e
		}
	}
	quiet := "none"
	if d.QuietHours != nil {
		var q struct {
			Timezone string            `json:"timezone"`
			Windows  []json.RawMessage `json:"windows"`
		}
		if err := json.Unmarshal(*d.QuietHours, &q); err == nil && q.Timezone != "" {
			quiet = fmt.Sprintf("%s (%d windows)", q.Timezone, len(q.Windows))
		} else {
			quiet = "configured (details in --json)"
		}
	}
	_, err = fmt.Fprintf(w, "  quiet hours %s · next tick %s · last tick %s\n  created %s · updated %s\n", quiet, dateCell(d.NextTickAt), dateCell(d.LastTickAt), d.CreatedAt, dateCell(d.UpdatedAt))
	return err
}
func dateCell(s *string) string {
	if s == nil {
		return "none"
	}
	return *s
}
func writeOne(ctx context.Context, client graphql.Client, f *cmdutil.Factory, x gen.TickerFields, scope string) error {
	d := tickerFrom(x)
	return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		if scope != "" {
			if err := writeScope(ctx, client, f, w, scope); err != nil {
				return err
			}
		}
		return writeTicker(w, d)
	})
}

// writeCreated is writeOne for create: the receipt (or the JSON object) as
// before, plus the call's warnings, which never change stdout's receipt or the
// exit code (cor:agt:110:02).
func writeCreated(ctx context.Context, client graphql.Client, f *cmdutil.Factory, x gen.TickerFields, scope string, warnings []warningDTO) error {
	d := tickerFrom(x)
	if err := output.Write(f.IOStreams, f.JSON, createdDTO{tickerDTO: d, Warnings: warnings}, func(w io.Writer) error {
		if scope != "" {
			if err := writeScope(ctx, client, f, w, scope); err != nil {
				return err
			}
		}
		return writeTicker(w, d)
	}); err != nil {
		return err
	}
	if f.JSON {
		return nil
	}
	return writeWarnings(f.IOStreams.ErrOut, warnings)
}

// NewCmdTicker builds ticker CRUD and subscription commands only.
func NewCmdTicker(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "ticker <command>", Short: "Manage named ticker schedules and worker subscriptions", Long: `Manage named ticker schedules through the server. The server determines
ownership, permissions, cadence limits and subscriber consent; this command
never computes due wakes. Wake delivery is a separate host operation.

Current server behavior may change. Create defaults reasons to
read-team-channel, relevance to all, anchor to now and enabled to true.
The server determines edit/delete authority and reader access.`}
	c.AddCommand(newWrite(f, false), newWrite(f, true), newRead(f, false), newRead(f, true), newDelete(f), newSubscribe(f, false), newSubscribe(f, true), newApplyTemplates(f))
	return c
}

func qualifiedRef(value, kind string) (string, error) {
	v := strings.TrimSpace(value)
	if cmdutil.IsAppID(v) {
		return v, nil
	}
	// Ticker and worker are v2-only types: the shared v1 compatibility
	// parser deliberately rejects them. Keep this fix local to these commands.
	parsed, err := urn.ParseUrnV2(v)
	if err == nil && parsed.Type == kind && parsed.Fragment == "" {
		return urn.ComposeUrnV2(kind, parsed.Root, parsed.Segments...)
	}
	listing := "hadron ticker list"
	if kind == "worker" {
		listing = "hadron team worker list"
	}
	return "", exitcode.Newf(exitcode.Usage, "pass a %s id or fully-qualified hrn:%s: URN (names are not addresses); %s shows the refs", kind, kind, listing)
}

func subscribers(ctx context.Context, client graphql.Client, ref string) ([]subscriptionDTO, error) {
	result := []subscriptionDTO{}
	var after *string
	seen := map[string]bool{}
	for {
		r, err := gen.TickerSubscribers(ctx, client, ref, 200, after)
		if err != nil {
			return nil, api.MapError(err)
		}
		if r.Ticker == nil {
			return nil, exitcode.Newf(exitcode.NotFound, "ticker not found")
		}
		p := r.Ticker.Subscribers
		if p == nil {
			return nil, exitcode.Newf(exitcode.Error, "server returned no subscriber page")
		}
		for _, s := range p.Items {
			if s != nil {
				result = append(result, subFrom(s.TickerSubscriptionFields))
			}
		}
		if !p.HasNextPage {
			return result, nil
		}
		if p.EndCursor == nil || *p.EndCursor == "" || seen[*p.EndCursor] {
			return nil, exitcode.Newf(exitcode.Error, "server returned a non-advancing subscriber cursor")
		}
		seen[*p.EndCursor] = true
		after = p.EndCursor
	}
}
func newRead(f *cmdutil.Factory, list bool) *cobra.Command {
	use, short := "get <id-or-URN>", "Read one ticker and its live subscribers"
	if list {
		use = "list"
		short = "List the tickers returned by the server"
	}
	c := &cobra.Command{Use: use, Short: short, Long: short + `. Channels are the reader's view, not the full configured set.
Subscribers are paged to the end. The ticker list is read page by page until the
server's own total is reached, so it is the whole set the caller may read (a
page that stops early or repeats rows fails the command rather than printing a
partial list).`, Args: cobra.ExactArgs(1)}
	if list {
		c.Args = cobra.NoArgs
		c.Aliases = []string{"ls"}
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		var ref, app string
		var err error
		if list {
			app, err = cmdutil.ResolveAppRef(f, "")
		} else {
			ref, err = qualifiedRef(args[0], "ticker")
		}
		if err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		rows := []gen.TickerFields{}
		if list {
			var e error
			rows, e = listAllTickers(cmd.Context(), client, app)
			if e != nil {
				return e
			}
		} else {
			r, e := gen.GetTicker(cmd.Context(), client, ref)
			if e != nil {
				return api.MapError(e)
			}
			if r.Ticker == nil {
				return exitcode.Newf(exitcode.NotFound, "ticker not found")
			}
			rows = append(rows, r.Ticker.TickerFields)
		}
		result := []readDTO{}
		for _, x := range rows {
			s, e := subscribers(cmd.Context(), client, x.Id)
			if e != nil {
				return e
			}
			result = append(result, readDTO{tickerFrom(x), s})
		}
		var dto any = result
		if !list {
			dto = result[0]
		}
		return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
			if list {
				if err := writeScope(cmd.Context(), client, f, w, app); err != nil {
					return err
				}
			}
			if list && len(result) == 0 {
				_, err := fmt.Fprintln(w, "No tickers in this App.")
				return err
			}
			for _, d := range result {
				if e := writeTicker(w, d.tickerDTO); e != nil {
					return e
				}
				if _, e := fmt.Fprintf(w, "  live subscribers: %d\n", len(d.Subscribers)); e != nil {
					return e
				}
				for _, s := range d.Subscribers {
					label := s.ID
					if s.Worker != nil {
						label = s.Worker.ID
						if s.Worker.Name != "" {
							label = s.Worker.Name
						}
					}
					if _, e := fmt.Fprintf(w, "    %s · %s\n", s.ID, label); e != nil {
						return e
					}
				}
			}
			return nil
		})
	}
	return c
}

type writeFlags struct {
	name                               string
	cadence                            int
	anchor, quiet, relevance, reasons  string
	channels                           []string
	enabled, clearQuiet, clearChannels bool
}

func newWrite(f *cmdutil.Factory, update bool) *cobra.Command {
	v := &writeFlags{}
	use, short := "create", "Create a named ticker"
	if update {
		use = "update <id-or-URN>"
		short = "Update a ticker atomically"
	}
	long := `Create a named ticker. Name and cadence are required. Omitted inputs
use the current server defaults: reasons read-team-channel, relevance all,
anchor at creation time, and enabled. These defaults may change.
The caller becomes the owner; owner and immutable slug cannot be supplied.`
	if update {
		long = `Update a ticker atomically. Omitted fields keep their current values,
including enabled/disabled state. Pass at least one field flag.
Adding every-tick may require subscriber consent: refusal rejects the whole
edit. The server determines edit authority; current server behavior may change.
Owner and immutable slug cannot be supplied.`
	}
	long += `

--reasons is a nonempty set: read-team-channel, every-tick, live-check (any-of).
The server checks permissions and cadence limits. If a write's outcome is
unknown, verify the current state before retrying; this command never retries.
Quiet hours are a JSON object {"timezone":"UTC","windows":[{"days":[1],"from":"09:00","to":"10:00"}]}.
Watched channels are those in the subscriber's WATCH or BOTH channel register.`
	c := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, Long: long}
	if update {
		c.Args = cobra.ExactArgs(1)
	}
	flags := c.Flags()
	nameUsage, cadenceUsage := "Ticker name (required)", "Cadence in whole seconds (required; server validates limits)"
	anchorUsage, relevanceUsage := "Tick anchor as RFC3339", "all or mentions"
	keep := ""
	if update {
		nameUsage = "Replacement ticker name"
		cadenceUsage = "Replacement cadence in whole seconds (server validates limits)"
		keep = "; omitted keeps the current value"
	}
	flags.StringVar(&v.name, "name", "", nameUsage+keep)
	flags.IntVar(&v.cadence, "cadence-seconds", 0, cadenceUsage+keep)
	flags.StringVar(&v.anchor, "anchor-at", "", anchorUsage+keep)
	flags.StringVar(&v.quiet, "quiet-hours", "", "Quiet-hours JSON object"+keep)
	channelUsage := "Channel IDs, comma-separated/repeated; watched means WATCH/BOTH register"
	if update {
		channelUsage += keep
	} else {
		channelUsage += "; omit for watched channels"
	}
	flags.StringSliceVar(&v.channels, "channel-ids", nil, channelUsage)
	flags.StringVar(&v.relevance, "relevance", "", relevanceUsage+keep)
	flags.StringVar(&v.reasons, "reasons", "", "Nonempty comma-separated reasons: read-team-channel,every-tick,live-check"+keep)
	// The bound bool's default is not a server default. Changed controls omission.
	flags.BoolVar(&v.enabled, "enabled", false, "Enable ticker; --enabled=false disables"+keep)
	if update {
		flags.BoolVar(&v.clearQuiet, "clear-quiet-hours", false, "Remove quiet hours")
		flags.BoolVar(&v.clearChannels, "clear-channels", false, "Remove channel selection; count watched channels")
		c.MarkFlagsMutuallyExclusive("quiet-hours", "clear-quiet-hours")
		c.MarkFlagsMutuallyExclusive("channel-ids", "clear-channels")
	} else {
		_ = c.MarkFlagRequired("name")
		_ = c.MarkFlagRequired("cadence-seconds")
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if update {
			changed := false
			for _, name := range []string{"name", "cadence-seconds", "anchor-at", "quiet-hours", "channel-ids", "relevance", "reasons", "enabled", "clear-quiet-hours", "clear-channels"} {
				changed = changed || cmd.Flags().Changed(name)
			}
			if !changed {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
			}
		}
		p, err := v.patch(cmd)
		if err != nil {
			return err
		}
		var ref, app string
		if update {
			ref, err = qualifiedRef(args[0], "ticker")
		} else {
			app, err = cmdutil.ResolveAppRef(f, "")
		}
		if err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		if update {
			r, e := gen.UpdateTicker(cmd.Context(), client, ref, p)
			if e != nil {
				return api.MapError(e)
			}
			if r.UpdateTicker == nil {
				return exitcode.Newf(exitcode.Error, "server returned no ticker")
			}
			return writeOne(cmd.Context(), client, f, r.UpdateTicker.TickerFields, "")
		}
		in := &gen.TickerInput{Name: v.name, CadenceSeconds: v.cadence, AnchorAt: p.AnchorAt, QuietHours: p.QuietHours, ChannelIds: p.ChannelIds, Relevance: p.Relevance, Reasons: p.Reasons, Enabled: p.Enabled}
		mctx, sink := api.WithWarningSinkForFields(cmd.Context(), "createTicker")
		r, e := gen.CreateTicker(mctx, client, app, in)
		if e != nil {
			return api.MapError(e)
		}
		if r.CreateTicker == nil {
			return exitcode.Newf(exitcode.Error, "server returned no ticker")
		}
		return writeCreated(cmd.Context(), client, f, r.CreateTicker.TickerFields, app, warningsFor(sink, "createTicker"))
	}
	return c
}
func (v *writeFlags) patch(c *cobra.Command) (*gen.TickerPatch, error) {
	p := &gen.TickerPatch{}
	changed := c.Flags().Changed
	if changed("name") {
		if strings.TrimSpace(v.name) == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--name must not be empty")
		}
		p.Name = &v.name
	}
	if changed("cadence-seconds") {
		p.CadenceSeconds = &v.cadence
	}
	if changed("anchor-at") {
		if _, err := time.Parse(time.RFC3339, v.anchor); err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "--anchor-at takes an RFC3339 timestamp")
		}
		p.AnchorAt = &v.anchor
	}
	if changed("quiet-hours") {
		var err error
		p.QuietHours, err = ParseQuietHours(v.quiet)
		if err != nil {
			return nil, err
		}
	}
	if changed("channel-ids") {
		if len(v.channels) == 0 {
			return nil, exitcode.Newf(exitcode.Usage, "--channel-ids must not be empty; use --clear-channels on update")
		}
		for _, id := range v.channels {
			if strings.TrimSpace(id) == "" {
				return nil, exitcode.Newf(exitcode.Usage, "--channel-ids contains an empty ID")
			}
		}
		p.ChannelIds = v.channels
	}
	if changed("enabled") {
		p.Enabled = &v.enabled
	}
	if changed("clear-quiet-hours") {
		p.ClearQuietHours = &v.clearQuiet
	}
	if changed("clear-channels") {
		p.ClearChannels = &v.clearChannels
	}
	if changed("relevance") {
		r, err := ParseRelevance(v.relevance)
		if err != nil {
			return nil, err
		}
		p.Relevance = &r
	}
	if changed("reasons") {
		var err error
		p.Reasons, err = ParseReasons(v.reasons)
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}
func newDelete(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	c := &cobra.Command{Use: "delete <id-or-URN>", Short: "Permanently delete a ticker and end its subscriptions", Long: `Delete a ticker. There is no restore. Recreating its name gives a new slug
and URN; existing subscriptions do not transfer to the replacement.`, Args: cobra.ExactArgs(1)}
	c.Flags().BoolVar(&yes, "yes", false, "Confirm permanent deletion without prompting")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		ref, err := qualifiedRef(args[0], "ticker")
		if err != nil {
			return err
		}
		if err = cmdutil.ConfirmDeletion(f.IOStreams, yes, "ticker "+ref+" (ending its live subscriptions)"); err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		r, err := gen.DeleteTicker(cmd.Context(), client, ref)
		if err != nil {
			return api.MapError(err)
		}
		if !r.DeleteTicker {
			return exitcode.Newf(exitcode.Error, "server did not delete ticker; verify the current state before retrying")
		}
		d := struct {
			Ref     string `json:"ref"`
			Deleted bool   `json:"deleted"`
		}{ref, r.DeleteTicker}
		return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
			_, e := fmt.Fprintf(w, "ticker %s · deleted %t\n", d.Ref, d.Deleted)
			return e
		})
	}
	return c
}
func newSubscribe(f *cmdutil.Factory, unsubscribe bool) *cobra.Command {
	worker := ""
	verb := "subscribe"
	if unsubscribe {
		verb = "unsubscribe"
	}
	c := &cobra.Command{Use: verb + " <ticker-id-or-URN> --worker <id-or-URN>", Short: strings.ToUpper(verb[:1]) + verb[1:] + " a worker to a ticker", Args: cobra.ExactArgs(1), Long: `Change a worker's ticker subscription. The server checks holder, manager,
self-subscribe cadence and grant authority. Current server behavior permits
self-unsubscribe at any cadence; current server behavior may change.
A grant announcement is best-effort:
FAILED leaves the subscription change standing; this command never retries it.`}
	if unsubscribe {
		c.Short = "Unsubscribe a worker from a ticker"
	}
	c.Flags().StringVar(&worker, "worker", "", "Worker ID or fully-qualified worker URN (required)")
	_ = c.MarkFlagRequired("worker")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		ref, err := qualifiedRef(args[0], "ticker")
		if err != nil {
			return err
		}
		wref, err := qualifiedRef(worker, "worker")
		if err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		var sub gen.TickerSubscriptionFields
		var announcement *gen.GrantAnnouncement
		if unsubscribe {
			r, e := gen.UnsubscribeTicker(cmd.Context(), client, ref, wref)
			if e != nil {
				return api.MapError(e)
			}
			if r.UnsubscribeWorker == nil || r.UnsubscribeWorker.Subscription == nil {
				return exitcode.Newf(exitcode.Error, "server returned no subscription")
			}
			sub = r.UnsubscribeWorker.Subscription.TickerSubscriptionFields
			announcement = r.UnsubscribeWorker.Announcement
		} else {
			r, e := gen.SubscribeTicker(cmd.Context(), client, ref, wref)
			if e != nil {
				return api.MapError(e)
			}
			if r.SubscribeWorker == nil || r.SubscribeWorker.Subscription == nil {
				return exitcode.Newf(exitcode.Error, "server returned no subscription")
			}
			sub = r.SubscribeWorker.Subscription.TickerSubscriptionFields
			announcement = r.SubscribeWorker.Announcement
		}
		var a *string
		if announcement != nil {
			s := string(*announcement)
			a = &s
		}
		d := struct {
			Subscription subscriptionDTO `json:"subscription"`
			Announcement *string         `json:"announcement"`
		}{subFrom(sub), a}
		if a != nil && *a == "FAILED" {
			if _, e := fmt.Fprintln(f.IOStreams.ErrOut, "Subscription change stands, but the grant announcement failed. Tell the worker's holder; do not rerun to retry the announcement."); e != nil {
				return e
			}
		}
		return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
			label := wref
			if d.Subscription.Worker != nil {
				label = d.Subscription.Worker.ID
				if d.Subscription.Worker.Name != "" {
					label = d.Subscription.Worker.Name + " (" + d.Subscription.Worker.ID + ")"
				}
			}
			preposition := "to"
			if unsubscribe {
				preposition = "from"
			}
			_, e := fmt.Fprintf(w, "%s worker %s %s ticker %s · subscription %s\n", verb, label, preposition, ref, d.Subscription.ID)
			if e == nil && a != nil && *a == "POSTED" {
				_, e = fmt.Fprintln(w, "Grant use announced to the team chat.")
			}
			return e
		})
	}
	return c
}

// Scope decoration is best-effort, human-only and never a new gate.
func writeScope(ctx context.Context, client graphql.Client, f *cmdutil.Factory, w io.Writer, ref string) error {
	label := ref
	if r, err := gen.TeamAppIdentity(ctx, client, ref); err == nil && r.App != nil && r.App.Urn != "" {
		label = r.App.Urn
		if r.App.Name != "" {
			label += " — " + r.App.Name
		}
	}
	source := "the App context"
	if f.AppFlag != "" {
		source = "--app"
	}
	_, err := fmt.Fprintf(w, "app: %s (from %s)\n", label, source)
	return err
}

func cadenceCell(seconds int) string {
	if seconds%3600 == 0 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	if seconds%60 == 0 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return (time.Duration(seconds) * time.Second).String()
}

const tickerPageSize = 200

// listAllTickers pages tickersPage to exhaustion (cli#1339): the contract of
// `ticker list` is the whole set. The page's total is the proof of the end; a
// page that is empty before the total is reached, or that repeats an id, means
// the list is not advancing and fails the command instead of returning a
// partial census.
func listAllTickers(ctx context.Context, client graphql.Client, app string) ([]gen.TickerFields, error) {
	rows := []gen.TickerFields{}
	seen := map[string]bool{}
	limit := tickerPageSize
	for offset := 0; ; {
		off := offset
		r, err := gen.ListTickersPage(ctx, client, app, &limit, &off)
		if err != nil {
			return nil, api.MapError(err)
		}
		page := r.TickersPage
		fresh := 0
		for _, x := range page.Items {
			if x == nil || seen[x.Id] {
				continue
			}
			seen[x.Id] = true
			fresh++
			rows = append(rows, x.TickerFields)
		}
		offset += len(page.Items)
		// The end is proven by UNIQUE rows reaching the server's total, never by
		// the offset: a server that ignores the offset repeats rows, which would
		// otherwise count toward the total and end the read on a partial list.
		if !page.HasMore && len(rows) >= page.Total {
			return rows, nil
		}
		if fresh == 0 {
			// Nothing new while the total is still unmet is a partial list, whatever
			// hasMore says: fail rather than print it.
			return nil, exitcode.Newf(exitcode.Unavailable, "the server's ticker list is not advancing: %d of %d read, offset %d", len(rows), page.Total, offset)
		}
	}
}
