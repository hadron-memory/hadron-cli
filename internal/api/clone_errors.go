package api

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// This marker limits detail rendering and semantic overrides to the clone call.
// Unwrap retains MapError's transport/schema classification and original envelope.
type cloneAgentError struct{ error }

func (e *cloneAgentError) Unwrap() error { return e.error }

// CloneOutcomeUnknownError is the result of a clone whose answer is unusable:
// the request may have been applied, so the one thing to do is inspect state.
// Exit 7, like a lost response; never a success and never retried.
func CloneOutcomeUnknownError(what string) error {
	return exitcode.New(exitcode.Unavailable, &cloneAgentError{fmt.Errorf(
		"clone outcome unknown: %s. The clone may have been created; check the target organization's Agents (`hadron agent list --org <org>`) before cloning again. This command does not retry", what)})
}

// firstCloneError is THE error the clone's category and its typed details both
// read, so the two can never describe different errors.
func firstCloneError(err error) *gqlerror.Error {
	for _, e := range graphQLErrors(err) {
		if e != nil {
			return e
		}
	}
	return nil
}

// firstErrorView lets MapError see only the first GraphQL error, so the exit
// category, internal-fault ids and the rendered text can't be borrowed from a
// later one (MapError scans the whole envelope on several paths, and falls back
// to the raw HTTP body text). It is a REBUILT error, not a wrapper: nothing of
// the original envelope stays reachable through Error() or Unwrap(). Only the
// response's request-id header, which belongs to the whole response, is kept.
func firstErrorView(err error) error {
	first := firstCloneError(err)
	if first == nil {
		return err
	}
	var view error = gqlerror.List{first}
	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) {
		view = &graphql.HTTPError{StatusCode: httpErr.StatusCode, Response: graphql.Response{Errors: gqlerror.List{first}}}
	}
	var h *headerRequestIDError
	if errors.As(err, &h) {
		view = &headerRequestIDError{id: h.id, err: view}
	}
	return view
}

func MapCloneAgentError(err error) error {
	if err == nil {
		return nil
	}
	mapped := MapError(firstErrorView(err))
	if exitcode.FromError(mapped) == exitcode.Unavailable {
		// MapError's generic "retry shortly" advice is wrong for a non-idempotent
		// create: say what failed, then to inspect state before repeating.
		what := "the connection failed before the server's answer arrived"
		if f, ok := classifyTransport(err); ok {
			what = f.what
		}
		return CloneOutcomeUnknownError(what)
	}
	if e := firstCloneError(err); e != nil { // the first refusal is the exit category
		var category int
		switch extensionCode(e) {
		case "AGENT_NOT_PUBLIC", "CLONE_LINKED_MEMORY_UNREADABLE", "CLONE_BASE_UNUSABLE", "CLONE_IMPORT_UNUSABLE":
			category = exitcode.Forbidden
		case "CLONE_TARGET_UNSUPPORTED", "CLONE_ENCRYPTED_CONTENT", "CLONE_LINKED_MEMORY_ENCRYPTED":
			category = exitcode.Usage
		}
		if category != 0 {
			mapped = exitcode.New(category, mapped)
		}
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
	e := firstCloneError(err)
	if e == nil {
		return nil
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

// CloneAgentDetailText is the human one-line suffix naming the answer: the
// documented label, then code, reason and "retryable: rerun by hand" in the
// order a reader needs them (cli#1264, Uma's wording). retryable=false is
// omitted, never printed as a negative. Blockers follow on their own lines
// (CloneAgentBlockerLines). Every server-supplied string goes through
// output.SafeCell, so it can neither forge a line nor drive the terminal.
// Text mode only: --json carries CloneAgentDetails unchanged.
func CloneAgentDetailText(err error) string {
	details := CloneAgentDetails(err)
	if details == nil {
		return ""
	}
	var parts []string
	if code, ok := details["code"].(string); ok {
		parts = append(parts, output.SafeCell(code))
	}
	if reason, ok := details["reason"].(string); ok {
		parts = append(parts, "reason "+output.SafeCell(reason))
	}
	if retryable, ok := details["retryable"].(bool); ok && retryable {
		// The CLI never retries a clone (agentic-usage): the reader reruns it.
		parts = append(parts, "retryable: rerun by hand")
	}
	if len(parts) == 0 {
		if len(CloneAgentBlockerLines(err)) == 0 {
			return ""
		}
		parts = append(parts, "blockers follow")
	}
	return " (" + cloneDetailLabel(err) + ": " + strings.Join(parts, ", ") + ")"
}

// CloneAgentBlockerLines renders one line per blocker, in the order received:
// its code, its count when one was sent, then each subject as a quoted name
// with its position, or the bare position when the server sent no name (a
// hidden subject never gets one). position is printed literally, as received.
func CloneAgentBlockerLines(err error) []string {
	blockers, _ := CloneAgentDetails(err)["blockers"].([]map[string]any)
	lines := make([]string, 0, len(blockers))
	for _, b := range blockers {
		code, _ := b["code"].(string)
		line := "blocker " + output.SafeCell(code)
		if count, ok := b["count"].(int64); ok {
			line += fmt.Sprintf("  count %d", count)
		}
		subjects, _ := b["subjects"].([]map[string]any)
		rendered := make([]string, 0, len(subjects))
		for _, s := range subjects {
			position, _ := s["position"].(int64)
			if name, ok := s["name"].(string); ok {
				rendered = append(rendered, fmt.Sprintf("\"%s\" (position %d)", output.SafeCell(name), position))
				continue
			}
			rendered = append(rendered, fmt.Sprintf("position %d", position))
		}
		if len(rendered) > 0 {
			line += "  " + strings.Join(rendered, ", ")
		}
		lines = append(lines, line)
	}
	return lines
}

// cloneDetailLabel names what the details describe. "refusal" is only true of
// a clone refusal; an authentication or internal-fault answer is not one.
func cloneDetailLabel(err error) string {
	if _, ok := InternalFaultOf(err); ok {
		return "clone server error"
	}
	if exitcode.FromError(err) == exitcode.AuthRequired {
		return "clone authentication"
	}
	return "clone refusal"
}
