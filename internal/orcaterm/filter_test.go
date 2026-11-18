package orcaterm

import (
	"context"
	"testing"
)

func TestFilterProof953(t *testing.T) {
	header := "ⓘ This content can’t be shown"
	body := "  Apply for Daybreak: https://openai.com/form/test"
	for _, tc := range []struct {
		name       string
		lines      []string
		want, note string
	}{
		{"full", []string{header, body, "› "}, "BLOCKED", "provider_notice"},
		{"header", []string{header, "› "}, "AMBIGUOUS", "header_only"},
		{"body", []string{body, "› "}, "AMBIGUOUS", "body_only"},
		{"running", []string{header, body, "esc to interrupt", "› "}, "AMBIGUOUS", "turn_running"},
		{"footer title", []string{"Worked for 2s", "› ", "Recover Dara worker session"}, "NOT_BLOCKED", ""},
		{"quoted header", []string{"User: " + header, body, "› "}, "AMBIGUOUS", "body_only"},
		{"noncontiguous", []string{header, "ordinary conversation", body, "› "}, "AMBIGUOUS", "header_only"},
		{"older notice", []string{header, body, "Worked for 3s", "› "}, "AMBIGUOUS", "older_notice"},
		{"notice title below prompt", []string{"Worked for 3s", "› ", header, body}, "NOT_BLOCKED", ""},
		{"no idle proof", []string{header, body}, "AMBIGUOUS", "idle_unproven"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, note := FilterProof(&Screen{Source: "screen", Tail: tc.lines})
			if got != tc.want || note != tc.note {
				t.Fatalf("%s/%s want %s/%s", got, note, tc.want, tc.note)
			}
		})
	}
	if got, note := FilterProof(nil); got != "UNREADABLE" || note != "read_failed" {
		t.Fatal(got, note)
	}
}

func TestReadOnlyClientRejectsMutations953(t *testing.T) {
	calls := 0
	c := NewReadOnlyClient(func(_ context.Context, _ []string) Result { calls++; return Result{} })
	for _, verb := range []string{"send", "wait", "create", "close", "rename"} {
		if got := c.run(context.Background(), []string{"terminal", verb}); got.Code == 0 {
			t.Errorf("accepted %s", verb)
		}
	}
	if calls != 0 {
		t.Fatal("runner called", calls)
	}
}
