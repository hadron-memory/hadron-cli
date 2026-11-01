package cmd

import (
	"strings"
	"testing"
)

// #1278: `session log` used to move the PR/branch pointer BEFORE recording the
// worklog row, so a refused record left the pointer moved with no row and an
// error that said nothing about it. The record now comes first and the pointer
// moves only after it; the reverse partial is named.
func TestSessionLogOrder1278(t *testing.T) {
	refusal := `{"data":null,"errors":[{"message":"Team Agent cannot be inferred","extensions":{"code":"NOT_FOUND"}}]}`
	pointerRefused := `{"data":null,"errors":[{"message":"session is ended","extensions":{"code":"BAD_USER_INPUT"}}]}`
	recorded := `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
		"tool":"github","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"worked-on",
		"at":"2026-08-13T10:00:00Z","detail":null,"model":null}}}`

	run := func(t *testing.T, args []string, responses map[string]string) (called map[string]bool, errOut string, err error) {
		t.Helper()
		bindWorklogModelTest(t)
		gql, captured := captureGraphQL(t, responses)
		f, _, eo := testFactoryTTY(t, "")
		root := NewRootCmd(f)
		root.SetArgs(append(args, "--server", gql.URL))
		err = root.Execute()
		called = map[string]bool{}
		for op := range captured {
			called[op] = true
		}
		return called, eo.String(), err
	}

	t.Run("a refused record leaves the pointer untouched and keeps its exit code", func(t *testing.T) {
		for _, kind := range [][]string{{"--pr", "371"}, {"--branch", "feature/x"}} {
			called, _, err := run(t, append([]string{"team", "session", "log"}, kind...), map[string]string{
				"UpdateTeamSession": modelLogSessionJSON,
				"RecordTeamWork":    refusal,
			})
			if err == nil || exitCodeFor(err) != 4 {
				t.Fatalf("%v: want exit 4, got %v", kind, err)
			}
			if !called["RecordTeamWork"] {
				t.Fatalf("%v: record never attempted", kind)
			}
			if called["UpdateTeamSession"] {
				t.Errorf("%v: pointer moved although the record was refused", kind)
			}
		}
	})

	t.Run("a refused pointer after a recorded row names the half-applied state", func(t *testing.T) {
		called, errOut, err := run(t, []string{"team", "session", "log", "--pr", "371"}, map[string]string{
			"UpdateTeamSession": pointerRefused,
			"RecordTeamWork":    recorded,
		})
		if err == nil || exitCodeFor(err) != 2 {
			t.Fatalf("want the pointer refusal's exit 2, got %v", err)
		}
		if !called["RecordTeamWork"] || !called["UpdateTeamSession"] {
			t.Fatalf("both calls expected: %v", called)
		}
		if !strings.Contains(errOut, "worklog row WAS recorded") || !strings.Contains(errOut, "NOT updated") {
			t.Errorf("stderr does not name the partial state: %q", errOut)
		}
	})

	t.Run("a lost pointer answer after a recorded row does not claim NOT updated", func(t *testing.T) {
		called, errOut, err := run(t, []string{"team", "session", "log", "--pr", "371"}, map[string]string{
			"UpdateTeamSession": `{"data":`, // truncated body: the write may have been applied
			"RecordTeamWork":    recorded,
		})
		if err == nil || exitCodeFor(err) != 7 {
			t.Fatalf("want exit 7 (answer lost), got %v", err)
		}
		if !called["RecordTeamWork"] || !called["UpdateTeamSession"] {
			t.Fatalf("both calls expected: %v", called)
		}
		if !strings.Contains(errOut, "worklog row WAS recorded") || !strings.Contains(errOut, "could NOT be confirmed") || strings.Contains(errOut, "was NOT updated") {
			t.Errorf("stderr must state the uncertainty, not a definite NOT updated: %q", errOut)
		}
	})

	t.Run("an undecodable pointer answer after a recorded row is uncertain too", func(t *testing.T) {
		for name, body := range map[string]string{"not json": `not-json`, "empty body": ``, "html": `<html>oops</html>`} {
			called, errOut, err := run(t, []string{"team", "session", "log", "--pr", "371"}, map[string]string{
				"UpdateTeamSession": body,
				"RecordTeamWork":    recorded,
			})
			if err == nil || !called["UpdateTeamSession"] {
				t.Fatalf("%s: err=%v called=%v", name, err, called)
			}
			if !strings.Contains(errOut, "could NOT be confirmed") || strings.Contains(errOut, "was NOT updated") {
				t.Errorf("%s: undecodable answer must read as uncertain: %q", name, errOut)
			}
		}
	})

	t.Run("an internal fault on the pointer after a recorded row is also uncertain", func(t *testing.T) {
		_, errOut, err := run(t, []string{"team", "session", "log", "--pr", "371"}, map[string]string{
			"UpdateTeamSession": `{"data":null,"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR","requestId":"req-1"}}]}`,
			"RecordTeamWork":    recorded,
		})
		if err == nil {
			t.Fatal("want an error")
		}
		if !strings.Contains(errOut, "could NOT be confirmed") || strings.Contains(errOut, "was NOT updated") {
			t.Errorf("internal fault must read as uncertain: %q", errOut)
		}
	})

	t.Run("success sends both requests and prints the usual receipt", func(t *testing.T) {
		called, _, err := run(t, []string{"team", "session", "log", "--pr", "371"}, map[string]string{
			"UpdateTeamSession": modelLogSessionJSON,
			"RecordTeamWork":    recorded,
		})
		if err != nil || !called["RecordTeamWork"] || !called["UpdateTeamSession"] {
			t.Fatalf("err=%v called=%v", err, called)
		}
	})
}
