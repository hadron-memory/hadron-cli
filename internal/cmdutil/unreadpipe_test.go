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

type boom struct{}

func (*boom) Read([]byte) (int, error) { return 0, errors.New("boom") }
