package automation

import (
	"context"
	"fmt"
	"io"
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

type snapshotGrantDTO struct {
	ID          string  `json:"id"`
	AppID       string  `json:"appId"`
	TriggerKind string  `json:"triggerKind"`
	TriggerID   string  `json:"triggerId"`
	GrantedBy   string  `json:"grantedBy"`
	CreatedAt   string  `json:"createdAt"`
	RevokedAt   *string `json:"revokedAt"`
	RevokedBy   *string `json:"revokedBy"`
}

type snapshotGrantPageDTO struct {
	Items   []snapshotGrantDTO `json:"items"`
	Total   int                `json:"total"`
	HasMore bool               `json:"hasMore"`
}

func snapshotGrantFrom(g gen.AutomationTeamSnapshotGrantFields) (snapshotGrantDTO, error) {
	if g.Id == "" || g.AppId == "" || g.TriggerKind == "" || g.TriggerId == "" || g.GrantedBy == "" || g.CreatedAt == "" {
		return snapshotGrantDTO{}, exitcode.Newf(exitcode.Error, "the server returned an incomplete team snapshot grant; list grants before retrying a write")
	}
	return snapshotGrantDTO{ID: g.Id, AppID: g.AppId, TriggerKind: g.TriggerKind, TriggerID: g.TriggerId, GrantedBy: g.GrantedBy, CreatedAt: g.CreatedAt, RevokedAt: g.RevokedAt, RevokedBy: g.RevokedBy}, nil
}

func snapshotOptional(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

func writeSnapshotGrant(f *cmdutil.Factory, d snapshotGrantDTO) error {
	return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "id: %s\napp: %s\ntrigger: %s %s\ngranted by: %s\ncreated: %s\nrevoked: %s\nrevoked by: %s\n", output.SafeCell(d.ID), output.SafeCell(d.AppID), output.SafeCell(d.TriggerKind), output.SafeCell(d.TriggerID), output.SafeCell(d.GrantedBy), output.SafeCell(d.CreatedAt), output.SafeCell(snapshotOptional(d.RevokedAt)), output.SafeCell(snapshotOptional(d.RevokedBy)))
		return err
	})
}

func newCmdTeamSnapshotGrant(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{Use: "team-snapshot-grant <command>", Short: "Grant an automation read-only access to its App's roster and tasks", Long: `Manage per-automation team snapshot grants. A live schedule or webhook may read
its own App's roster and tasks only while it holds this grant; none is given
by default. A manual run has no automation and cannot hold one. This grants no
writes and no access to another App. The server decides all authority.

Only a person managing the App may give one (set stays person-only). Set and
list take an explicit App ID, qualified URN or root:slug; revoke takes the grant
ID: a given --app must match the grant's App, and with --instruction --app or
the active App names the App whose history is read. JSON keeps
revokedAt/revokedBy null for a live grant; human output uses -. An older server
refuses these commands; its refusal is passed through.`}
	for _, c := range []*cobra.Command{newCmdSnapshotSet(f), newCmdSnapshotList(f), newCmdSnapshotRevoke(f)} {
		c.Long += "\n\n" + cmd.Long
		if c.Flags().Lookup("instruction") != nil {
			c.Long += "\n\n" + snapshotInstructionHelp
		}
		cmd.AddCommand(c)
	}
	return cmd
}

