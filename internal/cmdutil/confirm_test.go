package cmdutil

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// failingPromptWriter fails every write: stderr gone (closed pipe, full disk), so the question was never shown.
type failingPromptWriter struct{}

func (failingPromptWriter) Write([]byte) (int, error) {
	return 0, errors.New("write /dev/stderr: broken pipe")
}

// spyReader records whether anything read the answer.
type spyReader struct {
	r    io.Reader
	read bool
}

func (s *spyReader) Read(p []byte) (int, error) {
	s.read = true
	return s.r.Read(p)
}

// unseenPromptStreams is a terminal stdin answering "y" while stderr (the prompt) fails.
func unseenPromptStreams() (*output.IOStreams, *spyReader) {
	streams, _, _ := output.TestTTY("y\n")
	spy := &spyReader{r: streams.In}
	streams.In = spy
	streams.ErrOut = failingPromptWriter{}
	return streams, spy
}

func requireUnshownPromptRefusal(t *testing.T, err error, spy *spyReader) {
	t.Helper()
	if err == nil {
		t.Fatal("a 'y' given to a prompt nobody saw was accepted as consent")
	}
	var ee *exitcode.CodedError
	if !errors.As(err, &ee) || ee.Code != exitcode.Error {
		t.Fatalf("want exitcode.Error (1), got %v", err)
	}
	if !strings.Contains(err.Error(), "could not show the confirmation prompt") || !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatalf("message does not say the prompt was not shown and nothing was sent: %v", err)
	}
	if spy.read {
		t.Fatal("stdin was read after the prompt write failed")
	}
}

func TestConfirmDeletionRefusesWhenPromptCannotBeShown(t *testing.T) {
	streams, spy := unseenPromptStreams()
	requireUnshownPromptRefusal(t, ConfirmDeletion(streams, false, "memory x"), spy)
}

func TestConfirmRefusesWhenPromptCannotBeShown(t *testing.T) {
	streams, spy := unseenPromptStreams()
	requireUnshownPromptRefusal(t, Confirm(streams, false, "Proceed?"), spy)
}

func TestConfirmHelpersYesSkipsPromptEvenWithBrokenStderr(t *testing.T) {
	streams, spy := unseenPromptStreams()
	if err := ConfirmDeletion(streams, true, "memory x"); err != nil {
		t.Fatalf("--yes must proceed: %v", err)
	}
	if err := Confirm(streams, true, "Proceed?"); err != nil {
		t.Fatalf("--yes must proceed: %v", err)
	}
	if spy.read {
		t.Fatal("--yes read stdin")
	}
}

func TestConfirmHelpersStillAcceptShownPrompt(t *testing.T) {
	streams, _, errOut := output.TestTTY("y\ny\n")
	if err := ConfirmDeletion(streams, false, "memory x"); err != nil {
		t.Fatalf("ConfirmDeletion: %v", err)
	}
	if err := Confirm(streams, false, "Proceed?"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !strings.Contains(errOut.String(), "Delete memory x?") || !strings.Contains(errOut.String(), "Proceed? (y/N)") {
		t.Fatalf("prompts not shown: %q", errOut.String())
	}
}

func TestConfirmHelpersDecline(t *testing.T) {
	streams, _, _ := output.TestTTY("n\nn\n")
	for name, err := range map[string]error{
		"ConfirmDeletion": ConfirmDeletion(streams, false, "memory x"),
		"Confirm":         Confirm(streams, false, "Proceed?"),
	} {
		var ee *exitcode.CodedError
		if !errors.As(err, &ee) || ee.Code != exitcode.Cancelled {
			t.Errorf("%s: want Cancelled, got %v", name, err)
		}
	}
}

func TestConfirmHelpersNonInteractiveWithoutYesStillRefuses(t *testing.T) {
	streams, _, _ := output.Test()
	for name, err := range map[string]error{
		"ConfirmDeletion": ConfirmDeletion(streams, false, "memory x"),
		"Confirm":         Confirm(streams, false, "Proceed?"),
	} {
		var ee *exitcode.CodedError
		if !errors.As(err, &ee) || ee.Code != exitcode.Usage {
			t.Errorf("%s: want Usage, got %v", name, err)
		}
	}
}
