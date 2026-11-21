package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2777 (SEC10 P3 from cli#2714 round 5): the --json envelope JSON-ENCODES the
// drop path's message on stdout, so a server echo of the DECODED form of a
// backslash-bearing token re-forms the token's own spelling in the envelope bytes.
// The token `a\"b` (backslash, quote) against a message carrying `a"b`: the
// message is clean as a string, and json.Marshal turns it into `a\"b` on stdout.
const reformedToken2777 = `a\"b`

// reformedResponse2777 is a server refusal whose DECODED message carries a"b.
const reformedResponse2777 = `{"errors":[{"message":"bad value a\"b here","extensions":{"code":"FORBIDDEN"}}],"data":null}`

func envelopeError2777(t *testing.T, stdout string) map[string]json.RawMessage {
	t.Helper()
	var envelope struct {
		Error map[string]json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil || envelope.Error == nil {
		t.Fatalf("--json failure is not one envelope with an error object: %v (%q)", err, stdout)
	}
	return envelope.Error
}

// The envelope bytes never carry the token's spelling; the refusal is the fixed
// text, the exit code and the clean server code stay.
func TestSetTokenEnvelopeDoesNotReformTheToken2777(t *testing.T) {
	stdout, _, code := runSetToken2714(t, reformedResponse2777, reformedToken2777, true)
	if strings.Contains(stdout, reformedToken2777) {
		t.Fatalf("the --json envelope re-formed the token in its stdout bytes:\n%s", stdout)
	}
	e := envelopeError2777(t, stdout)
	if string(e["message"]) != `"the server refused the source token"` {
		t.Errorf("message = %s, want the fixed refusal", e["message"])
	}
	if string(e["serverCode"]) != `"FORBIDDEN"` {
		t.Errorf("the clean server code stays, got %s", e["serverCode"])
	}
	if code != exitcode.Forbidden {
		t.Errorf("exit code = %d, want %d", code, exitcode.Forbidden)
	}
}

// The human/stderr path is already clean and stays byte-identical: the same echo
// prints as the server wrote it, because nothing JSON-encodes it there.
func TestSetTokenHumanPathUnchangedByTheEnvelopeFix2777(t *testing.T) {
	_, stderr, code := runSetToken2714(t, reformedResponse2777, reformedToken2777, false)
	if strings.Contains(stderr, reformedToken2777) {
		t.Fatalf("stderr carries the token: %q", stderr)
	}
	if stderr != "hadron: bad value a\"b here\n" {
		t.Errorf("human stderr = %q, want the server's message unchanged", stderr)
	}
	if code != exitcode.Forbidden {
		t.Errorf("exit code = %d, want %d", code, exitcode.Forbidden)
	}
}

// Controls: a token with no backslash or quote keeps today's behaviour in --json
// (a message that echoes any covered spelling is replaced by the fixed refusal,
// not scrubbed piecemeal), and an ordinary echo-free message is untouched.
func TestSetTokenEnvelopeControls2777(t *testing.T) {
	clean := `{"errors":[{"message":"request denied","extensions":{"code":"FORBIDDEN"}}],"data":null}`
	stdout, _, _ := runSetToken2714(t, clean, "plainsecret", true)
	if got := string(envelopeError2777(t, stdout)["message"]); got != `"request denied"` {
		t.Errorf("an unrelated message must pass through, got %s", got)
	}
	echo := `{"errors":[{"message":"bad value plainsecret here","extensions":{"code":"FORBIDDEN"}}],"data":null}`
	stdout, _, _ = runSetToken2714(t, echo, "plainsecret", true)
	if got := string(envelopeError2777(t, stdout)["message"]); got != `"the server refused the source token"` {
		t.Errorf("a plain token echo is still replaced by the fixed refusal, got %s", got)
	}
}

// cli#2767 (Dara's round-5 P3 on cli#2714): the envelope's field presence is the
// documented contract, so it is asserted as presence, not as an empty string
// decoded into a zero value. message is present and empty on the no-echo tier; a
// dropped serverCode KEY is absent; and the dirty-code envelope's exit code is the
// one the drop path produces, not just any nonzero.
func TestSetTokenEnvelopePresenceAndExit2767(t *testing.T) {
	const token = "ed"
	clean := `{"errors":[{"message":"request denied","extensions":{"code":"FORBIDDEN"}}],"data":null}`
	dirty := `{"errors":[{"message":"request denied","extensions":{"code":"FORBIDDEN ` + token + `"}}],"data":null}`

	stdout, _, code := runSetToken2714(t, clean, token, true)
	e := envelopeError2777(t, stdout)
	if raw, ok := e["message"]; !ok || string(raw) != `""` {
		t.Errorf("message must be PRESENT and empty, got present=%v %s", ok, raw)
	}
	if raw, ok := e["serverCode"]; !ok || string(raw) != `"FORBIDDEN"` {
		t.Errorf("a clean serverCode must be present, got present=%v %s", ok, raw)
	}
	if raw, ok := e["code"]; !ok || string(raw) != "8" {
		t.Errorf("code must be present and the Forbidden exit (8), got present=%v %s", ok, raw)
	}
	if code != exitcode.Forbidden {
		t.Errorf("exit = %d, want %d", code, exitcode.Forbidden)
	}

	stdout, _, dirtyExit := runSetToken2714(t, dirty, token, true)
	d := envelopeError2777(t, stdout)
	if _, ok := d["serverCode"]; ok {
		t.Errorf("a server code carrying the token must be ABSENT from the envelope, got %s", d["serverCode"])
	}
	if raw, ok := d["message"]; !ok || string(raw) != `""` {
		t.Errorf("message must stay present and empty, got present=%v %s", ok, raw)
	}
	var codeField int
	if err := json.Unmarshal(d["code"], &codeField); err != nil || codeField != dirtyExit {
		t.Errorf("the envelope's code (%v) must equal the process exit (%d)", d["code"], dirtyExit)
	}
	if dirtyExit == 0 || dirtyExit == exitcode.Forbidden {
		t.Errorf("a code carrying the token is no recognized code, so the exit is the generic failure, got %d", dirtyExit)
	}
}
