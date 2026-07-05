package memory

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdSetActive(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "set-active <memory-urn-or-id>",
		Aliases: []string{"use"},
		Short:   "Set the default memory context for future invocations",
		Long: `Set the default memory URN or ID stored in ~/.config/hadron/config.toml.
Pass an empty string ("") to clear it. A single invocation can
override the default with the global -m/--memory flag.`,
		Example: `  hadron memory set-active hadronmemory.com::dev
  hadron memory set-active acme.com::kb
  hadron memory set-active ""  # clear the default`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			if args[0] == "" {
				if err := cfg.Unset("memory"); err != nil {
					return err
				}
				return output.Write(f.IOStreams, f.JSON, map[string]string{"memory": ""}, func(w io.Writer) error {
					_, err := fmt.Fprintln(w, "✓ Cleared default memory context")
					return err
				})
			}
			if err := cfg.Set("memory", args[0]); err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, map[string]string{"memory": args[0]}, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Default memory set to %s\n", args[0])
				return err
			})
		},
	}
}