func newCmdSnapshotSet(f *cmdutil.Factory) *cobra.Command {
	var kind, id string
	var yes bool
	cmd := &cobra.Command{Use: "set <app-ref> --trigger-kind SCHEDULE|WEBHOOK --trigger-id <id> [--yes]", Short: "Give one schedule or webhook a team snapshot grant", Long: `Confirm the App, trigger kind and trigger ID, then send one grant mutation.
An identical repeat returns the live row. Scripts must pass --yes. No request
is retried: if an answer is lost, list grants to check before trying again.
The returned grant metadata is printed; this does not run the automation. The
App is the <app-ref> argument: the global --app and the active App do not apply.

--trigger-id is the ID column of "hadron schedule list" (with --trigger-kind
SCHEDULE) or "hadron webhook list" (with --trigger-kind WEBHOOK), run for the
same App.`, Example: `  hadron automation team-snapshot-grant set acme.com:ops --trigger-kind SCHEDULE --trigger-id <schedule-id> --yes --json`, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		app, err := snapshotAppRef(args[0])
		if err != nil {
			return err
		}
		if kind != "SCHEDULE" && kind != "WEBHOOK" {
			return exitcode.Newf(exitcode.Usage, "--trigger-kind must be SCHEDULE or WEBHOOK; manual runs cannot hold this grant")
		}
		if strings.TrimSpace(id) == "" {
			return exitcode.Newf(exitcode.Usage, "--trigger-id must not be empty")
		}
		if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Grant App %s trigger %s %s read-only roster and task access?", strconv.Quote(app), kind, strconv.Quote(id))); err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		r, err := gen.SetAutomationTeamSnapshotGrant(cmd.Context(), client, app, kind, id)
		if err != nil {
			return mapSnapshotWriteError(err)
		}
		if r == nil || r.SetAutomationTeamSnapshotGrant == nil {
			return exitcode.Newf(exitcode.Error, "the server did not confirm a team snapshot grant; list grants before retrying")
		}
		d, err := snapshotGrantFrom(r.SetAutomationTeamSnapshotGrant.AutomationTeamSnapshotGrantFields)
		if err != nil {
			return err
		}
		if d.TriggerKind != kind || d.TriggerID != id || d.RevokedAt != nil || d.RevokedBy != nil {
			return exitcode.Newf(exitcode.Error, "the server did not confirm a live grant for the selected trigger; list grants before retrying")
		}
		return writeSnapshotGrant(f, d)
	}}
	cmd.Flags().StringVar(&kind, "trigger-kind", "", "SCHEDULE or WEBHOOK (required); manual runs have no automation")
	cmd.Flags().StringVar(&id, "trigger-id", "", "the schedule or webhook ID (required); it is the ID column of hadron schedule list or hadron webhook list")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation (required non-interactively)")
	_ = cmd.MarkFlagRequired("trigger-kind")
	_ = cmd.MarkFlagRequired("trigger-id")
	return cmd
}

// collectSnapshotGrants advances by rows actually returned. A broken/nonprogressing
// page fails the whole command, so it never prints a partial list as complete.
func collectSnapshotGrants(ctx context.Context, client graphql.Client, app string, include *bool, trigger snapshotTriggerFilter, limit, offset int, all bool, claim actInstruction) (snapshotGrantPageDTO, error) {
	d := snapshotGrantPageDTO{Items: []snapshotGrantDTO{}}
	seen := map[string]bool{}
	for {
		p, err := claim.page(ctx, client, app, include, trigger, limit, offset)
		if err != nil {
			return d, err
		}
		if p.Total < 0 || len(p.Items) > limit || (p.HasMore && len(p.Items) == 0) {
			return d, exitcode.Newf(exitcode.Error, "the server returned an invalid or non-progressing team snapshot grant page")
		}
		for _, row := range p.Items {
			if seen[row.ID] {
				return d, exitcode.Newf(exitcode.Error, "the server repeated a team snapshot grant while paging; list again")
			}
			seen[row.ID] = true
			d.Items = append(d.Items, row)
		}
		d.Total = p.Total
		d.HasMore = p.HasMore
		if !all || !p.HasMore {
			return d, nil
		}
		offset += len(p.Items)
	}
}

