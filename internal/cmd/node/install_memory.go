package node

import (
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/spf13/cobra"
)

const installFlagHelp = "App install memory as <app>/<agent> (ids or fully-qualified URNs); excludes --memory"

// installDetails is the shared Details paragraph for --install; extra adds
// the one sentence that differs per command.
func installDetails(extra string) string {
	s := `--install <app>/<agent> works in the memory an App keeps for one installed
Agent (its customisation memory) instead of -m. Give the App and the Agent as
ids or full URNs, separated by /. It excludes -m, even an empty one, and node
arguments are then bare locs inside that memory. Only an App manager may write
there, and only while the App is live and the Agent installed. Being able to
see that the memory exists does not let you read or write it. A read never
creates the memory: if it is unavailable, the read exits 4.`
	if extra != "" {
		s += "\n" + extra
	}
	return s
}

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
