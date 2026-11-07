package team

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type failingNotesReader1665 struct {
	data string
	err  error
}

func (r *failingNotesReader1665) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, r.err
}

func TestShedNotesAlreadyOpenReadFailure1665(t *testing.T) {
	for _, tc := range []struct {
		name, partial string
		err           error
	}{
		{"empty-unexpected-eof", "", io.ErrUnexpectedEOF},
		{"partial-unexpected-eof", "PRIVATE partial narrative", io.ErrUnexpectedEOF},
		{"partial-raw-error", "PRIVATE partial narrative", errors.New("PRIVATE raw OS error /secret/notes.txt")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, why := shedNotesFrom(&failingNotesReader1665{data: tc.partial, err: tc.err})
			if got != "" || why != "--note-file could not be read" {
				t.Fatalf("want withheld notes and fixed read-failure reason, got notes%q reason%q", got, why)
			}
		})
	}
}

func TestShedNotesReadAndOpenControls1665(t *testing.T) {
	notes := "  Exact Unicode notes ☃\r\n\tkeep bytes\n"
	if got, why := shedNotesFrom(strings.NewReader(notes)); got != notes || why != "" {
		t.Fatalf("normal read %q %q", got, why)
	}
	if got, why := shedNotesOf(t.TempDir() + "/missing"); got != "" || why != "--note-file could not be opened" {
		t.Fatalf("open failure %q %q", got, why)
	}
}
