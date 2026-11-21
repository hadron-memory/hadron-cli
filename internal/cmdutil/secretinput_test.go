package cmdutil

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// #1490: every os.ReadFile failure class carries the argument in its
// *PathError text, so ReadSecretInput must report a fixed message that names
// the flag and never the argument.
func TestReadSecretInputFailedReadNeverEchoesArgument(t *testing.T) {
	const pasted = "sk-live-123"
	tmp := t.TempDir()

	dir := filepath.Join(tmp, pasted+"-dir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"not found":       pasted,
		"@ not found":     "@" + pasted,
		"directory":       dir,
		"@ directory":     "@" + dir,
		"name too long":   pasted + strings.Repeat("x", 300),
		"not a directory": filepath.Join(writeFile(t, tmp, "plain"), pasted),
	}
	// Permission denied cannot be produced as root, which ignores the mode.
	if os.Geteuid() != 0 {
		locked := filepath.Join(tmp, pasted+"-locked")
		if err := os.WriteFile(locked, []byte("v"), 0o000); err != nil {
			t.Fatal(err)
		}
		cases["permission denied"] = locked
	}

	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			ios, _, stderr := output.Test()
			_, err := ReadSecretInput(ios, source, "value-file", "Secret value", "secret value")
			if err == nil {
				t.Fatal("expected an error")
			}
			var coded *exitcode.CodedError
			if !errors.As(err, &coded) || coded.Code != exitcode.Usage {
				t.Fatalf("want a usage error (exit 2), got %T %v", err, err)
			}
			if strings.Contains(err.Error(), pasted) || strings.Contains(stderr.String(), pasted) {
				t.Fatalf("error repeats the argument: %q / %q", err.Error(), stderr.String())
			}
			if !strings.Contains(err.Error(), "--value-file") {
				t.Fatalf("error must name the flag: %q", err.Error())
			}
		})
	}
}

func writeFile(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("v"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// cli#2654: "-" on a terminal would echo a pasted secret into the operator's
// screen and scrollback, so it is refused before anything is read.
func TestReadSecretInputDashOnTerminalRefusedWithoutReading2654(t *testing.T) {
	const pasted = "pasted-secret-must-stay-unread"
	ios, _, stderr := output.TestTTY(pasted + "\n")
	_, err := ReadSecretInput(ios, "-", "private-key-file", "GitHub App private key", "private key")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	var coded *exitcode.CodedError
	if !errors.As(err, &coded) || coded.Code != exitcode.Usage {
		t.Fatalf("want a usage error (exit 2), got %T %v", err, err)
	}
	for _, want := range []string{"--private-key-file", "stdin is a terminal", "echo", "file path"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %q", want, err.Error())
		}
	}
	if strings.Contains(err.Error(), pasted) || strings.Contains(stderr.String(), pasted) {
		t.Errorf("the pasted material must never appear: %q / %q", err.Error(), stderr.String())
	}
	// Nothing was read: the scripted answer is still there to be read.
	if rest, _ := io.ReadAll(ios.In); !strings.Contains(string(rest), pasted) {
		t.Errorf("the refusal must happen before any read of stdin; left %q", rest)
	}
}

func TestReadSecretInputDashStillReadsPipedStdin2654(t *testing.T) {
	ios, _, _ := output.Test()
	ios.In = strings.NewReader("piped-value\n")
	got, err := ReadSecretInput(ios, "-", "value-file", "Secret value", "secret value")
	if err != nil || got != "piped-value" {
		t.Fatalf("got %q, %v; want the piped value", got, err)
	}
}

func TestReadSecretInputPathsUnchangedOnTerminal2654(t *testing.T) {
	path := writeFile(t, t.TempDir(), "secret\n")
	for name, source := range map[string]string{"plain path": path, "@path": "@" + path} {
		ios, _, _ := output.TestTTY("")
		got, err := ReadSecretInput(ios, source, "value-file", "Secret value", "secret value")
		if err != nil || got != "v" {
			t.Errorf("%s: got %q, %v", name, got, err)
		}
	}
}

// cli#2654 (BLIND P3 on #2641): a caller that refuses the masked prompt must
// not have its empty-value message steer the user into that refusal.
func TestReadSecretInputEmptyHintFollowsTheCallersModes2654(t *testing.T) {
	ios, _, _ := output.Test()
	_, err := ReadSecretInput(ios, "", "value-file", "Secret value", "secret value")
	if err == nil || !strings.Contains(err.Error(), "run interactively") {
		t.Fatalf("a caller with a prompt keeps the interactive hint: %v", err)
	}
	ios, _, _ = output.Test()
	_, err = ReadSecretInput(ios, "", "private-key-file", "GitHub App private key", "private key", NoPrompt())
	if err == nil {
		t.Fatal("expected an empty-value error")
	}
	if strings.Contains(err.Error(), "interactively") {
		t.Errorf("a caller without a prompt must not advertise it: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "--private-key-file") {
		t.Errorf("the hint must still name the flag: %q", err.Error())
	}
}
