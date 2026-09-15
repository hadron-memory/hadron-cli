package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// #334 — `--json` must mean "stdout is always valid JSON", success or failure.
//
// Before this, a single-entity failure left stdout EMPTY and put the envelope
// on stderr, so `json.loads(stdout)` died with "Expecting value: line 1 column
// 1" — which reads like corrupt data or a parser bug rather than a server
// error. The diagnostic existed, structured, on a stream the caller was not
// reading.

func decodeEnvelope(t *testing.T, s string) (int, string) {
	t.Helper()
	var env struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(s), &env); err != nil {
		t.Fatalf("stdout must be valid JSON under --json: %v\ngot: %q", err, s)
	}
	return env.Error.Code, env.Error.Message
}

// The headline case from the issue: a single-ref read of something that is not
// there. stdout must parse, and must carry the exit code and the reason.
func TestJSONFailureEnvelopeGoesToStdout(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":null}}`,
	})
	f, out := testFactory(t)
	errOut := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "hrn:node:acme.com:kb:nope", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a missing node must fail")
	}
	renderError(f, err)

	code, msg := decodeEnvelope(t, out.String())
	if code != exitcode.NotFound {
		t.Errorf("the envelope carries the exit code: got %d, want %d", code, exitcode.NotFound)
	}
	if msg == "" {
		t.Error("the envelope must carry a message")
	}
	// stderr is no longer where it lives — and a caller told to parse stdout
	// must not ALSO have to drain stderr to avoid a deadlock on a pipe.
	if got := errOut(); strings.Contains(got, `"error"`) {
		t.Errorf("the envelope must not be duplicated onto stderr: %q", got)
	}
}

// The exit code is the primary signal and this change must not disturb it.
// Asserted separately from the envelope, because a fix that routed the document
// correctly while perturbing the code would break every existing script.
func TestJSONFailureKeepsTheExitCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"malformed ref is usage", []string{"node", "get", "not a urn!!", "--json"}, exitcode.Usage},
		{"unknown flag is usage", []string{"node", "get", "--nosuchflag", "--json"}, exitcode.Usage},
	} {
		// Unknown SUBCOMMANDS are deliberately absent: that path is caught by
		// checkUnknownSubcommand inside Execute(), before cobra dispatches, so
		// root.Execute() alone returns nil for it and a case here would assert
		// against a path this harness cannot reach.
		t.Run(tc.name, func(t *testing.T) {
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(tc.args)
			err := root.Execute()
			if err == nil {
				t.Fatal("expected a failure")
			}
			if got := exitCodeFor(err); got != tc.want {
				t.Errorf("exit code = %d, want %d", got, tc.want)
			}
		})
	}
}

// Part 3 of the issue: a FLAG-parse failure never binds --json, so the envelope
// used to degrade to plain text. `--json` with a hole in it is worse than no
// `--json`, because a script that has switched to parsing JSON meets raw prose
// on the first typo.
func TestFlagErrorStillRendersJSON(t *testing.T) {
	f, out := testFactory(t)
	// Exactly the production shape: f.JSON is FALSE here, because cobra aborted
	// the parse at the bad flag and never bound it. A test that pre-set it
	// would pass without the fix.
	f.JSON = false
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "--nosuchflag", "--json"})
	err := root.Execute()
	if err == nil {
		t.Fatal("an unknown flag must fail")
	}
	if f.JSON {
		t.Fatal("precondition: cobra must NOT have bound --json on a flag-parse failure")
	}
	// Through renderFailure, which is the wiring the binary runs — NOT by
	// calling jsonRequested here and handing the answer to renderError, which
	// is what this test did at first and which passed with the binding removed.
	renderFailure(f, []string{"node", "get", "--nosuchflag", "--json"}, err)

	code, msg := decodeEnvelope(t, out.String())
	if code != exitcode.Usage {
		t.Errorf("a flag error is exit 2, got %d", code)
	}
	if !strings.Contains(msg, "nosuchflag") {
		t.Errorf("the envelope must name the offending flag: %q", msg)
	}
}

