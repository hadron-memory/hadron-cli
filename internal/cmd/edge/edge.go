// Package edge implements `hadron edge ...` — directed, labeled
// connections between two nodes.
package edge

import (
	"encoding/json"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
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

// resolveNodeURN mirrors the node package's resolver: a node ref is
// always a fully-qualified URN.
func resolveNodeURN(cmd *cobra.Command, client graphql.Client, ref string) (string, error) {
	urn := ref
	if !strings.HasPrefix(urn, "urn:") {
		if strings.Count(urn, ":") < 2 {
			return "", exitcode.Newf(exitcode.Usage,
				"%q is not a fully-qualified node URN — expected <org>:<memory>:<loc>", ref)
		}
		urn = "urn:node:" + urn
	}
	resp, err := gen.ResolveUrn(cmd.Context(), client, urn)
	if err != nil {
		return "", api.MapError(err)
	}
	if resp.ResolveUrn == nil || resp.ResolveUrn.Kind != "node" {
		return "", exitcode.Newf(exitcode.NotFound, "node %q not found", ref)
	}
	return resp.ResolveUrn.Id, nil
}

// parseJSONFlag parses an optional JSON-valued flag.
func parseJSONFlag(name, value string) (*json.RawMessage, error) {
	if value == "" {
		return nil, nil
	}
	if !json.Valid([]byte(value)) {
		return nil, exitcode.Newf(exitcode.Usage, "--%s must be valid JSON", name)
	}
	raw := json.RawMessage(value)
	return &raw, nil
}
