package cmdutil

import (
	"errors"
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
