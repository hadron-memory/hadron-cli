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

func hostDocumentField1635(document, field string) bool {
	for _, line := range strings.Split(document, "\n") {
		if strings.TrimSpace(line) == field {
			return true
		}
	}
	return false
}
