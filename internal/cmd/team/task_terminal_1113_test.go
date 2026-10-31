package team

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode"
)

func TestTaskTerminal1113(t *testing.T) {
	hostile := "x\x1b[31m\x1b]8;;https://evil\x07\u009b\u202e\u200b\u2028\t\r\r\nx"
	p := func(s string) *string { return &s }
	party := &taskPartyDTO{ID: hostile, Name: p(hostile)}
	task := taskDTO{ID: hostile, Handle: hostile, Title: hostile, Status: hostile, AppID: hostile, Description: p(hostile), Owner: party, CreatedBy: party, Assignee: party, AssignedBy: party, ParentTaskID: p(hostile), StartBy: p(hostile), Tags: []string{hostile}, Source: p(hostile), Requirements: &taskRequirementsDTO{Role: p(hostile), FamilyIn: []string{hostile}, FamilyNotIn: []string{hostile}, ExcludeWorkerIds: []string{hostile}}, Result: &taskResultDTO{Outcome: hostile, Summary: p(hostile), Evidence: p(hostile)}, CreatedAt: hostile, StartedAt: p(hostile), FinishedAt: p(hostile), CancelledAt: p(hostile), UpdatedAt: p(hostile)}
	before, _ := json.Marshal(task)
	var out bytes.Buffer
	if err := renderTaskDetail(&out, task); err != nil {
		t.Fatal(err)
	}
	for depth := 0; depth < 4; depth++ {
		if err := renderTreeNode(&out, &task, depth); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range out.String() {
		if (unicode.IsControl(r) && r != '\n') || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			t.Fatalf("unsafe rune %U", r)
		}
	}
	after, _ := json.Marshal(task)
	if !bytes.Equal(before, after) {
		t.Fatal("DTO mutated")
	}
	for depth := 0; depth < 4; depth++ {
		if !strings.Contains(out.String(), "\n"+strings.Repeat("  ", depth)+"• ") {
			t.Fatal("tree indent changed")
		}
	}
	task = taskDTO{ID: "id", Title: "title", Status: "PENDING", Description: p("first\n  owner: forged"), Result: &taskResultDTO{Summary: p("first\n  owner: forged"), Evidence: p("first\n  owner: forged")}}
	out.Reset()
	if err := renderTaskDetail(&out, task); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  first\n      owner: forged", "    first\n        owner: forged", "    evidence: first\n        owner: forged"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %q", want, out.String())
		}
	}
}
