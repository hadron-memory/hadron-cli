package gqltypes

import "encoding/json"

// ContextCapInput is bound rather than generated (cli#2162) because
// createAgent/updateAgent's contextCap has THREE wire states and genqlient's
// optional pointer has two: omitted preserves (update) or means no cap
// (create), a whole number sets the cap, and an explicit null clears it
// (hadron-server#4258).
//
// The variable is a *ContextCapInput with omitempty: a nil pointer is dropped
// from the wire; a non-nil pointer is always sent, and this type decides WHAT:
// Clear marshals as the 4-byte `null`, anything else as the number. Ordinary
// nil+omitempty alone cannot express null, so omission can never turn into a
// clear and a clear can never be dropped.
type ContextCapInput struct {
	// Tokens is sent when Clear is false; the CLI guarantees it is at least 1.
	Tokens int
	// Clear sends an explicit null (no cap).
	Clear bool
}

// MarshalJSON implements the wire form described above.
func (c ContextCapInput) MarshalJSON() ([]byte, error) {
	if c.Clear {
		return []byte("null"), nil
	}
	return json.Marshal(c.Tokens)
}
