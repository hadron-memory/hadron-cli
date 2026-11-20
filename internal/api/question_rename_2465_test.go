package api

import (
	"os"
	"path/filepath"
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
	if m := retiredServerName.FindString(doc); m != "" {
		t.Errorf("decision.graphql still names a retired server identifier: %q", m)
	}

	sdl, err := os.ReadFile("../../schema/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sdl), "type Question {") {
		t.Error("schema snapshot does not carry type Question (refresh it from a server at or after #4435)")
	}
	if m := leftoverDecisionType.FindString(string(sdl)); m != "" {
		t.Errorf("schema snapshot still carries a retired Decision type: %q", m)
	}
}

// retiredServerName matches a retired server identifier in a query document:
// a call to a renamed field, a type condition or variable type, or a quoted
// genqlient `for:` directive target. The client-chosen names that keep the
// Decision spelling (see decisionClientNames) are not matched, because they are
// never called with `(`, never follow `on`/`:`, and never open a `for:` string
// as a server input type (AskQuestionInput etc. are the live ones).
var retiredServerName = regexp.MustCompile(
	`\b(decisionLog|decision|decisions|verifyDecision|createDecisionLog|setDecisionLogDeciders|askDecision|amendDecision|withdrawDecision|purgeUnansweredDecision)\(` +
		`|\bon Decision\w*\b` +
		`|:\s*\[?(Decision|AskDecision|AmendDecision)\w+!?\]?` +
		`|for:\s*"(Ask|Amend)?Decision\w*[."]`)

// leftoverDecisionType matches any retired `type Decision... {` in the schema
// snapshot, not just `type Decision {`.
var leftoverDecisionType = regexp.MustCompile(`type Decision\w* \{`)

// decisionClientNames is the explicit allowlist of client-chosen operation and
// fragment names that intentionally keep the Decision spelling (see the header
// of decision.graphql). Any other `Decision`-spelled name in a queries/ document
// is a leftover of the rename.
var decisionClientNames = regexp.MustCompile(
	`\b(?:fragment|query|mutation)\s+(?:Decision\w*|AskDecision\w*|AmendDecision\w*|WithdrawDecision\w*|VerifyDecision\w*|PurgeUnansweredDecision\w*)\b|\.\.\.Decision\w*Fields\b`)

// TestNoRetiredDecisionNamesInAnyQueryDocument2644 sweeps every document under
// queries/, not just decision.graphql: a retired server identifier anywhere
// fails on a current server. decision.graphql's own client-chosen names are
// stripped first via the allowlist.
func TestNoRetiredDecisionNamesInAnyQueryDocument2644(t *testing.T) {
	files, err := filepath.Glob("queries/*.graphql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no query documents found: %v", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		doc := decisionClientNames.ReplaceAllString(string(raw), "")
		if m := retiredServerName.FindString(doc); m != "" {
			t.Errorf("%s still names a retired server identifier: %q", f, m)
		}
	}
}
