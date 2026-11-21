package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #2441: the local empty --run-id refusal must not advertise a 1-200 bound the
// CLI does not enforce. The server owns the upper bound (RUN_ID_MAX_LENGTH);
// the CLI refuses only the blank value, before any request.
func TestValidationRecordBlankRunIDRefusalMakesNoBoundClaim2441(t *testing.T) {
	srv, sent := server5xx1795(t, 200, `{"data":{}}`)
	err := record1795(t, srv, "--run-id", "   ")
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("blank --run-id must exit %d: %v", exitcode.Usage, err)
	}
	if !strings.Contains(err.Error(), "must not be empty") || strings.Contains(err.Error(), "1-200") {
		t.Fatalf("the refusal says it is empty and claims no bound: %q", err.Error())
	}
	if len(*sent) != 0 {
		t.Fatalf("a blank --run-id sends nothing: %v", *sent)
	}
}

// #2441: an over-long --run-id is NOT pre-checked in the CLI (green on main
// too; it guards against adding a client copy of the server's bound): it reaches
// the server, and the server's BAD_USER_INPUT refusal exits 2 with its message.
func TestValidationRecordOverlongRunIDIsTheServersToRefuse2441(t *testing.T) {
	srv, sent := server5xx1795(t, 200, `{"errors":[{"message":"runId is too long","extensions":{"code":"BAD_USER_INPUT"}}]}`)
	err := record1795(t, srv, "--run-id", strings.Repeat("r", 201))
	if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "runId is too long") {
		t.Fatalf("the server's refusal passes through at exit %d: %v", exitcode.Usage, err)
	}
	if len(*sent) != 1 {
		t.Fatalf("the 201-character id must reach the server exactly once: sent=%d", len(*sent))
	}
}
