package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// runSetToken2714 drives the real root command and then the real failure
// renderer, so the pins in this file measure the RENDERED output (stderr,
// --json envelope, exit code), not only the error value — the wiring, not
// just the helpers (renderFailure exists for exactly this; see its comment).
func runSetToken2714(t *testing.T, response, token string, asJSON bool) (stdout, stderr string, code int) {
	t.Helper()
	gql, _ := captureGraphQL(t, map[string]string{"SetMemorySourceToken": response})
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	f.IOStreams.In = strings.NewReader(token)
	args := []string{"memory", "sync", "set-token", "acme.com:kb", "--server", gql.URL}
	if asJSON {
		args = append(args, "--json")
	}
	root := NewRootCmd(f)
	root.SetArgs(args)
	err := root.Execute()
	code = renderFailure(f, args, err)
	return out.String(), errOut.String(), code
}

// TestSetToken2714NoEchoTierRendersBare pins Jerry's asks (2) and (3) for the
// terminal tier of the refusal cascade (cli#2714 round 5): a token inside
// BOTH fixed refusals ("ed" is in "refused" and in "declined") renders no
// echo at all — the human failure is the bare renderer prefix with nothing
// after it, and the --json envelope keeps the exit code and the clean server
// code while its message is empty. A server code that carries the token is
// dropped on the same tier: no serverCode key, exit code and empty message
// unchanged. The renderer's own fixed words (the "hadron: " prefix) are a
// stated limit, not the drop path's output (Jerry's design note 4).
func TestSetToken2714NoEchoTierRendersBare(t *testing.T) {
	const token = "ed"
	refused := `{"errors":[{"message":"request denied","extensions":{"code":"FORBIDDEN"}}],"data":null}`
	dirtyCode := `{"errors":[{"message":"request denied","extensions":{"code":"FORBIDDEN ` + token + `"}}],"data":null}`

	// Human: the message the drop path chose is empty, so stderr is the bare
	// prefix and nothing else — no token, no fixed refusal, no echo.
	_, stderr, code := runSetToken2714(t, refused, token, false)
	if stderr != "hadron: \n" {
		t.Errorf("human stderr = %q, want the bare prefix with an empty message", stderr)
	}
	if code != exitcode.Forbidden {
		t.Errorf("exit code = %d, want %d (the refusal still identifies itself)", code, exitcode.Forbidden)
	}

	// --json with a clean server code: the envelope carries the exit code and
	// the server code; the message is empty.
	stdout, _, code := runSetToken2714(t, refused, token, true)
	var envelope struct {
		Error struct {
			Code       int    `json:"code"`
			Message    string `json:"message"`
			ServerCode string `json:"serverCode"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("--json failure is not one envelope: %v (%q)", err, stdout)
	}
	if envelope.Error.Code != exitcode.Forbidden || envelope.Error.Message != "" || envelope.Error.ServerCode != "FORBIDDEN" {
		t.Errorf("--json envelope = %+v, want code %d, empty message, serverCode FORBIDDEN", envelope.Error, exitcode.Forbidden)
	}
	if code != exitcode.Forbidden {
		t.Errorf("exit code = %d, want %d", code, exitcode.Forbidden)
	}

	// --json with a code that carries the token: dropped — no serverCode key,
	// message still empty. The exit code is only asserted non-zero: a code
	// string carrying the token is by construction not a recognized code, so
	// what it maps to is MapError's generic behavior, not this change's
	// contract.
	stdout, _, dirtyExit := runSetToken2714(t, dirtyCode, token, true)
	var dropped struct {
		Error struct {
			Code       int    `json:"code"`
			Message    string `json:"message"`
			ServerCode string `json:"serverCode"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &dropped); err != nil {
		t.Fatalf("--json failure is not one envelope: %v (%q)", err, stdout)
	}
	if dropped.Error.ServerCode != "" {
		t.Errorf("a server code carrying the token must be dropped, got %q", dropped.Error.ServerCode)
	}
	if dropped.Error.Message != "" || dropped.Error.Code == 0 {
		t.Errorf("dropping the code must not change the refusal: %+v", dropped.Error)
	}
	if dirtyExit == 0 {
		t.Errorf("the process exit must still report failure, got %d", dirtyExit)
	}
}

// TestSetToken2714SecondTierRendersOnStderr pins the same wiring one tier up
// (Jerry's ask 2): a token that is a word of the first refusal ("refused")
// renders the SECOND fixed text on the rendered stderr — not only in
// err.Error() — and never the token.
func TestSetToken2714SecondTierRendersOnStderr(t *testing.T) {
	refused := `{"errors":[{"message":"that value was refused","extensions":{"code":"FORBIDDEN"}}],"data":null}`
	_, stderr, code := runSetToken2714(t, refused, "refused", false)
	if stderr != "hadron: input declined\n" {
		t.Errorf("human stderr = %q, want the second fixed refusal after the prefix", stderr)
	}
	if strings.Contains(stderr, "refused") {
		t.Errorf("the token survives in the rendered stderr %q", stderr)
	}
	if code != exitcode.Forbidden {
		t.Errorf("exit code = %d, want %d", code, exitcode.Forbidden)
	}
}
