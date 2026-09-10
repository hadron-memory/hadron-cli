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
	cmd.AddCommand(newCmdAgent(f))
	cmd.AddCommand(newCmdInstall(f))
	cmd.AddCommand(newCmdUninstall(f))
	return cmd
}

func newCmdUse(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "set-active <app-ref>",
		Aliases: []string{"use"},
		Short:   "Set the default App context for future invocations",
		Long: `Set the default App stored in ~/.config/hadron/config.toml, as
hrn:app:<root>:<slug> (canonical), the <root>:<slug> short form, or an App id.
The value is shape-checked before it is stored and kept in canonical form.
Pass an empty string ("") to clear it. A single invocation can override the
default with the global --app flag.`,
		Example: `  hadron app set-active hrn:app:acme.com:eng-team
  hadron app set-active acme.com:eng-team`,
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
			appRef, err := cmdutil.CanonicalAppRef("<app-ref>", args[0])
			if err != nil {
				return err
			}
			if err := cfg.Set("app", appRef); err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, map[string]string{"app": appRef}, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Default App set to %s\n", appRef)
				return err
			})
		},
	}
}
