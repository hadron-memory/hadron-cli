package app

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

type memoryGrantDTO struct {
	ID              string  `json:"id"`
	AppID           string  `json:"appId"`
	MemoryURN       *string `json:"memoryUrn"`
	Prefix          string  `json:"prefix"`
	RegisterNodeURN *string `json:"registerNodeUrn"`
	GrantedBy       string  `json:"grantedBy"`
	CreatedAt       string  `json:"createdAt"`
	RevokedAt       *string `json:"revokedAt"`
	RevokedBy       *string `json:"revokedBy"`
}

func memoryGrantFrom(g gen.AppMemoryGrantFields) (memoryGrantDTO, error) {
	if g.Id == "" || g.AppId == "" || g.Prefix == "" {
		return memoryGrantDTO{}, exitcode.Newf(exitcode.Error, "the server returned an incomplete memory write grant")
	}
	return memoryGrantDTO{ID: g.Id, AppID: g.AppId, MemoryURN: g.MemoryUrn, Prefix: g.Prefix, RegisterNodeURN: g.RegisterNodeUrn, GrantedBy: g.GrantedBy, CreatedAt: g.CreatedAt, RevokedAt: g.RevokedAt, RevokedBy: g.RevokedBy}, nil
}

// A null memory URN may be concealed; distinguish it from absent optional metadata.
func memoryGrantMemoryText(value *string) string {
	if value == nil {
		return "?"
	}
	return *value
}

func writeMemoryGrant(f *cmdutil.Factory, d memoryGrantDTO) error {
	return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "id: %s\napp: %s\nmemory: %s\nprefix: %s\nregister: %s\ngranted by: %s\ncreated: %s\nrevoked: %s\nrevoked by: %s\n", output.SafeCell(d.ID), output.SafeCell(d.AppID), output.SafeCell(memoryGrantMemoryText(d.MemoryURN)), output.SafeCell(d.Prefix), output.SafeCell(optionalReadText(d.RegisterNodeURN)), output.SafeCell(d.GrantedBy), output.SafeCell(d.CreatedAt), output.SafeCell(optionalReadText(d.RevokedAt)), output.SafeCell(optionalReadText(d.RevokedBy)))
		return err
	})
}

func newCmdMemoryGrant(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{Use: "memory-grant <command>", Short: "Manage prefix-scoped writes by an App's userless runs", Long: `Manage the server's memory write grants for an App's userless runs.
A grant covers one loc prefix and its colon-boundary descendants: reports:team
covers reports:team:daily, but not reports:teamX. It never grants a whole memory.
These commands grant no read access and do not change a user's own authority.
The server checks every write again, including the memory's current class.

Only a person managing the App, acting as themselves, may add, list or revoke.
A setter must also be able to write the target memory. Shared memories must
belong to the App's own org or team space; personal/private memories require
the owner, who must also manage the App. Encrypted and system-class memories
are never grantable. Listing personal/private grants is owner-only. Unknown
or inaccessible targets may receive the same refusal; it proves no existence.
The CLI does not look up or reproduce these authorization decisions.`}
	for _, child := range []*cobra.Command{newCmdMemoryGrantAdd(f), newCmdMemoryGrantList(f), newCmdMemoryGrantRM(f)} {
		child.Long += "\n\n" + cmd.Long
		cmd.AddCommand(child)
	}
	return cmd
}

func newCmdMemoryGrantAdd(f *cmdutil.Factory) *cobra.Command {
	var memory, prefix, register string
	var yes bool
	cmd := &cobra.Command{Use: "add <app-ref> --memory <ref> --prefix <loc> [--register-node <urn>] [--yes]", Short: "Grant an App's userless runs writes under one memory loc prefix", Long: `Grant prefix-scoped node create/update access. Pass an App ID or qualified
URN (the root:slug App shorthand also works), a memory ID/URN or root:slug,
and a nonempty loc prefix. The server validates the prefix and authority.
--register-node is an optional node-URN citation, not evidence that the cited
line exists; it is omitted unless given. Empty reference flags are refused.

The target App, memory and prefix are confirmed before sending. Scripts need
--yes. This is one mutation, never retried. A lost answer is an unknown
outcome: list the App's grants before retrying. The server decides whether an
existing live grant is reused. No file, credential or source content is read.
JSON is the returned grant metadata; nullable memory/citation/revoker fields
stay null. Human output uses ? for a concealed/null memory and - for other null metadata.`, Example: `  hadron app memory-grant add acme.com:team --memory acme.com:notes --prefix reports:team --yes
  hadron app memory-grant add acme.com:team --memory acme.com:notes --prefix reports --register-node hrn:node:acme.com:notes:register:1 --yes --json`, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ref, err := requiredReadAppRef(args[0])
		if err != nil {
			return err
		}
		if strings.TrimSpace(memory) == "" || strings.TrimSpace(prefix) == "" {
			return exitcode.Newf(exitcode.Usage, "--memory and --prefix must be nonempty")
		}
		memoryRef := cmdutil.CanonicalMemoryRef(strings.TrimSpace(memory))
		var citation *string
		if cmd.Flags().Changed("register-node") {
			if strings.TrimSpace(register) == "" {
				return exitcode.Newf(exitcode.Usage, "--register-node must be a nonempty node URN")
			}
			citation = &register
		}
		if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Grant App %s writes in memory %s under prefix %s?", strconv.Quote(ref), strconv.Quote(memoryRef), strconv.Quote(prefix))); err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		r, err := gen.SetAppMemoryWriteGrant(cmd.Context(), client, ref, memoryRef, prefix, citation)
		if err != nil {
			return api.MapErrorKeepingCode(err)
		}
		if r == nil || r.SetAppMemoryWriteGrant == nil {
			return exitcode.Newf(exitcode.Error, "the server did not confirm a memory grant; list grants before retrying")
		}
		d, err := memoryGrantFrom(r.SetAppMemoryWriteGrant.AppMemoryGrantFields)
		if err != nil {
			return err
		}
		return writeMemoryGrant(f, d)
	}}
	cmd.Flags().StringVar(&memory, "memory", "", "target memory ID, URN or root:slug (required)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "loc prefix including its colon-boundary descendants (required)")
	cmd.Flags().StringVar(&register, "register-node", "", "optional node-URN citation; no node is fetched")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation (required non-interactively)")
	_ = cmd.MarkFlagRequired("memory")
	_ = cmd.MarkFlagRequired("prefix")
	return cmd
}

