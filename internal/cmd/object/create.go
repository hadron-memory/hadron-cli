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
		Long: `Add a record to a collection. --type names the collection (for example
competitor) and --fields gives the record's fields as a JSON object. Prints the
new record, including the id that get, update and delete take.

Wrap the JSON in single quotes, as in the example, so the shell passes it
unchanged; or put it in a file and pass --fields-file <path>.

--key gives the record a readable id of your own (one word, no ':'), so it can
also be named hrn:node:<org>:<memory>:<type>:<key>. Without --key the server
generates one. --name sets the title shown in node listings (default: the
record's "name" or "title" field, else the type).

id and type cannot be field names. If the memory declares a schema for the
collection, a missing required field or a value of the wrong type is refused
(exit 2), naming the field; see hadron object --help.`,
		Example: `  hadron object create -m hrn:mem:acme.com:market --type competitor \
    --fields '{"name":"Letta","stage":"series-a","fundingUsd":12000000}' --key letta
  hadron object create -m hrn:mem:acme.com:market --type competitor --fields-file zep.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if objectType == "" {
				return exitcode.Newf(exitcode.Usage, "--type is required")
			}
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
			// The flag was set (guarded above) but resolved empty — an explicit
			// --fields "" / empty file is a bad value, not an absent one.
			if fieldsArg == nil {
				return exitcode.Newf(exitcode.Usage, "--fields must contain a JSON object")
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
