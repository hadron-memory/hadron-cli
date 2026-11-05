package automation

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// readGrantDTO is the stable --json shape; prefix is null for a whole-memory
// grant and revokedAt/revokedBy are null for a live one.
type readGrantDTO struct {
	ID          string  `json:"id"`
	AppID       string  `json:"appId"`
	MemoryURN   *string `json:"memoryUrn"`
	Prefix      *string `json:"prefix"`
	TriggerKind string  `json:"triggerKind"`
	TriggerID   string  `json:"triggerId"`
	GrantedBy   string  `json:"grantedBy"`
	CreatedAt   string  `json:"createdAt"`
	RevokedAt   *string `json:"revokedAt"`
	RevokedBy   *string `json:"revokedBy"`
}

func readGrantFrom(g gen.AutomationReadGrantFields) (readGrantDTO, error) {
	if g.Id == "" || g.AppId == "" || g.TriggerKind == "" || g.TriggerId == "" || g.GrantedBy == "" || g.CreatedAt == "" {
		return readGrantDTO{}, exitcode.Newf(exitcode.Error, "the server returned an incomplete read grant; list grants before retrying a write")
	}
	return readGrantDTO{ID: g.Id, AppID: g.AppId, MemoryURN: g.MemoryUrn, Prefix: g.Prefix, TriggerKind: g.TriggerKind, TriggerID: g.TriggerId, GrantedBy: g.GrantedBy, CreatedAt: g.CreatedAt, RevokedAt: g.RevokedAt, RevokedBy: g.RevokedBy}, nil
}

func readGrantOptional(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

func writeReadGrant(f *cmdutil.Factory, d readGrantDTO) error {
	return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "id: %s\napp: %s\nmemory: %s\nprefix: %s\ntrigger: %s %s\ngranted by: %s\ncreated: %s\nrevoked: %s\nrevoked by: %s\n",
			output.SafeCell(d.ID), output.SafeCell(d.AppID), output.SafeCell(readGrantOptional(d.MemoryURN)), output.SafeCell(readGrantOptional(d.Prefix)),
			output.SafeCell(d.TriggerKind), output.SafeCell(d.TriggerID), output.SafeCell(d.GrantedBy), output.SafeCell(d.CreatedAt),
			output.SafeCell(readGrantOptional(d.RevokedAt)), output.SafeCell(readGrantOptional(d.RevokedBy)))
		return err
	})
}

// sameReadGrantScope reports whether the returned grant is for the memory and
// prefix that were asked for. The memory is compared only when the asked-for
// reference is a memory URN (an opaque id cannot be compared with the URN the
// server returns, and no resolution rule is invented for it).
func sameReadGrantScope(d readGrantDTO, mem string, pfx *string) bool {
	if (pfx == nil) != (d.Prefix == nil) || (pfx != nil && *pfx != *d.Prefix) {
		return false
	}
	if strings.HasPrefix(mem, "hrn:mem:") {
		return d.MemoryURN != nil && cmdutil.CanonicalMemoryRef(*d.MemoryURN) == mem
	}
	return true
}

func newCmdGrant(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{Use: "grant <command>", Short: "Give an automation read-only access to one memory or loc prefix", Long: `Manage per-automation READ grants (cor:agt:010:01, rulings 0079/0081). A live
schedule or webhook of an App may read one memory, or one loc prefix of it,
only while it holds this grant; none is given by default. Grants are read-only
(never writes, listing or search), carried by the trigger a run executes, and
survive an edit or re-adoption of the automation. A manual run holds none.

Whoever manages the TARGET memory gives a grant, acting as themselves (a
person, not a worker session). The App's managers or the target's manager may
revoke it. Refusals are the server's and are passed through; every write is
sent once and never retried. This is not the team snapshot grant (see
team-snapshot-grant), which covers an App's roster and tasks.`}
	cmd.AddCommand(newCmdGrantSet(f), newCmdGrantList(f), newCmdGrantRevoke(f))
	return cmd
}

