package cmd

// hadron-cli#2919: the unknown-outcome guidance of `question ask`. The reworded
// read-back (list the log, look for the title you sent, page it) was pinned only
// by containment of "hadron question list --log LOG", so reverting it kept every
// test green; and the ask help did not say exit 7 is not retry-safe.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const askReadBack2919 = "list the log with `hadron question list --log LOG` and look for the title you sent " +
	"(the listing is paged: use --limit and --offset) before asking again"

// Every unknown outcome of an ask ends with the whole reworded sentence: the log
// listing, the title to look for, and the paging hint.
func TestQuestionAskReadBackWordingIsPinned2919(t *testing.T) {
	askFile := filepath.Join(t.TempDir(), "ask.json")
	if err := os.WriteFile(askFile, []byte(`{"title":"Pick a database"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, resp := range map[string]string{
		"a null result with no error":    `{"data":{"askQuestion":null}}`,
		"an absent result with no error": `{"data":{}}`,
		"an internal error at the field": `{"data":{"askQuestion":null},"errors":[{"message":"x","path":["askQuestion"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`,
		"an error below the field":       `{"data":{"askQuestion":null},"errors":[{"message":"x","path":["askQuestion","answer"],"extensions":{"code":"NOT_FOUND"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			calls, out, err := runQuestion(t, map[string]string{"AskQuestion": resp}, "ask", "LOG", "--input-file", askFile)
			if err == nil || exitCodeFor(err) != exitcode.Unavailable {
				t.Fatalf("exit = %d, want 7: %v", exitCodeFor(err), err)
			}
			if len(calls.ops) != 1 || out != "" {
				t.Errorf("sent once, nothing on stdout: sends=%d out=%q", len(calls.ops), out)
			}
			if !strings.Contains(err.Error(), askReadBack2919) {
				t.Errorf("the read-back is the whole reworded sentence:\nwant it to contain: %s\ngot: %v", askReadBack2919, err)
			}
		})
	}
}

// The ask help says exit 7 is an unknown outcome that is not retry-safe: the
// question may exist, and asking again can create a second one.
func TestQuestionAskHelpSaysExitSevenIsNotRetrySafe2919(t *testing.T) {
	help := strings.Join(strings.Fields(cmdHelpOut(t, "question", "ask")), " ")
	for _, want := range []string{
		"Exit 7",
		"may have been created",
		"not retry-safe",
		"a second question",
		"look for your title",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("ask help lacks %q:\n%s", want, help)
		}
	}
	for _, banned := range []string{"cor:", "hadron-server#", "hadron-cli#"} {
		if strings.Contains(help, banned) {
			t.Errorf("help carries no %q:\n%s", banned, help)
		}
	}
}
