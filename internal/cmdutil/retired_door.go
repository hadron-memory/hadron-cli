package cmdutil

import (
	"fmt"

	"github.com/spf13/cobra"
)

// RetireDoorCommands is the deprecation window of a server door that hadron-server retired (cli#3319, hadron-server#5404): the
// server answers every call DOOR_RETIRED, so each child command prints notice to stderr first (never stdout, so --json stays one
// document) and then runs unchanged, surfacing the server's refusal. The notice says the command no longer works and names the
// replacement. The group and its children are marked DEPRECATED in their help, and hidden from neither: an agent that still has
// the old verb in a script must find out why it fails. The commands are removed when the server removes the doors.
func RetireDoorCommands(f *Factory, group *cobra.Command, notice string) {
	group.Short = "DEPRECATED, no longer works: " + group.Short
	group.Long = "DEPRECATED: " + notice + "\n\n" + group.Long
	for _, child := range group.Commands() {
		child.Short = "DEPRECATED, no longer works: " + child.Short
		child.Long = "DEPRECATED: " + notice + "\n\n" + child.Long
		run := child.RunE
		if run == nil {
			continue
		}
		child.RunE = func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(f.IOStreams.ErrOut, "warning: "+notice)
			return run(cmd, args)
		}
	}
}
