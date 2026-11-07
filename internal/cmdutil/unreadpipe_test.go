package cmdutil

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestRefuseUnreadStdinPipe(t *testing.T) {
	t.Run("non-empty pipe is refused with exit 2", func(t *testing.T) {
		err := RefuseUnreadStdinPipe(strings.NewReader("body"), false, "--content -", "store an empty body")
		if exitcode.FromError(err) != exitcode.Usage {
			t.Fatalf("want exit 2, got %v", err)
		}
		if !strings.Contains(err.Error(), "--content -") || !strings.Contains(err.Error(), "store an empty body") {
			t.Errorf("message lacks the remedy or consequence: %v", err)
		}
	})
	t.Run("empty pipe passes", func(t *testing.T) {
		if err := RefuseUnreadStdinPipe(strings.NewReader(""), false, "f", "c"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("terminal passes without a read", func(t *testing.T) {
		if err := RefuseUnreadStdinPipe(&boom{}, true, "f", "c"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("nil stdin passes", func(t *testing.T) {
		if err := RefuseUnreadStdinPipe(nil, false, "f", "c"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("read error passes", func(t *testing.T) {
		if err := RefuseUnreadStdinPipe(&boom{}, false, "f", "c"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a silent open pipe never hangs", func(t *testing.T) {
		old := unreadPipeWait
		unreadPipeWait = 20 * time.Millisecond
		defer func() { unreadPipeWait = old }()
		pr, pw := io.Pipe()
		defer func() { _ = pw.Close() }()
		done := make(chan error, 1)
		go func() { done <- RefuseUnreadStdinPipe(pr, false, "f", "c") }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("a silent pipe must pass, got %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("guard blocked on a silent open pipe")
		}
	})
}

func TestRefuseUnreadStdinPipeDelayedByte(t *testing.T) {
	old := unreadPipeWait
	unreadPipeWait = 2 * time.Second
	defer func() { unreadPipeWait = old }()
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	go func() {
		time.Sleep(75 * time.Millisecond)
		_, _ = pw.Write([]byte("x"))
	}()
	err := RefuseUnreadStdinPipe(pr, false, "f", "c")
	if exitcode.FromError(err) != exitcode.Usage {
		t.Fatalf("a byte arriving inside the window must refuse, got %v", err)
	}
}

func TestRefuseUnreadStdinPipeLateByteMissesWindow(t *testing.T) {
	old := unreadPipeWait
	unreadPipeWait = 20 * time.Millisecond
	defer func() { unreadPipeWait = old }()
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	go func() {
		time.Sleep(300 * time.Millisecond)
		_, _ = pw.Write([]byte("x"))
	}()
	if err := RefuseUnreadStdinPipe(pr, false, "f", "c"); err != nil {
		t.Fatalf("a byte after the window is the accepted edge and must pass, got %v", err)
	}
}

func TestRefuseUnreadStdinPipeRestoresSeekableStdin(t *testing.T) {
	r := strings.NewReader("line1\nline2\n")
	if err := RefuseUnreadStdinPipe(r, false, "f", "c"); exitcode.FromError(err) != exitcode.Usage {
		t.Fatalf("want refusal, got %v", err)
	}
	rest, _ := io.ReadAll(r)
	if string(rest) != "line1\nline2\n" {
		t.Errorf("a refusal must not consume a seekable stdin, left %q", rest)
	}
}

type boom struct{}

func (*boom) Read([]byte) (int, error) { return 0, errors.New("boom") }
