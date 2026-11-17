package api

import (
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2206: a run-settings refusal depends on the task's requirements, so the
// server judges it; the CLI maps TASK_RUN_SETTINGS_INVALID to a usage error
// (exit 2) and shows the server's reason.
func TestTaskRunSettingsRefusalShowsItsReason2206(t *testing.T) {
	for _, reason := range []string{"MODEL_OUTSIDE_FAMILY", "FAMILY_EXCLUDED", "TIER_BELOW_MINIMUM", "BAD_VALUE"} {
		refusal := gqlerror.List{{Message: "provider wording", Extensions: map[string]any{"code": "TASK_RUN_SETTINGS_INVALID", "reason": reason}}}
		for _, raw := range []error{refusal, &graphql.HTTPError{StatusCode: 400, Response: graphql.Response{Errors: refusal}}} {
			err := MapError(raw)
			if exitcode.FromError(err) != exitcode.Usage {
				t.Fatalf("%s: exit %d, want usage (2)", reason, exitcode.FromError(err))
			}
			if !strings.Contains(err.Error(), reason) {
				t.Fatalf("%s: the reason must be shown: %q", reason, err.Error())
			}
			if !HasErrorCode(err, "TASK_RUN_SETTINGS_INVALID") {
				t.Fatal("mapping lost the typed refusal")
			}
		}
	}
}
