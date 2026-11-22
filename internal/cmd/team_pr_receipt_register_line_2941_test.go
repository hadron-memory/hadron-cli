package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#2941: the pr write receipts printed the register answer (hadron-server
// #3448's optional guidance) as a bare "action <code>  UNMAPPED" line plus an
// all-dash "task loc —  task URN —" line, which reviewers read as the write not
// recording — it had recorded; the register answer only says where an action
// code routes. The text receipt now leads with the task line (the actual
// receipt) and renders the register answer as one self-describing line.
func TestPRReceiptRegisterLine2941(t *testing.T) {
	cases := []struct{ state, want string }{
		{"MAPPED", "register review.verdict MAPPED → hrn:node:acme.com:role:tasks:review"},
		{"UNRESOLVED", "register review.verdict UNRESOLVED → no runnable task found at tasks:review"},
		{"UNMAPPED", "register review.verdict UNMAPPED — no register entry matches this code"},
		{"WITHHELD", "register review.verdict WITHHELD — code-only guidance, no register lookup"},
	}
	for _, tc := range cases {
		loc, urn := "null", "null"
		switch tc.state {
		case "MAPPED":
			urn = `"hrn:node:acme.com:role:tasks:review"`
		case "UNRESOLVED":
			loc = `"tasks:review"`
		}
		reply := `{"data":{"recordReviewVerdict":{"task":{"id":"t1","handle":"AT0.7","status":"STARTED"},"action":{"code":"review.verdict","state":"` + tc.state + `","taskLoc":` + loc + `,"taskUrn":` + urn + `}}}}`
		out, n, _, err := runPR1664(t, "PRVerdict", reply, "verdict", "AT0.7", "--head", prHead1664, "--verdict", "PASS")
		if err != nil || n != 1 {
			t.Fatalf("%s: calls=%d err=%v out=%s", tc.state, n, err, out)
		}
		if !strings.HasPrefix(out, "AT0.7 t1 (STARTED)\n") {
			t.Fatalf("%s: the receipt must lead with the task line, got %q", tc.state, out)
		}
		if !strings.Contains(out, tc.want) {
			t.Fatalf("%s: want register line %q, got %q", tc.state, tc.want, out)
		}
		for _, gone := range []string{"action review.verdict", "task loc", "task URN"} {
			if strings.Contains(out, gone) {
				t.Fatalf("%s: the old misleading %q output is still printed: %q", tc.state, gone, out)
			}
		}
	}
}

// A state the CLI does not know relays verbatim with whatever fields arrived —
// never a bare code that could read as a failed write, and never a fabricated
// loc or URN.
func TestPRReceiptUnknownStateRelaysFields2941(t *testing.T) {
	reply := `{"data":{"recordReadyCall":{"task":{"id":"t1","handle":"AT0.7","status":"STARTED"},"action":{"code":"pr.ready","state":"NEW_STATE","taskLoc":"tasks:review","taskUrn":null}}}}`
	out, n, _, err := runPR1664(t, "PRReady", reply, "ready", "AT0.7", "--head", prHead1664)
	if err != nil || n != 1 {
		t.Fatalf("calls=%d err=%v out=%s", n, err, out)
	}
	if !strings.Contains(out, "register pr.ready NEW_STATE  task loc tasks:review") || strings.Contains(out, "task URN") {
		t.Fatalf("unknown state must relay verbatim beside its fields: %q", out)
	}
}

// The --json receipt is a stable contract: it keeps the server's answer,
// UNMAPPED included, exactly as sent.
func TestPRReceiptJSONShapeUnchanged2941(t *testing.T) {
	reply := `{"data":{"recordReviewVerdict":{"task":{"id":"t1","handle":"AT0.7","status":"STARTED"},"action":{"code":"review.verdict","state":"UNMAPPED","taskLoc":null,"taskUrn":null}}}}`
	out, n, _, err := runPR1664(t, "PRVerdict", reply, "verdict", "AT0.7", "--head", prHead1664, "--verdict", "PASS", "--json")
	if err != nil || n != 1 {
		t.Fatalf("calls=%d err=%v out=%s", n, err, out)
	}
	var dto struct {
		Task   struct{ ID, Handle, Status string }
		Action struct {
			Code, State      string
			TaskLoc, TaskURN *string
		}
	}
	if json.Unmarshal([]byte(out), &dto) != nil || dto.Task.Handle != "AT0.7" || dto.Task.Status != "STARTED" || dto.Action.Code != "review.verdict" || dto.Action.State != "UNMAPPED" || dto.Action.TaskLoc != nil || dto.Action.TaskURN != nil {
		t.Fatal(out)
	}
}
