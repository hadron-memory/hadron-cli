package object

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var (
		memory     string
		objectType string
		fields     string
		fieldsFile string
		key        string
		name       string
	)
	cmd := &cobra.Command{
		Use:   "create -m <memory> --type <type> --fields '<json>'",
		Short: "Create an object in a collection",
		Long: `Create an object — a node in the memory's <type> collection whose typed
properties are the given fields. Prints the flat record { id, type, ...fields }.

--fields takes the fields inline as a JSON object, or --fields-file from a file.
--key sets a human-meaningful natural id (a single loc segment, no ':'); omit it
for a server-generated id. --name overrides the auto-derived node name.

id and type are reserved and cannot be field names. On a schema-governed memory
the fields are validated against the collection.`,
		Example: `  hadron object create -m acme.com::market --type competitor \
    --fields '{"name":"Letta","stage":"series-a","fundingUsd":12000000}' --key letta`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if objectType == "" {
				return exitcode.Newf(exitcode.Usage, "--type is required")
			}
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
			var keyArg, nameArg *string
			if changed("key") {
				keyArg = &key
			}
			if changed("name") {
				nameArg = &name
			}
			resp, err := gen.CreateObject(cmd.Context(), client, cmdutil.CanonicalMemoryRef(memory), objectType, *fieldsArg, keyArg, nameArg)
			if err != nil {
				return api.MapError(err)
			}
			return writeObject(f, resp.CreateObject)
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().StringVar(&objectType, "type", "", "collection / object type (required)")
	cmd.Flags().StringVar(&fields, "fields", "", "the object's fields as a JSON object")
	cmd.Flags().StringVar(&fieldsFile, "fields-file", "", "read the fields JSON object from a file")
	cmd.Flags().StringVar(&key, "key", "", "natural id — a single loc segment, no ':' (omit for a generated id)")
	cmd.Flags().StringVar(&name, "name", "", "override the auto-derived node name")
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}
