package gqltypes

import "encoding/json"

// RegisterDefaultsInput is bound rather than generated (cli#58 slice 2)
// because updateAgent's registerDefaults has THREE wire states and genqlient's
// optional pointer has two: omitted PRESERVES the Agent's register defaults,
// an object sets mentionOnly, and an explicit null CLEARS them to the platform
// defaults (hadron-server resolvers.mutation.agent.ts:873-875).
//
// The variable is a *RegisterDefaultsInput with omitempty: a nil pointer is
// dropped from the wire (preserve); a non-nil pointer is always sent, and this
// type decides WHAT: Clear marshals as the 4-byte `null`, anything else as
// {"mentionOnly": <bool>} (mentionOnly omitted when nil, which the CLI never
// produces: --register-mention-only always carries a value). Ordinary
// nil+omitempty alone cannot express null, so omission can never turn into a
// clear and a clear can never be dropped.
type RegisterDefaultsInput struct {
	// MentionOnly is sent as {"mentionOnly": v} when Clear is false.
	MentionOnly *bool `json:"mentionOnly,omitempty"`
	// Clear sends an explicit null (reset to the platform defaults).
	Clear bool `json:"-"`
}

// The json tag on MentionOnly is also how the coverage matrix learns the
// GraphQL field is reached (scripts/unboundops maps a Go field to its input
// field by tag), so it must keep naming the schema field.

// MarshalJSON implements the three-state wire form described above.
func (r RegisterDefaultsInput) MarshalJSON() ([]byte, error) {
	if r.Clear {
		return []byte("null"), nil
	}
	type plain RegisterDefaultsInput // no methods: avoids recursing into MarshalJSON
	return json.Marshal(plain(r))
}
