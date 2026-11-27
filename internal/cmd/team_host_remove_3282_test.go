package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#3282: `team host remove <host> [--force]`, the separate command Holger
// ruled for removing a host that is not this machine's (a lost machine), past open
// sessions. `leave` stays force-free. The server's force ends the registration, fails
// its operations, revokes its keys and removes its assignments, and does NOT end the
// open sessions: the listing and the confirmation say exactly that.

const desired3282 = `{"data":{"hostDesiredState":{"hostId":"box1","sweepMode":"ON","workers":[` +
	`{"workerId":"w1","name":"Iris","lifecycle":"ACTIVE","desired":"RUNNING","reason":"ASSIGNED","wouldBe":null,"wouldBeReason":null,"openSessionId":"s1"},` +
	`{"workerId":"w2","name":"Zed","lifecycle":"ACTIVE","desired":"RUNNING","reason":"ASSIGNED","wouldBe":null,"wouldBeReason":null,"openSessionId":null}]}}}`

const desiredNone3282 = `{"data":{"hostDesiredState":{"hostId":"box1","sweepMode":"ON","workers":[` +
	`{"workerId":"w2","name":"Zed","lifecycle":"ACTIVE","desired":"RUNNING","reason":"ASSIGNED","wouldBe":null,"wouldBeReason":null,"openSessionId":null}]}}}`

func srv3282(t *testing.T, desired string, removeReply string) *srv3239 {
	return newSrv3239(t, func(op string, _ map[string]any) string {
		switch op {
		case "HostDesiredState":
			return desired
		case "RemoveAgentTeamHost":
			return removeReply
		}
		return ""
	})
}

func holdBox1(t *testing.T, s *srv3239) func(string) {
	return func(dir string) {
		writeStore3239(t, dir, 0o600, [4]string{s.url, app3239, "box1", key3239}, [4]string{s.url, "capp-other", "box9", otherKey3239})
	}
}

func orderOf3282(s *srv3239) string { return strings.Join(s.ops(), ",") }

// Without --force the argument is omitted (never false), so a host with no open
// session is removed exactly as `leave` would, and the stored key of THIS host goes
// only after the server confirms.
func TestHostRemove3282WithoutForceSendsNoForce(t *testing.T) {
	for _, extra := range [][]string{nil, {"--force=false"}} {
		s := srv3282(t, desiredNone3282, "")
		r := exec3239(t, s, holdBox1(t, s), append([]string{"team", "host", "remove", "box1", "--app", "acme.com:eng", "--yes", "--json"}, extra...)...)
		if r.err != nil {
			t.Fatalf("remove %v: %v\n%s", extra, r.err, r.errOut)
		}
		if _, sent := s.last("RemoveAgentTeamHost").vars["force"]; sent {
			t.Errorf("%v: force must be omitted, not false: %v", extra, s.last("RemoveAgentTeamHost").vars)
		}
		if got := readStore3239(t); len(got) != 1 || got[0].HostID != "box9" {
			t.Errorf("%v: stored %+v, want only the other host's entry", extra, got)
		}
		s.assertPersonOnly(t)
		r.assertNoKey(t)
	}
}

// With --force: force:true is sent exactly once, after the listing, and the open
// sessions are reported; nothing about them is ended by the CLI.
func TestHostRemove3282ForceIsSentOnceAfterTheListing(t *testing.T) {
	s := srv3282(t, desired3282, "")
	r := exec3239(t, s, holdBox1(t, s), "team", "host", "remove", "box1", "--force", "--app", "acme.com:eng", "--yes", "--json")
	if r.err != nil {
		t.Fatalf("remove --force: %v\n%s", r.err, r.errOut)
	}
	if s.count("RemoveAgentTeamHost") != 1 {
		t.Fatalf("the mutation must be sent once: %v", s.ops())
	}
	if got := s.last("RemoveAgentTeamHost").vars["force"]; got != true {
		t.Errorf("force = %v, want true", got)
	}
	ops := orderOf3282(s)
	if !strings.Contains(ops, "HostDesiredState") || strings.Index(ops, "HostDesiredState") > strings.Index(ops, "RemoveAgentTeamHost") {
		t.Errorf("the open-session listing must come before the mutation: %s", ops)
	}
	dto := jsonObject3072(t, r.out)
	if dto["forced"] != true || dto["removedOnServer"] != true || dto["hostId"] != "box1" {
		t.Errorf("DTO: %v", dto)
	}
	if sessions, _ := dto["openSessions"].([]any); len(sessions) != 1 || sessions[0] != "Iris" {
		t.Errorf("openSessions must name exactly the workers with an open session: %v", dto["openSessions"])
	}
	s.assertPersonOnly(t)
	r.assertNoKey(t)
}

