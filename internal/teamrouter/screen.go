package teamrouter

// The screen half of the two-signal discipline: what a rendered Orca screen
// positively proves about the agent behind it. Every classifier here holds on
// anything it does not recognize — an unknown shape is missing evidence, never
// quiet. Ported from router.ts (promptLineIsEmpty, idleProofAbovePrompt,
// screenAgentFamily, promptIsEmptyFromTerm) with the regexes kept 1:1.

import (
	"regexp"
	"strings"
)

// jsSpace is JavaScript's `\s` class. Go's `\s` is ASCII-only, and the TS
// router collapses NBSP and the other Unicode spaces too (Codex draws its
// prompt as `›` + U+00A0), so the port spells the JS class out.
const jsSpace = `[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

var (
	jsSpaceRun = regexp.MustCompile(jsSpace + `+`)
	// A column-0 rule: Claude Code's composer box is the region between the
	// last two of these. A typed rule is indented, so it never matches.
	ruleLine   = regexp.MustCompile(`^─{3,}` + jsSpace + `*$`)
	bareClaude = regexp.MustCompile(`^❯` + jsSpace + `*$`)
)

// squash collapses whitespace (NBSP included) to single spaces and trims.
func squash(s string) string {
	return strings.TrimSpace(jsSpaceRun.ReplaceAllString(s, " "))
}

// codexPlaceholders is what an idle, EMPTY Codex composer shows. An unknown
// placeholder reads as typed text and holds the nudge: the safe direction.
var codexPlaceholders = map[string]bool{"Ask Codex to do anything": true}

// claudeEmptyHint is the suffix Claude Code's status line carries only while
// its composer is EMPTY — typing anything, a single space included, removes
// it. If Claude Code renames it, every Claude worker holds: fail closed.
const claudeEmptyHint = "· ← for agents"

func ruleIndexes(lines []string) []int {
	var out []int
	for i, l := range lines {
		if ruleLine.MatchString(l) {
			out = append(out, i)
		}
	}
	return out
}

// lastPromptLine is the index of the last column-0 `›` line, or -1.
func lastPromptLine(lines []string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "›") {
			return i
		}
	}
	return -1
}

// PromptLineIsEmpty is true only on positive proof the agent's prompt holds
// no typed text (#5473: tui-idle reports idle while a HUMAN is typing). Both
// agents draw their real chrome at column 0 and indent anything typed onto a
// continuation line, so only column-0 lines count as chrome.
//   - claude: the box between the last two column-0 rules is a bare `❯` plus
//     blank lines, and the ONE nonblank line below it — the terminal's last
//     content — ends with the empty-input hint (typed whitespace renders as
//     nothing; trailing content means the box is history, #1823).
//   - codex: the last column-0 `›` line shows the idle placeholder and every
//     line below it is footer.
//
// Anything unrecognized — no box, no marker, an unknown family — is NOT empty.
func PromptLineIsEmpty(lines []string, family string) bool {
	switch family {
	case "claude":
		rules := ruleIndexes(lines)
		if len(rules) < 2 {
			return false
		}
		closing := rules[len(rules)-1]
		box := lines[rules[len(rules)-2]+1 : closing]
		if len(box) == 0 || !bareClaude.MatchString(box[0]) {
			return false
		}
		for _, l := range box[1:] {
			if squash(l) != "" {
				return false
			}
		}
		var below []string
		for _, l := range lines[closing+1:] {
			if s := squash(l); s != "" {
				below = append(below, s)
			}
		}
		return len(below) == 1 && strings.HasSuffix(below[0], claudeEmptyHint)
	case "codex":
		at := lastPromptLine(lines)
		if at < 0 {
			return false
		}
		// Only the placeholder proves empty: a BARE `›` means the composer
		// holds something it does not draw (whitespace, or text in the draft).
		if !codexPlaceholders[squash(strings.TrimPrefix(lines[at], "›"))] {
			return false
		}
		for _, l := range lines[at+1:] {
			s := squash(l)
			if s != "" && s != "? for shortcuts" && !strings.Contains(s, " · ") {
				return false
			}
		}
		return true
	}
	return false
}

// Screen is one `orca terminal read --screen` result: the rendered tail, the
// optional draft field, and the source (only "screen" is a full frame — the
// default stream read returns repaint fragments, useless for shape checks).
type Screen struct {
	Source string
	Tail   []string
	Draft  string
}

func (sc *Screen) usable() bool { return sc != nil && sc.Source == "screen" && sc.Tail != nil }

// promptIsEmptyFromScreen reports whether the rendered prompt is empty now,
// with the reason when it is not — or, when it is, a non-empty note if the
// verdict DISCARDED a stale draft. Never carries the text itself.
//
// A Codex draft is authoritative (its composer renders empty while text is
// pending). Claude Code draws typed text on its `❯` line, so a screen that
// proves the composer empty REFUTES the draft: Orca's draft has been
// observed carrying text no client showed, pinning workers for hours (#6736).
func promptIsEmptyFromScreen(sc *Screen, family string) (bool, string) {
	if !sc.usable() {
		return false, "screen unreadable"
	}
	if sc.Draft != "" {
		if family != "claude" || !PromptLineIsEmpty(sc.Tail, family) {
			return false, "text in the composer draft"
		}
		return true, "stale draft (screen proves the composer empty)"
	}
	if PromptLineIsEmpty(sc.Tail, family) {
		return true, ""
	}
	return false, "text in the prompt"
}

var (
	spinnerParen  = regexp.MustCompile(`[（(]\d`)
	spinnerClock  = regexp.MustCompile(`\d\s*[mh]?\s*\d*s`)
	spinnerDone   = regexp.MustCompile(`(?i)done`)
	spinnerWorked = regexp.MustCompile(`(?i)worked for`)
	callingTool   = regexp.MustCompile(`(?i)^calling [a-z]`)
)

// spinnerTimer: the line carries a parenthesized elapsed timer (a running spinner).
func spinnerTimer(s string) bool {
	return spinnerParen.MatchString(s) && spinnerClock.MatchString(s) && !spinnerDone.MatchString(s) && !spinnerWorked.MatchString(s)
}

// idleMarkers POSITIVELY prove the agent is not working: a completed turn, or
// the chrome a freshly launched terminal draws (live evidence 2026-10-10).
var idleMarkers = []*regexp.Regexp{
	regexp.MustCompile(`· done`), regexp.MustCompile(`^⏺ Done`), regexp.MustCompile(`^Done\.`),
	regexp.MustCompile(`^✻ \w+ for \d+[ms]`), regexp.MustCompile(`^✻ Compacted while idle`),
	regexp.MustCompile(`/clear to save`), regexp.MustCompile(`^new task\? /clear`), regexp.MustCompile(`recap:`), regexp.MustCompile(`Share Claude Code`),
	regexp.MustCompile(`Claude Code v\d`), regexp.MustCompile(`glm-[\d.]`),
	regexp.MustCompile(`OpenAI Codex \(v`), regexp.MustCompile(`Update available!`), regexp.MustCompile(`Worked for `),
	regexp.MustCompile(`Update installed`),
}

var (
	weakEcho   = regexp.MustCompile(`^❯ `)
	weakBanner = regexp.MustCompile(`^[▐▛█▝▜▀│╭╮╰╯ ]*[~❯]`)
	weakPerms  = regexp.MustCompile(`^permissions: `)
)

// isWeakChrome is chrome that may surround a strong marker without explaining
// it away; it never substitutes for one.
func isWeakChrome(s string) bool {
	return weakEcho.MatchString(s) || weakBanner.MatchString(s) || weakPerms.MatchString(s)
}

// idleProofAbovePrompt judges the window directly above the prompt chrome:
// "busy" (a running-turn line), "idle" (the NEWEST non-weak line is a strong
// idle marker), or "unknown" (anything else — missing evidence, not quiet).
// Both agents pin their running-turn line directly above the prompt.
func idleProofAbovePrompt(lines []string, family string) (string, string) {
	var at int
	switch family {
	case "claude":
		rules := ruleIndexes(lines)
		if len(rules) < 2 {
			return "unknown", "no prompt box"
		}
		at = rules[len(rules)-2]
	case "codex":
		at = lastPromptLine(lines)
		if at < 0 {
			return "unknown", "no prompt line"
		}
	default:
		return "unknown", "unknown agent " + family
	}
	var window []string
	for _, l := range lines[max(0, at-8):at] {
		if s := squash(l); s != "" {
			window = append(window, s)
		}
	}
	for _, l := range window {
		if strings.Contains(l, "to interrupt") || strings.Contains(l, "run in background") ||
			strings.Contains(l, "Working (") || callingTool.MatchString(l) || spinnerTimer(l) {
			return "busy", l
		}
	}
	i := len(window) - 1
	for i >= 0 && isWeakChrome(window[i]) {
		i--
	}
	if i < 0 {
		return "unknown", "no positive liveness evidence above the prompt"
	}
	for _, re := range idleMarkers {
		if re.MatchString(window[i]) {
			return "idle", "completed or fresh-launch chrome"
		}
	}
	return "unknown", "unrecognized chrome above the prompt"
}

// bannerChrome: banner/completion markers anchor to the START of a line,
// optionally behind banner chrome — never after arbitrary content, so
// `orca$ echo "Worked for 33s"` is output, not Codex chrome (#1823 blind P2).
const bannerChrome = `[│╭╮╰╯─▐▛█▝▜▀ >_› ]*`

var (
	claudeBanner = regexp.MustCompile(`^` + bannerChrome + `(Claude Code v\d|glm-[\d.]|/clear to save|※ recap:)`)
	codexBanner  = regexp.MustCompile(`^` + bannerChrome + `(OpenAI Codex \(v|Worked for |Ask Codex to do anything)`)
)

// ScreenAgentFamily is the agent family a rendered screen POSITIVELY shows,
// or "" — the identity-flap fallback's evidence (#1823). Only the agents' own
// chrome counts: anchored banner lines, column-0 `✻`/`⏺`/`permissions:`,
// Claude's pair of rules, and Codex's `›` only WITH content after it (a bare
// glyph is a shell theme as easily as a composer). Both families on one
// screen reads "": hold, never guess.
func ScreenAgentFamily(sc *Screen) string {
	if !sc.usable() {
		return ""
	}
	claude := len(ruleIndexes(sc.Tail)) >= 2
	codex := false
	for _, l := range sc.Tail {
		if strings.HasPrefix(l, "✻ ") || strings.HasPrefix(l, "⏺ ") || claudeBanner.MatchString(l) {
			claude = true
		}
		if (strings.HasPrefix(l, "›") && squash(strings.TrimPrefix(l, "›")) != "") ||
			strings.HasPrefix(l, "permissions: ") || codexBanner.MatchString(l) {
			codex = true
		}
	}
	switch {
	case claude && !codex:
		return "claude"
	case codex && !claude:
		return "codex"
	}
	return ""
}
