package node

import (
	"errors"
	"fmt"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// recordMutationError maps a failure of the content-validation RECORD mutation
// (#1795 item 1, option A: recorder-only, api.MapError untouched; the whole 5xx
// class since #2430).
//
// A 5xx answer (500-599) is not proof the report was refused: a gateway can
// answer after the server committed it, and the shared mapper reads a JSON
// envelope inside it as a refusal (exit 1, 5 or 8). For the record mutation it
// is an unknown outcome, exit 7, whatever the envelope says. The text shows
// only the HTTP status and, when the envelope's own code is INTERNAL_SERVER_ERROR,
// that code and its request id (cor:api:260:01); never server detail. Every other
// failure keeps the shared mapping.
func recordMutationError(err error, hasRunID bool) error {
	mapped := api.MapError(err)
	var httpErr *graphql.HTTPError
	if !errors.As(err, &httpErr) {
		return mapped
	}
	// The whole 5xx class (#2430): a proxy or gateway can answer 503, 501 or 599
	// after the upstream committed, so none of them proves "not applied".
	if httpErr.StatusCode < 500 || httpErr.StatusCode > 599 {
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
	// Recording never edits the node, so a retry after a recorded first attempt
	// re-reads the same node and task revisions and resends the identical
	// request. The schema (schema/schema.graphql, recordNodeContentValidationReport)
	// says: "Advisory: it never edits the node" and "runId (1-200 characters)
	// makes a retry idempotent: within this node in this memory, the same runId
	// from the same reporter for the same nodeRevision and task revision returns
	// the first report (replayed: true); any other use of it is
	// CONTENT_VALIDATION_RUN_ID_CONFLICT". The advice below restates that.
	retry := "Re-running with the same --run-id repeats the same request; if the first attempt was recorded, the server answers it as a replay when the node and task revisions and your identity are unchanged, and otherwise refuses it as a conflict (re-read the node's latest report, as above)."
	if !hasRunID {
		retry = "This run sent no --run-id, so a repeat sends no idempotency key and may record a second report; pass --run-id to make a repeat safe to compare."
	}
	return exitcode.New(exitcode.Unavailable, &recordUncertainError{
		msg: fmt.Sprintf("the record request failed with %s: unknown outcome; the report may or may not have been recorded. Do not assume it failed: read the node's latest validation report (hadron node get <ref>; the ref given is not shown) before recording again. %s This command does not retry.",
			what, retry),
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
