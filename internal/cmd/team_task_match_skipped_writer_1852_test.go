package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1852 (P3 from #1851): `task create --match` prints the skipped-candidate
// lines before it exits 5, and the --match help says a writer failure takes
// precedence over that exit 5. These pin it for the skipped lines themselves.

var errSkippedLineWrite1852 = errors.New("skipped line write failed")

// failOnSkipped1852 accepts every write until the nth line starting with
// "  skipped:", then rejects that write — so the receipt and the match-failed
// line are written and only the skipped-candidate line fails.
type failOnSkipped1852 struct {
	n    int
	seen int
}

func (w *failOnSkipped1852) Write(p []byte) (int, error) {
	if strings.HasPrefix(string(p), "  skipped:") {
		w.seen++
		if w.seen == w.n {
			return 0, errSkippedLineWrite1852
		}
	}
	return len(p), nil
}

func TestTaskMatchSkipped1852WriterFailureBeatsExit5(t *testing.T) {
	for _, tc := range []struct {
		name    string
		skipped string
		failOn  int
	}{
		{"first candidate line", skipped1848, 1},
		{"second candidate line", skipped1848, 2},
		{"no candidates line", `[]`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTeamBinding(t)
			r := taskResponses()
			r["AgentTaskCreate"] = `{"data":{"createAgentTask":` + createFields1848(t, true, tc.skipped) + `}}`
			srv, _ := attnServer(t, r)
			setTeamBindingServer(t, srv.URL)
			f, _ := testFactory(t)
			w := &failOnSkipped1852{n: tc.failOn}
			f.IOStreams.Out = w
			root := NewRootCmd(f)
			root.SetArgs([]string{"--server", srv.URL, "team", "task", "create", "example", "--match"})
			err := root.Execute()
			if w.seen != tc.failOn {
				t.Fatalf("the failing skipped line was never reached (saw %d, want %d): err=%v", w.seen, tc.failOn, err)
			}
			if !errors.Is(err, errSkippedLineWrite1852) {
				t.Fatalf("writer error must surface, got %v", err)
			}
			if errors.Is(err, exitcode.ErrSilent) || exitCodeFor(err) == 5 {
				t.Fatalf("writer error must beat the silent exit 5: %v", err)
			}
		})
	}
}
