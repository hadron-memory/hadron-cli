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
		Args:  cobra.NoArgs,
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