func newCmdSnapshotList(f *cmdutil.Factory) *cobra.Command {
	var revoked bool
	var limit, offset int
	var instructionRaw, triggerKind, triggerID string
	cmd := &cobra.Command{Use: "list <app-ref> [--include-revoked] [--trigger-kind k] [--trigger-id id] [--limit n] [--offset n]", Aliases: []string{"ls"}, Short: "List an App's team snapshot grants, paging to the end by default", Long: `Read grants in the server's order (oldest first). Without --limit, read every
page (20 rows each) starting at --offset (default 0). With --limit, read exactly
one page of 1..200 rows; a continuation note goes to stderr when more remain.
Revoked grants are excluded unless --include-revoked is true. --trigger-kind
and --trigger-id keep only the grants for that trigger kind or trigger ID; each
is sent as given only when set, and the server checks the kind. A paged walk is
not an atomic snapshot: concurrent changes can affect the list. A later-page
refusal fails the command without printing a partial result.
JSON is {items,total,hasMore}; empty items is [], and total is the server's
count, not a count inferred by the CLI. Human output shows grant, App, trigger
and revocation metadata. No credentials or task content are read.`, Example: `  hadron automation team-snapshot-grant list acme.com:ops --json
  hadron automation team-snapshot-grant list acme.com:ops --include-revoked --limit 20 --offset 20
  hadron automation team-snapshot-grant list acme.com:ops --trigger-kind SCHEDULE --trigger-id <schedule-id>`, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		claim, err := parseActInstruction(cmd, f, instructionRaw)
		if err != nil {
			return err
		}
		app, err := snapshotAppRef(args[0])
		if err != nil {
			return err
		}
		if limit < 1 || limit > 200 || offset < 0 {
			return exitcode.Newf(exitcode.Usage, "--limit must be 1..200 and --offset must be nonnegative")
		}
		var include *bool
		if cmd.Flags().Changed("include-revoked") {
			include = &revoked
		}
		var trigger snapshotTriggerFilter
		if cmd.Flags().Changed("trigger-kind") {
			trigger.kind = &triggerKind
		}
		if cmd.Flags().Changed("trigger-id") {
			trigger.id = &triggerID
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		d, err := collectSnapshotGrants(cmd.Context(), client, app, include, trigger, limit, offset, !cmd.Flags().Changed("limit"), claim)
		if err != nil {
			return err
		}
		err = output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
			t := output.NewTable(w, "ID", "APP", "TRIGGER KIND", "TRIGGER ID", "REVOKED")
			for _, g := range d.Items {
				t.Row(output.SafeCell(g.ID), output.SafeCell(g.AppID), output.SafeCell(g.TriggerKind), output.SafeCell(g.TriggerID), output.SafeCell(snapshotOptional(g.RevokedAt)))
			}
			return t.Flush()
		})
		if err != nil {
			return err
		}
		if d.HasMore {
			_, err = fmt.Fprintf(f.IOStreams.ErrOut, "more grants remain — continue with --limit %d --offset %d\n", limit, offset+len(d.Items))
		}
		return err
	}}
	cmd.Flags().StringVar(&instructionRaw, "instruction", "", instructionFlagUsage)
	cmd.Flags().BoolVar(&revoked, "include-revoked", false, "include ended grants; false keeps live grants only")
	cmd.Flags().StringVar(&triggerKind, "trigger-kind", "", "only grants for this trigger kind (SCHEDULE or WEBHOOK; the server checks it)")
	cmd.Flags().StringVar(&triggerID, "trigger-id", "", "only grants for this schedule or webhook ID")
	cmd.Flags().IntVar(&limit, "limit", 20, "rows per page (1..200); given, reads just that one page; omitted, reads every page")
	cmd.Flags().IntVar(&offset, "offset", 0, "skip this many rows before reading (nonnegative)")
	return cmd
}

func snapshotAppRef(ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", exitcode.Newf(exitcode.Usage, "pass a nonempty App ID, qualified URN or root:slug")
	}
	return cmdutil.CanonicalAppRef("<app-ref>", ref)
}

