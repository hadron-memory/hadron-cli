package asset

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

// assetDeleteDTO is the stable --json shape for a delete or restore. DeletedAt
// is a pointer because that null IS the outcome of a restore.
type assetDeleteDTO struct {
	ID        string  `json:"id"`
	URN       string  `json:"urn"`
	Filename  string  `json:"filename"`
	DeletedAt *string `json:"deletedAt"`
}

func newCmdRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <asset-ref>",
		Aliases: []string{"delete"},
		Short:   "Soft-delete an asset",
		Long: `Delete an asset. It stops appearing in listings, downloads and its public link
at once. For 24 hours, hadron asset restore brings it back (with its public-link
setting); after that it is deleted for good. asset list --include-deleted shows
what can still be restored.

Asks for confirmation on a terminal; pass --yes in scripts.`,
		Example: `  hadron asset rm hrn:asset:acme.com:kb:assets:01a189a840f4717c98af75cf4d7aed09
  hadron asset rm 01a189a840f4717c98af75cf4d7aed09 --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := parseAssetRef(args[0])
			if err != nil {
				return err
			}
			// Confirm, not ConfirmDeletion: the latter wraps its argument in
			// "Delete …? This cannot be undone.", which is both garbled here
			// and false — this delete IS undoable. An operator who reads
			// "cannot be undone" on a reversible action learns to skim the
			// prompts that are not.
			if err := cmdutil.Confirm(f.IOStreams, yes,
				fmt.Sprintf("Soft-delete asset %s? It stays restorable for the retention window.", ref.ID)); err != nil {
				return err
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SoftDeleteAsset(cmd.Context(), client, ref.ID)
			if err != nil {
				return api.MapError(err)
			}
			a := resp.SoftDeleteAsset
			if a == nil {
				return exitcode.Newf(exitcode.NotFound, "no asset found for %q", args[0])
			}
			dto := assetDeleteDTO{ID: a.Id, URN: a.Urn, Filename: a.Filename, DeletedAt: a.DeletedAt}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "deleted %s — restore with: hadron asset restore %s\n", output.SafeCell(dto.Filename), output.SafeCell(dto.ID))
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func newCmdRestore(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore <asset-ref>",
		Short: "Restore a soft-deleted asset",
		Long: `Bring back an asset deleted in the last 24 hours, with its public-link setting.
After 24 hours it cannot be restored.

asset list --include-deleted shows what can still be restored. Restoring does
not ask for confirmation.`,
		Example: `  hadron asset restore 01a189a840f4717c98af75cf4d7aed09`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := parseAssetRef(args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RestoreAsset(cmd.Context(), client, ref.ID)
			if err != nil {
				return api.MapError(err)
			}
			a := resp.RestoreAsset
			if a == nil {
				return exitcode.Newf(exitcode.NotFound, "no asset found for %q", args[0])
			}
			dto := assetDeleteDTO{ID: a.Id, URN: a.Urn, Filename: a.Filename, DeletedAt: a.DeletedAt}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "restored %s\n", output.SafeCell(dto.Filename))
				return nil
			})
		},
	}
	return cmd
}
