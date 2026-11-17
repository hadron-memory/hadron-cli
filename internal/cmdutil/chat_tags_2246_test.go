package cmdutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChatTagHelpWrap2246(t *testing.T) {
	for _, line := range strings.Split(ChatTagHelp, "\n") {
		if utf8.RuneCountInString(line) > 80 {
			t.Fatalf("shared help line exceeds 80 columns: %s", line)
		}
	}
}
