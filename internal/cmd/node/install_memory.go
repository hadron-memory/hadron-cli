package node

import (
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/spf13/cobra"
)

const installFlagHelp = "App install memory as <app>/<agent> (ids or fully-qualified URNs); excludes --memory"

// installTarget checks presence, not value: an explicit empty selector is invalid.
func installTarget(cmd *cobra.Command, raw string, locs []string) (*gen.InstallTargetInput, error) {
	if !cmd.Flags().Changed("install") {
		return nil, nil
	}
	if cmd.Flags().Changed("memory") {
		return nil, exitcode.Newf(exitcode.Usage, "--install and --memory are mutually exclusive")
	}
	t, err := cmdutil.ParseInstallTarget(raw)
	if err != nil {
		return nil, err
	}
	for _, loc := range locs {
		if cmdutil.IsQualifiedNodeRef(loc) || cmdutil.IsEntityID(loc) || strings.Contains(loc, "/") {
			return nil, exitcode.Newf(exitcode.Usage, "--install takes a bare node loc, not a node id or qualified node reference")
		}
		if err := cmdutil.ValidateURNPath("<loc>", loc); err != nil {
			return nil, err
		}
	}
	return t, nil
}