// (a) and (b): the listing and the four effects are printed before anything is sent,
// and say that the sessions stay open.
func TestHostRemove3282StatesTheFourEffectsAndThatSessionsStayOpen(t *testing.T) {
	s := srv3282(t, desired3282, "")
	r := exec3239(t, s, holdBox1(t, s), "team", "host", "remove", "box1", "--force", "--app", "acme.com:eng", "--yes")
	if r.err != nil {
		t.Fatalf("%v\n%s", r.err, r.errOut)
	}
	text := strings.ToLower(strings.Join(strings.Fields(r.errOut), " "))
	for _, want := range []string{"iris", "registration", "operations", "keys", "assignments", "stay open"} {
		if !strings.Contains(text, want) {
			t.Errorf("the listing must mention %q before the removal:\n%s", want, r.errOut)
		}
	}
	if strings.Contains(text, "zed") {
		t.Errorf("a worker with no open session must not be listed as one:\n%s", r.errOut)
	}
}

// (c): declining the confirmation sends nothing and keeps the stored key; the
// prompt itself carries the effects.
func TestHostRemove3282DecliningSendsNothing(t *testing.T) {
	s := srv3282(t, desired3282, "")
	teamGitDir(t)
	f, _, errOut := testFactoryTTY(t, "n\n")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	holdBox1(t, s)(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "hadron"))
	before, _ := os.ReadFile(storePath3239())
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", s.url, "team", "host", "remove", "box1", "--force", "--app", "acme.com:eng"})
	err := root.Execute()
	if got := exitCodeFor(err); got != exitcode.Cancelled {
		t.Errorf("declining must exit %d, got %d (%v)", exitcode.Cancelled, got, err)
	}
	if s.count("RemoveAgentTeamHost") != 0 {
		t.Errorf("nothing may be sent after a decline: %v", s.ops())
	}
	if after, _ := os.ReadFile(storePath3239()); !bytes.Equal(before, after) {
		t.Error("the stored key changed after a decline")
	}
	if p := strings.ToLower(errOut.String()); !strings.Contains(p, "stay open") || !strings.Contains(p, "keys") {
		t.Errorf("the prompt must carry the effects:\n%s", errOut.String())
	}
}

// Without --yes and without a terminal the command refuses (exit 2) and sends no
// mutation.
func TestHostRemove3282NeedsYesOffATerminal(t *testing.T) {
	s := srv3282(t, desired3282, "")
	r := exec3239(t, s, holdBox1(t, s), "team", "host", "remove", "box1", "--force", "--app", "acme.com:eng")
	if r.code != exitcode.Usage {
		t.Errorf("exit %d (%v), want 2", r.code, r.err)
	}
	if s.count("RemoveAgentTeamHost") != 0 {
		t.Errorf("no mutation off a terminal without --yes: %v", s.ops())
	}
}

// Without --force a host with an open session is refused like `leave` does: exit 5,
// the workers named, no force sent, the stored key kept, and the way forward named.
func TestHostRemove3282WithoutForceRefusesOnOpenSessions(t *testing.T) {
	s := srv3282(t, desired3282, refusal3239("AGENT_TEAM_HOST_HAS_OPEN_SESSIONS"))
	var before []byte
	r := exec3239(t, s, func(dir string) {
		holdBox1(t, s)(dir)
		before, _ = os.ReadFile(filepath.Join(dir, "host-credentials.json"))
	}, "team", "host", "remove", "box1", "--app", "acme.com:eng", "--yes")
	if r.code != exitcode.Conflict {
		t.Fatalf("exit %d (%v), want 5", r.code, r.err)
	}
	if !strings.Contains(r.err.Error(), "Iris") || strings.Contains(r.err.Error(), "Zed") || !strings.Contains(r.err.Error(), "--force") {
		t.Errorf("the refusal must name the open session's worker and the --force way forward: %v", r.err)
	}
	if strings.Contains(r.err.Error(), "box1") {
		t.Errorf("a refusal must not echo the host given (#2898): %v", r.err)
	}
	if _, sent := s.last("RemoveAgentTeamHost").vars["force"]; sent {
		t.Error("force must not be sent without the flag")
	}
	if after, _ := os.ReadFile(storePath3239()); !bytes.Equal(before, after) {
		t.Error("the stored key changed after a refusal")
	}
	r.assertNoKey(t)
}

