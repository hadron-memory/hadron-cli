package secret

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

type versionDTO struct {
	VersionNo         int     `json:"versionNo"`
	SizeBytes         int     `json:"sizeBytes"`
	WrittenAt         string  `json:"writtenAt"`
	SupersededAt      *string `json:"supersededAt"`
	WrittenByUserID   *string `json:"writtenByUserId"`
	WrittenByWorkerID *string `json:"writtenByWorkerId"`
}

type versionsDTO struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	CurrentVersion *int         `json:"currentVersion"`
	Protected      bool         `json:"protected"`
	Total          int          `json:"total"`
	HasMore        bool         `json:"hasMore"`
	Versions       []versionDTO `json:"versions"`
}

func newCmdVersions(f *cmdutil.Factory) *cobra.Command {
	var limit, offset int
	var all bool
	cmd := &cobra.Command{
		Use:   "versions <id|urn> [--limit N] [--offset N] [--all]",
		Short: "List an App secret's kept versions (never values)",
		Long: `List the versions the server keeps for a secret, newest first: number, size in
bytes, when it was written and by whom, and when it was superseded. These are
the KEPT versions, not necessarily the full history: the server retires old
ones. No value is ever returned.

The current version and whether the secret is protected are shown as the
server answers them. An org or user secret has no versions: the list is empty
and the current version is "-".

The server pages the list (default 20, at most 200 a page). --limit and
--offset pass straight through; --all walks the pages to the end. A secret that
does not exist and one you may not see both exit 4.

--json gives id, name, currentVersion, protected, total, hasMore and versions
(versionNo, sizeBytes, writtenAt, supersededAt, writtenByUserId,
writtenByWorkerId); an empty list is [].`,
		Example: `  hadron secret versions 01a18cf643247d0fa261f320ec74d878
  hadron secret versions 01a18cf643247d0fa261f320ec74d878 --all --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && (cmd.Flags().Changed("limit") || cmd.Flags().Changed("offset")) {
				return exitcode.Newf(exitcode.Usage, "--all cannot be combined with --limit/--offset")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			out := versionsDTO{Versions: []versionDTO{}}
			fetch := func(l, o *int) ([]versionDTO, int, error) {
				resp, err := gen.SecretVersions(cmd.Context(), client, args[0], l, o)
				if err != nil {
					return nil, 0, api.MapErrorKeepingCode(err)
				}
				if resp == nil || resp.Secret == nil {
					return nil, 0, exitcode.Newf(exitcode.NotFound, "secret %q not found (or not visible to you)", args[0])
				}
				s := resp.Secret
				out.ID, out.Name, out.CurrentVersion, out.Protected = s.Id, s.Name, s.CurrentVersion, s.Protected
				out.Total, out.HasMore = s.Versions.Total, s.Versions.HasMore
				page := make([]versionDTO, 0, len(s.Versions.Items))
				for _, v := range s.Versions.Items {
					if v == nil {
						continue
					}
					page = append(page, versionDTO{VersionNo: v.VersionNo, SizeBytes: v.SizeBytes, WrittenAt: v.WrittenAt, SupersededAt: v.SupersededAt, WrittenByUserID: v.WrittenByUserId, WrittenByWorkerID: v.WrittenByWorkerId})
				}
				return page, s.Versions.Total, nil
			}
			if all {
				items, err := api.CollectAll(func(l, o int) ([]versionDTO, int, error) { return fetch(&l, &o) })
				if err != nil {
					return err
				}
				out.Versions, out.HasMore = items, false
			} else {
				items, _, err := fetch(pagePtr(cmd, "limit", limit), pagePtr(cmd, "offset", offset))
				if err != nil {
					return err
				}
				out.Versions = items
			}
			return output.Write(f.IOStreams, f.JSON, out, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "secret %s  current version: %s  protected: %t\n", output.SafeCell(out.Name), versionCell(out.CurrentVersion), out.Protected); err != nil {
					return err
				}
				if len(out.Versions) == 0 {
					if out.Total > 0 {
						_, err := fmt.Fprintf(w, "%d kept versions (none shown on this page; --limit 0 counts only)\n", out.Total)
						return err
					}
					_, err := fmt.Fprintln(w, "no versions")
					return err
				}
				t := output.NewTable(w, "VERSION", "SIZE", "WRITTEN", "BY", "SUPERSEDED")
				for _, v := range out.Versions {
					by := output.Dash(v.WrittenByUserID)
					if v.WrittenByUserID == nil {
						by = output.Dash(v.WrittenByWorkerID)
					}
					t.Row(fmt.Sprint(v.VersionNo), fmt.Sprintf("%d B", v.SizeBytes), output.SafeCell(v.WrittenAt), output.SafeCell(by), output.SafeCell(output.Dash(v.SupersededAt)))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				if out.HasMore {
					_, err := fmt.Fprintf(w, "kept versions: showing %d of %d; more with --offset or --all\n", len(out.Versions), out.Total)
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "versions per page (the server's default and cap apply)")
	cmd.Flags().IntVar(&offset, "offset", 0, "versions to skip")
	cmd.Flags().BoolVar(&all, "all", false, "walk every page")
	return cmd
}

// pagePtr is a paging flag only when the caller set it: an unset --limit must
// be omitted, not sent as 0 (the server reads limit 0 as "count only").
func pagePtr(cmd *cobra.Command, name string, v int) *int {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	return &v
}
