package node

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// edgeFlagUsage is the --edge help shared by node add and node update.
const edgeFlagUsage = `an outgoing edge, repeatable: <name>=<target>, or a JSON object with target (required), name, loc, description, runnable`

// edgeSpec is the JSON form of --edge. Unknown keys are refused, so a typo
// (descripton) is an error rather than an edge without its description.
type edgeSpec struct {
	Target      string  `json:"target"`
	Name        *string `json:"name"`
	Loc         *string `json:"loc"`
	Description *string `json:"description"`
	Runnable    *bool   `json:"runnable"`
}

// parseEdgeFlags turns --edge values into the node-write edge inputs
// (cli#1341). The target is any form NodeEdgeInput.targetId takes (a node id,
// a node URN, or a loc in the node's own memory): the server resolves it.
// Every malformed value is a usage error before anything is sent.
func parseEdgeFlags(values []string) ([]*gen.NodeEdgeInput, error) {
	out := []*gen.NodeEdgeInput{}
	for _, raw := range values {
		v := strings.TrimSpace(raw)
		if v == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--edge is empty: give <name>=<target> or a JSON object. Nothing was sent")
		}
		if strings.HasPrefix(v, "{") {
			var spec edgeSpec
			dec := json.NewDecoder(bytes.NewReader([]byte(v)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&spec); err != nil {
				return nil, exitcode.Newf(exitcode.Usage, "--edge %s is not a valid edge object (keys: target, name, loc, description, runnable): %v. Nothing was sent", v, err)
			}
			if dec.More() {
				return nil, exitcode.Newf(exitcode.Usage, "--edge %s has trailing text after the JSON object. Nothing was sent", v)
			}
			if strings.TrimSpace(spec.Target) == "" {
				return nil, exitcode.Newf(exitcode.Usage, "--edge %s has no target. Nothing was sent", v)
			}
			out = append(out, &gen.NodeEdgeInput{
				TargetId: strings.TrimSpace(spec.Target), Name: spec.Name, Loc: spec.Loc,
				Description: spec.Description, IsRunnable: spec.Runnable,
			})
			continue
		}
		name, target, ok := strings.Cut(v, "=")
		name, target = strings.TrimSpace(name), strings.TrimSpace(target)
		if !ok || name == "" || target == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--edge %q must be <name>=<target> (or a JSON object). Nothing was sent", raw)
		}
		out = append(out, &gen.NodeEdgeInput{TargetId: target, Name: &name})
	}
	return out, nil
}
