package cmdutil

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// WithFlagAliases makes a command accept extra spellings of its flags via the
// flagset normalizer: an alias passed on the command line resolves to its
// canonical flag. The aliases map is alias→canonical. This reconciles small
// vocabulary drifts across subcommands (e.g. body vs content) without
// adding a second help-listed flag or renaming the documented --json/flag
// contract (issue #99 item 5). Any existing normalizer (e.g. an inherited
// underscore→dash mapping) is chained first, so the alias lookup composes with
// it rather than clobbering it.
func WithFlagAliases(cmd *cobra.Command, aliases map[string]string) {
	prev := cmd.Flags().GetNormalizeFunc()
	cmd.Flags().SetNormalizeFunc(func(fs *pflag.FlagSet, name string) pflag.NormalizedName {
		if prev != nil {
			name = string(prev(fs, name))
		}
		if canon, ok := aliases[name]; ok {
			name = canon
		}
		return pflag.NormalizedName(name)
	})
}
