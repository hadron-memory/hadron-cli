package agent

import (
	"io"
	"testing"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// A failed write (closed stdout) must be returned, never exit 0 with
// truncated output (cli#3267 local review F3).
func TestWriteInstructionsPropagatesAWriteError(t *testing.T) {
	route := "SEARCH"
	body := "body"
	for name, a := range map[string]instructionsDTO{
		"matched":  {State: "MATCHED", Route: &route, Instructions: &body, Candidates: []phraseTaskDTO{{}}},
		"no match": {State: "NO_MATCH", Candidates: []phraseTaskDTO{}, Nearest: []phraseTaskDTO{}},
	} {
		if err := writeInstructions(failingWriter{}, a, ""); err == nil {
			t.Errorf("%s: a failed write must be returned", name)
		}
	}
}
