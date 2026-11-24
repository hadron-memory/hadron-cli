package team

import (
	"errors"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#3069 (found by QA on #3065, the sibling of #3004): `team chat inbox` reads the server URL FIRST, from
// config when --server is not given. A config that cannot be read used to reach inboxFailure(err, "cannot read
// inbox binding"): the binding file was fine, and the cause (a config fault) was dropped. The server-resolution
// step now names its stage and a fixed cause class, like the App-context stage, with the exit class the cause has
// today and none of its text.

const serverStageWant3069 = "could not read the server URL: a local configuration problem (`hadron config get server` shows what is set)"

func TestInboxServerResolutionFaultNamesItsStage3069(t *testing.T) {
	const own = "parse /secret/path-3069/config.toml: " + inboxIdentityLeak3004
	for _, tc := range []struct {
		name string
		err  error
		exit int
	}{
		{"plain config fault", errors.New(own), exitcode.Error},
		// The cause's own exit class is kept whatever it is, and its text is still never printed.
		{"usage-coded config fault", exitcode.New(exitcode.Usage, errors.New(own)), exitcode.Usage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := driveInboxIdentity3004(t, inboxIdentityOpts3004{noServerFlag: true, configErr: tc.err})
			if run.err == nil {
				t.Fatal("an unreadable config must fail")
			}
			if got := exitcode.FromError(run.err); got != tc.exit {
				t.Fatalf("exit %d, want the cause's class %d: %v", got, tc.exit, run.err)
			}
			msg := run.err.Error()
			// Exact: the stage, the fixed class, the pointer; no cause text, no path, not the binding.
			if msg != serverStageWant3069 {
				t.Fatalf("the message must be exactly %q, got %q", serverStageWant3069, msg)
			}
			if strings.Contains(msg, "/secret/path-3069") || strings.Contains(msg, "cannot read inbox binding") {
				t.Fatalf("the message must not echo the cause or blame the binding: %q", msg)
			}
			assertNoLeak3004(t, msg)
			if run.requests != 0 {
				t.Fatalf("nothing may be sent, got %d requests", run.requests)
			}
		})
	}
}

// Both reads say it: the ack recheck re-reads the server URL and says so (unit-driven, as the real factory cannot
// fail differently on the second read).
func TestInboxServerResolutionFaultSaysReReadOnTheRecheck3069(t *testing.T) {
	err := inboxIdentityFailure(&inboxIdentityStageError{stage: inboxStageServer, err: errors.New("read /secret/path-3069: " + inboxIdentityLeak3004)}, "cannot read inbox binding", true)
	want := "could not re-read the server URL: a local configuration problem (`hadron config get server` shows what is set)"
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
	if got := exitcode.FromError(err); got != exitcode.Error {
		t.Fatalf("exit %d, want 1", got)
	}
	if strings.Contains(err.Error(), "/secret/path-3069") || strings.Contains(err.Error(), inboxIdentityLeak3004) {
		t.Fatalf("the cause's text must never be printed: %q", err.Error())
	}
}

// With --server given, config is not read at the server step, so the same fault reaches the App stage and keeps its
// own message: the server-stage change must not touch that path.
func TestInboxConfigFaultWithServerFlagStillReachesTheAppStage3069(t *testing.T) {
	run := driveInboxIdentity3004(t, inboxIdentityOpts3004{configErr: errors.New("read /secret/path-3069: " + inboxIdentityLeak3004)})
	if run.err == nil {
		t.Fatal("an unreadable config must fail")
	}
	const want = "could not read the App context: a local configuration problem (`hadron config get app` shows what is set)"
	if run.err.Error() != want {
		t.Fatalf("got %q, want the App-stage message %q", run.err.Error(), want)
	}
}