func mapReadGrantWriteError(raw error, what string) error {
	mapped := api.MapErrorKeepingCode(raw)
	code := exitcode.FromError(mapped)
	if code == exitcode.Error || code == exitcode.Unavailable {
		return api.WithServerCode(exitcode.Newf(code, "read grant %s outcome unknown (sent once, not retried); list grants before retrying: %s", what, mapped), raw)
	}
	return mapped
}

func newCmdGrantSet(f *cmdutil.Factory) *cobra.Command {
	var memory, prefix, kind, id string
	var yes bool
	cmd := &cobra.Command{Use: "set <app-ref> --memory <ref> --trigger-kind SCHEDULE|WEBHOOK --trigger-id <id> [--prefix <loc>] [--yes]",
		Short: "Give one schedule or webhook a read grant on a memory or loc prefix",
		Long: `Confirm the App, memory, optional prefix and trigger, then send one grant
mutation. Without --prefix the grant covers the whole memory; with it, the
loc itself and its colon-boundary descendants. A blank --prefix is refused
here (it is a mistake, not the whole memory). An identical repeat returns the
live row. Scripts must pass --yes. No request is retried: if an answer is
lost, list grants to check before trying again.`,
		Example: `  hadron automation grant set acme.com:ops --memory acme.com:notes --prefix docs --trigger-kind SCHEDULE --trigger-id <schedule-id> --yes --json`,
		Args:    cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			app, err := snapshotAppRef(args[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(memory) == "" {
				return exitcode.Newf(exitcode.Usage, "--memory must name a memory (id, URN or root:slug)")
			}
			if kind != "SCHEDULE" && kind != "WEBHOOK" {
				return exitcode.Newf(exitcode.Usage, "--trigger-kind must be SCHEDULE or WEBHOOK; manual runs cannot hold this grant")
			}
			if strings.TrimSpace(id) == "" {
				return exitcode.Newf(exitcode.Usage, "--trigger-id must not be empty")
			}
			var pfx *string
			if cmd.Flags().Changed("prefix") {
				if strings.TrimSpace(prefix) == "" {
					return exitcode.Newf(exitcode.Usage, "--prefix must be a loc; omit it to grant the whole memory")
				}
				p := strings.TrimSpace(prefix)
				pfx = &p
			}
			mem := cmdutil.CanonicalMemoryRef(memory)
			scope := "the whole memory"
			if pfx != nil {
				scope = "prefix " + strconv.Quote(*pfx)
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Give App %s trigger %s %s read-only access to %s of %s?", strconv.Quote(app), kind, strconv.Quote(id), scope, strconv.Quote(mem))); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.SetAutomationReadGrant(cmd.Context(), client, app, mem, pfx, id, kind)
			if err != nil {
				return mapReadGrantWriteError(err, "set")
			}
			if r == nil || r.SetAutomationReadGrant == nil {
				return exitcode.Newf(exitcode.Error, "the server did not confirm a read grant; list grants before retrying")
			}
			d, err := readGrantFrom(r.SetAutomationReadGrant.AutomationReadGrantFields)
			if err != nil {
				return err
			}
			if d.TriggerKind != kind || d.TriggerID != id || d.RevokedAt != nil || d.RevokedBy != nil || !sameReadGrantScope(d, mem, pfx) {
				return exitcode.Newf(exitcode.Error, "the server did not confirm a live grant for the selected trigger, memory and prefix; list grants before retrying")
			}
			return writeReadGrant(f, d)
		}}
	cmd.Flags().StringVar(&memory, "memory", "", "the memory to grant read access to (required)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "a loc prefix within the memory; omit for the whole memory")
	cmd.Flags().StringVar(&kind, "trigger-kind", "", "SCHEDULE or WEBHOOK (required); manual runs have no automation")
	cmd.Flags().StringVar(&id, "trigger-id", "", "the schedule or webhook ID (required)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation (required non-interactively)")
	_ = cmd.MarkFlagRequired("memory")
	_ = cmd.MarkFlagRequired("trigger-kind")
	_ = cmd.MarkFlagRequired("trigger-id")
	return cmd
}

func newCmdGrantList(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var revoked bool
	cmd := &cobra.Command{Use: "list [--app <ref>] [--memory <ref>] [--include-revoked]", Aliases: []string{"ls"},
		Short: "List the read grants you may see",
		Long: `List automation read grants. The server shows an App's managers that App's
grants, and a target's manager the grants on what they manage; nobody sees
more. --app and --memory filter; with neither, every grant you may see is
listed. This does not fall back to the active App. Revoked grants are
excluded unless --include-revoked. JSON is an array; empty is [].`,
		Example: `  hadron automation grant list --app acme.com:ops --json
  hadron automation grant list --memory acme.com:notes --include-revoked`,
		Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			var appRef, memRef *string
			if fl := cmd.Flag("app"); fl != nil && fl.Changed {
				a, err := snapshotAppRef(f.AppFlag)
				if err != nil {
					return err
				}
				appRef = &a
			}
			if cmd.Flags().Changed("memory") {
				if strings.TrimSpace(memory) == "" {
					return exitcode.Newf(exitcode.Usage, "--memory was given a blank value; name a memory or omit it")
				}
				m := cmdutil.CanonicalMemoryRef(memory)
				memRef = &m
			}
			var include *bool
			if cmd.Flags().Changed("include-revoked") {
				include = &revoked
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.AutomationReadGrants(cmd.Context(), client, appRef, memRef, include)
			if err != nil {
				return api.MapError(err)
			}
			if r == nil || r.AutomationReadGrants == nil {
				// The field is non-null in the schema: a missing list is a malformed
				// answer, never an empty inventory.
				return exitcode.Newf(exitcode.Error, "the server returned no read grant list")
			}
			rows := []readGrantDTO{}
			for _, g := range r.AutomationReadGrants {
				d, err := readGrantFrom(g.AutomationReadGrantFields)
				if err != nil {
					return err
				}
				rows = append(rows, d)
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "APP", "MEMORY", "PREFIX", "TRIGGER", "REVOKED")
				for _, g := range rows {
					t.Row(output.SafeCell(g.ID), output.SafeCell(g.AppID), output.SafeCell(readGrantOptional(g.MemoryURN)), output.SafeCell(readGrantOptional(g.Prefix)),
						output.SafeCell(g.TriggerKind+" "+g.TriggerID), output.SafeCell(readGrantOptional(g.RevokedAt)))
				}
				return t.Flush()
			})
		}}
	cmd.Flags().StringVar(&memory, "memory", "", "only grants on this memory")
	cmd.Flags().BoolVar(&revoked, "include-revoked", false, "include ended grants; false keeps live grants only")
	return cmd
}

func newCmdGrantRevoke(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{Use: "revoke <grant-id> [--yes]", Short: "End one automation read grant",
		Long: `Revoke the exact grant ID from list. The App's managers or the target's
manager may revoke. Revocation is terminal; a later set creates a new row.
Scripts must pass --yes. One mutation is sent, never retried: a lost answer is
an unknown outcome, so list --include-revoked before trying again. JSON
returns the grant with its revokedAt/revokedBy.`,
		Example: `  hadron automation grant revoke <grant-id> --yes --json`,
		Args:    cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if id == "" {
				return exitcode.Newf(exitcode.Usage, "pass a nonempty grant ID from list")
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Revoke read grant %s? This is terminal.", strconv.Quote(id))); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.RevokeAutomationReadGrant(cmd.Context(), client, id)
			if err != nil {
				return mapReadGrantWriteError(err, "revoke")
			}
			if r == nil || r.RevokeAutomationReadGrant == nil {
				return exitcode.Newf(exitcode.Error, "the server did not confirm revocation; list --include-revoked before retrying")
			}
			d, err := readGrantFrom(r.RevokeAutomationReadGrant.AutomationReadGrantFields)
			if err != nil {
				return err
			}
			if d.ID != id || d.RevokedAt == nil || d.RevokedBy == nil {
				return exitcode.Newf(exitcode.Error, "the server did not confirm revocation of the selected grant; list --include-revoked before retrying")
			}
			return writeReadGrant(f, d)
		}}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation (required non-interactively)")
	return cmd
}
