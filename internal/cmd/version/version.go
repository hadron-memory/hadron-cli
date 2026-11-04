// Package version implements `hadron version`.
package version

import (
	"fmt"
	"io"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/build"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type versionDTO struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func NewCmdVersion(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show the hadron CLI version",
		Long: `Print this CLI's version, the commit it was built from and the build date,
for example:

  hadron 0.29.0 (decec03, 2026-10-29)

'hadron -v' and 'hadron --version' print the same line. --json adds the Go
version, operating system and architecture. Needs no network or sign-in.

The server's version is a different number: see 'hadron server-info'. To
update the CLI, use the package manager you installed it with (for example
'brew upgrade --cask hadron').`,
		Example: `  hadron version
  hadron version --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dto := versionDTO{
				Version: build.Version,
				Commit:  build.Commit,
				Date:    build.Date,
				Go:      runtime.Version(),
				OS:      runtime.GOOS,
				Arch:    runtime.GOARCH,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "hadron %s (%s, %s)\n", dto.Version, dto.Commit, dto.Date)
				return err
			})
		},
	}
}
