package cmdutil

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// TargetApp resolves the ONE App a command that writes against an App acts on
// (app member, automation approval). The shared resolver cannot be used as-is:
// Factory.App treats an explicit blank `--app ""` exactly like no flag and
// falls back to the active App, which would silently aim a write at an App the
// caller never named. An explicit blank refuses; an absent flag uses the active
// App; no App at all refuses.
//
// It also returns which branch answered ("from --app" or "from the active App")
// so the prompt and receipt can say so: a write aimed at an ambient App must
// not look like one aimed at a named one.
func TargetApp(cmd *cobra.Command, f *Factory) (app, source string, err error) {
	explicit := false
	if fl := cmd.Flag("app"); fl != nil && fl.Changed {
		explicit = true
		if strings.TrimSpace(f.AppFlag) == "" {
			return "", "", exitcode.Newf(exitcode.Usage, "--app was given a blank value — name the App, or omit --app to use the active App")
		}
	}
	app, err = f.App()
	if err != nil {
		return "", "", err
	}
	if app == "" {
		return "", "", exitcode.Newf(exitcode.Usage, "an App is required: pass --app <ref> or select one with `hadron app use`")
	}
	if explicit {
		return app, "from --app", nil
	}
	return app, "from the active App", nil
}