// --force never removes a host without first listing: if the listing cannot be read,
// nothing is removed.
func TestHostRemove3282ForceNeedsTheListing(t *testing.T) {
	s := srv3282(t, refusal3239("INTERNAL_SERVER_ERROR"), "")
	r := exec3239(t, s, holdBox1(t, s), "team", "host", "remove", "box1", "--force", "--app", "acme.com:eng", "--yes")
	if r.err == nil {
		t.Fatal("must fail when the open sessions cannot be listed")
	}
	if s.count("RemoveAgentTeamHost") != 0 {
		t.Errorf("no force without the listing: %v", s.ops())
	}
}

// A host the server does not have: exit 4, and the stored entry is kept (this command
// deletes a stored key only after the server confirmed a removal).
func TestHostRemove3282HostNotFoundKeepsTheEntry(t *testing.T) {
	s := srv3282(t, desiredNone3282, refusal3239("HOST_NOT_FOUND"))
	var before []byte
	r := exec3239(t, s, func(dir string) {
		holdBox1(t, s)(dir)
		before, _ = os.ReadFile(filepath.Join(dir, "host-credentials.json"))
	}, "team", "host", "remove", "box1", "--force", "--app", "acme.com:eng", "--yes")
	if r.code != exitcode.NotFound {
		t.Errorf("exit %d (%v), want 4", r.code, r.err)
	}
	if after, _ := os.ReadFile(storePath3239()); !bytes.Equal(before, after) {
		t.Error("the stored key changed without a server confirmation")
	}
}