// The probe PARSES rather than greps, so `--json` appearing as another flag's
// VALUE does not silently switch a caller into JSON output.
func TestJSONProbeDoesNotMatchAFlagValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"real flag", []string{"node", "get", "x", "--json"}, true},
		{"explicit true", []string{"node", "get", "x", "--json=true"}, true},
		{"explicit false", []string{"node", "get", "x", "--json=false"}, false},
		{"absent", []string{"node", "get", "x"}, false},
		// The reason a substring scan is wrong: here --json is a VALUE.
		{"as another flag's value", []string{"node", "get", "-m", "--json"}, false},
		{"after --", []string{"node", "get", "--", "--json"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := jsonRequested(tc.args); got != tc.want {
				t.Errorf("jsonRequested(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// The deliberate exception: a command that has ALREADY written to stdout keeps
// its error on stderr, because appending an envelope would concatenate two JSON
// values and produce the unparseable stdout this whole change removes.
//
// Driven through the real tracking rather than a flag, so it measures what the
// binary does (review:a-test-double-must-satisfy-the-real-access-pattern).
func TestErrorStaysOnStderrOnceStdoutHasAPayload(t *testing.T) {
	f, out := testFactory(t)
	errOut := captureErrOut(f)
	f.JSON = true

	// A command wrote the document already — the `asset get -o -` /
	// `node export` shape, where the bytes ARE the output.
	if _, err := f.IOStreams.Out.Write([]byte(`{"already":"written"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	if !f.IOStreams.Wrote() {
		t.Fatal("precondition: the stream must report that it was written to")
	}

	renderError(f, exitcode.Newf(exitcode.Error, "the stream broke"))

	if strings.Count(out.String(), "{") != 1 {
		t.Errorf("stdout must stay ONE document, got: %q", out.String())
	}
	code, msg := decodeEnvelope(t, errOut())
	if code != exitcode.Error || !strings.Contains(msg, "the stream broke") {
		t.Errorf("the envelope must reach stderr instead: code=%d msg=%q", code, msg)
	}
}

// An untracked stream reports "clean", so the envelope goes to stdout — the new
// behaviour, not the old one. Pinned because the fallback direction is the kind
// of thing a refactor flips without noticing.
func TestUntrackedStreamDefaultsToStdout(t *testing.T) {
	f, _ := testFactory(t)
	raw := &strings.Builder{}
	f.IOStreams.Out = raw // deliberately NOT output.Tracked
	f.JSON = true
	if f.IOStreams.Wrote() {
		t.Fatal("an untracked stream must report clean")
	}
	renderError(f, exitcode.Newf(exitcode.NotFound, "nope"))
	if code, _ := decodeEnvelope(t, raw.String()); code != exitcode.NotFound {
		t.Errorf("an untracked stream still gets the envelope on stdout")
	}
}

// Plain text is untouched: without --json the message still goes to stderr, so
// a human piping stdout to a file still sees the error on their terminal.
func TestPlainTextErrorStillGoesToStderr(t *testing.T) {
	f, out := testFactory(t)
	errOut := captureErrOut(f)
	f.JSON = false
	renderError(f, exitcode.Newf(exitcode.NotFound, "nope"))
	if out.String() != "" {
		t.Errorf("plain-text mode must leave stdout alone, got %q", out.String())
	}
	if !strings.Contains(errOut(), "hadron: nope") {
		t.Errorf("plain text belongs on stderr, got %q", errOut())
	}
}

var _ = output.Tracked

// PR #585 review, @codex P2. A failed stdout write leaves Wrote() false —
// correctly, since nothing landed — so the envelope is aimed at that same
// broken stream. Discarding the second error would make the failure TOTALLY
// silent: an exit code and not one word on either stream. The old routing would
// have survived this, so the fallback is what stops the improvement being a
// regression exactly when the caller most needs telling.
func TestJSONEnvelopeFallsBackToStderrWhenStdoutIsBroken(t *testing.T) {
	f, _ := testFactory(t)
	errOut := captureErrOut(f)
	// The package's existing failingWriter — a stdout that rejects every write:
	// a full filesystem behind a redirect, a closed pipe, a broken writer from
	// an embedded caller.
	f.IOStreams.Out = output.Tracked(failingWriter{})
	f.JSON = true

	renderError(f, exitcode.Newf(exitcode.NotFound, "nope"))

	if f.IOStreams.Wrote() {
		t.Fatal("precondition: a failed write must not mark the stream as written")
	}
	code, msg := decodeEnvelope(t, errOut())
	if code != exitcode.NotFound || !strings.Contains(msg, "nope") {
		t.Errorf("the envelope must reach stderr when stdout fails: code=%d msg=%q", code, msg)
	}
}
