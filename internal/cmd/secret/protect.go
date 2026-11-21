package secret

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdProtect(f *cmdutil.Factory) *cobra.Command {
	var on, off bool
	cmd := &cobra.Command{
		Use:   "protect <id|urn> (--on | --off)",
		Short: "Open a change that protects or unprotects an App secret",
		Long: `Ask to turn protection on or off for an App secret. A protected secret changes
only through a passkey ceremony that a person completes on a server page, so this
command never applies the change: it opens a change request and prints the
request id and the ceremony URL, exactly as the server sent them. Give the URL
to the person who must sign; hadron never completes the ceremony itself.

The same holds for any later secret create, update, set or rm that reaches a
protected secret: it is staged, not applied, and answers with the same request
id and URL.

Exit codes: 5 the change is staged (the request id and URL are on stderr, or in
the --json error); 2 a server that does not offer protected-secret changes
(signing is off), or a caller that is not a person acting as themselves, or
neither --on nor --off; 8 you may not change this secret; 4 no such secret.
Check a request with hadron secret change get, and withdraw it with
hadron secret change withdraw.`,
		Example: `  hadron secret protect 01a18cf643247d0fa261f320ec74d878 --on
  hadron secret protect 01a18cf643247d0fa261f320ec74d878 --off --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if on == off {
				return exitcode.Newf(exitcode.Usage, "pass exactly one of --on or --off")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			ref := args[0]
			resp, err := gen.SetSecretProtected(cmd.Context(), client, ref, on)
			if err != nil {
				if api.IsFrameworkValidationOnlyFor(err, "setSecretProtected") {
					return api.WithServerCode(exitcode.Newf(exitcode.Usage, "%s", noProtectedChanges), err)
				}
				return mapChange(err, "the protection change for secret "+output.SafeCell(ref))
			}
			if resp == nil || resp.SetSecretProtected == nil {
				return exitcode.Newf(exitcode.Error, "server returned no secret")
			}
			// The server answered with the secret instead of staging a request:
			// the change applied. Show the secret as it now stands.
			dto := dtoFromFields(resp.SetSecretProtected.SecretFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeSecretLine(w, "✓ updated", dto)
			})
		},
	}
	cmd.Flags().BoolVar(&on, "on", false, "protect the secret")
	cmd.Flags().BoolVar(&off, "off", false, "remove the secret's protection")
	return cmd
}
