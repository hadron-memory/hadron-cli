package orcaterm

// What a rendered Orca screen positively proves about the agent behind it.
// Every classifier holds on anything it does not recognize: an unknown shape
// is missing evidence, never quiet. Copied from internal/teamrouter's
// screen.go with the regexes kept 1:1 to router.ts at hadron-server
// b8384631, then corrected: the #1968/#1976/#1983/#1985 recap and notice
// rules (final merged form), reason codes instead of screen lines, and the
// R2 composer verdict.

import (
	"regexp"
	"strings"
)

// jsSpace is JavaScript's `\s` class: Go's `\s` is ASCII-only, and both
// agents draw NBSP (Claude's `❯`+U+00A0, Codex's prompt).
const jsSpace = `[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

var (
	jsSpaceRun = regexp.MustCompile(jsSpace + `+`)
	// A column-0 rule: Claude Code's composer box is the region between the
	// last two of these. A typed rule is indented, so it never matches.
	ruleLine   = regexp.MustCompile(`^─{3,}` + jsSpace + `*$`)
	bareClaude = regexp.MustCompile(`^❯` + jsSpace + `*$`)
	leadSpace  = regexp.MustCompile(`^` + jsSpace + `*`)
)

// squash collapses whitespace (NBSP included) to single spaces and trims.
func squash(s string) string {
	return strings.TrimSpace(jsSpaceRun.ReplaceAllString(s, " "))
}

// Normalize removes every whitespace character, so a composer's wrapped
// rendering compares equal to the one-line text that was typed.
func Normalize(s string) string { return jsSpaceRun.ReplaceAllString(s, "") }

func indent(s string) int { return len([]rune(leadSpace.FindString(s))) }

// codexPlaceholders is what an idle, EMPTY Codex composer shows.
var codexPlaceholders = map[string]bool{"Ask Codex to do anything": true}

// claudeEmptyHint is the suffix Claude Code's status line carries only while
// its composer is EMPTY. If Claude Code renames it, every Claude worker
// holds: fail closed.
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
// no typed text (#5473: tui-idle reports idle while a human is typing).
//   - claude: the box between the last two column-0 rules is a bare `❯` plus
//     blank lines, and the ONE nonblank line below it ends with the
//     empty-input hint.
//   - codex: the last column-0 `›` line shows the idle placeholder and every
//     line below it is footer.
//
// Anything unrecognized is NOT empty.
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
		if !codexPlaceholders[squash(strings.TrimPrefix(lines[at], "›"))] {
			return false
		}
		return codexFooterOnly(lines[at+1:])
	}
	return false
}

func codexFooterOnly(lines []string) bool {
	for _, l := range lines {
		s := squash(l)
		if s != "" && s != "? for shortcuts" && !strings.Contains(s, " · ") {
			return false
		}
	}
	return true
}

// Composer is the verdict on the composer: may it be typed into? The
// non-empty values are S5's hold reasons (hadron-server#2021) where one
// applies.
type Composer string

const (
	// ComposerEmpty: positively proven empty. The only state typed into.
	ComposerEmpty Composer = "empty"
	// ComposerOwn: it holds text that starts with our prefix, our own
	// leftover from an earlier send. The caller decides recovery.
	ComposerOwn Composer = "own-leftover"
	// ComposerDraft: it holds anything else, a human's text included. R2:
	// held, never cleared, never stored.
	ComposerDraft Composer = "draft"
	// ComposerQuestion: a question, menu or approval prompt has the screen.
	ComposerQuestion Composer = "question"
	// ComposerUnknown: no recognized composer at all, a shell included.
	ComposerUnknown Composer = "screen-unknown"
)

// questionLine recognizes a question, menu or approval prompt's own lines: a
// numbered option under a selection cursor, and the dialogs' questions.
// PROVISIONAL (cli#907): written from the agents' documented dialogs, owed
// verbatim captures from a scratch terminal before APPROVE. It is consulted
// only when the composer is not proven empty, so a false match costs a hold
// reason's name, never a send.
var questionLine = []*regexp.Regexp{
	regexp.MustCompile(`^[❯›>]` + jsSpace + `*\d+\.` + jsSpace),
	regexp.MustCompile(`(?i)^do you want to `),
	regexp.MustCompile(`(?i)^would you like to `),
	regexp.MustCompile(`(?i)^allow (this|command|the following)`),
	regexp.MustCompile(`(?i)^(approve|press enter to confirm|esc to cancel)`),
}

func isQuestion(lines []string) bool {
	for _, l := range lines[max(0, len(lines)-16):] {
		s := squash(l)
		for _, re := range questionLine {
			if re.MatchString(s) {
				return true
			}
		}
	}
	return false
}

// ReadComposer judges the composer under R2. leftover is the composer's own
// text, returned only with ComposerOwn so the caller can compare it with
// what it is about to send; it must never be logged or reported.
//
// Order matters. A draft is judged first and for BOTH families: the router
// let a Claude screen that proves the composer empty refute Orca's draft
// (#6736), and R2 inverts that. Any draft that is not ours holds, so a ghost
// draft now reaches a human through S5's stuck alarm instead of being typed
// over.
func ReadComposer(sc *Screen, family, ownPrefix string) (state Composer, leftover string) {
	if !sc.usable() {
		return ComposerUnknown, ""
	}
	prefix := Normalize(ownPrefix)
	if sc.Draft != "" {
		if prefix != "" && strings.HasPrefix(Normalize(sc.Draft), prefix) {
			return ComposerOwn, sc.Draft
		}
		return ComposerDraft, ""
	}
	if PromptLineIsEmpty(sc.Tail, family) {
		return ComposerEmpty, ""
	}
	if isQuestion(sc.Tail) {
		return ComposerQuestion, ""
	}
	text, ok := typedText(sc.Tail, family)
	if !ok {
		return ComposerUnknown, ""
	}
	if prefix != "" && strings.HasPrefix(Normalize(text), prefix) {
		return ComposerOwn, text
	}
	return ComposerDraft, ""
}

// typedText is what a recognized composer shows typed into it: Claude's box
// (the `❯` line plus its continuation lines, between the last two rules,
// with the hint line below gone), or Codex's last `›` line with footer only
// below it. ok is false when no composer is recognized.
func typedText(lines []string, family string) (string, bool) {
	switch family {
	case "claude":
		rules := ruleIndexes(lines)
		if len(rules) < 2 {
			return "", false
		}
		box := lines[rules[len(rules)-2]+1 : rules[len(rules)-1]]
		if len(box) == 0 || !strings.HasPrefix(box[0], "❯") {
			return "", false
		}
		parts := []string{strings.TrimPrefix(box[0], "❯")}
		parts = append(parts, box[1:]...)
		text := squash(strings.Join(parts, " "))
		return text, text != ""
	case "codex":
		at := lastPromptLine(lines)
		if at < 0 || !codexFooterOnly(lines[at+1:]) {
			return "", false
		}
		text := squash(strings.TrimPrefix(lines[at], "›"))
		if text == "" || codexPlaceholders[text] {
			return "", false
		}
		return text, true
	}
	return "", false
}

// Liveness is what the window above the prompt proves.
type Liveness string

const (
	Busy    Liveness = "busy"
	Idle    Liveness = "idle"
	Unknown Liveness = "unknown"
)

var (
	spinnerParen  = regexp.MustCompile(`[（(]\d`)
	spinnerClock  = regexp.MustCompile(`\d\s*[mh]?\s*\d*s`)
	spinnerDone   = regexp.MustCompile(`(?i)done`)
	spinnerWorked = regexp.MustCompile(`(?i)worked for`)
	callingTool   = regexp.MustCompile(`(?i)^calling [a-z]`)
)

// spinnerTimer is a parenthesized elapsed timer: a running spinner.
func spinnerTimer(s string) bool {
	return spinnerParen.MatchString(s) && spinnerClock.MatchString(s) && !spinnerDone.MatchString(s) && !spinnerWorked.MatchString(s)
}

// idleMarkers POSITIVELY prove the agent is not working: a completed turn,
// a fresh launch's chrome, or (#1983) the content-suppression notice header,
// which is a suppressed turn's only completion evidence (it draws no
// footer). Its BODY lines are never markers (noticeBody).
var idleMarkers = []*regexp.Regexp{
	regexp.MustCompile(`· done`), regexp.MustCompile(`^⏺ Done`), regexp.MustCompile(`^Done\.`),
	regexp.MustCompile(`^✻ \w+ for \d+[ms]`), regexp.MustCompile(`^✻ Compacted while idle`),
	regexp.MustCompile(`/clear to save`), regexp.MustCompile(`^new task\? /clear`), regexp.MustCompile(`recap:`), regexp.MustCompile(`Share Claude Code`),
	regexp.MustCompile(`Claude Code v\d`), regexp.MustCompile(`glm-[\d.]`),
	regexp.MustCompile(`OpenAI Codex \(v`), regexp.MustCompile(`Update available!`), regexp.MustCompile(`Worked for `),
	regexp.MustCompile(`Update installed`),
	regexp.MustCompile(`ⓘ This content can’t be shown`),
}

var (
	weakEcho      = regexp.MustCompile(`^❯ `)
	weakBanner    = regexp.MustCompile(`^[▐▛█▝▜▀│╭╮╰╯ ]*[~❯]`)
	weakPerms     = regexp.MustCompile(`^permissions: `)
	weakCollapsed = regexp.MustCompile(`^\+ (Show details|\d+ lines)`)
)

// isWeakChrome may surround a strong marker without explaining it away; it
// never substitutes for one. #1983 adds the collapsed-output indicators.
func isWeakChrome(s string) bool {
	return weakEcho.MatchString(s) || weakBanner.MatchString(s) || weakPerms.MatchString(s) || weakCollapsed.MatchString(s)
}

var (
	recapHeader  = regexp.MustCompile(`^(` + jsSpace + `*)(?:↳ Recap:|※ recap:)`)
	noticeHeader = regexp.MustCompile(`^(` + jsSpace + `*)ⓘ This content can’t be shown`)
	// noticeBody: the suppression notice box's body shapes, on the squashed
	// line. Never standalone evidence: they count only as contiguous members
	// of an identified notice block (#1985 round 2, Ada's #12876 ruling).
	noticeBody = []*regexp.Regexp{
		regexp.MustCompile(`^We take extra care`), regexp.MustCompile(`^authorized security work`), regexp.MustCompile(`^get broader access\.`),
		regexp.MustCompile(`^Learn more: https:`), regexp.MustCompile(`^Apply for Daybreak:`),
		regexp.MustCompile(`^https://openai\.com/form/`), regexp.MustCompile(`^openai\.com/form/`),
	}
)

