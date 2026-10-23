package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// This marker limits detail rendering and semantic overrides to the clone call.
// Unwrap retains MapError's transport/schema classification and original envelope.
type cloneAgentError struct{ error }

func (e *cloneAgentError) Unwrap() error { return e.error }

func MapCloneAgentError(err error) error {
	if err == nil {
		return nil
	}
	mapped := MapError(err)
	if exitcode.FromError(mapped) == exitcode.Unavailable {
		mapped = exitcode.New(exitcode.Unavailable, fmt.Errorf("clone outcome unknown: %w", mapped))
	}
	for _, e := range graphQLErrors(err) {
		if e == nil {
			continue
		}
		code := extensionCode(e)
		var category int
		switch code {
		case "AGENT_NOT_PUBLIC", "CLONE_LINKED_MEMORY_UNREADABLE", "CLONE_BASE_UNUSABLE", "CLONE_IMPORT_UNUSABLE":
			category = exitcode.Forbidden
		case "CLONE_TARGET_UNSUPPORTED", "CLONE_ENCRYPTED_CONTENT", "CLONE_LINKED_MEMORY_ENCRYPTED":
			category = exitcode.Usage
		}
		if category != 0 {
			mapped = exitcode.New(category, mapped)
		}
		break // the existing mapping uses the first refusal as the exit category
	}
	return &cloneAgentError{mapped}
}

// CloneAgentDetails forwards only the clone contract, never arbitrary extensions.
// A missing subject name remains absent: the CLI never resolves it itself.
func CloneAgentDetails(err error) map[string]any {
	var scoped *cloneAgentError
	if !errors.As(err, &scoped) {
		return nil
	}
	for _, e := range graphQLErrors(err) {
		if e == nil {
			continue
		}
		details := map[string]any{}
		if code, ok := e.Extensions["code"].(string); ok && code != "" {
			details["code"] = code
		}
		if reason, ok := e.Extensions["reason"].(string); ok {
			details["reason"] = reason
		}
		if retryable, ok := e.Extensions["retryable"].(bool); ok {
			details["retryable"] = retryable
		}
		if raw, ok := e.Extensions["blockers"].([]any); ok {
			blockers := make([]map[string]any, 0, len(raw))
			for _, value := range raw {
				b, ok := value.(map[string]any)
				if !ok {
					continue
				}
				code, ok := b["code"].(string)
				if !ok || code == "" {
					continue
				}
				clean := map[string]any{"code": code}
				if count, ok := cloneOrdinal(b["count"]); ok {
					clean["count"] = count
				}
				if rawSubjects, ok := b["subjects"].([]any); ok {
					subjects := make([]map[string]any, 0, len(rawSubjects))
					for _, value := range rawSubjects {
						subject, ok := value.(map[string]any)
						if !ok {
							continue
						}
						position, ok := cloneOrdinal(subject["position"])
						if !ok {
							continue
						}
						cleanSubject := map[string]any{"position": position}
						if name, ok := subject["name"].(string); ok {
							cleanSubject["name"] = name
						}
						subjects = append(subjects, cleanSubject)
					}
					clean["subjects"] = subjects
				}
				blockers = append(blockers, clean)
			}
			details["blockers"] = blockers
		}
		if len(details) > 0 {
			return details
		}
	}
	return nil
}

// JSON numbers are float64 here. Reject malformed ordinals instead of coercing
// them, and keep the exact JSON-safe integer range on supported 64-bit hosts.
func cloneOrdinal(v any) (int64, bool) {
	n, ok := v.(float64)
	if !ok || math.IsNaN(n) || n < 0 || n > 9007199254740991 || math.Trunc(n) != n {
		return 0, false
	}
	return int64(n), true
}

func CloneAgentDetailText(err error) string {
	details := CloneAgentDetails(err)
	if details == nil {
		return ""
	}
	// All values above are known JSON scalar/container types; no raw error body.
	encoded, _ := json.Marshal(details)
	return " (clone refusal: " + string(encoded) + ")"
}
