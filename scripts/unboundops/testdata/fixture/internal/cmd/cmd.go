// Package cmd stands in for the CLI's commands.
package cmd

import (
	"encoding/json"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func listAgents(mine bool) error {
	return gen.Agents(&gen.AgentFilter{OwnedByMe: &mine})
}

func createThing(raw []byte) error {
	var in gen.ThingInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return err
	}
	return nil
}

// A reference that is never called reaches nothing.
var _ = gen.CreateThing

var _, _ = listAgents, createThing
