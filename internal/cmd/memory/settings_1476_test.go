package memory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// cli#1476 BLIND P2 (Bruno): --data-file is read once, by validate. A file
// that disappears or changes afterwards cannot make the request drop or
// alter the data validate approved.
func TestDataFileReadOnce1476(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.json")
	if err := os.WriteFile(p, []byte(`{"k":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var s memorySettings
	cmd := &cobra.Command{Use: "x"}
	s.register(cmd)
	if err := cmd.ParseFlags([]string{"--data-file", p}); err != nil {
		t.Fatal(err)
	}
	if err := s.validate(cmd, false, false); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := os.WriteFile(p, []byte(`{bad`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.dataArg(); got == nil || string(*got) != `{"k":1}` {
		t.Fatalf("after the file changed: data = %v, want the validated {\"k\":1}", got)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if got := s.dataArg(); got == nil || string(*got) != `{"k":1}` {
		t.Fatalf("after the file was removed: data = %v, want the validated {\"k\":1}", got)
	}
}
