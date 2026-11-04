package cmdutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#1234: each ResolveUnlock refusal is its own guard, so each is
// pinned by its own message (a later guard must not mask an earlier one).
func TestResolveUnlockRefusalsNameTheirRule(t *testing.T) {
	dir := t.TempDir()
	file := func(i int) string {
		p := filepath.Join(dir, "p"+string(rune('a'+i)))
		if err := os.WriteFile(p, []byte("pw\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	nine, nineFiles := []string{}, []string{}
	for i := 0; i < 9; i++ {
		nine = append(nine, "acme.com:m"+string(rune('a'+i)))
		nineFiles = append(nineFiles, file(i))
	}
	cases := map[string]struct {
		u     UnlockFlags
		stdin string
		want  string
	}{
		"ninth":     {UnlockFlags{Memories: nine, Files: nineFiles, FilesSet: true}, "", "at most 8"},
		"stdin two": {UnlockFlags{Memories: []string{"acme.com:a", "acme.com:b"}, Stdin: true, StdinSet: true}, "pw\n", "ONE memory"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ios, _, _ := output.Test()
			ios.In = strings.NewReader(c.stdin)
			_, err := ResolveUnlock(ios, c.u)
			if exitcode.FromError(err) != exitcode.Usage || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "pw") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
