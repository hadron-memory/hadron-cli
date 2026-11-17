package gqltypes

import "encoding/json"

// AiServiceConfigTestOverride preserves omitted, explicit null and supplied
// values from an override document. Null restores defaults for style/endpoint/
// params; omission uses the saved value. The server validates provider policy.
type AiServiceConfigTestOverride struct {
	Provider json.RawMessage `json:"provider,omitempty"`
	Model    json.RawMessage `json:"model,omitempty"`
	APIKey   json.RawMessage `json:"apiKey,omitempty"`
	Endpoint json.RawMessage `json:"endpoint,omitempty"`
	APIStyle json.RawMessage `json:"apiStyle,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
}
