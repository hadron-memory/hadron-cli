package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #648 — a handoff is a DOCUMENT: `team session end --handoff -` refuses an
// interactive terminal with exit 2, before any request, naming --handoff-file.
// A truncated handoff is the lost continuity it exists to prevent. The refusal
// happens before anything is read, so there is nothing to rescue, and nothing
// must be claimed as saved. The piped form is covered by
// TestSessionEndReadsTheHandoffFromAFileAndStdin, which runs non-TTY.
func TestSessionEndHandoffStdinRefusesATerminal(t *testing.T) {
	teamGitDir(t)
	requests := 0
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	t.Cleanup(gql.Close)
	f, _, errOut := testFactoryTTY(t, "typed at a terminal\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end", "--session", "s1", "--handoff", "-", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("--handoff - from a terminal must be refused")
	}
	if got := renderError(f, err); got != exitcode.Usage {
		t.Errorf("want exit %d (usage), got %d", exitcode.Usage, got)
	}
	if requests != 0 {
		t.Errorf("a refusal on argument grounds must make no requests, got %d", requests)
	}
	msg := errOut.String()
	for _, want := range []string{"--handoff-file <path>", "interactive terminal"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal must mention %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "%!") {
		t.Errorf("the refusal has a formatting fault:\n%s", msg)
	}
	// Nothing was taken, so nothing may be reported as saved.
	if strings.Contains(strings.ToLower(msg), "saved") {
		t.Errorf("a refusal before the read must not claim a rescue:\n%s", msg)
	}
}
