package nodedoc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ContentHash recomputes the server's content fingerprint: sha256 of the
// content, hex, first 8 chars; empty content has no hash. Matches
// hadron-server's computeContentHash (src/lib/contentHash.ts) so an exported
// contentHash equals the value the server would have written. The GraphQL API
// does not expose the stored hash column, so the codec recomputes it.
func ContentHash(content string) string {
	if content == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:8]
}

// DecodeJSON turns a GraphQL JSON scalar into a value yaml/json can render as a
// proper structure (not a quoted JSON string). A nil pointer, empty bytes, or a
// literal `null` decode to nil so a caller's omitempty drops the key.
func DecodeJSON(raw *json.RawMessage) any {
	if raw == nil {
		return nil
	}
	trimmed := bytes.TrimSpace(*raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	var v any
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return nil
	}
	return v
}

// EncodeJSON is the inverse of DecodeJSON: it marshals a decoded value back to a
// GraphQL JSON scalar (json.RawMessage), returning nil for a nil value so the
// caller omits the field. Used to map a Document's Data/Properties/edge
// Condition into mutation inputs.
func EncodeJSON(v any) (*json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	raw := json.RawMessage(b)
	return &raw, nil
}
