package run

import (
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

func newCmdLatest(f *cmdutil.Factory) *cobra.Command {
	var previous bool
	cmd := &cobra.Command{Use: "latest <app-ref> [--previous]", Short: "Read an App's latest concrete run, or the one before it", Long: `Ask latestAppRun for the most recent run of an explicit App ID, qualified
URN or root:slug. --previous asks for the run before the latest; it is omitted
unless given, and --previous=false sends false. The result has the same full
JSON/detail shape as run get, including its concrete run ID. No magic latest
or previous run ID is constructed, and no run is started or changed.
A null result exits 4: no readable run was returned, not proof that none exists.
The server decides access; the CLI does not fall back to a different App.`, Example: `  hadron run latest acme.com:team --json
  hadron run latest acme.com:team --previous`, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(args[0]) == "" {
			return exitcode.Newf(exitcode.Usage, "pass an App ID or qualified URN")
		}
		ref, err := cmdutil.CanonicalAppRef("<app-ref>", args[0])
		if err != nil {
			return err
		}
		var before *bool
		if cmd.Flags().Changed("previous") {
			before = &previous
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		r, err := gen.LatestAppRun(cmd.Context(), client, ref, before)
		if err != nil {
			return api.MapErrorKeepingCode(err)
		}
		if r == nil || r.LatestAppRun == nil {
			return exitcode.Newf(exitcode.NotFound, "no readable run was returned for this App")
		}
		if r.LatestAppRun.Id == "" {
			return exitcode.Newf(exitcode.Error, "the server returned an incomplete run")
		}
		d := dtoFromFields(r.LatestAppRun.AppRunFields)
		return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error { return writeRunDetail(w, d) })
	}}
	cmd.Flags().BoolVar(&previous, "previous", false, "read the run before the latest (default: latest)")
	return cmd
}
