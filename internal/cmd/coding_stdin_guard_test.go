package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #648 — the coding group's stdin readers are DOCUMENTS: a check or preflight
// body (`review create` / `preflight create --content -`) and a diff
// (`review run --diff -`). Each refuses an interactive terminal with exit 2,
// before any request, naming the remedy. The diff case matters beyond a bad
// write: a truncated diff drops changed paths, so the run would select checks
// for less than the real change. Piped forms stay covered by the existing
// `--diff -` tests, which run non-TTY.
func TestCodingDocumentStdinRefusesATerminal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		remedy string
	}{
		{"review create", []string{"coding", "review", "create", "thin-resolver", "-m", codingMem,
			"--trigger", "a resolver changes", "--description", "Resolver fields stay thin.", "--content", "-"}, "--content-file <path>"},
		{"preflight create", []string{"coding", "preflight", "create", "findings:flaky-timer", "-m", codingMem,
			"--route", "fix a flaky timer", "--description", "The countdown starts before the await.", "--content", "-"}, "--content-file <path>"},
		{"review run --diff -", []string{"coding", "review", "run", "-m", codingMem, "--diff", "-"}, "--diff <path>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				_, _ = w.Write([]byte(`{"data":{}}`))
			}))
			t.Cleanup(gql.Close)
			f, _, errOut := testFactoryTTY(t, "typed at a terminal\n")
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			err := root.Execute()
			if err == nil {
				t.Fatal("a document read from a terminal must be refused")
			}
			if got := renderError(f, err); got != exitcode.Usage {
				t.Errorf("want exit %d (usage), got %d", exitcode.Usage, got)
			}
			if requests != 0 {
				t.Errorf("a refusal on argument grounds must make no requests, got %d", requests)
			}
			msg := errOut.String()
			for _, want := range []string{tc.remedy, "interactive terminal"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal must mention %q:\n%s", want, msg)
				}
			}
			if strings.Contains(msg, "%!") {
				t.Errorf("the refusal has a formatting fault:\n%s", msg)
			}
		})
	}
}
