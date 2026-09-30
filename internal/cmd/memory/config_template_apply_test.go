package memory

import (
	"errors"
	"io"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type failOnWrite struct {
	call int
	fail int
	err  error
}

func (w *failOnWrite) Write(p []byte) (int, error) {
	w.call++
	if w.call == w.fail {
		return 0, w.err
	}
	return len(p), nil
}

func TestWriteApplyResultReportsOutputFailure(t *testing.T) {
	writeErr := errors.New("output unavailable")
	for _, fail := range []int{1, 2, 3} {
		t.Run(string(rune('0'+fail)), func(t *testing.T) {
			w := &failOnWrite{fail: fail, err: writeErr}
			f := &cmdutil.Factory{IOStreams: &output.IOStreams{Out: w, ErrOut: io.Discard}}
			r := applyResultDTO{DryRun: true, Entries: []applyEntryDTO{}}
			if err := writeApplyResult(f, r); !errors.Is(err, writeErr) {
				t.Fatalf("write %d: got %v, want output error", fail, err)
			}
		})
	}
}
