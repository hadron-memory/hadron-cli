package cmdutil

// Helpers shared by the connection commands (`slack connect|disconnect`,
// `telegram connect|disconnect`; cli#2968). The token handling and the error
// redaction are security-relevant, so they live once: a hardening of one
// command reaches the other. Each command still owns its own wording and its
// own server-specific error handling (telegramErrorCode is telegram-only).

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// FinishConnectError is the final error of a failed connect. The server's text
// is untrusted everywhere it can reach the operator, so the finished error is
// cut from its raw chain and every rendered string (message, server code,
// internal fault detail) has each submitted secret removed, in every spelling
// the shared redactor knows. The caller builds base (its own recovery text)
// BEFORE this, so an echo never costs the operator the unknown-outcome notice;
// the exit class and a clean server code survive.
func FinishConnectError(raw, base error, secrets []string) error {
	return api.Detached(api.WithServerCode(base, raw), func(s string) string { return api.RedactSecretText(s, secrets) })
}

// HasControlOrFormatCharacter reports whether s carries a Unicode control or
// format character (an id with ESC/CSI or a newline would reach the terminal
// raw once it is no longer Go-quoted), or a line/paragraph separator
// (U+2028/U+2029: Zl/Zp are not Cc, but some terminals and clipboards treat
// them as line breaks — the same line-forgery the guard exists to stop). Shell
// quoting is a separate step.
func HasControlOrFormatCharacter(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)
	}) >= 0
}

// ConnectionStatusSuggestion is the closing sentence of a disconnect message:
// how to check the outcome. The id the operator typed is never repeated
// (#2898 policy), so it names the listing, which runs as printed; a command in
// a message is always its last line. command is the group's name ("slack",
// "telegram").
func ConnectionStatusSuggestion(command string) string {
	return fmt.Sprintf("The listing is paged, so follow its --offset hint before concluding it is gone. Check whether it still exists by listing connections with:\n  hadron %s status", command)
}

// ReadConnectionToken reads one token from its file flag. "-" is stdin and is
// refused on a terminal (the token would be typed in the clear); the file
// reader itself never puts the path or the material in an error.
func ReadConnectionToken(f *Factory, source, flag, label, what string) (string, error) {
	if source == "-" && f.IOStreams.IsInputTerminal() {
		return "", exitcode.Newf(exitcode.Usage, "--%s - reads stdin, which is a terminal: pipe the token in, or name a file. Nothing was sent", flag)
	}
	return ReadSecretInput(f.IOStreams, source, flag, label, what)
}
