package node

import (
	"errors"
	"fmt"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// recordMutationError maps a failure of the content-validation RECORD mutation
// (#1795 item 1, option A: recorder-only, api.MapError untouched).
//
// A 500, 502 or 504 answer is not proof the report was refused: a gateway can
// answer after the server committed it, and the shared mapper reads a JSON
// envelope inside it as a refusal (exit 1, 5 or 8). For the record mutation it
// is an unknown outcome, exit 7, whatever the envelope says. The text shows
// only the HTTP status and, when the envelope's own code is INTERNAL_SERVER_ERROR,
// that code and its request id (cor:api:260:01); never server detail. Every other
// failure keeps the shared mapping.
func recordMutationError(err error, nodeRef string, hasRunID bool) error {
	mapped := api.MapError(err)
	var httpErr *graphql.HTTPError
	if !errors.As(err, &httpErr) {
		return mapped
	}
	switch httpErr.StatusCode {
	case 500, 502, 504:
	default:
		return mapped
	}
	what := fmt.Sprintf("HTTP %d", httpErr.StatusCode)
	if fault, ok := api.InternalFaultOf(mapped); ok && fault.ServerCode == api.InternalServerErrorCode {
		what += " (" + api.InternalServerErrorCode
		if fault.RequestID != "" {
			what += ", request id " + fault.RequestID
		}
		what += ")"
	}
	// Recording never edits the node (the mutation's schema description), so a
	// retry after a recorded first attempt re-reads the same node revision and
	// task revision and resends the identical request; the schema documents
	// that as a replay (replayed: true) for the same reporter and a
	// CONTENT_VALIDATION_RUN_ID_CONFLICT for any other use of the run id.
	retry := "Re-running with the same --run-id repeats the same request; if the first attempt was recorded, the server answers it as a replay when the node and task revisions and your identity are unchanged, and otherwise refuses it as a conflict (re-read the node's latest report, as above)."
	if !hasRunID {
		retry = "This run sent no --run-id, so a repeat sends no idempotency key and may record a second report; pass --run-id to make a repeat safe to compare."
	}
	return exitcode.New(exitcode.Unavailable, &recordUncertainError{
		msg: fmt.Sprintf("the record request failed with %s: unknown outcome; the report may or may not have been recorded. Do not assume it failed: read the node's latest validation report (hadron node get %s) before recording again. %s This command does not retry.",
			what, output.SafeLine(nodeRef, 256), retry),
		cause: mapped,
	})
}

// recordUncertainError renders only its own text. The mapped cause stays on the
// chain (the --json envelope reads the internal-fault detail from it) but is
// never rendered.
type recordUncertainError struct {
	msg   string
	cause error
}

func (e *recordUncertainError) Error() string { return e.msg }
func (e *recordUncertainError) Unwrap() error { return e.cause }
