package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2970: a --ref label is at most 120 characters. The server counts label.length, UTF-16 code
// units, and refuses a longer one with BAD_USER_INPUT "Reference N: a label is at most 120
// characters." (exit 2, nothing written). The CLI says the cap in every --ref help and refuses a
// longer label BEFORE the request with the same message, counting code units exactly like the server
// so it never refuses what the server accepts.

const refLabelCapMessage2970 = "a label is at most 120 characters."

func disposeWithLabel2970(t *testing.T, label string) (calls int, err error) {
	t.Helper()
	r := taskResponses()
	r["AgentTaskDisposeFinding"] = dispositionResponse1556("fixed", nil)
	c, _, e := runTeamTask(t, r, "team", "task", "dispose-finding", "AT1.2", "F1", "--disposition", "fixed", "--ref", "issue=https://example.invalid/1|"+label)
	return len(*c), e
}

// The cap on the dispose-finding write: 120 goes out, 121 is refused locally (exit 2, nothing sent).
func TestRefLabelOverTheCapIsRefusedBeforeTheRequest2970(t *testing.T) {
	if n, err := disposeWithLabel2970(t, strings.Repeat("a", 120)); err != nil || n != 1 {
		t.Fatalf("a 120-character label goes out: calls=%d err=%v", n, err)
	}
	n, err := disposeWithLabel2970(t, strings.Repeat("a", 121))
	if err == nil || exitCodeFor(err) != exitcode.Usage || n != 0 {
		t.Fatalf("a 121-character label is refused before any request (exit 2): calls=%d exit=%d err=%v", n, exitCodeFor(err), err)
	}
	if !strings.Contains(err.Error(), "Reference 1: "+refLabelCapMessage2970) {
		t.Errorf("the refusal carries the server's wording and the reference position: %v", err)
	}
}

// The count is UTF-16 code units, like the server's label.length: 60 astral-plane characters are
// 120 units (accepted), 61 are 122 units (refused: the same answer the server gives); a plain
// non-BMP-free label counts one unit per character.
func TestRefLabelCountsCodeUnitsLikeTheServer2970(t *testing.T) {
	astral := "\U0001F600" // 2 UTF-16 code units, 1 rune
	if n, err := disposeWithLabel2970(t, strings.Repeat(astral, 60)); err != nil || n != 1 {
		t.Fatalf("60 astral characters (120 units) go out: calls=%d err=%v", n, err)
	}
	n, err := disposeWithLabel2970(t, strings.Repeat(astral, 61))
	if err == nil || exitCodeFor(err) != exitcode.Usage || n != 0 {
		t.Fatalf("61 astral characters (122 units) are refused locally: calls=%d err=%v", n, err)
	}
}

// Every typed --ref surface shares the parser, so the check holds for create, clone and update
// too, and the position names the offending reference (1-based, in order).
func TestRefLabelCapOnEveryTypedRefCommand2970(t *testing.T) {
	long := strings.Repeat("b", 121)
	for name, args := range map[string][]string{
		"create": {"team", "task", "create", "x", "--ref", "issue=https://example.invalid/1", "--ref", "url=https://example.invalid/2|" + long},
		"clone":  {"team", "task", "clone", "AT1.2", "--ref", "issue=https://example.invalid/1", "--ref", "url=https://example.invalid/2|" + long},
		"update": {"team", "task", "update", "AT1.2", "--ref", "issue=https://example.invalid/1", "--ref", "url=https://example.invalid/2|" + long},
	} {
		t.Run(name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, taskResponses(), args...)
			if err == nil || exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
				t.Fatalf("calls=%d exit=%d err=%v", len(*calls), exitCodeFor(err), err)
			}
			if !strings.Contains(err.Error(), "Reference 2: "+refLabelCapMessage2970) {
				t.Errorf("the second reference is named: %v", err)
			}
		})
	}
}

// The caps are documented once and every --ref command says them: label 120 characters, target 512,
// at most 20 references.
func TestRefHelpStatesTheCaps2970(t *testing.T) {
	for _, page := range [][]string{
		{"team", "task", "dispose-finding"}, {"team", "task", "create"}, {"team", "task", "clone"}, {"team", "task", "update"},
	} {
		help := strings.Join(strings.Fields(cmdHelpOut(t, page...)), " ")
		for _, want := range []string{"120 characters", "512", "20 references"} {
			if !strings.Contains(help, want) {
				t.Errorf("%v --help must state the reference caps, lacks %q", page, want)
			}
		}
	}
}