// recapBlockSkip flags the lines of a completed turn's own summary chrome,
// positionally newer than the marker that proves the turn completed. The
// membership is structural (router.ts recapBlockSkip at b8384631):
//   - a recap header is skipped, and so is each following RAW line indented
//     STRICTLY deeper than it; the first line at or below its indent ends the
//     block and is judged (#1976, tightened by #1985's blind P2);
//   - a notice header is NOT skipped (it is the evidence), but the contiguous
//     notice-body shapes below it are, so the scan reaches it; any other line
//     ends the block (#12876).
//
// A deep indent with no header above it is never skipped.
func recapBlockSkip(raw []string) []bool {
	skip := make([]bool, len(raw))
	for j := range raw {
		if m := recapHeader.FindStringSubmatch(raw[j]); m != nil {
			skip[j] = true
			base := len([]rune(m[1]))
			for k := j + 1; k < len(raw); k++ {
				if indent(raw[k]) > base {
					skip[k] = true
				} else {
					break
				}
			}
			continue
		}
		if noticeHeader.MatchString(raw[j]) {
			for k := j + 1; k < len(raw); k++ {
				if !matchesAny(noticeBody, squash(raw[k])) {
					break
				}
				skip[k] = true
			}
		}
	}
	return skip
}

func matchesAny(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// IdleProof judges the window directly above the prompt chrome: Busy (a
// running-turn line), Idle (the newest line that is neither weak chrome nor
// a completed turn's recap/notice tail is a strong idle marker), or Unknown.
// The reason is a code: the line that decided is never returned.
func IdleProof(lines []string, family string) (Liveness, string) {
	var at int
	switch family {
	case "claude":
		rules := ruleIndexes(lines)
		if len(rules) < 2 {
			return Unknown, "no-prompt-box"
		}
		at = rules[len(rules)-2]
	case "codex":
		at = lastPromptLine(lines)
		if at < 0 {
			return Unknown, "no-prompt-line"
		}
	default:
		return Unknown, "unknown-family"
	}
	// Depth 16 (#1968): a completed turn's summary tail must not push its
	// footer out of evidence. Busy detection does not trade for it: both
	// agents pin the running-turn line directly above the prompt.
	var raw []string
	for _, l := range lines[max(0, at-16):at] {
		if strings.TrimSpace(l) != "" {
			raw = append(raw, l)
		}
	}
	window := make([]string, len(raw))
	for i, l := range raw {
		window[i] = squash(l)
	}
	for _, l := range window {
		if strings.Contains(l, "to interrupt") || strings.Contains(l, "run in background") ||
			strings.Contains(l, "Working (") || callingTool.MatchString(l) || spinnerTimer(l) {
			return Busy, "running-turn"
		}
	}
	skip := recapBlockSkip(raw)
	i := len(window) - 1
	for i >= 0 && (isWeakChrome(window[i]) || skip[i]) {
		i--
	}
	if i < 0 {
		return Unknown, "no-evidence"
	}
	if matchesAny(idleMarkers, window[i]) {
		return Idle, "completed-turn"
	}
	return Unknown, "unrecognized-chrome"
}

// bannerChrome: banner/completion markers anchor to the START of a line,
// optionally behind banner chrome, never after arbitrary content (#1823).
const bannerChrome = `[│╭╮╰╯─▐▛█▝▜▀ >_› ]*`

var (
	claudeBanner = regexp.MustCompile(`^` + bannerChrome + `(Claude Code v\d|glm-[\d.]|/clear to save|※ recap:)`)
	codexBanner  = regexp.MustCompile(`^` + bannerChrome + `(OpenAI Codex \(v|Worked for |Ask Codex to do anything)`)
)

// ScreenAgentFamily is the agent family a rendered screen POSITIVELY shows,
// or "": a shell, or both families' chrome at once (hold, never guess).
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
