package node

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// edgeFlagUsage is the --edge help shared by node add and node update.
const edgeFlagUsage = `an outgoing edge, repeatable: <name>=<target>, or a JSON object with target (required), name, loc, description, runnable`

// edgeSpec is the JSON form of --edge. Unknown keys are refused, so a typo
// in a key name is an error rather than an edge silently missing that field.
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
				return nil, exitcode.Newf(exitcode.Usage, "--edge (the value given is not shown) is not a valid edge object (keys: target, name, loc, description, runnable; name, loc, target and description are strings, runnable is true or false). Nothing was sent")
			}
			// A second Decode must hit EOF: More() reads a stray `]` or `}`
			// as the end of a container and would let `{…}]` through.
			var rest json.RawMessage
			if err := dec.Decode(&rest); !errors.Is(err, io.EOF) {
				return nil, exitcode.Newf(exitcode.Usage, "--edge (the value given is not shown) has trailing text after the JSON object. Nothing was sent")
			}
			if strings.TrimSpace(spec.Target) == "" {
				return nil, exitcode.Newf(exitcode.Usage, "--edge (the value given is not shown) has no target. Nothing was sent")
			}
			out = append(out, &gen.NodeEdgeInput{
				TargetRef: strings.TrimSpace(spec.Target), Name: spec.Name, Loc: spec.Loc,
				Description: spec.Description, IsRunnable: spec.Runnable,
			})
			continue
		}
		name, target, ok := strings.Cut(v, "=")
		name, target = strings.TrimSpace(name), strings.TrimSpace(target)
		if !ok || name == "" || target == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--edge must be <name>=<target> (or a JSON object); the value given is not shown. Nothing was sent")
		}
		out = append(out, &gen.NodeEdgeInput{TargetRef: target, Name: &name})
	}
	return out, nil
}
