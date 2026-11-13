//go:build !windows

package hostpoller

import "regexp"

// redactKnownTokens renders every known credential token inside one argument
// as <redacted> and leaves the rest of the argument readable (cli#1972, N-19,
// the coordinator's ruling). It matches the token wherever it sits: the whole
// argument, after '=' or ':', glued to a flag, or inside a URL.
//
// The list is fixed and deliberately narrow: Hadron's own tokens first
// (hdr_user_, hdr_app_), the webhook platform token (a JWT in the `hpt=`
// query parameter, and the secret path segment of /hooks/<secret>/<name>),
// then GitHub, GitLab, Slack, sk-/sk_live_/sk_test_ and AWS access-key ids.
// A prefix counts only when a minimum body follows it, so a prefix-alike in
// prose or a short path segment (sk-, ghp_) does not fire. Over-redaction is
// the acceptable direction.
//
// Non-goals, deliberately: entropy or shape heuristics and general secret
// scanning. A credential that carries neither a keyword (credentialName,
// credentialHeader) nor a prefix listed here still renders verbatim.
var knownTokenPatterns = []*regexp.Regexp{
	// Prefix tokens. Group 1 is the boundary before the token (start of
	// argument or any character that cannot continue a word), kept as is.
	regexp.MustCompile(`(^|[^A-Za-z0-9])(?:` +
		`hdr_(?:user|app)_[A-Za-z0-9_-]{16,}` +
		`|gh[pousr]_[A-Za-z0-9]{20,}` +
		`|github_pat_[A-Za-z0-9_]{20,}` +
		`|glpat-[A-Za-z0-9_-]{20,}` +
		`|xox[abposr]-[A-Za-z0-9-]{10,}` +
		`|sk_(?:live|test)_[A-Za-z0-9]{16,}` +
		`|sk-[A-Za-z0-9_-]{20,}` +
		`|(?:AKIA|ASIA)[0-9A-Z]{16}` +
		`)`),
	// The webhook platform token: the hpt query value.
	regexp.MustCompile(`([?&]hpt=)[^&#\s"']+`),
	// The webhook path secret in a URL shaped /hooks/<secret>/<name>.
	regexp.MustCompile(`(://[^/\s"']*/hooks/)[^/?#\s"']+`),
}

func redactKnownTokens(arg string) string {
	for _, re := range knownTokenPatterns {
		arg = re.ReplaceAllString(arg, "${1}<redacted>")
	}
	return arg
}
