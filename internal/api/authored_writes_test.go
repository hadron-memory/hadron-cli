package api

import (
	"fmt"
	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"strings"
	"testing"
)

// Check the static documents too: omitting a variable cannot make an unknown
// argument valid on an older server.
func TestAuthoredOrdinaryDocuments1685(t *testing.T) {
	for name, doc := range map[string]string{"data": gen.UpdateNodeData_Operation, "properties": gen.UpdateNodeProperties_Operation, "clearNode": gen.UpdateNodeClearingRole_Operation, "clearTask": gen.UpdateTaskNodeClearingRole_Operation, "clearSpec": gen.UpdateSpecNodeClearingRole_Operation, "clearReview": gen.UpdateReviewNodeClearingRole_Operation} {
		if strings.Contains(doc, "authoredWith") {
			t.Errorf("ordinary %s document requires the new argument", name)
		}
	}
	for name, doc := range map[string]string{"data": gen.UpdateNodeDataAuthored_Operation, "properties": gen.UpdateNodePropertiesAuthored_Operation} {
		if !strings.Contains(doc, "authoredWith: $authoredWith") {
			t.Errorf("attested %s document drops claim", name)
		}
	}
}

func TestAuthoredConcealmentPreservesExit1685(t *testing.T) {
	for _, code := range []string{"AUTHORING_ATTESTATION_MISSING", "AUTHORING_ATTESTATION_WRONG_TASK", "AUTHORING_ATTESTATION_STALE", "AUTHOR_TASK_BROKEN"} {
		for _, status := range []int{0, -7, -5, 400, 403, 409, 500, 502} {
			t.Run(fmt.Sprintf("%s/%d", code, status), func(t *testing.T) {
				makeErr := func(access string) error {
					list := gqlerror.List{{Message: "SECRET_TASK", Extensions: map[string]any{"code": code, "taskAccess": access, "task": "SECRET_TASK"}}}
					if status < 0 {
						return exitcode.New(-status, list)
					}
					if status == 0 {
						return list
					}
					return &graphql.HTTPError{StatusCode: status, Response: graphql.Response{Errors: list}}
				}
				readable := MapError(makeErr("READABLE"))
				if status >= 0 && exitcode.FromError(readable) != 1 {
					t.Fatalf("absolute refusal exit changed: %d", exitcode.FromError(readable))
				}
				if status < 0 && exitcode.FromError(readable) != -status {
					t.Fatal("preclassified control was not exercised")
				}
				unavailable := MapError(makeErr("UNAVAILABLE"))
				if exitcode.FromError(readable) != exitcode.FromError(unavailable) {
					t.Fatalf("exit changed %d -> %d", exitcode.FromError(readable), exitcode.FromError(unavailable))
				}
				if strings.Contains(unavailable.Error(), "SECRET_TASK") {
					t.Fatal("message leaked")
				}
				if !HasErrorCode(unavailable, code) {
					t.Fatal("chain lost")
				}
			})
		}
	}
}

func TestAuthoredReadableOrder1685(t *testing.T) {
	list := gqlerror.List{
		{Message: "first", Extensions: map[string]any{"code": "AUTHORING_ATTESTATION_MISSING", "taskAccess": "READABLE", "task": "first-task", "taskRevision": 4}},
		{Message: "second", Extensions: map[string]any{"code": "AUTHORING_ATTESTATION_STALE", "taskAccess": "READABLE", "task": "second-task", "taskRevision": 9}},
	}
	d := AuthoringAttestationDetails(list)
	if d["task"] != "first-task" || d["taskRevision"] != int64(4) || d["code"] != "AUTHORING_ATTESTATION_MISSING" {
		t.Fatalf("first readable projection lost: %v", d)
	}
}
