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

Only a person managing the App may give one. These commands send no worker
session header, even from a bound worktree. Listing and revoking through a
worker instruction are not implemented here; there is no --instruction flag.
Set and list take an explicit App ID, qualified URN or root:slug.
JSON keeps revokedAt/revokedBy null for a live grant; human output uses -.
Needs hadron-server ecdde802 or later; older-server refusals are passed through.`}
	for _, c := range []*cobra.Command{newCmdSnapshotSet(f), newCmdSnapshotList(f), newCmdSnapshotRevoke(f)} {
		c.Long += "\n\n" + cmd.Long
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
The returned grant metadata is printed; this does not run the automation.`, Example: `  hadron automation team-snapshot-grant set acme.com:ops --trigger-kind SCHEDULE --trigger-id <schedule-id> --yes --json`, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd.Flags().StringVar(&id, "trigger-id", "", "the schedule or webhook ID (required)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation (required non-interactively)")
	_ = cmd.MarkFlagRequired("trigger-kind")
	_ = cmd.MarkFlagRequired("trigger-id")
	return cmd
}

// collectSnapshotGrants advances by rows actually returned. A broken/nonprogressing
// page fails the whole command, so it never prints a partial list as complete.
func collectSnapshotGrants(ctx context.Context, client graphql.Client, app string, include *bool, limit, offset int, all bool) (snapshotGrantPageDTO, error) {
	d := snapshotGrantPageDTO{Items: []snapshotGrantDTO{}}
	seen := map[string]bool{}
	for {
		r, err := gen.AutomationTeamSnapshotGrants(ctx, client, app, include, limit, offset)
		if err != nil {
			return d, api.MapErrorKeepingCode(err)
		}
		if r == nil || r.AutomationTeamSnapshotGrants == nil {
			return d, exitcode.Newf(exitcode.Error, "the server returned no team snapshot grant page")
		}
		p := r.AutomationTeamSnapshotGrants
		if p.Total < 0 || len(p.Items) > limit || (p.HasMore && len(p.Items) == 0) {
			return d, exitcode.Newf(exitcode.Error, "the server returned an invalid or non-progressing team snapshot grant page")
		}
		for _, g := range p.Items {
			if g == nil {
				return d, exitcode.Newf(exitcode.Error, "the server returned a null team snapshot grant")
			}
			row, err := snapshotGrantFrom(g.AutomationTeamSnapshotGrantFields)
			if err != nil {
				return d, err
			}
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
	cmd := &cobra.Command{Use: "list <app-ref> [--include-revoked] [--limit n] [--offset n]", Aliases: []string{"ls"}, Short: "List an App's team snapshot grants, paging to the end by default", Long: `Read grants in the server's order (oldest first). Without --limit, read every
page starting at --offset (default 0). With --limit, read exactly one page of
1..200 rows; a continuation note goes to stderr when more remain.
Revoked grants are excluded unless --include-revoked is true. A paged walk is
not an atomic snapshot: concurrent changes can affect the list. A later-page
refusal fails the command without printing a partial result.
JSON is {items,total,hasMore}; empty items is [], and total is the server's
count, not a count inferred by the CLI. Human output shows grant, App, trigger
and revocation metadata. No credentials or task content are read.`, Example: `  hadron automation team-snapshot-grant list acme.com:ops --json
  hadron automation team-snapshot-grant list acme.com:ops --include-revoked --limit 20 --offset 20`, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
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
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		d, err := collectSnapshotGrants(cmd.Context(), client, app, include, limit, offset, !cmd.Flags().Changed("limit"))
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
	cmd.Flags().BoolVar(&revoked, "include-revoked", false, "include ended grants; false keeps live grants only")
	cmd.Flags().IntVar(&limit, "limit", 20, "one page size (1..200); omitted walks all pages of 20")
	cmd.Flags().IntVar(&offset, "offset", 0, "skip this many rows before reading (nonnegative)")
	return cmd
}

func snapshotAppRef(ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", exitcode.Newf(exitcode.Usage, "pass a nonempty App ID, qualified URN or root:slug")
	}
	return cmdutil.CanonicalAppRef("<app-ref>", ref)
}

func newCmdSnapshotRevoke(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{Use: "revoke <grant-id> [--yes]", Short: "End one automation team snapshot grant in the target App", Long: `Revoke the exact grant ID from list. The App is --app, else the active App
selected by hadron app use. Read that App's grant history (including revoked
rows), then show the returned App ID and trigger kind/ID for confirmation.
Scripts must pass --yes. A row absent from this App is not found and sends no
mutation; nothing is inferred about any other App. An already ended row is
shown without re-sending revocation. Revocation is terminal; a future set
creates a new row rather than reopening the old one.

One revoke mutation is sent, never retried. A lost answer is an unknown
outcome: list --include-revoked before trying again. JSON returns the grant
with explicit revokedAt/revokedBy. No --instruction arm is implemented.`, Example: `  hadron automation team-snapshot-grant revoke <grant-id> --app acme.com:ops --yes --json`, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		id := strings.TrimSpace(args[0])
		if id == "" {
			return exitcode.Newf(exitcode.Usage, "pass a nonempty grant ID from list")
		}
		app, source, err := cmdutil.TargetApp(cmd, f)
		if err != nil {
			return err
		}
		if !f.JSON {
			if _, err := fmt.Fprintf(f.IOStreams.ErrOut, "App: %s (%s)\n", strconv.Quote(app), source); err != nil {
				return err
			}
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		include := true
		page, err := collectSnapshotGrants(cmd.Context(), client, app, &include, 20, 0, true)
		if err != nil {
			return err
		}
		var selected *snapshotGrantDTO
		for i := range page.Items {
			if page.Items[i].ID == id {
				selected = &page.Items[i]
				break
			}
		}
		if selected == nil {
			return exitcode.Newf(exitcode.NotFound, "team snapshot grant not found")
		}
		if selected.RevokedAt != nil {
			if _, err := fmt.Fprintln(f.IOStreams.ErrOut, "grant is already revoked; no mutation sent"); err != nil {
				return err
			}
			return writeSnapshotGrant(f, *selected)
		}
		prompt := fmt.Sprintf("Revoke team snapshot grant %s for App %s trigger %s %s (%s)?", strconv.Quote(id), strconv.Quote(selected.AppID), strconv.Quote(selected.TriggerKind), strconv.Quote(selected.TriggerID), source)
		if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
			return err
		}
		r, err := gen.RevokeAutomationTeamSnapshotGrant(cmd.Context(), client, id)
		if err != nil {
			return mapSnapshotWriteError(err)
		}
		if r == nil || r.RevokeAutomationTeamSnapshotGrant == nil {
			return exitcode.Newf(exitcode.Error, "the server did not confirm revocation; list --include-revoked before retrying")
		}
		d, err := snapshotGrantFrom(r.RevokeAutomationTeamSnapshotGrant.AutomationTeamSnapshotGrantFields)
		if err != nil {
			return err
		}
		if d.ID != selected.ID || d.AppID != selected.AppID || d.TriggerKind != selected.TriggerKind || d.TriggerID != selected.TriggerID || d.RevokedAt == nil || d.RevokedBy == nil {
			return exitcode.Newf(exitcode.Error, "the server did not confirm revocation of the selected grant; list --include-revoked before retrying")
		}
		return writeSnapshotGrant(f, d)
	}}
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
