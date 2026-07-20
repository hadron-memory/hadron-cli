package object

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var (
		fields     string
		fieldsFile string
		reason     string
	)
	cmd := &cobra.Command{
		Use:   "update <ref> --fields '<json>'",
		Short: "Merge fields into an object",
		Long: `Update an object by its id or node URN. --fields is a JSON object that is
shallow-MERGED into the existing fields: the patch wins on key collision,
unmentioned fields are preserved (the merge is atomic server-side). The merged
result is validated against the collection schema when declared. Prints the
updated flat record.

--reason is recorded in the object's revision history.`,
		Example: `  hadron object update 019f7d… --fields '{"stage":"series-b","fundingUsd":40000000}'`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if changed("fields") && changed("fields-file") {
				return exitcode.Newf(exitcode.Usage, "--fields and --fields-file are mutually exclusive")
			}
			fieldsArg, err := resolveJSON("--fields", fields, fieldsFile)
			if err != nil {
				return err
			}
			if fieldsArg == nil {
				return exitcode.Newf(exitcode.Usage, "--fields (or --fields-file) is required")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var reasonArg *string
			if r := strings.TrimSpace(reason); r != "" {
				reasonArg = &r
			}
			resp, err := gen.UpdateObject(cmd.Context(), client, cmdutil.CanonicalNodeRef(args[0]), *fieldsArg, reasonArg)
			if err != nil {
				return api.MapError(err)
			}
			return writeObject(f, resp.UpdateObject)
		},
	}
	cmd.Flags().StringVar(&fields, "fields", "", "fields to merge, as a JSON object")
	cmd.Flags().StringVar(&fieldsFile, "fields-file", "", "read the fields JSON object from a file")
	cmd.Flags().StringVar(&reason, "reason", "", "why this change was made (recorded in revision history)")
	return cmd
}
