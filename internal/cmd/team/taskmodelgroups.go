package team

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func addTaskModelGroupFlags(cmd *cobra.Command, in, notIn *string) {
	// What the flag does to the task's requirements differs by command.
	effect := "sets the task's matching requirements"
	switch cmd.Name() {
	case "update":
		effect = "replaces the task's whole requirements"
	case "clone":
		effect = "replaces the clone's whole requirements"
	}
	cmd.Flags().StringVar(in, "model-group", "", "comma list of model groups: the worker's model must match at least one; "+effect)
	cmd.Flags().StringVar(notIn, "model-group-not", "", "comma list of model groups: the worker's model must match none; "+effect)
}

// taskModelGroupFlags only splits syntax. Keep case, whitespace, duplicates,
// grammar and overlapping lists for the server to normalize and validate.
// A nil result slice means its flag was absent, not an explicit empty list.
func taskModelGroupFlags(cmd *cobra.Command, in, notIn string) ([]string, []string, error) {
	var lists [2][]string
	for i, flag := range []struct{ name, value string }{{"model-group", in}, {"model-group-not", notIn}} {
		if !cmd.Flags().Changed(flag.name) {
			continue
		}
		lists[i] = strings.Split(flag.value, ",")
		nonempty := false
		for _, name := range lists[i] {
			nonempty = nonempty || strings.TrimSpace(name) != ""
		}
		if !nonempty {
			return nil, nil, exitcode.Newf(exitcode.Usage, "--%s must name a model group; use --clear-requirements on update to remove requirements", flag.name)
		}
	}
	return lists[0], lists[1], nil
}
