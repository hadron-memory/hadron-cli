package api

import (
	"encoding/json"
	"math"
)

// AuthoringAttestationDetails curates the four typed refusals. Anything except
// an exact READABLE projection is concealed, even if a server supplies fields.
func AuthoringAttestationDetails(err error) map[string]any {
	var readable map[string]any
	for _, e := range graphQLErrors(err) {
		switch extensionCode(e) {
		case "AUTHORING_ATTESTATION_MISSING", "AUTHORING_ATTESTATION_WRONG_TASK", "AUTHORING_ATTESTATION_STALE", "AUTHOR_TASK_BROKEN":
		default:
			continue
		}
		out := map[string]any{"code": extensionCode(e), "taskAccess": "UNAVAILABLE"}
		if access, ok := e.Extensions["taskAccess"].(string); !ok || access != "READABLE" {
			return out
		}
		out["taskAccess"] = "READABLE"
		for _, k := range []string{"task", "taskId"} {
			out[k] = nil
			if s, ok := e.Extensions[k].(string); ok {
				out[k] = s
			}
		}
		out["taskRevision"] = authoringRevision(e.Extensions["taskRevision"])
		out["load"] = nil
		if load, ok := e.Extensions["load"].(map[string]any); ok {
			fields := map[string]any{}
			for _, k := range []string{"graphql", "mcp", "cli"} {
				fields[k] = nil
				if s, ok := load[k].(string); ok {
					fields[k] = s
				}
			}
			out["load"] = fields
		}
		if readable == nil {
			readable = out
		}
	}
	return readable
}

type attestationCause struct{ presentation, cause error }

func (e *attestationCause) Error() string   { return e.presentation.Error() }
func (e *attestationCause) Unwrap() []error { return []error{e.presentation, e.cause} }

// WithAuthoringAttestationCause retains recovery fields across an existing
// partial-write error without changing its message or exit classification.
// Unrelated errors retain their exact original wrapper and output behavior.
func WithAuthoringAttestationCause(presentation, cause error) error {
	if presentation == nil || AuthoringAttestationDetails(cause) == nil {
		return presentation
	}
	return &attestationCause{presentation: presentation, cause: cause}
}

// Error extensions decode numbers as float64. Canonicalize GraphQL integers so
// a large revision never becomes an unparseable scientific-notation CLI hint.
func authoringRevision(value any) any {
	var n int64
	switch v := value.(type) {
	case int:
		n = int64(v)
	case int64:
		n = v
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v || v < math.MinInt32 || v > math.MaxInt32 {
			return nil
		}
		n = int64(v)
	case json.Number:
		var err error
		n, err = v.Int64()
		if err != nil {
			return nil
		}
	default:
		return nil
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		return nil
	}
	return n
}
