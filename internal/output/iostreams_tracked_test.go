package output

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// #334's tracking must not cost `asset get -o -` its streaming fast path
// (PR #585 review, @copilot). io.Copy prefers io.ReaderFrom, and wrapping a
// stream in a plain io.Writer HIDES the one os.Stdout implements.

// plainReader exposes ONLY Read — like an http.Response.Body, which is what
// `asset get` actually streams. A strings.Reader would not do: it implements
// io.WriterTo, and io.Copy prefers the SOURCE's WriteTo over the destination's
// ReadFrom, so the fast path under test is never reached and the assertion
// measures the wrong half. (It failed that way first.)
type plainReader struct{ r io.Reader }

func (p plainReader) Read(b []byte) (int, error) { return p.r.Read(b) }

// readerFromWriter stands in for os.Stdout: it implements io.ReaderFrom and
// records whether that path was taken.
type readerFromWriter struct {
	buf  bytes.Buffer
	used bool
}

func (w *readerFromWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }
func (w *readerFromWriter) ReadFrom(r io.Reader) (int64, error) {
	w.used = true
	return w.buf.ReadFrom(r)
}

func TestTrackedPreservesTheReaderFromFastPath(t *testing.T) {
	under := &readerFromWriter{}
	s := &IOStreams{Out: Tracked(under)}

	n, err := io.Copy(s.Out, plainReader{strings.NewReader("streamed bytes")})
	if err != nil || n != 14 {
		t.Fatalf("copy: n=%d err=%v", n, err)
	}
	if !under.used {
		t.Error("io.Copy must reach the underlying ReadFrom, not fall back to buffered writes")
	}
	if under.buf.String() != "streamed bytes" {
		t.Errorf("bytes must arrive intact, got %q", under.buf.String())
	}
	// And the whole point of the wrapper still holds on that path.
	if !s.Wrote() {
		t.Error("a ReadFrom copy must still mark the stream as written")
	}
}

// A writer with NO ReadFrom must still work, and still be tracked — the
// fallback routes through Write rather than recursing into ReadFrom.
func TestTrackedFallsBackForAPlainWriter(t *testing.T) {
	var plain bytes.Buffer
	s := &IOStreams{Out: Tracked(&plain)}
	if _, err := io.Copy(s.Out, plainReader{strings.NewReader("plain bytes")}); err != nil {
		t.Fatal(err)
	}
	if plain.String() != "plain bytes" {
		t.Errorf("got %q", plain.String())
	}
	if !s.Wrote() {
		t.Error("the fallback must still mark the stream as written")
	}
}
