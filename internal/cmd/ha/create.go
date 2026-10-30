package ha

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var (
		org, slug, name, url, tokenFile string
		allowOps                        []string
		disabled                        bool
	)
	cmd := &cobra.Command{
		Use:   "create --org <ref> --slug <slug> --name <name> --url <url> [--token-file <src>]",
		Short: "Register a Home Assistant instance (org admin)",
		Long: `Register a Home Assistant instance for an organization. You must be an admin
or owner of the organization (--org, an organization ID or hrn:org:<slug> URN).

--slug names the instance in its ops, ha__<slug>__<op>, and can never be changed.
--url is the instance's address; the server decides which addresses it accepts.

The access token (a Home Assistant long-lived access token) is never taken as an
argument, where your shell history and the process list would keep it:
  --token-file -       read stdin
  --token-file @path   read a file (a plain path works too)
  (omitted)            read piped stdin, or prompt without echo on a terminal
It is stored encrypted and is write-only: no command ever prints it, and
"ha update --token-file" replaces it.

--allow-op <op> (repeatable) allows only those ops; leave it out to allow every
op in the catalog ("hadron ha ops <id>" lists them). --disabled registers the
instance switched off.

Registering contacts nothing: the Home Assistant host is not called, and no
run may use an op until the policy chain allows tool.ha__<slug>__<op>.

A slug already used in the organization, a malformed slug or an address the
server does not accept is refused (exit 2); a member who is not an admin is
refused (exit 8). Nothing is registered either way.

Prints "✓ registered Home Assistant instance <slug> (id <id>, enabled)"; --json
prints the registered instance as "ha get --json" does, without the token.`,
		Example: `  printf '%s' "$HA_TOKEN" | hadron ha create --org hrn:org:acme.com --slug home \
    --name "Home" --url https://ha.example.com --token-file -
  hadron ha create --org hrn:org:acme.com --slug cabin --name Cabin \
    --url https://cabin.example.com --token-file @ha-token.txt --allow-op get_state --disabled`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			token, err := readToken(f.IOStreams, tokenFile, cmd.Flags().Changed("token-file"))
			if err != nil {
				return err
			}
			var allowArg *[]string
			if cmd.Flags().Changed("allow-op") {
				allowArg = &allowOps
			}
			var enabledArg *bool
			if cmd.Flags().Changed("disabled") {
				v := !disabled
				enabledArg = &v
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CreateHomeAssistantInstance(cmd.Context(), client, org, slug, name, url, token, allowArg, enabledArg)
			if err != nil {
				return writeError(err, "hadron ha list --org "+cmdutil.ShellQuote(org))
			}
			if resp == nil || resp.CreateHomeAssistantInstance == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no instance; run `hadron ha list --org %s` to see whether %s was registered", cmdutil.ShellQuote(org), cmdutil.ShellQuote(slug))
			}
			dto := dtoFromFields(resp.CreateHomeAssistantInstance.HomeAssistantInstanceFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ registered Home Assistant instance %s (id %s, %s)\n", output.SafeCell(dto.Slug), output.SafeCell(dto.ID), dto.statusLabel())
				return err
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "owning organization (ID or URN; required)")
	cmd.Flags().StringVar(&slug, "slug", "", "slug used in ha__<slug>__<op> (can never be changed; required)")
	cmd.Flags().StringVar(&name, "name", "", "display name (required)")
	cmd.Flags().StringVar(&url, "url", "", "the instance's address (required)")
	cmd.Flags().StringVar(&tokenFile, "token-file", "", `read the access token from "-" stdin, "@file", or a file path`)
	cmd.Flags().StringArrayVar(&allowOps, "allow-op", nil, "allow only this op (repeatable; leave out to allow every op)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "register the instance switched off")
	_ = cmd.MarkFlagRequired("org")
	_ = cmd.MarkFlagRequired("slug")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}
