package auth

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

// cor:aut:010:05 / ruling 0224: only the answer, never identity,
// credential source, a refusal cause or credential material.
type statusResult struct {
	Status string `json:"status"`
}

func newCmdStatus(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Ask the server whether this request's credential authenticates",
		Long: `Ask the server whether the credential presented on this request authenticates.
The active credential comes from HADRON_TOKEN, a live impersonation session or
the stored login, in that order. With none held, the query is anonymous.

AUTHENTICATED exits 0. REFUSED exits 3 and says a credential was presented but
the server did not accept it. ANONYMOUS exits 3 and says none was presented.
No answer includes a reason, cause, identity or credential. --json prints only
{"status":"AUTHENTICATED"}, {"status":"REFUSED"} or {"status":"ANONYMOUS"}.

A check that fails, an older server without authenticationStatus or an invalid
answer is an error, never REFUSED. Such failures do not exit 3; missing or
unknown status values exit 7. No fallback to authContext and no automatic retry.
Use hadron auth whoami for the signed-in user's profile.`,
		Example: "  hadron auth status\n  hadron auth status --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// This operation is public: absence of a local credential must not turn
			// into the factory's authenticated-client refusal before the server asks.
			client, _, err := f.PublicGraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CheckAuthenticationStatus(cmd.Context(), client)
			if err != nil {
				mapped := api.MapError(err)
				if exitcode.FromError(mapped) == exitcode.AuthRequired {
					// An error envelope is not the enum REFUSED. Do not reclassify a
					// check failure as a credential verdict, or echo its alleged cause.
					return exitcode.Newf(exitcode.Error, "authentication status check failed; no authentication answer was returned")
				}
				return mapped
			}
			dto := statusResult{Status: string(resp.AuthenticationStatus)}
			var line string
			code := exitcode.AuthRequired
			switch dto.Status {
			case "AUTHENTICATED":
				line = "credential authenticated"
				code = exitcode.OK
			case "REFUSED":
				line = "a credential was presented and the server did not accept it"
			case "ANONYMOUS":
				line = "no credential was presented"
			default:
				return exitcode.Newf(exitcode.Unavailable, "authentication status check returned no valid answer")
			}
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { _, err := fmt.Fprintln(w, line); return err }); err != nil {
				return err
			}
			if code != exitcode.OK {
				return exitcode.Silent(code)
			}
			return nil
		},
	}
}
