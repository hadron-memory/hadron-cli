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
		Long: `Change some fields of a record and keep the rest. --fields is a JSON object:
each key you give replaces that field's value, keys you leave out keep theirs,
and a new key adds a field. The merge happens on the server in one step. Prints
the updated record.

To clear a field, set it to null ('{"stage":null}'): the key stays, with the
value null. To remove a key, replace the whole record with
hadron node update <ref> --properties '<every field you keep>'.

Name the record by its id or node URN. --reason is saved with the change in the
record's revision history (hadron node revision list <ref>). If the memory
declares a schema for the collection, the result must satisfy it (exit 2
otherwise).`,
		Example: `  hadron object update 01a189a8175c7145b6c3680628ef0101 --fields '{"stage":"series-b","fundingUsd":40000000}'
  hadron object update hrn:node:acme.com:market:competitor:letta --fields '{"stage":null}' --reason "stage unknown"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if changed("fields") && changed("fields-file") {
				return exitcode.Newf(exitcode.Usage, "--fields and --fields-file are mutually exclusive")
			}
			if !changed("fields") && !changed("fields-file") {
				return exitcode.Newf(exitcode.Usage, "--fields (or --fields-file) is required")
			}
			fieldsArg, err := resolveJSON("--fields", fields, fieldsFile)
			if err != nil {
				return err
			}
			// Set but resolved empty — an explicit --fields "" is a bad value.
			if fieldsArg == nil {
				return exitcode.Newf(exitcode.Usage, "--fields must contain a JSON object")
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