func newCmdMemoryGrantList(f *cmdutil.Factory) *cobra.Command {
	var revoked bool
	cmd := &cobra.Command{Use: "list <app-ref>", Aliases: []string{"ls"}, Short: "List an App's visible memory write grants, oldest first", Long: `Read the server's grant list once, in server order. Revoked rows are excluded
unless --include-revoked is true. Personal/private grants belonging to someone
else are hidden by the server, so this is not an inventory of hidden grants.
Pass an explicit App ID, URN or root:slug; ambient --app is not the target.
JSON is an array of grant metadata ([] when empty), with nullable fields kept
as null. Text shows ? for a null memory and - for other null metadata. No keys or node content are requested.`, Example: `  hadron app memory-grant list acme.com:team --json
  hadron app memory-grant list acme.com:team --include-revoked`, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ref, err := requiredReadAppRef(args[0])
		if err != nil {
			return err
		}
		var include *bool
		if cmd.Flags().Changed("include-revoked") {
			include = &revoked
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		r, err := gen.AppMemoryWriteGrants(cmd.Context(), client, ref, include)
		if err != nil {
			return api.MapErrorKeepingCode(err)
		}
		if r == nil || r.AppMemoryWriteGrants == nil {
			return exitcode.Newf(exitcode.Error, "the server returned no memory grant list")
		}
		rows := make([]memoryGrantDTO, 0, len(r.AppMemoryWriteGrants))
		for _, g := range r.AppMemoryWriteGrants {
			if g == nil {
				return exitcode.Newf(exitcode.Error, "the server returned a null memory grant")
			}
			d, err := memoryGrantFrom(g.AppMemoryGrantFields)
			if err != nil {
				return err
			}
			rows = append(rows, d)
		}
		return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
			t := output.NewTable(w)
			t.Row("ID", "MEMORY", "PREFIX", "REGISTER", "REVOKED")
			for _, d := range rows {
				t.Row(output.SafeCell(d.ID), output.SafeCell(memoryGrantMemoryText(d.MemoryURN)), output.SafeCell(d.Prefix), output.SafeCell(optionalReadText(d.RegisterNodeURN)), output.SafeCell(optionalReadText(d.RevokedAt)))
			}
			return t.Flush()
		})
	}}
	cmd.Flags().BoolVar(&revoked, "include-revoked", false, "include revoked rows (default: live rows only)")
	return cmd
}

func newCmdMemoryGrantRM(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{Use: "rm <grant-id> [--yes]", Short: "Revoke a memory write grant; keep its historical row", Long: `Revoke one grant by the ID from app memory-grant list. The App's next write
using that grant is refused. The row stays; revoking an already revoked row
changes nothing. Confirmation is required; scripts pass --yes.
One mutation is sent and never retried. If its answer is lost, list with
--include-revoked to check the outcome before retrying. JSON returns the grant
metadata including revokedAt; human output preserves nullable metadata as -.`, Example: `  hadron app memory-grant rm <grant-id> --yes --json`, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		id := strings.TrimSpace(args[0])
		if id == "" {
			return exitcode.Newf(exitcode.Usage, "pass the grant ID from app memory-grant list")
		}
		if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Revoke memory write grant %s?", strconv.Quote(id))); err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		r, err := gen.RevokeAppMemoryWriteGrant(cmd.Context(), client, id)
		if err != nil {
			return api.MapErrorKeepingCode(err)
		}
		if r == nil || r.RevokeAppMemoryWriteGrant == nil || r.RevokeAppMemoryWriteGrant.RevokedAt == nil {
			return exitcode.Newf(exitcode.Error, "the server did not confirm revocation; list grants with --include-revoked")
		}
		d, err := memoryGrantFrom(r.RevokeAppMemoryWriteGrant.AppMemoryGrantFields)
		if err != nil {
			return err
		}
		return writeMemoryGrant(f, d)
	}}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation (required non-interactively)")
	return cmd
}