// (d): `leave` is frozen: its open-session refusal text and its no-force request.
func TestHostLeave3282StaysForceFree(t *testing.T) {
	s := srv3282(t, desired3282, refusal3239("AGENT_TEAM_HOST_HAS_OPEN_SESSIONS"))
	r := exec3239(t, s, holdBox1(t, s), "team", "host", "leave", "--app", "acme.com:eng", "--yes")
	if r.code != exitcode.Conflict || !strings.Contains(r.err.Error(), "(there is no force)") || !strings.Contains(r.err.Error(), "Iris") {
		t.Errorf("leave's refusal changed: exit %d, %v", r.code, r.err)
	}
	if _, sent := s.last("RemoveAgentTeamHost").vars["force"]; sent {
		t.Error("leave must never send force")
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(f.IOStreams.Out)
	root.SetArgs([]string{"team", "host", "leave", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "--force") && !strings.Contains(strings.Join(strings.Fields(out.String()), " "), "there is no force") {
		t.Errorf("leave has no --force flag: %s", out.String())
	}
}

// (e): the help says what the command is for, what force does and that the sessions
// stay open.
func TestHostRemove3282HelpStatesItsPurposeAndEffects(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(f.IOStreams.Out)
	root.SetArgs([]string{"team", "host", "remove", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	h := strings.ToLower(strings.Join(strings.Fields(out.String()), " "))
	for _, want := range []string{"not this machine's", "leave", "registration", "operations", "keys", "assignments", "stay open", "--force", "--yes"} {
		if !strings.Contains(h, want) {
			t.Errorf("help missing %q:\n%s", want, out.String())
		}
	}
}

// Error arms: not permitted (8) and a lost answer (7) keep the stored key and the
// right exit; a confirmed forced removal of this machine's own host reports
// removedLocally; a server that predates team App hosts is named.
func TestHostRemove3282ErrorArms(t *testing.T) {
	for name, c := range map[string]struct {
		reply string
		want  int
		down  bool
		text  string
	}{
		"forbidden":     {refusal3239("FORBIDDEN"), exitcode.Forbidden, false, ""},
		"unreachable":   {"", exitcode.Unavailable, true, "could not list"},
		"not confirmed": {`{"data":{"removeAgentTeamHost":false}}`, exitcode.Error, false, "did not confirm"},
		"older server":  {`{"errors":[{"message":"Cannot query field \"removeAgentTeamHost\" on type \"Mutation\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, exitcode.Error, false, "0.47.0"},
	} {
		t.Run(name, func(t *testing.T) {
			s := srv3282(t, desiredNone3282, c.reply)
			s.down = c.down
			var before []byte
			r := exec3239(t, s, func(dir string) {
				holdBox1(t, s)(dir)
				before, _ = os.ReadFile(filepath.Join(dir, "host-credentials.json"))
			}, "team", "host", "remove", "box1", "--force", "--app", "acme.com:eng", "--yes")
			if r.code != c.want {
				t.Fatalf("exit %d (%v), want %d", r.code, r.err, c.want)
			}
			if c.text != "" && !strings.Contains(r.err.Error(), c.text) {
				t.Errorf("message %q lacks %q", r.err.Error(), c.text)
			}
			if after, _ := os.ReadFile(storePath3239()); !bytes.Equal(before, after) {
				t.Error("the stored key changed without a confirmed removal")
			}
			r.assertNoKey(t)
		})
	}
}

// A forced removal of this machine's own host reports removedLocally, and a non-forced
// run whose listing cannot be read says so on stderr instead of presenting [] as known.
func TestHostRemove3282ReportsLocalDeletionAndAnUnreadableListing(t *testing.T) {
	s := srv3282(t, refusal3239("INTERNAL_SERVER_ERROR"), "")
	r := exec3239(t, s, holdBox1(t, s), "team", "host", "remove", "box1", "--app", "acme.com:eng", "--yes", "--json")
	if r.err != nil {
		t.Fatalf("%v\n%s", r.err, r.errOut)
	}
	dto := jsonObject3072(t, r.out)
	if dto["removedLocally"] != true || dto["removedOnServer"] != true {
		t.Errorf("DTO: %v", dto)
	}
	if !strings.Contains(r.errOut, "could not list") {
		t.Errorf("an unreadable listing must be said on stderr:\n%s", r.errOut)
	}
}

// The stored key goes only when THIS machine holds the removed host's key: removing
// another host of the same App, with the server confirming, leaves this machine's key
// (and removedLocally false). This is the pin for the host guard.
func TestHostRemove3282KeepsTheStoredKeyOfADifferentHost(t *testing.T) {
	s := srv3282(t, desiredNone3282, "")
	var before []byte
	r := exec3239(t, s, func(dir string) {
		holdBox1(t, s)(dir)
		before, _ = os.ReadFile(filepath.Join(dir, "host-credentials.json"))
	}, "team", "host", "remove", "box2", "--force", "--app", "acme.com:eng", "--yes", "--json")
	if r.err != nil {
		t.Fatalf("%v\n%s", r.err, r.errOut)
	}
	if after, _ := os.ReadFile(storePath3239()); !bytes.Equal(before, after) {
		t.Error("removing another host must not delete this machine's stored key")
	}
	dto := jsonObject3072(t, r.out)
	if dto["removedOnServer"] != true || dto["removedLocally"] != false || dto["hostId"] != "box2" {
		t.Errorf("DTO: %v", dto)
	}
}

// Without --force and with an unreadable listing the refusal still says what it can:
// no worker names (none were read), the way forward, no echo of the host.
func TestHostRemove3282RefusalWithoutAListingNamesNoWorkers(t *testing.T) {
	s := newSrv3239(t, func(op string, _ map[string]any) string {
		switch op {
		case "HostDesiredState":
			return refusal3239("INTERNAL_SERVER_ERROR")
		case "RemoveAgentTeamHost":
			return refusal3239("AGENT_TEAM_HOST_HAS_OPEN_SESSIONS")
		}
		return ""
	})
	r := exec3239(t, s, holdBox1(t, s), "team", "host", "remove", "box1", "--app", "acme.com:eng", "--yes")
	if r.code != exitcode.Conflict {
		t.Fatalf("exit %d (%v), want 5", r.code, r.err)
	}
	if !strings.Contains(r.err.Error(), "--force") || strings.Contains(r.err.Error(), "Iris") || strings.Contains(r.err.Error(), "box1") {
		t.Errorf("refusal: %v", r.err)
	}
}
