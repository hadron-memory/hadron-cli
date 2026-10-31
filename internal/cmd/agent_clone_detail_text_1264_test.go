package cmd

import (
	"strings"
	"testing"
)

// cli#1264 item 3 (Uma's wording, issue comment 6544300870): the human
// rendering of a clone refusal's details. The answer stays on the first line
// behind the documented label; each blocker gets its own line. Text mode only:
// --json is pinned unchanged by TestAgentClone994RefusalDetailsAndNoRetry.

func cloneStderr1264(t *testing.T, ext string) (string, int) {
	t.Helper()
	_, stderr, code, calls := runClone994(t, `{"errors":[{"message":"Server refusal","extensions":`+ext+`}]}`, "source", "--to", "org")
	if len(calls) != 1 {
		t.Fatalf("calls %v", calls)
	}
	return stderr, code
}

func TestAgentClone1264BlockersRenderOnePerLine(t *testing.T) {
	// The #1257 blocker fixture: a named subject, a hidden one, a second blocker.
	stderr, code := cloneStderr1264(t, `{"code":"CLONE_IMPORT_UNUSABLE","blockers":[{"code":"CLONE_IMPORT_UNUSABLE","count":2,"subjects":[{"position":0,"name":"Visible import"},{"position":2}]},{"code":"CLONE_BASE_UNUSABLE","count":1,"subjects":[{"position":0}]}]}`)
	want := "hadron: Server refusal (clone refusal: CLONE_IMPORT_UNUSABLE)\n" +
		"  blocker CLONE_IMPORT_UNUSABLE  count 2  \"Visible import\" (position 0), position 2\n" +
		"  blocker CLONE_BASE_UNUSABLE  count 1  position 0\n"
	if code != 8 || stderr != want {
		t.Fatalf("exit %d\ngot:\n%s\nwant:\n%s", code, stderr, want)
	}
}

func TestAgentClone1264BlockerWithoutSubjects(t *testing.T) {
	for _, tc := range []struct{ ext, line string }{
		{`{"code":"CLONE_BASE_UNUSABLE","blockers":[{"code":"CLONE_BASE_UNUSABLE","count":1}]}`, "  blocker CLONE_BASE_UNUSABLE  count 1\n"},
		{`{"code":"CLONE_BASE_UNUSABLE","blockers":[{"code":"CLONE_BASE_UNUSABLE","count":1,"subjects":[]}]}`, "  blocker CLONE_BASE_UNUSABLE  count 1\n"},
		// No count received: none is printed, none invented.
		{`{"code":"CLONE_BASE_UNUSABLE","blockers":[{"code":"CLONE_BASE_UNUSABLE","subjects":[{"position":3}]}]}`, "  blocker CLONE_BASE_UNUSABLE  position 3\n"},
	} {
		stderr, _ := cloneStderr1264(t, tc.ext)
		want := "hadron: Server refusal (clone refusal: CLONE_BASE_UNUSABLE)\n" + tc.line
		if stderr != want {
			t.Fatalf("%s\ngot:\n%s\nwant:\n%s", tc.ext, stderr, want)
		}
	}
}

func TestAgentClone1264ReasonAndRetryableOnOneLine(t *testing.T) {
	for _, tc := range []struct{ ext, want string }{
		{`{"code":"CONFLICT","reason":"AGENT_CLONE_SOURCE_CHANGED","retryable":true}`, "(clone refusal: CONFLICT, reason AGENT_CLONE_SOURCE_CHANGED, retryable: rerun by hand)\n"},
		// retryable=false is omitted rather than printed as a negative.
		{`{"code":"CONFLICT","reason":"AGENT_CLONE_SOURCE_CHANGED","retryable":false}`, "(clone refusal: CONFLICT, reason AGENT_CLONE_SOURCE_CHANGED)\n"},
		{`{"code":"AGENT_NOT_PUBLIC"}`, "(clone refusal: AGENT_NOT_PUBLIC)\n"},
	} {
		stderr, _ := cloneStderr1264(t, tc.ext)
		if stderr != "hadron: Server refusal "+tc.want {
			t.Fatalf("%s\ngot:  %q\nwant: %q", tc.ext, stderr, "hadron: Server refusal "+tc.want)
		}
	}
}

func TestAgentClone1264AuthAndServerFaultLabels(t *testing.T) {
	for _, tc := range []struct{ ext, suffix string }{
		{`{"code":"UNAUTHENTICATED"}`, "(clone authentication: UNAUTHENTICATED)\n"},
		{`{"code":"INTERNAL_SERVER_ERROR","requestId":"req-1"}`, "(clone server error: INTERNAL_SERVER_ERROR)\n"},
	} {
		stderr, _ := cloneStderr1264(t, tc.ext)
		if !strings.HasSuffix(stderr, tc.suffix) || strings.Count(stderr, "\n") != 1 {
			t.Fatalf("%s\ngot: %q\nwant suffix: %q", tc.ext, stderr, tc.suffix)
		}
	}
}

// Server-supplied strings go through output.SafeCell: a hostile subject name,
// blocker code, code or reason can neither forge a line nor drive the
// terminal, and the name stays inside its quotes.
func TestAgentClone1264ServerStringsAreSanitized(t *testing.T) {
	stderr, _ := cloneStderr1264(t, `{"code":"CODE\u001b[31m\nFORGED","reason":"R\r\nFORGED","blockers":[{"code":"B\u202eX\nFORGED","count":1,"subjects":[{"position":0,"name":"N\u001b]0;t\u0007\nFORGED"}]}]}`)
	want := "hadron: Server refusal (clone refusal: CODE [31m FORGED, reason R  FORGED)\n" +
		"  blocker B X FORGED  count 1  \"N ]0;t  FORGED\" (position 0)\n"
	if stderr != want {
		t.Fatalf("got:\n%q\nwant:\n%q", stderr, want)
	}
}

// A first error with blockers but no code, reason or retryable still says
// what kind of answer it was on the first line; the blockers follow.
func TestAgentClone1264BlockersWithoutCode(t *testing.T) {
	stderr, _ := cloneStderr1264(t, `{"blockers":[{"code":"CLONE_BASE_UNUSABLE","count":1}]}`)
	want := "hadron: Server refusal (clone refusal: blockers follow)\n  blocker CLONE_BASE_UNUSABLE  count 1\n"
	if stderr != want {
		t.Fatalf("got:\n%q\nwant:\n%q", stderr, want)
	}
}
