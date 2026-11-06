package bootpreview

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func selects1655(doc, field string) bool {
	for _, line := range strings.Split(doc, "\n") {
		v := strings.TrimSpace(line)
		if v == field || v == field+" {" {
			return true
		}
	}
	return false
}

func TestPreviewSelectionBoundary(t *testing.T) {
	for _, f := range []string{"bootPromptRef", "bootPromptTargetDeleted"} {
		if !selects1655(gen.GetWorkerBootPromptServe_Operation, f) {
			t.Errorf("missing own same-read field %s", f)
		}
	}
	for _, doc := range []string{gen.AppAgentEffectiveRoster_Operation, gen.GetWorkerBootPromptServe_Operation} {
		for _, f := range []string{"bootPromptServe", "pointerLevel", "compiledNodeId", "servedRevSeq", "refusal"} {
			if !selects1655(doc, f) {
				t.Errorf("missing selected field %s", f)
			}
		}
		for _, f := range []string{"content", "compiledContentHash", "provenanceRevSeq", "renderedContent", "node", "nodeById"} {
			if selects1655(doc, f) {
				t.Errorf("unneeded/past-gate selection %s", f)
			}
		}
	}
	for _, doc := range []string{gen.AppAgentRoster_Operation, gen.AppAgentInstallationRoster_Operation, gen.GetWorker_Operation, gen.GetWorkerInApp_Operation, gen.WorkersRoster_Operation} {
		if selects1655(doc, "bootPromptServe") {
			t.Fatal("shared/legacy provider floor widened")
		}
	}
}
