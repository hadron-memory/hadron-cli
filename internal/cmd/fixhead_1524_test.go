package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1531 fix head: Atilla's LOCAL P2s and Diego's BLIND P3.

// P2-1: the generated setAppProperties result is a nullable pointer. A null or
// missing result is a controlled exit 1 with nothing on stdout, never a panic:
// the write may have landed, so the message says to read before writing again.
func TestAppPropertiesWriteMissingResult1524(t *testing.T) {
	for _, verb := range [][]string{
		{"app", "properties", "set", appRef1524, `{"a":1}`},
		{"app", "properties", "clear", appRef1524, "--yes"},
	} {
		for name, data := range map[string]string{
			"null result":    `{"data":{"setAppProperties":null}}`,
			"missing result": `{"data":{}}`,
		} {
			t.Run(verb[2]+" "+name, func(t *testing.T) {
				calls, out, err := runApp1524(t, map[string]string{"SetAppProperties": data}, "", verb...)
				if exitCodeFor(err) != exitcode.Error || out != "" {
					t.Fatalf("exit %d, out %q: %v", exitCodeFor(err), out, err)
				}
				if !strings.Contains(err.Error(), "app properties get") {
					t.Errorf("no read-back remedy: %v", err)
				}
				if n := len(*calls); n != 1 {
					t.Errorf("the write must be sent once, got %d calls", n)
				}
			})
		}
	}
}

// Control: app properties writes are the App manager's and stay
// session-free even in a bound worktree.
func TestAppPropertiesWriteNotAttributedToWorker1524(t *testing.T) {
	r := setResponses1524(`{"a":1}`)
	calls, _, _, err := runTeamTaskAll1524(t, r, "app", "properties", "set", appRef1524, `{"a":1}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "SetAppProperties" {
			if c.Session != "" {
				t.Fatalf("app properties set carried worker session %q", c.Session)
			}
			return
		}
	}
	t.Fatal("no SetAppProperties call")
}
