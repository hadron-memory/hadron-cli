package gqltypes

import "encoding/json"

// AgentTeamHostUpdateInput is updateAgentTeamHost's input (hadron-cli#3239 over
// hadron-server#5259), bound in genqlient.yaml because two of its fields are
// three-state: an omitted field keeps the stored value, null clears it to the
// default, a value sets it. A nil pointer with its Clear flag unset is omitted;
// a Clear flag sends null. The label is never cleared (the server refuses it).
//
// The json tags name the GraphQL fields for the coverage matrix
// (scripts/unboundops); MarshalJSON, not the tags, decides what is sent.
type AgentTeamHostUpdateInput struct {
	AppRef string  `json:"appRef"`
	HostID string  `json:"hostId"`
	Label  *string `json:"label"`

	PollIntervalSeconds *int `json:"pollIntervalSeconds"`
	ClearPollInterval   bool `json:"-"`

	DeliveryEnabled *bool `json:"deliveryEnabled"`
	ClearDelivery   bool  `json:"-"`
}

func (i AgentTeamHostUpdateInput) MarshalJSON() ([]byte, error) {
	fields := map[string]any{"appRef": i.AppRef, "hostId": i.HostID}
	if i.Label != nil {
		fields["label"] = *i.Label
	}
	switch {
	case i.ClearPollInterval:
		fields["pollIntervalSeconds"] = nil
	case i.PollIntervalSeconds != nil:
		fields["pollIntervalSeconds"] = *i.PollIntervalSeconds
	}
	switch {
	case i.ClearDelivery:
		fields["deliveryEnabled"] = nil
	case i.DeliveryEnabled != nil:
		fields["deliveryEnabled"] = *i.DeliveryEnabled
	}
	return json.Marshal(fields)
}
