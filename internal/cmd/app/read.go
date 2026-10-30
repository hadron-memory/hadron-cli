package app

import (
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Deliberately separate from the list DTO: this gate exposes identity only.
type appGetDTO struct {
	ID          string  `json:"id"`
	URN         string  `json:"urn"`
	Name        string  `json:"name"`
	AppType     string  `json:"appType"`
	Description *string `json:"description"`
}

type appKeyMetadataDTO struct {
	ID         string  `json:"id"`
	KeyPreview string  `json:"keyPreview"`
	Label      *string `json:"label"`
	CreatedAt  string  `json:"createdAt"`
	LastUsedAt *string `json:"lastUsedAt"`
	RevokedAt  *string `json:"revokedAt"`
}

func requiredReadAppRef(ref string) (string, error) {
	canonical, err := cmdutil.CanonicalAppRef("<app-ref>", ref)
	if err != nil {
		return "", err
	}
	if canonical == "" {
		return "", exitcode.Newf(exitcode.Usage, "an App reference is required; pass an App ID or hrn:app:<root>:<slug>")
	}
	return canonical, nil
}

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <app-ref>",
		Short: "Read one App's identity",
		Long: `Read one App's ID, URN, name, type and nullable description.

Pass an App ID, hrn:app:<root>:<slug>, or the <root>:<slug> short form.
This read does not request settings, nested keys or AI configuration. A null
description stays null in JSON and displays as - in text; it does not say
whether the value is unset or withheld. The server decides access.`,
		Example: `  hadron app get hrn:app:acme.com:support
  hadron app get acme.com:support --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := requiredReadAppRef(args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.GetAppMetadata(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			a := resp.App
			if a == nil {
				return exitcode.Newf(exitcode.NotFound, "no App found for %q", args[0])
			}
			dto := appGetDTO{ID: a.Id, URN: a.Urn, Name: a.Name, AppType: string(a.AppType), Description: a.Description}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "id: %s\nurn: %s\nname: %s\ntype: %s\ndescription: %s\n", output.SafeCell(dto.ID), output.SafeCell(dto.URN), output.SafeCell(dto.Name), output.SafeCell(dto.AppType), output.SafeCell(optionalReadText(dto.Description)))
				return err
			})
		},
	}
}

type appKeyUseDTO struct {
	HostID      string  `json:"hostId"`
	Action      string  `json:"action"`
	Count       *int    `json:"count"`
	OperationID *string `json:"operationId"`
	RefusedCode *string `json:"refusedCode"`
	UsedAt      string  `json:"usedAt"`
}

// appKeyDetailDTO is the --scopes row: the metadata fields plus what the key
// holds. HostUses is a pointer so it is absent unless --host-uses asked for it.
type appKeyDetailDTO struct {
	appKeyMetadataDTO
	Scopes   []appKeyScopeDTO `json:"scopes"`
	HostUses *[]appKeyUseDTO  `json:"hostUses,omitempty"`
}

func newCmdKeyList(f *cmdutil.Factory) *cobra.Command {
	var scopes bool
	var hostUses int
	cmd := &cobra.Command{
		Use:     "list <app-ref> [--scopes [--host-uses <n>]]",
		Aliases: []string{"ls"},
		Short:   "List an App's active key metadata",
		Long: `List active App keys through the server's manager-authorized metadata read.

Pass an App ID, hrn:app:<root>:<slug>, or the <root>:<slug> short form.
Prints only id, keyPreview, label, createdAt, lastUsedAt and revokedAt.
No raw key, nested App.keys, host usage or AI configuration is requested.
This is active-key inventory, not a history of revoked keys. Nullable values
stay null in JSON and display as - in text, without guessing why they are null.
An empty JSON result is []. Access refusals come from the server.

--scopes adds what each key holds now (scope, hostId for host:operate,
grantedAt, grantedBy) in the same single read; a key with none shows [].
--host-uses <n> (with --scopes) also adds each key's newest n host uses
(host, action, count, refusedCode, usedAt), a refused use included. Neither
ever returns the raw key.

The text table replaces control and invisible characters (including newlines,
escapes and bidi/zero-width marks) with a space and cuts a cell at 256
characters, ending it with "…". --json keeps every original value.`,
		Example: `  hadron app key list hrn:app:acme.com:support
  hadron app key list acme.com:support --json
  hadron app key list acme.com:ops --scopes --host-uses 10`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := requiredReadAppRef(args[0])
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("host-uses") {
				if !scopes {
					return exitcode.Newf(exitcode.Usage, "--host-uses needs --scopes")
				}
				if hostUses < 1 {
					return exitcode.Newf(exitcode.Usage, "--host-uses must be at least 1")
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if scopes {
				return listKeysDetail(cmd, f, client, ref, cmd.Flags().Changed("host-uses"), hostUses)
			}
			resp, err := gen.AppKeysMetadata(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			dto := []appKeyMetadataDTO{}
			for _, k := range resp.AppKeys {
				if k == nil {
					return exitcode.Newf(exitcode.Error, "the server returned an invalid App-key metadata row")
				}
				dto = append(dto, appKeyMetadataDTO{ID: k.Id, KeyPreview: k.KeyPreview, Label: k.Label, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt})
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto) == 0 {
					_, err := fmt.Fprintln(w, "no active App keys returned")
					return err
				}
				table := output.NewTable(w, "ID", "PREVIEW", "LABEL", "CREATED", "LAST USED", "REVOKED")
				for _, k := range dto {
					table.Row(output.SafeCell(k.ID), output.SafeCell(k.KeyPreview), output.SafeCell(optionalReadText(k.Label)),
						output.SafeCell(k.CreatedAt), output.SafeCell(optionalReadText(k.LastUsedAt)), output.SafeCell(optionalReadText(k.RevokedAt)))
				}
				return table.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&scopes, "scopes", false, "also show the scopes each key holds")
	cmd.Flags().IntVar(&hostUses, "host-uses", 0, "with --scopes: also show each key's newest <n> host uses")
	return cmd
}

// listKeysDetail is `list --scopes [--host-uses n]`: one appKeys call whose
// selection carries scopes, and host uses only when asked for.
func listKeysDetail(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, ref string, withUses bool, first int) error {
	dto := []appKeyDetailDTO{}
	add := func(m appKeyMetadataDTO, sc []*scopeRow, uses *[]appKeyUseDTO) {
		row := appKeyDetailDTO{appKeyMetadataDTO: m, Scopes: []appKeyScopeDTO{}, HostUses: uses}
		for _, s := range sc {
			if s != nil {
				row.Scopes = append(row.Scopes, appKeyScopeDTO{Scope: s.Scope, HostID: s.HostId, GrantedAt: s.GrantedAt, GrantedBy: s.GrantedBy})
			}
		}
		dto = append(dto, row)
	}
	if withUses {
		resp, err := gen.AppKeysScopesHostUses(cmd.Context(), client, ref, first)
		if err != nil {
			return withServerCode(api.MapError(err), err)
		}
		for _, k := range resp.AppKeys {
			if k == nil {
				return exitcode.Newf(exitcode.Error, "the server returned an invalid App-key row")
			}
			uses := []appKeyUseDTO{}
			for _, u := range k.HostUses {
				if u != nil {
					uses = append(uses, appKeyUseDTO{HostID: u.HostId, Action: u.Action, Count: u.Count, OperationID: u.OperationId, RefusedCode: u.RefusedCode, UsedAt: u.UsedAt})
				}
			}
			sc := make([]*scopeRow, 0, len(k.Scopes))
			for _, s := range k.Scopes {
				if s != nil {
					sc = append(sc, &scopeRow{Scope: s.Scope, HostId: s.HostId, GrantedAt: s.GrantedAt, GrantedBy: s.GrantedBy})
				}
			}
			add(appKeyMetadataDTO{ID: k.Id, KeyPreview: k.KeyPreview, Label: k.Label, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt}, sc, &uses)
		}
	} else {
		resp, err := gen.AppKeysScopes(cmd.Context(), client, ref)
		if err != nil {
			return withServerCode(api.MapError(err), err)
		}
		for _, k := range resp.AppKeys {
			if k == nil {
				return exitcode.Newf(exitcode.Error, "the server returned an invalid App-key row")
			}
			sc := make([]*scopeRow, 0, len(k.Scopes))
			for _, s := range k.Scopes {
				if s != nil {
					sc = append(sc, &scopeRow{Scope: s.Scope, HostId: s.HostId, GrantedAt: s.GrantedAt, GrantedBy: s.GrantedBy})
				}
			}
			add(appKeyMetadataDTO{ID: k.Id, KeyPreview: k.KeyPreview, Label: k.Label, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt}, sc, nil)
		}
	}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if len(dto) == 0 {
			_, err := fmt.Fprintln(w, "no active App keys returned")
			return err
		}
		for i, k := range dto {
			if i > 0 {
				if _, err := fmt.Fprintln(w); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(w, "%s  %s  label %s  created %s  last used %s\n", output.SafeCell(k.ID), output.SafeCell(k.KeyPreview),
				output.SafeCell(optionalReadText(k.Label)), output.SafeCell(k.CreatedAt), output.SafeCell(optionalReadText(k.LastUsedAt))); err != nil {
				return err
			}
			if err := renderKeyScopes(w, appKeyScopesDTO{Scopes: k.Scopes}); err != nil {
				return err
			}
			if k.HostUses != nil {
				if len(*k.HostUses) == 0 {
					if _, err := fmt.Fprintln(w, "host uses: none"); err != nil {
						return err
					}
					continue
				}
				t := output.NewTable(w, "HOST", "ACTION", "COUNT", "REFUSED", "USED")
				for _, u := range *k.HostUses {
					count := "-"
					if u.Count != nil {
						count = fmt.Sprint(*u.Count)
					}
					t.Row(output.SafeCell(u.HostID), output.SafeCell(u.Action), count, output.SafeCell(optionalReadText(u.RefusedCode)), output.SafeCell(u.UsedAt))
				}
				if err := t.Flush(); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// scopeRow is one scope of a key, copied out of whichever generated type the
// selection produced so the two --scopes operations share one rendering.
type scopeRow struct {
	Scope     string
	HostId    *string
	GrantedAt string
	GrantedBy string
}

func optionalReadText(value *string) string {
	if value == nil {
		return "-"
	}
	return *value
}