// lookupSnapshotGrantByID is a person's pre-confirmation read: one by-id query
// (server#3381), the same eight fields, revoked rows included. An unknown and an
// unauthorized id are both NOT_FOUND on the server; they map to the same CLI
// error the paged lookup gave for an absent row, so nothing distinguishes them.
func lookupSnapshotGrantByID(ctx context.Context, client graphql.Client, id string) (snapshotGrantDTO, error) {
	r, err := gen.AutomationTeamSnapshotGrantById(ctx, client, id)
	if err != nil {
		// Only the server's own NOT_FOUND is "the grant is not there": a bare HTTP 404
		// (a mistyped --server) keeps its own mapped error, not a false "not found".
		if api.ServerCodeOf(err) == "NOT_FOUND" {
			return snapshotGrantDTO{}, exitcode.Newf(exitcode.NotFound, "team snapshot grant not found")
		}
		return snapshotGrantDTO{}, api.MapErrorKeepingCode(err)
	}
	if r == nil || r.AutomationTeamSnapshotGrant == nil {
		return snapshotGrantDTO{}, exitcode.Newf(exitcode.Error, "the server returned no team snapshot grant for that ID")
	}
	d, err := snapshotGrantFrom(r.AutomationTeamSnapshotGrant.AutomationTeamSnapshotGrantFields)
	if err != nil {
		return snapshotGrantDTO{}, err
	}
	if d.ID != id {
		return snapshotGrantDTO{}, exitcode.Newf(exitcode.Error, "the server returned a different team snapshot grant than the one asked for")
	}
	return d, nil
}

// snapshotGrantInApp reports whether the App named by --app (an id, a URN or
// root:slug) is the one that holds the grant. An id is compared directly; any
// other form costs exactly one app(ref:) read to learn its id. An App that
// resolves to null, or whose read is NOT_FOUND, is not the grant's App. Any OTHER
// error on that read keeps its own mapped error: a FORBIDDEN exits 8 and sends no
// mutation. Treating FORBIDDEN as "not the grant's App" too would be a behavior
// change that needs its own decision (cli#2436).
func snapshotGrantInApp(ctx context.Context, client graphql.Client, appRef, grantAppID string) (bool, error) {
	if cmdutil.IsAppID(appRef) {
		return appRef == grantAppID, nil
	}
	r, err := gen.GetAppMetadata(ctx, client, appRef)
	if err != nil {
		if api.ServerCodeOf(err) == "NOT_FOUND" {
			return false, nil
		}
		return false, api.MapErrorKeepingCode(err)
	}
	if r == nil || r.App == nil {
		return false, nil
	}
	return r.App.Id == grantAppID, nil
}

