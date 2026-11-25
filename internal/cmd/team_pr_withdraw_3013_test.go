package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3013 (follow-up to #3006; cor:dmo:050:12, cor:api:240:02): `hadron team pr withdraw --instruction
// <kind>:<ref>` carries a holder's instruction claim on the one withdrawReviewVerdict call, so a worker on
// the App-manager arm can reach it. The CLI parses the claim and sends it; the server is the judge.

const prWithdrawInstructed3013 = "PRWithdrawInstructed"

func TestPRWithdrawInstructionSendsTheParsedClaimUnchanged3013(t *testing.T) {
	ref := "hrn:message:hadronmemory.com:hadron-dev-team:team-chat:147618"
	_, n, raw, err := runPR1664(t, prWithdrawInstructed3013, prWithdrawReply3006,
		"withdraw", prWithdrawTask3006, "--head", prHead1664, "--receipt", "https://github.com/acme/app/pull/9#issuecomment-1",
		"--instruction", "team-chat-message:"+ref)
	if err != nil || n != 1 {
		t.Fatalf("exactly one instructed withdraw call: calls=%d err=%v", n, err)
	}
	var vars map[string]any
	if json.Unmarshal(raw, &vars) != nil {
		t.Fatalf("variables: %s", raw)
	}
	want := map[string]any{"kind": "TEAM_CHAT_MESSAGE", "ref": ref}
	got, _ := vars["instruction"].(map[string]any)
	if len(got) != len(want) || got["kind"] != want["kind"] || got["ref"] != want["ref"] {
		t.Errorf("instruction = %#v, want %#v (the ref goes as written)", vars["instruction"], want)
	}
	if vars["taskRef"] != prWithdrawTask3006 || vars["head"] != prHead1664 || vars["receipt"] != "https://github.com/acme/app/pull/9#issuecomment-1" {
		t.Errorf("the other variables are unchanged by the flag: %s", raw)
	}
}

// Without the flag the plain document goes out and no instruction variable rides it (omitted, not null).
func TestPRWithdrawWithoutInstructionSendsNoInstruction3013(t *testing.T) {
	_, n, raw, err := runPR1664(t, "PRWithdraw", prWithdrawReply3006, "withdraw", prWithdrawTask3006, "--head", prHead1664)
	if err != nil || n != 1 {
		t.Fatalf("one plain call: calls=%d err=%v", n, err)
	}
	if strings.Contains(string(raw), "instruction") {
		t.Errorf("no flag, no instruction variable: %s", raw)
	}
}

func TestPRWithdrawInstructionBadClaimIsRefusedBeforeAnyRequest3013(t *testing.T) {
	for name, claim := range map[string]string{
		"no colon":     "team-chat-message",
		"unknown kind": "email:me@example.com",
		"blank ref":    "task:   ",
		"empty":        "",
	} {
		t.Run(name, func(t *testing.T) {
			for _, op := range []string{prWithdrawInstructed3013, "PRWithdraw"} {
				_, n, _, err := runPR1664(t, op, prWithdrawReply3006, "withdraw", prWithdrawTask3006, "--head", prHead1664, "--instruction", claim)
				if err == nil || exitCodeFor(err) != exitcode.Usage || n != 0 {
					t.Fatalf("a bad claim is a usage error and sends nothing (%s): calls=%d exit=%d err=%v", op, n, exitCodeFor(err), err)
				}
			}
		})
	}
}

// The server judges the claim: its refusal comes back with its code, after one call, and the CLI never
// retries the withdraw without the instruction.
func TestPRWithdrawInstructionRefusalIsTheServersAndNeverRetried3013(t *testing.T) {
	for _, code := range []string{"REVIEW_WITHDRAW_FORBIDDEN", "INSTRUCTION_REFUSED", "BAD_USER_INPUT"} {
		t.Run(code, func(t *testing.T) {
			reply := `{"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}],"data":null}`
			out, n, _, err := runPR1664(t, prWithdrawInstructed3013, reply,
				"withdraw", prWithdrawTask3006, "--head", prHead1664, "--instruction", "task:"+prWithdrawTask3006, "--json")
			if err == nil || n != 1 {
				t.Fatalf("one call, then an error: calls=%d err=%v", n, err)
			}
			if !strings.Contains(out, code) || exitCodeFor(err) == exitcode.OK {
				t.Errorf("the server's code reaches the user: exit=%d out=%q", exitCodeFor(err), out)
			}
		})
	}
	srv, count, _ := countingGraphQL(t, map[string]string{
		prWithdrawInstructed3013: `{"errors":[{"message":"refused","extensions":{"code":"REVIEW_WITHDRAW_FORBIDDEN"}}],"data":null}`,
		"PRWithdraw":             prWithdrawReply3006,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "pr", "--server", srv.URL, "withdraw", prWithdrawTask3006, "--head", prHead1664, "--instruction", "task:x"})
	if err := root.Execute(); err == nil || count("PRWithdraw") != 0 {
		t.Errorf("a refused instructed withdraw is never resent as the plain one: plain calls=%d err=%v", count("PRWithdraw"), err)
	}
}

func TestPRWithdrawInstructionWithoutReceiptIsUnavailable3013(t *testing.T) {
	_, n, _, err := runPR1664(t, prWithdrawInstructed3013, `{"data":{"withdrawReviewVerdict":null}}`,
		"withdraw", prWithdrawTask3006, "--head", prHead1664, "--instruction", "task:x")
	if err == nil || exitCodeFor(err) != exitcode.Unavailable || n != 1 {
		t.Fatalf("no receipt is exit 7 after one call: calls=%d exit=%d err=%v", n, exitCodeFor(err), err)
	}
}

// The help names the flag, still names the refusal a manager-arm worker gets without an instruction, and
// does not say an instruction admits a headless withdraw (the server refuses that; the CLI does not
// pre-empt it).
func TestPRWithdrawInstructionHelp3013(t *testing.T) {
	leaf := strings.Join(strings.Fields(cmdHelpOut(t, "team", "pr", "withdraw")), " ")
	for _, want := range []string{"--instruction", "<kind>:<ref>", "REVIEW_WITHDRAW_FORBIDDEN", "no instruction"} {
		if !strings.Contains(leaf, want) {
			t.Errorf("pr withdraw --help lacks %q:\n%s", want, leaf)
		}
	}
	for _, bad := range []string{"headless withdraw is admitted", "admits a headless"} {
		if strings.Contains(strings.ToLower(leaf), bad) {
			t.Errorf("pr withdraw --help must not suggest an instruction admits a headless withdraw (%q):\n%s", bad, leaf)
		}
	}
}
