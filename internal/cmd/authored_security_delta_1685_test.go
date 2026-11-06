package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestAuthoredUnavailablePartialRender1685(t *testing.T) {
	for _, status := range []string{"created root but its contract failed", "scaffolding failed after creating A, B", "linked old to replacement; retirement failed"} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", status, jsonMode), func(t *testing.T) {
				cause := gqlerror.List{&gqlerror.Error{Message: "SECRET_TASK", Extensions: map[string]interface{}{"code": "AUTHOR_TASK_BROKEN", "taskAccess": "UNAVAILABLE", "task": "SECRET_TASK"}}}
				err := fmt.Errorf("%s: %w", status, api.MapError(cause))
				f, out := testFactory(t)
				var stderr bytes.Buffer
				f.IOStreams.ErrOut = &stderr
				args := []string{"spec", "new"}
				if jsonMode {
					args = append(args, "--json")
				}
				renderFailure(f, args, err)
				text := out.String() + stderr.String()
				if !strings.Contains(text, status) || strings.Contains(text, "SECRET_") {
					t.Fatalf("partial status or concealment lost: %s", text)
				}
			})
		}
	}
}

func TestAuthoredMalformedAccessConcealed1685(t *testing.T) {
	for _, access := range []interface{}{nil, "readable", "", 1} {
		t.Run(fmt.Sprint(access), func(t *testing.T) {
			ext := map[string]interface{}{"code": "AUTHOR_TASK_BROKEN", "task": "SECRET_TASK", "taskId": "SECRET_ID", "load": map[string]interface{}{"cli": "SECRET_CLI"}}
			if access != nil {
				ext["taskAccess"] = access
			}
			cause := gqlerror.List{&gqlerror.Error{Message: "SECRET_TASK", Extensions: ext}}
			details := api.AuthoringAttestationDetails(cause)
			if len(details) != 2 || details["taskAccess"] != "UNAVAILABLE" {
				t.Fatalf("fail-open details: %v", details)
			}
			for _, jsonMode := range []bool{false, true} {
				f, out := testFactory(t)
				var stderr bytes.Buffer
				f.IOStreams.ErrOut = &stderr
				args := []string{"node", "create"}
				if jsonMode {
					args = append(args, "--json")
				}
				renderFailure(f, args, api.MapError(cause))
				if strings.Contains(out.String()+stderr.String(), "SECRET_") {
					t.Fatalf("leak: %s", out.String())
				}
			}
		})
	}
}

func TestAuthoredMixedAccessConcealed1685(t *testing.T) {
	cause := gqlerror.List{
		&gqlerror.Error{Message: "readable", Extensions: map[string]interface{}{"code": "AUTHORING_ATTESTATION_MISSING", "taskAccess": "READABLE", "task": "public"}},
		&gqlerror.Error{Message: "SECRET_TASK", Extensions: map[string]interface{}{"code": "AUTHOR_TASK_BROKEN", "taskAccess": "UNAVAILABLE", "task": "SECRET_TASK"}},
	}
	for _, jsonMode := range []bool{false, true} {
		f, out := testFactory(t)
		var stderr bytes.Buffer
		f.IOStreams.ErrOut = &stderr
		args := []string{"node", "create"}
		if jsonMode {
			args = append(args, "--json")
		}
		renderFailure(f, args, api.MapError(cause))
		if strings.Contains(out.String()+stderr.String(), "SECRET_") {
			t.Fatalf("mixed response leaked: %s", out.String())
		}
	}
}
