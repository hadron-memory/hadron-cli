// Package edge implements `hadron edge ...` — directed, labeled
// connections between two nodes.
package edge

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// edgeDTO is the stable --json shape for an edge.
type edgeDTO struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Priority  int    `json:"priority"`
	SourceID  string `json:"sourceId"`
	SourceLoc string `json:"sourceLoc"`
	TargetID  string `json:"targetId"`
	TargetLoc string `json:"targetLoc"`
}

func NewCmdEdge(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "edge <command>",
		Aliases: []string{"edges"},
		Short:   "Work with edges between nodes",
	}
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdAdd(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdRm(f))
	return cmd
}

// parseJSONFlag parses an explicitly-set JSON-valued flag. Callers
// only invoke it when the flag was passed, so an empty value is a
// usage error rather than a silent no-op.
func parseJSONFlag(name, value string) (*json.RawMessage, error) {
	if !json.Valid([]byte(value)) {
		return nil, exitcode.Newf(exitcode.Usage, "--%s must be valid JSON", name)
	}
	raw := json.RawMessage(value)
	return &raw, nil
}
