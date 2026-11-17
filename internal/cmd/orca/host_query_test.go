package orca

import (
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"strings"
	"testing"
)

func TestHostPlan1635MinimalIdentityDocuments(t *testing.T) {
	for _, bad := range []string{"prompt", "signature", "trailer", "continuity", "sessions", "modelGroups", "dueWakes", "tickers"} {
		if hostDocumentField1635(gen.HostPlanWorker_Operation, bad) {
			t.Fatalf("unneeded worker field %s selected", bad)
		}
	}
	for _, field := range []string{"id", "appId", "family", "retiredAt"} {
		found := false
		for _, line := range strings.Split(gen.HostPlanWorker_Operation, "\n") {
			if strings.TrimSpace(line) == field {
				found = true
			}
		}
		if !found {
			t.Fatalf("required identity %s not selected", field)
		}
	}
	for _, bad := range []string{"name", "urn", "description", "appType"} {
		for _, line := range strings.Split(gen.HostPlanApp_Operation, "\n") {
			if strings.TrimSpace(line) == bad {
				t.Fatalf("unneeded App field %s selected", bad)
			}
		}
	}
}

func TestHostPlan1635ExactIdentitySelections(t *testing.T) {
	expected := map[string]string{
		gen.HostPlanWorker_Operation: `query HostPlanWorker ($appRef: ID!, $ref: ID!) { worker(appRef: $appRef, ref: $ref) { id appId family retiredAt } }`,
		gen.HostPlanApp_Operation:    `query HostPlanApp ($ref: ID!) { app(ref: $ref) { id } }`,
	}
	normalize := func(s string) string { return strings.Join(strings.Fields(s), "") }
	for actual, want := range expected {
		if normalize(actual) != normalize(want) {
			t.Fatalf("identity selection widened or changed: %s", actual)
		}
	}
}

func hostDocumentField1635(document, field string) bool {
	for _, line := range strings.Split(document, "\n") {
		if strings.TrimSpace(line) == field {
			return true
		}
	}
	return false
}
