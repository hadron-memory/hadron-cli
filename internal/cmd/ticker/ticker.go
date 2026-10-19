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
}
type readDTO struct {
	tickerDTO
	Subscribers []subscriptionDTO `json:"subscribers"`
}

func tickerFrom(x gen.TickerFields) tickerDTO {
	d := tickerDTO{ID: x.Id, URN: x.Urn, Name: x.Name, Slug: x.Slug, CadenceSeconds: x.CadenceSeconds, AnchorAt: x.AnchorAt, QuietHours: x.QuietHours, ReadableChannels: []channelDTO{}, Relevance: strings.ToLower(string(x.Relevance)), Reasons: []string{}, Enabled: x.Enabled, NextTickAt: x.NextTickAt, LastTickAt: x.LastTickAt, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
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
	_, err := fmt.Fprintf(w, "%s · %s · every %ds · enabled %t · relevance %s · reasons %s\n  owner %s · anchor %s\n  readable channels (reader's view): %d\n", d.Name, ref, d.CadenceSeconds, d.Enabled, d.Relevance, strings.Join(d.Reasons, "+"), owner, d.AnchorAt, len(d.ReadableChannels))
	if err != nil {
		return err
	}
	for _, channel := range d.ReadableChannels {
		if _, e := fmt.Fprintf(w, "    %s · %s\n", channel.ID, channel.Name); e != nil {
			return e
		}
	}
	quiet := "none"
	if d.QuietHours != nil {
		quiet = string(*d.QuietHours)
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

// NewCmdTicker builds ticker CRUD and subscription commands only.
func NewCmdTicker(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "ticker <command>", Short: "Manage named ticker schedules and worker subscriptions", Long: `Manage named ticker schedules through the server. The server determines
ownership, permissions, cadence limits and subscriber consent; this command
never computes due wakes. Wake delivery is a separate host operation.

Current server behavior includes Proposed defaults and management/read rules:
create reasons default to read-team-channel, relevance to all, anchor to now,
and enabled to true. Edits/deletes admit the owner, its worker's holder or an
App manager; reads admit App participants. These are current implementation
behaviors, not additional settled platform guarantees.`}
	c.AddCommand(newWrite(f, false), newWrite(f, true), newRead(f, false), newRead(f, true), newDelete(f), newSubscribe(f, false), newSubscribe(f, true))
	return c
}

func qualifiedRef(value, kind string) (string, error) {
	v := strings.TrimSpace(value)
	if cmdutil.IsAppID(v) {
		return v, nil
	}
	if urn.HasSchemePrefix(v) {
		canon, err := cmdutil.CanonicalizeURN(kind, v)
		if err != nil {
			return "", err
		}
		parsed, err := urn.ParseUrn(canon)
		if err == nil && parsed.Type == kind {
			return canon, nil
		}
	}
	return "", exitcode.Newf(exitcode.Usage, "pass a %s id or fully-qualified hrn:%s: URN (names are not addresses)", kind, kind)
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
Subscribers are paged to the end. The server ticker list is currently unpaged;
this command reports its response and does not promise an exhaustive census.`, Args: cobra.ExactArgs(1)}
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
			r, e := gen.ListTickers(cmd.Context(), client, app)
			if e != nil {
				return api.MapError(e)
			}
			for _, x := range r.Tickers {
				if x != nil {
					rows = append(rows, x.TickerFields)
				}
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
	c := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, Long: short + `.
Omitted inputs use current server defaults on create and preserve on update.
--reasons is a nonempty set: read-team-channel, every-tick, or both (any-of).
Adding every-tick may require subscriber consent; refusal rejects the whole
edit. On create the current server defaults reasons to read-team-channel
(Proposed). The server owns authorization and cadence limits. No uncertain write
is retried automatically. Owner and immutable slug cannot be supplied.

Quiet hours are a JSON object {"timezone":"UTC","windows":[{"days":[1],"from":"09:00","to":"10:00"}]}.
Anchor, relevance, enabled, quiet-hours shape, read audiences and edit authority
are current server behavior with Proposed clauses in the corpus.`}
	if update {
		c.Args = cobra.ExactArgs(1)
	}
	flags := c.Flags()
	flags.StringVar(&v.name, "name", "", "Ticker name (required on create)")
	flags.IntVar(&v.cadence, "cadence-seconds", 0, "Cadence in whole seconds (required on create; server validates limits)")
	flags.StringVar(&v.anchor, "anchor-at", "", "Tick anchor as RFC3339 (current server default: creation time)")
	flags.StringVar(&v.quiet, "quiet-hours", "", "Quiet-hours JSON object")
	flags.StringSliceVar(&v.channels, "channel-ids", nil, "Channel IDs, comma-separated or repeated; omit for watched channels")
	flags.StringVar(&v.relevance, "relevance", "", "all or mentions (current server default: all)")
	flags.StringVar(&v.reasons, "reasons", "", "Nonempty comma-separated reasons: read-team-channel,every-tick")
	flags.BoolVar(&v.enabled, "enabled", true, "Enable ticker; --enabled=false disables (current server default: true)")
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
		r, e := gen.CreateTicker(cmd.Context(), client, app, in)
		if e != nil {
			return api.MapError(e)
		}
		if r.CreateTicker == nil {
			return exitcode.Newf(exitcode.Error, "server returned no ticker")
		}
		return writeOne(cmd.Context(), client, f, r.CreateTicker.TickerFields, app)
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
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(v.quiet), &obj); err != nil || obj == nil {
			return nil, exitcode.Newf(exitcode.Usage, "--quiet-hours takes a JSON object")
		}
		raw := json.RawMessage(v.quiet)
		p.QuietHours = &raw
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
		r := gen.TickerRelevance(strings.ToUpper(v.relevance))
		if r != gen.TickerRelevanceAll && r != gen.TickerRelevanceMentions {
			return nil, exitcode.Newf(exitcode.Usage, "--relevance takes all or mentions")
		}
		p.Relevance = &r
	}
	if changed("reasons") {
		seen := map[string]bool{}
		for _, r := range strings.Split(v.reasons, ",") {
			r = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(r)), "_", "-")
			if r == "" {
				return nil, exitcode.Newf(exitcode.Usage, "reasons must name at least one reason: a ticker with none would never fire.")
			}
			if r != "read-team-channel" && r != "every-tick" {
				return nil, exitcode.Newf(exitcode.Usage, "reasons are from: read-team-channel, every-tick.")
			}
			seen[r] = true
		}
		for _, r := range []string{"read-team-channel", "every-tick"} {
			if seen[r] {
				p.Reasons = append(p.Reasons, gen.TickerReason(strings.ReplaceAll(strings.ToUpper(r), "-", "_")))
			}
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
		if err = cmdutil.ConfirmDeletion(f.IOStreams, yes, "ticker "+ref); err != nil {
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
	c := &cobra.Command{Use: verb + " <ticker-id-or-URN> --worker <id-or-URN>", Short: strings.ToUpper(verb[:1]) + verb[1:] + " a worker", Args: cobra.ExactArgs(1), Long: `Change a worker's ticker subscription. The server checks holder, manager,
self-subscribe cadence and grant authority. Current server behavior permits
self-unsubscribe at any cadence (Proposed). A grant announcement is best-effort:
FAILED leaves the subscription change standing; this command never retries it.`}
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
			_, e := fmt.Fprintf(w, "%s · subscription %s\n", verb, d.Subscription.ID)
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
