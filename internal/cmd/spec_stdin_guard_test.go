package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #648 — the spec group's stdin readers, a spec body and its abstract, are
// DOCUMENTS: `spec new|edit|extract` refuse `--content -` / `--abstract -` from
// an interactive terminal with exit 2, before the citation is resolved over
// the network, naming the -file flag as the remedy. The piped forms are
// covered by the existing TestSpecEditContentStdin & co., which run non-TTY.
func TestSpecDocumentStdinRefusesATerminal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		remedy string
	}{
		{"new --content -", []string{"spec", "new", "-m", specMem, "--module", "msg", "--feature", "010", "--title", "T", "--content", "-"}, "--content-file <path>"},
		{"new --abstract -", []string{"spec", "new", "-m", specMem, "--module", "msg", "--feature", "010", "--title", "T", "--abstract", "-"}, "--abstract-file <path>"},
		{"edit --content -", []string{"spec", "edit", "msg:010:02", "-m", specMem, "--content", "-"}, "--content-file <path>"},
		{"edit --abstract -", []string{"spec", "edit", "msg:010:02", "-m", specMem, "--abstract", "-"}, "--abstract-file <path>"},
		{"extract --content -", []string{"spec", "extract", "cor:dmo:060:02", "-m", specMem, "--to-feature", "070", "--title", "T", "--content", "-"}, "--content-file <path>"},
		{"extract --abstract -", []string{"spec", "extract", "cor:dmo:060:02", "-m", specMem, "--to-feature", "070", "--title", "T", "--abstract", "-"}, "--abstract-file <path>"},
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
				t.Fatal("a spec document read from a terminal must be refused")
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
