package drive

import (
	"fmt"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/spf13/cobra"
)

const createDocUnknown = "the outcome is unknown; check the drive before retrying (a repeat creates another document)"

func createDoc(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	var title string
	var yes bool
	c := &cobra.Command{
		Use:   "create-doc <nodeRef>",
		Short: "Copy a Hadron node to a new Google Doc in a connected Drive",
		Long: `Create a NEW Google Doc from a Hadron node. The node reference is its ID or
fully-qualified URN. This copies its content to the connection owner's external
Google account; it cannot be recalled from Hadron. Every call creates another
document: this command never updates an existing Doc.

Choose the destination with --connection. Without --folder the Doc goes in the
Drive root; without --title its title is the node's name. The server reads the
node and resolves includes with your access; the CLI sends only its reference.
An encrypted node needs the global --unlock <memory-ref> option.

You must own the connection and act as yourself. App, headless and impersonated
callers are refused by the server; drive.read does not authorize creation.
The CLI makes no permission preflight, and passes server refusals through.

A terminal asks before any request. Scripts must pass --yes. Exactly one
mutation is sent, never retried. If its answer is lost, the outcome is unknown:
check the Drive before trying again, since a repeat creates another Doc.

Success prints the returned file's metadata, like "drive get"; --json returns
the file object. Size is in bytes, or null (not 0) for a Google Doc. No content
is printed. "drive export" only reads a Drive file to local stdout and never
calls this creation operation.

` + connectionHelp,
		Example: "  hadron drive create-doc hrn:node:example.com:kb:notes --connection <connection-id> --yes --json\n  hadron drive create-doc <node-id> --connection <connection-id> --folder <folder-id> --title 'Meeting notes'",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := o.validate(); err != nil {
				return err
			}
			node := cmdutil.CanonicalNodeRef(args[0])
			if strings.TrimSpace(node) == "" {
				return exitcode.Newf(exitcode.Usage, "give a nonempty node ID or fully-qualified URN")
			}
			folder, err := optional(c, "folder", o.folder)
			if err != nil {
				return err
			}
			givenTitle, err := optional(c, "title", title)
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Create a new Google Doc from node %q in connected Drive %q? This copies content to an external account and cannot be recalled from Hadron.", node, o.connection)
			if folder != nil {
				prompt += fmt.Sprintf(" Folder: %q.", *folder)
			}
			if givenTitle != nil {
				prompt += fmt.Sprintf(" Title: %q.", *givenTitle)
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.DriveCreateDoc(c.Context(), client, o.connection, node, folder, givenTitle)
			if err != nil {
				mapped := api.MapErrorKeepingCode(err)
				code := exitcode.FromError(mapped)
				if code == exitcode.Unavailable || (code == exitcode.Error && len(api.ServerMessages(err)) == 0) {
					return exitcode.Newf(exitcode.Unavailable, "%s", createDocUnknown)
				}
				return mapped
			}
			if r == nil || r.ExportNodeToGoogleDoc == nil || r.ExportNodeToGoogleDoc.Id == "" {
				return exitcode.Newf(exitcode.Unavailable, "%s", createDocUnknown)
			}
			row, err := project(r.ExportNodeToGoogleDoc.DriveReadFile)
			if err != nil {
				return exitcode.Newf(exitcode.Unavailable, "%s", createDocUnknown)
			}
			return render(f, o.connection, "Created Google Doc metadata (data):", row, false)
		},
	}
	scope(c, o)
	c.Flags().StringVar(&o.folder, "folder", "", "destination folder ID; omitted uses the Drive root")
	c.Flags().StringVar(&title, "title", "", "new document title; omitted uses the node's name")
	c.Flags().BoolVar(&yes, "yes", false, "confirm copying node content to an external account (required without a terminal)")
	return c
}
