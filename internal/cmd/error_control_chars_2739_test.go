package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2739 (Rodolfo's §10 N1 on #2735): the ONE error renderer prints server and
// provider text. A terminal control or escape sequence in it must not reach stderr
// (or the --json envelope's message), while the server's code, a request id and an
// ordinary message stay exactly as they were.

const hostile2739 = "boom \x1b[31mred\x1b[0m \x1b]0;pwned\x07 bell\x07 c1\u009b31m bidi‮evil nul\x00 end"

// errTextHasControl2739 reports a character a terminal could act on or that could forge text.
func errTextHasControl2739(s string) (rune, bool) {
	for _, r := range s {
		if r == '\n' {
			continue // the renderer's own line structure
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == 0x202e || r == 0x2028 || r == 0x2029 {
			return r, true
		}
	}
	return 0, false
}

func renderPlain2739(t *testing.T, err error) string {
	t.Helper()
	f, _ := testFactory(t)
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	renderError(f, err)
	return errOut.String()
}

func renderEnvelope2739(t *testing.T, err error, wroteStdout bool) (stdout, stderr string) {
	t.Helper()
	f, out := testFactory(t)
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	f.JSON = true
	if wroteStdout {
		_, _ = f.IOStreams.Out.Write([]byte("{}\n"))
	}
	renderError(f, err)
	return out.String(), errOut.String()
}

// The human line: a hostile server message is neutralised, the visible words survive.
func TestRenderErrorNeutralisesServerTextOnStderr2739(t *testing.T) {
	got := renderPlain2739(t, exitcode.Newf(exitcode.Error, "%s", hostile2739))
	if r, bad := errTextHasControl2739(got); bad {
		t.Fatalf("stderr carries %U: %q", r, got)
	}
	for _, want := range []string{"boom", "red", "bell", "end"} {
		if !strings.Contains(got, want) {
			t.Errorf("the visible words must survive neutralisation (%q missing): %q", want, got)
		}
	}
	if !strings.HasPrefix(got, "hadron: ") || !strings.HasSuffix(got, "\n") || strings.Count(got, "\n") != 1 {
		t.Errorf("one 'hadron: ' line, ended by the renderer's own newline: %q", got)
	}
}

// Escape sequences go whole: the parameters of ESC [ 31 m are not left as visible junk.
func TestRenderErrorRemovesWholeEscapeSequences2739(t *testing.T) {
	got := renderPlain2739(t, exitcode.Newf(exitcode.Error, "a\x1b[31mb\x1b[0mc \x1b]0;title\x07d"))
	if strings.Contains(got, "[31m") || strings.Contains(got, "[0m") || strings.Contains(got, "0;title") {
		t.Errorf("escape sequences must be removed whole, not just their ESC byte: %q", got)
	}
	if !strings.Contains(got, "abc d") {
		t.Errorf("the text around the sequences is kept: %q", got)
	}
}

// A server message may not forge a second diagnostic line.
func TestRenderErrorServerNewlineDoesNotForgeALine2739(t *testing.T) {
	got := renderPlain2739(t, exitcode.Newf(exitcode.Error, "refused\nhadron: ok, signed in as admin"))
	if strings.Count(got, "\n") != 1 {
		t.Errorf("a newline in the message must not start a new line: %q", got)
	}
}

// The --json envelope: the message is neutralised the same way; the exit code, the server's
// code and the request id are untouched.
func TestRenderErrorEnvelopeMessageIsNeutralised2739(t *testing.T) {
	for _, wrote := range []bool{false, true} {
		out, errOut := renderEnvelope2739(t, exitcode.Newf(exitcode.Error, "%s", hostile2739), wrote)
		doc := out
		if wrote {
			doc = errOut
		}
		var env struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if i := strings.Index(doc, `{"error"`); i >= 0 {
			doc = doc[i:]
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(doc)), &env); err != nil {
			t.Fatalf("wrote=%v: envelope: %v\n%s", wrote, err, doc)
		}
		if r, bad := errTextHasControl2739(env.Error.Message); bad {
			t.Errorf("wrote=%v: the envelope message carries %U: %q", wrote, r, env.Error.Message)
		}
		if env.Error.Code != exitcode.Error || !strings.Contains(env.Error.Message, "boom") {
			t.Errorf("wrote=%v: code and words must survive: %+v", wrote, env.Error)
		}
	}
}

// What must NOT change: an ordinary message (ASCII, accents, CJK, emoji) is byte-identical, and so
// are a server code and a request id carried beside the message.
func TestRenderErrorOrdinaryTextIsUnchanged2739(t *testing.T) {
	for _, msg := range []string{
		"memory not found: acme.com:kb",
		"héllo wörld: 日本語 ✓ 🚀",
		"refused: GRANT_NOT_MANAGER (request id req_01a1f0c2d3e4)",
		"a tab\tand spaces   are kept?",
	} {
		got := renderPlain2739(t, errors.New(msg))
		// A tab is a control character: it is neutralised to a space like the rest.
		want := "hadron: " + strings.ReplaceAll(msg, "\t", " ") + "\n"
		if got != want {
			t.Errorf("an ordinary message must pass unchanged:\n got %q\nwant %q", got, want)
		}
	}
}
