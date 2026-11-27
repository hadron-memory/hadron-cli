package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Blind review of hadron-cli#3283 (Gloria, P2 2): the recovery a failed join
// names must work from where it leaves this machine, which holds nothing. A
// bare `join --replace` names no host, so it would not re-key the one whose
// key nobody holds; and when the server's answer is one this CLI cannot store,
// repeating the join gets the same answer, so that advice is circular.

// The answer was lost: the recovery names the host the join asked for.
func TestHostJoin3239LostAnswerRecoveryNamesTheHost(t *testing.T) {
	s := newSrv3239(t, nil)
	s.down = true
	r := exec3239(t, s, nil, "team", "host", "join", "--app", "acme.com:eng", "--host-id", "box1")
	if r.code != exitcode.Unavailable {
		t.Fatalf("exit %d (%v), want 7", r.code, r.err)
	}
	if !strings.Contains(r.err.Error(), "join --replace --host-id box1") {
		t.Errorf("the lost answer's recovery does not name host box1: %v", r.err)
	}
	r.assertNoKey(t)
}

// The answer was lost with only a label: the recovery repeats that label, the
// input the server derives the host id from, without echoing its value.
func TestHostJoin3239LostAnswerRecoveryRepeatsTheLabel(t *testing.T) {
	s := newSrv3239(t, nil)
	s.down = true
	r := exec3239(t, s, nil, "team", "host", "join", "--app", "acme.com:eng", "--label", "Secret Box")
	if r.code != exitcode.Unavailable {
		t.Fatalf("exit %d (%v), want 7", r.code, r.err)
	}
	if !strings.Contains(r.err.Error(), "the same --label") {
		t.Errorf("the lost answer's recovery does not say to repeat the label: %v", r.err)
	}
	if strings.Contains(r.err.Error(), "Secret Box") {
		t.Errorf("the recovery echoes the label: %v", r.err)
	}
}

// The server answered with a host id this CLI cannot store: no circular
// `join --replace` (only after a newer hadron), and nothing stored or printed.
func TestHostJoin3239UnstorableAnswerIsNotSentRoundAgain(t *testing.T) {
	s := newSrv3239(t, func(op string, _ map[string]any) string {
		if op == "JoinAgentTeamHost" {
			row := strings.Replace(hostRow3239, `"hostId":"box1"`, `"hostId":"box 1/x"`, 1)
			return `{"data":{"joinAgentTeamHost":{"rawKey":"` + key3239 + `","host":` + row + `}}}`
		}
		return ""
	})
	r := exec3239(t, s, nil, "team", "host", "join", "--app", "acme.com:eng")
	if r.code != exitcode.Error {
		t.Fatalf("exit %d (%v), want 1", r.code, r.err)
	}
	msg := r.err.Error()
	upgrade, rejoin := strings.Index(msg, "newer hadron"), strings.Index(msg, "join --replace")
	if upgrade < 0 {
		t.Errorf("an unstorable answer does not say this CLI is the one to change: %v", msg)
	}
	if rejoin >= 0 && (upgrade < 0 || rejoin < upgrade) {
		t.Errorf("an unstorable answer advises the join that gets it again before an upgrade: %v", msg)
	}
	if got := readStore3239(t); len(got) != 0 {
		t.Errorf("stored %+v", got)
	}
	r.assertNoKey(t)
}
