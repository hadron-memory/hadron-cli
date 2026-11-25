package gqltypes

import "encoding/json"

// AuthoredWith is the claim a checked write would carry (AuthoredWithInput).
// Bound here, not generated, because gen imports this package.
type AuthoredWith struct {
	Task     string `json:"task"`
	Revision int    `json:"revision"`
}

// NodeAuthoringCheckInput is bound rather than generated (cli#3089) because
// `role` has THREE wire states and genqlient's optional pointer has two: omitted
// keeps the node's own role, a string is the role the write would leave, and an
// explicit null is a write that clears it (hadron-server NodeAuthoringCheckInput).
//
// Every other optional field is omitted when nil, so a flag the caller did not
// pass can never turn into a null or a default the caller did not choose.
type NodeAuthoringCheckInput struct {
	Ref                    *string       `json:"ref,omitempty"`
	MemoryID               *string       `json:"memoryId,omitempty"`
	Loc                    *string       `json:"loc,omitempty"`
	Role                   *string       `json:"role,omitempty"`
	IsRunnable             *bool         `json:"isRunnable,omitempty"`
	ChangesAuthoredContent *bool         `json:"changesAuthoredContent,omitempty"`
	AuthoredWith           *AuthoredWith `json:"authoredWith,omitempty"`
	// ClearRole sends `"role": null`; it wins over Role, which the CLI never
	// sets beside it.
	ClearRole bool `json:"-"`
}

// MarshalJSON implements the three-state role described above.
func (i NodeAuthoringCheckInput) MarshalJSON() ([]byte, error) {
	type plain NodeAuthoringCheckInput // no methods: avoids recursing into MarshalJSON
	raw, err := json.Marshal(plain(i))
	if err != nil || !i.ClearRole {
		return raw, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	fields["role"] = json.RawMessage("null")
	return json.Marshal(fields)
}
