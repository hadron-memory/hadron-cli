package team

import (
	"io"
	"strings"
	"testing"
)

// repeater yields n bytes of "a" without a file, so the cap+1 bound is pinned
// against a source whose size the validator never saw (a file that grows
// while it is read).
type repeater struct{ left int }

func (r *repeater) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.left {
		n = r.left
	}
	for i := 0; i < n; i++ {
		p[i] = 'a'
	}
	r.left -= n
	return n, nil
}

func TestShedNotesFromBoundsTheReadAtCapPlusOne(t *testing.T) {
	if _, why := shedNotesFrom(&repeater{left: shedNotesCap}); why != "" {
		t.Errorf("exactly the cap is accepted, got %q", why)
	}
	if _, why := shedNotesFrom(&repeater{left: 10 * shedNotesCap}); why != shedNotesTooBig {
		t.Errorf("a source that grows past the cap is refused, got %q", why)
	}
}

func TestShedNotesFromKeepsBytesExactAndNamesEachClass(t *testing.T) {
	in := "  keep\r\n\tme  \n"
	if got, why := shedNotesFrom(strings.NewReader(in)); why != "" || got != in {
		t.Errorf("bytes must come back unchanged: %q %q", got, why)
	}
	for in, want := range map[string]string{"": shedNotesBlank, " \n\t": shedNotesBlank, "\xff": shedNotesNotUTF8} {
		if _, why := shedNotesFrom(strings.NewReader(in)); why != want {
			t.Errorf("%q: want %q, got %q", in, want, why)
		}
	}
}
