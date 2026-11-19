package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestDecisionOperationsUseQuestionNames2465 pins cli#2465 S1-companion: the
// server (hadron-server#4425/#4435) renamed the governed-decision operations
// and types to question*/Question* with no aliases, so a document that still
// names the old ones fails on a current server with "Unknown type
// DecisionState". decision.graphql must address the new names only.
func TestDecisionOperationsUseQuestionNames2465(t *testing.T) {
	raw, err := os.ReadFile("queries/decision.graphql")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, op := range []string{
		"questionLog(", "question(", "questions(", "verifyQuestion(", "createQuestionLog(",
		"setQuestionLogDeciders(", "askQuestion(", "amendQuestion(", "withdrawQuestion(",
		"purgeUnansweredQuestion(",
	} {
		if !strings.Contains(doc, op) {
			t.Errorf("decision.graphql does not call %s", op)
		}
	}
	old := regexp.MustCompile(`\b(decisionLog|decision|decisions|verifyDecision|createDecisionLog|setDecisionLogDeciders|askDecision|amendDecision|withdrawDecision|purgeUnansweredDecision)\(|\bon Decision\w*\b|:\s*\[?(Decision|AskDecision|AmendDecision)\w+!?\]?`)
	if m := old.FindString(doc); m != "" {
		t.Errorf("decision.graphql still names a retired server identifier: %q", m)
	}

	sdl, err := os.ReadFile("../../schema/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sdl), "type Question {") || strings.Contains(string(sdl), "type Decision {") {
		t.Error("schema snapshot does not carry the Question types (refresh it from a server at or after #4435)")
	}
}