func newCmdSnapshotRevoke(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	var instructionRaw string
	cmd := &cobra.Command{Use: "revoke <grant-id> [--yes]", Short: "End one automation team snapshot grant by ID", Long: `Revoke the exact grant ID from list. A person's revoke reads that one grant
by ID (the grant's own App, including an already ended row), then shows the
returned App ID and trigger kind/ID for confirmation. No App's history is paged
and no App is needed: without --app the confirmation names the App the server
returned for that grant. With --app, the grant must belong to that App, else it
is not found (before any confirmation) and nothing is sent. Scripts must pass
--yes before any lookup, even for an ended row. An unknown ID, or one in an App
you may not manage, is not found and sends no mutation. An already ended row is
shown without re-sending revocation. Revocation is terminal; a future set
creates a new row rather than reopening the old one.

One revoke mutation is sent, never retried. A lost answer is an unknown
outcome: list --include-revoked before trying again. JSON returns the grant
with explicit revokedAt/revokedBy.

With --instruction (a held worker), the grant is still found by paging the
target App's history, because the by-id read has no worker arm: --app or the
active App selected by hadron app use names that App. Confirmation then happens
BEFORE any request: it names the grant ID and target App, and warns that the
lookup records one event per page. After consent the entire App history is read,
then the matching live row is revoked without a second prompt. No matching row
means no revoke.`, Example: `  hadron automation team-snapshot-grant revoke <grant-id> --yes --json`, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		claim, err := parseActInstruction(cmd, f, instructionRaw)
		if err != nil {
			return err
		}
		id := strings.TrimSpace(args[0])
		if id == "" {
			return exitcode.Newf(exitcode.Usage, "pass a nonempty grant ID from list")
		}
		// The held-worker path pages the target App's history, so it needs an App.
		// A person's by-id read does not (cli#1619): --app is still validated when
		// given, so a malformed ref fails before any request. When given, it also
		// SCOPES the revoke: the grant must belong to that App (snapshotGrantInApp),
		// else it is not found before the confirmation.
		var app, source string
		if claim.input != nil || cmd.Flags().Changed("app") {
			var err error
			app, source, err = cmdutil.TargetApp(cmd, f)
			if err != nil {
				return err
			}
		}
		// A script cannot answer the later metadata-rich prompt. Refuse
		// before building a client or reading even an ended grant.
		if claim.input == nil && !yes && !f.IOStreams.IsInputTerminal() {
			return cmdutil.Confirm(f.IOStreams, yes, "")
		}
		if claim.input != nil {
			if !f.JSON {
				if _, err := fmt.Fprintf(f.IOStreams.ErrOut, "App: %s (%s)\n", strconv.Quote(app), source); err != nil {
					return err
				}
			}
			prompt := fmt.Sprintf("Look up and revoke team snapshot grant %s in App %s (%s)? The lookup records one event per page, including empty pages; if the row is absent, no revoke is sent.", strconv.Quote(id), strconv.Quote(app), source)
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		var selected *snapshotGrantDTO
		if claim.input == nil {
			row, err := lookupSnapshotGrantByID(cmd.Context(), client, id)
			if err != nil {
				return err
			}
			// An explicit --app is never a silent no-op: the grant must belong to the
			// App it names, as the paged lookup required before it (cli#1619).
			if cmd.Flags().Changed("app") {
				inApp, err := snapshotGrantInApp(cmd.Context(), client, app, row.AppID)
				if err != nil {
					return err
				}
				if !inApp {
					return exitcode.Newf(exitcode.NotFound, "team snapshot grant not found")
				}
			}
			selected = &row
		} else {
			include := true
			page, err := collectSnapshotGrants(cmd.Context(), client, app, &include, snapshotTriggerFilter{}, 20, 0, true, claim)
			if err != nil {
				return err
			}
			for i := range page.Items {
				if page.Items[i].ID == id {
					selected = &page.Items[i]
					break
				}
			}
			if selected == nil {
				return exitcode.Newf(exitcode.NotFound, "team snapshot grant not found")
			}
		}
		if selected.RevokedAt != nil {
			if _, err := fmt.Fprintln(f.IOStreams.ErrOut, "grant is already revoked; no mutation sent"); err != nil {
				return err
			}
			return writeSnapshotGrant(f, *selected)
		}
		if claim.input == nil {
			prompt := fmt.Sprintf("Revoke team snapshot grant %s for App %s trigger %s %s?", strconv.Quote(id), strconv.Quote(selected.AppID), strconv.Quote(selected.TriggerKind), strconv.Quote(selected.TriggerID))
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}
		}

		row, err := claim.revoke(cmd.Context(), client, id)
		if err != nil {
			return err
		}
		if row == nil {
			return exitcode.Newf(exitcode.Error, "the server did not confirm revocation; list --include-revoked before retrying")
		}
		d, err := snapshotGrantFrom(*row)
		if err != nil {
			return err
		}
		if d.ID != selected.ID || d.AppID != selected.AppID || d.TriggerKind != selected.TriggerKind || d.TriggerID != selected.TriggerID || d.RevokedAt == nil || d.RevokedBy == nil {
			return exitcode.Newf(exitcode.Error, "the server did not confirm revocation of the selected grant; list --include-revoked before retrying")
		}
		return writeSnapshotGrant(f, d)
	}}
	cmd.Flags().StringVar(&instructionRaw, "instruction", "", instructionFlagUsage)
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation (required non-interactively)")
	return cmd
}

func mapSnapshotWriteError(raw error) error {
	mapped := api.MapErrorKeepingCode(raw)
	code := exitcode.FromError(mapped)
	if code == exitcode.Error || code == exitcode.Unavailable {
		return api.WithServerCode(exitcode.Newf(code, "team snapshot grant outcome unknown (sent once, not retried); list grants before retrying: %s", mapped), raw)
	}
	return mapped
}
