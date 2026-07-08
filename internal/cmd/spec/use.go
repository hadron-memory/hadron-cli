package spec

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// newCmdUse sets the default memory for `hadron spec` commands (the spec_memory
// config key), so spec subcommands can omit -m/--memory. The spec corpus is
// usually a fixed memory distinct from the global active memory, so it gets its
// own default that `hadron memory set-active` does not disturb.
func newCmdUse(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "use <memory-urn-or-id>",
		Aliases: []string{"set-active"},
		Short:   "Set the default memory for spec commands",
		Long: `Set the default memory (URN or ID) that ` + "`hadron spec`" + ` commands use
when -m/--memory is omitted, stored as spec_memory in
~/.config/hadron/config.toml. Pass an empty string ("") to clear it.

Resolution order for a spec command's memory: the -m/--memory flag, then
HADRON_SPEC_MEMORY / this spec_memory default, then the global active memory
(hadron memory set-active). This is separate from the global default so
switching your working memory does not change your spec corpus.`,
		Example: `  hadron spec use hadronmemory.com::specs
  hadron spec use ""  # clear the spec default`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			if args[0] == "" {
				if err := cfg.Unset("spec_memory"); err != nil {
					return err
				}
				return output.Write(f.IOStreams, f.JSON, map[string]string{"spec_memory": ""}, func(w io.Writer) error {
					_, err := fmt.Fprintln(w, "✓ Cleared default spec memory")
					return err
				})
			}
			if err := cfg.Set("spec_memory", args[0]); err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, map[string]string{"spec_memory": args[0]}, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Default spec memory set to %s\n", args[0])
				return err
			})
		},
	}
}
