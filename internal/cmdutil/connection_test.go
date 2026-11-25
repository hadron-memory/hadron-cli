package cmdutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#2968: the shared connection helpers. The suggestion text is pinned
// byte for byte, because the two commands' messages must stay what they were
// before the helpers moved.
func TestConnectionStatusSuggestionIsByteIdenticalPerCommand(t *testing.T) {
	const lead = "The listing is paged, so follow its --offset hint before concluding it is gone. Check whether it still exists by listing connections with:\n  hadron "
	for _, cmd := range []string{"slack", "telegram"} {
		if got, want := ConnectionStatusSuggestion(cmd), lead+cmd+" status"; got != want {
			t.Errorf("%s: got %q, want %q", cmd, got, want)
		}
	}
}

func TestHasControlOrFormatCharacter(t *testing.T) {
	for s, want := range map[string]bool{
		"plain-id": false, "a\x1b[31m": true, "a\nb": true, "a\u200bb": true, "a\u2028b": true, "a\u2029b": true,
		// cli#3195: DEL, a C1 control (NEL) and a bidi override.
		"a\x7fb": true, "a\u0085b": true, "a\u202eb": true,
		// Nothing to refuse: the empty string and ordinary non-ASCII text.
		"": false, "Zürich-café-Ωμέγα-日本": false,
	} {
		if got := HasControlOrFormatCharacter(s); got != want {
			t.Errorf("%q: got %v, want %v", s, got, want)
		}
	}
}

// A refusal that echoes a submitted secret reaches the operator without it,
// whichever command built the base; the exit class survives.
func TestFinishConnectErrorWithholdsEverySecret(t *testing.T) {
	secrets := []string{"xoxb-SECRET-ONE", "xapp-SECRET-TWO"}
	raw := errors.New("server said xoxb-SECRET-ONE and xapp-SECRET-TWO")
	base := exitcode.Newf(exitcode.Error, "connect outcome unknown: %s", raw)
	got := FinishConnectError(raw, base, secrets)
	for _, s := range secrets {
		if strings.Contains(got.Error(), s) {
			t.Errorf("secret %q leaked: %v", s, got)
		}
	}
	if exitcode.FromError(got) != exitcode.Error {
		t.Errorf("exit class lost: %v", exitcode.FromError(got))
	}
}

// cli#3195: the finished error is cut from its raw chain, so no later layer can
// reach the server's unredacted text through it, and a clean server code
// survives the cut. The base wraps the raw error on purpose: the precondition
// shows the chain assertions can fail.
func TestFinishConnectErrorCutsTheRawChainAndKeepsTheServerCode3195(t *testing.T) {
	const secret, code = "xoxb-SECRET-ONE", "CONNECTION_REFUSED"
	raw := fmt.Errorf("request failed: %w", gqlerror.List{{
		Message:    "server said " + secret,
		Extensions: map[string]any{"code": code},
	}})
	base := exitcode.Newf(exitcode.Conflict, "connect refused: %w", raw)
	if !errors.Is(base, raw) {
		t.Fatal("precondition: the base must carry the raw error")
	}
	got := FinishConnectError(raw, base, []string{secret})
	if errors.Is(got, raw) {
		t.Error("the raw error is still in the chain")
	}
	var list gqlerror.List
	if errors.As(got, &list) {
		t.Errorf("the server's error list is still reachable: %v", list)
	}
	if strings.Contains(got.Error(), secret) {
		t.Errorf("secret leaked: %v", got)
	}
	var coded interface{ ServerCode() string }
	if !errors.As(got, &coded) || coded.ServerCode() != code {
		t.Errorf("server code lost: %#v", got)
	}
	if exitcode.FromError(got) != exitcode.Conflict {
		t.Errorf("exit class lost: %v", exitcode.FromError(got))
	}
}

// cli#3195: "-" on a terminal is refused by this helper, in its own words,
// before stdin is read.
func TestReadConnectionTokenRefusesATerminalStdin3195(t *testing.T) {
	const typed = "xoxb-TYPED-SECRET"
	ios, _, stderr := output.TestTTY(typed + "\n")
	got, err := ReadConnectionToken(&Factory{IOStreams: ios}, "-", "bot-token-file", "Slack bot token", "bot token")
	if err == nil || got != "" {
		t.Fatalf("got %q, %v; want a refusal and no value", got, err)
	}
	if exitcode.FromError(err) != exitcode.Usage {
		t.Errorf("want a usage error, got %v", err)
	}
	for _, want := range []string{"--bot-token-file", "which is a terminal", "Nothing was sent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %q", want, err.Error())
		}
	}
	if strings.Contains(err.Error(), typed) || strings.Contains(stderr.String(), typed) {
		t.Errorf("the secret must not appear: %q / %q", err.Error(), stderr.String())
	}
	if rest, _ := io.ReadAll(ios.In); string(rest) != typed+"\n" {
		t.Errorf("stdin was read before the refusal; left %q", rest)
	}
}

// The refusal is only for "-" on a terminal: piped stdin and a file still read.
func TestReadConnectionTokenReadsPipedStdinAndFiles3195(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	piped, _, _ := output.Test()
	piped.In = strings.NewReader("from-pipe\n")
	terminal, _, _ := output.TestTTY("")
	for _, tc := range []struct {
		ios          *output.IOStreams
		source, want string
	}{{piped, "-", "from-pipe"}, {terminal, file, "from-file"}} {
		got, err := ReadConnectionToken(&Factory{IOStreams: tc.ios}, tc.source, "bot-token-file", "Slack bot token", "bot token")
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v", tc.want, got, err)
		}
	}
}
