// Package edge implements `hadron edge ...` — directed, labeled
// connections between two nodes.
package edge

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// edgeDTO is the stable --json shape for an edge (spec 037 — first-class
// edges: name is optional, loc is the identity).
type edgeDTO struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Loc        string `json:"loc"`
	IsRunnable bool   `json:"isRunnable"`
	Priority   int    `json:"priority"`
	SourceID   string `json:"sourceId"`
	SourceLoc  string `json:"sourceLoc"`
	TargetID   string `json:"targetId"`
	TargetLoc  string `json:"targetLoc"`
}

// edgeDTOFrom maps a created/updated edge result into the DTO. The mutation
// projections share the same field set (id, name, loc, isRunnable, priority,
// source, target).
func edgeDTOFrom(id string, name *string, loc string, isRunnable *bool, priority int, srcID, srcLoc, tgtID, tgtLoc string) edgeDTO {
	n := ""
	if name != nil {
		n = *name
	}
	run := false
	if isRunnable != nil {
		run = *isRunnable
	}
	return edgeDTO{
		ID: id, Name: n, Loc: loc, IsRunnable: run, Priority: priority,
		SourceID: srcID, SourceLoc: srcLoc, TargetID: tgtID, TargetLoc: tgtLoc,
	}
}

func NewCmdEdge(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "edge <command>",
		Aliases: []string{"edges"},
		Short:   "Link two nodes, see a node's links, change or remove a link",
		Long: `An edge is a one-way link from one node (a note) to another, with a
relationship name such as routes-to or supersedes. Edges connect your notes:

  edge create   link two nodes
  edge list     see what a node links to, and what links to it
  edge update   change a link's name or settings
  edge rm       remove a link (neither node is deleted)

Edges are addressed by an edge id, which edge create prints and edge list
shows in its EDGE-ID column.

An edge can also carry settings that only automation reads (flows and
runnable tasks): runnable, priority, condition and data. An ordinary link
needs none of them; edge create --help explains each.`,
		Example: `  hadron edge create -m hrn:mem:acme.com:kb --from findings:flaky-ci --to start-here --name routes-to
  hadron edge list start-here -m hrn:mem:acme.com:kb`,
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
