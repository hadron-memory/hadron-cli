package app

import (
	"fmt"
	"io"

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
				_, err := fmt.Fprintf(w, "id: %s\nurn: %s\nname: %s\ntype: %s\ndescription: %s\n", output.SafeText(dto.ID), output.SafeText(dto.URN), output.SafeText(dto.Name), output.SafeText(dto.AppType), output.SafeText(optionalReadText(dto.Description)))
				return err
			})
		},
	}
}

func newCmdKeyList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list <app-ref>",
		Aliases: []string{"ls"},
		Short:   "List an App's active key metadata",
		Long: `List active App keys through the server's manager-authorized metadata read.

Pass an App ID, hrn:app:<root>:<slug>, or the <root>:<slug> short form.
Prints only id, keyPreview, label, createdAt, lastUsedAt and revokedAt.
No raw key, nested App.keys, host usage or AI configuration is requested.
This is active-key inventory, not a history of revoked keys. Nullable values
stay null in JSON and display as - in text, without guessing why they are null.
An empty JSON result is []. Access refusals come from the server.

The text table replaces control and invisible characters (including newlines,
escapes and bidi/zero-width marks) with a space and cuts a cell at 256
characters, ending it with "…". --json keeps every original value.`,
		Example: `  hadron app key list hrn:app:acme.com:support
  hadron app key list acme.com:support --json`,
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
}

func optionalReadText(value *string) string {
	if value == nil {
		return "-"
	}
	return *value
}
