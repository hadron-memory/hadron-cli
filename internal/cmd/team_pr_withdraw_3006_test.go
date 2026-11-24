package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3006 (companion to hadron-server#4869, #4441): `hadron team pr withdraw <seat-task> --head <sha>
// [--receipt <url>]` makes one withdrawReviewVerdict call and prints the receipt the other pr verbs print.

const prWithdrawTask3006 = "hrn:agenttask:hadronmemory.com:team:AT0.7.1"

const prWithdrawReply3006 = `{"data":{"withdrawReviewVerdict":{"task":{"id":"t1","handle":"AT0.7.1","status":"FINISHED"},"action":{"code":"review.verdict.withdrawn","state":"UNMAPPED","taskLoc":null,"taskUrn":null}}}}`

func TestPRWithdrawCallsTheMutationOnceAndPrintsTheReceipt3006(t *testing.T) {
	out, n, raw, err := runPR1664(t, "PRWithdraw", prWithdrawReply3006, "withdraw", prWithdrawTask3006, "--head", prHead1664, "--json")
	if err != nil || n != 1 {
		t.Fatalf("exactly one withdrawReviewVerdict call: calls=%d err=%v %s", n, err, out)
	}
	var vars map[string]any
	if json.Unmarshal(raw, &vars) != nil || vars["taskRef"] != prWithdrawTask3006 || vars["head"] != prHead1664 {
		t.Fatalf("taskRef and head are passed as given: %s", raw)
	}
	if _, has := vars["receipt"]; has {
		t.Errorf("receipt is omitted when the flag is unset: %s", raw)
	}
	if _, has := vars["instruction"]; has {
		t.Errorf("no --instruction is sent (the gate's ruling): %s", raw)
	}
	var dto struct {
		Task   map[string]any `json:"task"`
		Action map[string]any `json:"action"`
	}
	if json.Unmarshal([]byte(out), &dto) != nil || dto.Task["handle"] != "AT0.7.1" || dto.Task["status"] != "FINISHED" || dto.Action["code"] != "review.verdict.withdrawn" || dto.Action["state"] != "UNMAPPED" {
		t.Errorf("--json prints the receipt shape the other pr verbs print: %s", out)
	}
	text, n, _, err := runPR1664(t, "PRWithdraw", prWithdrawReply3006, "withdraw", prWithdrawTask3006, "--head", prHead1664)
	if err != nil || n != 1 || !strings.Contains(text, "AT0.7.1 t1 (FINISHED)") {
		t.Errorf("text: the task line first: calls=%d err=%v %q", n, err, text)
	}
}

func TestPRWithdrawSendsTheReceiptWhenGiven3006(t *testing.T) {
	_, n, raw, err := runPR1664(t, "PRWithdraw", prWithdrawReply3006, "withdraw", prWithdrawTask3006, "--head", prHead1664, "--receipt", "https://github.com/acme/app/pull/9#issuecomment-1")
	var vars map[string]any
	if err != nil || n != 1 || json.Unmarshal(raw, &vars) != nil || vars["receipt"] != "https://github.com/acme/app/pull/9#issuecomment-1" {
		t.Fatalf("the receipt URL is sent as given: calls=%d err=%v %s", n, err, raw)
	}
	// a blank receipt is a usage error before any request, like on pr verdict
	_, n, _, err = runPR1664(t, "PRWithdraw", prWithdrawReply3006, "withdraw", prWithdrawTask3006, "--head", prHead1664, "--receipt", "  ")
	if err == nil || exitCodeFor(err) != exitcode.Usage || n != 0 {
		t.Errorf("a blank --receipt is refused before any request: calls=%d err=%v", n, err)
	}
}

func TestPRWithdrawChecksItsArgumentsBeforeAnyRequest3006(t *testing.T) {
	for name, args := range map[string][]string{
		"no head":           {"withdraw", prWithdrawTask3006},
		"a short head":      {"withdraw", prWithdrawTask3006, "--head", "0123456"},
		"a non-hex head":    {"withdraw", prWithdrawTask3006, "--head", strings.Repeat("z", 40)},
		"a blank task":      {"withdraw", "   ", "--head", prHead1664},
		"no task":           {"withdraw", "--head", prHead1664},
		"a second argument": {"withdraw", prWithdrawTask3006, "extra", "--head", prHead1664},
	} {
		t.Run(name, func(t *testing.T) {
			_, n, _, err := runPR1664(t, "PRWithdraw", prWithdrawReply3006, args...)
			if err == nil || exitCodeFor(err) != exitcode.Usage || n != 0 {
				t.Fatalf("a usage error (exit 2) and no request: calls=%d exit=%d err=%v", n, exitCodeFor(err), err)
			}
		})
	}
}

// Each server refusal reaches the user with its code, writes nothing (one call, never repeated), and
// keeps the code in --json (serverCode).
func TestPRWithdrawRefusalsReachTheUserWithTheirCode3006(t *testing.T) {
	for _, code := range []string{
		"TASK_NOT_FOUND", "REVIEW_SEAT_UNASSIGNED", "TASK_INVALID_TRANSITION", "REVIEW_HEAD_MISMATCH",
		"REVIEW_VERDICT_NOT_NEGATIVE", "REVIEW_VERDICT_ALREADY_WITHDRAWN", "REVIEW_WITHDRAW_FORBIDDEN", "BAD_USER_INPUT",
	} {
		t.Run(code, func(t *testing.T) {
			reply := `{"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}],"data":null}`
			out, n, _, err := runPR1664(t, "PRWithdraw", reply, "withdraw", prWithdrawTask3006, "--head", prHead1664, "--json")
			if err == nil || n != 1 {
				t.Fatalf("a refusal is an error after exactly one call: calls=%d err=%v", n, err)
			}
			if !strings.Contains(out, code) || exitCodeFor(err) == exitcode.OK {
				t.Errorf("the code reaches the user: exit=%d out=%q", exitCodeFor(err), out)
			}
		})
	}
}

func TestPRWithdrawWithoutAReceiptIsUnavailable3006(t *testing.T) {
	for name, reply := range map[string]string{
		"null result":      `{"data":{"withdrawReviewVerdict":null}}`,
		"no action":        `{"data":{"withdrawReviewVerdict":{"task":{"id":"t1","handle":"AT0.7.1","status":"FINISHED"},"action":null}}}`,
		"an empty task id": `{"data":{"withdrawReviewVerdict":{"task":{"id":"","handle":"AT0.7.1","status":"FINISHED"},"action":{"code":"review.verdict.withdrawn","state":"UNMAPPED","taskLoc":null,"taskUrn":null}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, n, _, err := runPR1664(t, "PRWithdraw", reply, "withdraw", prWithdrawTask3006, "--head", prHead1664)
			if err == nil || exitCodeFor(err) != exitcode.Unavailable || n != 1 {
				t.Fatalf("no receipt is the Unavailable exit (7), one call: calls=%d exit=%d err=%v", n, exitCodeFor(err), err)
			}
		})
	}
}

// The help says what a withdrawal is and is not, and the refusals a user can meet with their remedy; the
// two sentences it completes (the review rules shared by seat and verdict, and pr schedule) say so too.
func TestPRWithdrawHelpAndTheSentencesItCompletes3006(t *testing.T) {
	flat := func(args ...string) string { return strings.Join(strings.Fields(cmdHelpOut(t, args...)), " ") }
	leaf := flat("team", "pr", "withdraw")
	for _, want := range []string{
		"never an approval", "superseding seat", "TASK_INVALID_TRANSITION", "pr verdict", "REVIEW_HEAD_MISMATCH", "REVIEW_WITHDRAW_FORBIDDEN",
		"--head", "--receipt",
	} {
		if !strings.Contains(leaf, want) {
			t.Errorf("pr withdraw --help lacks %q:\n%s", want, leaf)
		}
	}
	if strings.Contains(leaf, "--instruction") {
		t.Errorf("no --instruction on pr withdraw (the gate's ruling):\n%s", leaf)
	}
	for _, page := range [][]string{{"team", "pr", "seat"}, {"team", "pr", "verdict"}} {
		if h := flat(page...); !strings.Contains(h, "pr withdraw") {
			t.Errorf("%v --help (the shared review rules) must name pr withdraw as the way a finished negative clears:\n%s", page, h)
		}
	}
	if h := flat("team", "pr", "schedule"); !strings.Contains(h, "unless that negative was withdrawn") {
		t.Errorf("pr schedule --help must say a withdrawn negative no longer pins its slot:\n%s", h)
	}
	if h := flat("team", "pr"); !strings.Contains(h, "pr withdraw") {
		t.Errorf("the pr group page lists withdraw:\n%s", h)
	}
}
