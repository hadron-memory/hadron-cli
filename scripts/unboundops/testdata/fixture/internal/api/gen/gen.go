// Package gen stands in for genqlient's output: operation functions named
// after their operations, and input structs named after their GraphQL types.
package gen

type AgentFilter struct {
	OwnedByMe *bool   `json:"ownedByMe,omitempty"`
	Type      *string `json:"type,omitempty"`
}

type ThingInput struct {
	Name *string `json:"name,omitempty"`
	Note *string `json:"note,omitempty"`
}

func Agents(filter *AgentFilter) error { return nil }

func Me() error { return nil }

func CreateThing(in *ThingInput) error { return nil }
