package cmdutil

import (
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ToolChoiceName is the server's tool-choice decision as it came back. The CLI
// reads this decision and never makes one (cor:dmo:050:10, Proposed), so a
// member it does not know is an error to report, never one mapped to a
// default (cli#2020).
func ToolChoiceName(c gen.ChatToolChoice) (string, error) {
	for _, known := range gen.AllChatToolChoice {
		if c == known {
			return string(c), nil
		}
	}
	return "", exitcode.Newf(exitcode.Error, "the server answered an unknown tool choice %q; this hadron cannot interpret it, so it is not shown as a default", string(c))
}

// ToolChoiceText is the one-line text form of a decision; nil means nothing
// resolved, with no reason claimed for it.
func ToolChoiceText(name *string) string {
	if name == nil {
		return "tool choice: not resolved"
	}
	return "tool choice: " + *name
}
