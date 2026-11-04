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
		Use:     "set-active <memoryRef>",
		Aliases: []string{"use"},
		Short:   "Set the default memory for spec and coding commands",
		Long: `Save a default memory in ~/.config/hadron/config.toml. Pass "" to clear it.

Only the spec and coding commands use it, and only when you leave out their
-m/--memory and nothing closer is set: spec commands first take a memory set
with hadron spec use, coding commands first take the repository's own coding
config. Other commands, such as node, edge and search, ignore it: give them -m
or a full URN.

To create a memory and make it the default: memory set --org <org> --name
<name> prints the new URN; pass that URN here.`,
		Example: `  hadron memory set-active hrn:mem:hadronmemory.com:dev
  hadron memory set-active hrn:mem:acme.com:kb
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
