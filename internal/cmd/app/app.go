// Package app implements `hadron app ...`.
package app

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func NewCmdApp(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "app <command>",
		Aliases: []string{"apps"},
		Short:   "Work with Hadron Apps",
	}
	cmd.AddCommand(newCmdUse(f))
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdInstall(f))
	cmd.AddCommand(newCmdUninstall(f))
	return cmd
}

func newCmdUse(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "use <app-urn>",
		Short: "Set the default App context for future invocations",
		Long: `Set the default App URN stored in ~/.config/hadron/config.toml.
Pass an empty string ("") to clear it. A single invocation can
override the default with the global --app flag.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			if args[0] == "" {
				if err := cfg.Unset("app"); err != nil {
					return err
				}
				return output.Write(f.IOStreams, f.JSON, map[string]string{"app": ""}, func(w io.Writer) error {
					_, err := fmt.Fprintln(w, "✓ Cleared default App context")
					return err
				})
			}
			if err := cfg.Set("app", args[0]); err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, map[string]string{"app": args[0]}, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Default App set to %s\n", args[0])
				return err
			})
		},
	}
}
