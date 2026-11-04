package cmdutil

import "strings"

// ShellQuote quotes s as one POSIX shell word for a copy-pasteable command in
// a message. A word made only of characters no shell treats specially is left
// bare; anything else is single-quoted, where every character but ' is
// literal. Go's %q is not a substitute: shell double quotes still expand $,
// backticks and backslashes, so a %q label changes when it is pasted.
func ShellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789:._/@-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
