package orcaterm

// The terminal half of `hadron orca terminal status` (cli#1187): one screen
// and one Orca terminal row in, a status out. This is the ONE classifier;
// the screen rules it stands on (ReadComposer, IdleProof, ScreenAgentFamily)
// are the ones delivery and guardedpush already run, so there is no second
// copy of them. Fail closed throughout: anything the rules do not positively
// recognize is locked/unrecognized, never idle, so a caller that sends only on
// idle can never type into a menu.
//
// Like the rest of the package it never returns screen text: an Observation
// carries codes only.

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	ts "github.com/hadron-memory/hadron-cli/internal/terminalstatus"
)

// Observation is what one terminal's screen proves. The dark rule's
// due-wake evidence is the caller's: only the terminal-level dark reasons
// (a shell, no live agent) are decided here.
type Observation struct {
	Status     ts.Status
	Prompt     ts.Prompt
	Reason     ts.Reason // "" when none
	Family     string
	LastTurnAt *time.Time
	// ResetAt is the reset time of a rate_limited screen in UTC (cli#2676), nil
	// when the screen shows none. ResetAssumedLocal: it carried no UTC offset,
	// so it was read as now's (the host's) local time.
	ResetAt           *time.Time
	ResetAssumedLocal bool
	// Detail is LOCAL only: for locked/unrecognized it names the screen line
	// that could not be matched (cli#1856). Screen text, so it must never
	// reach a report or a log; terminalstatus.Result.Detail is never marshalled.
	Detail string
}

func held(family string) Observation {
	return Observation{Status: ts.Locked, Prompt: ts.PromptUnknown, Reason: ts.Unrecognized, Family: family}
}

// Classify judges terminal t showing sc. now resolves the clock time a
// harness prints for its last finished turn.
func Classify(t Terminal, sc *Screen, now time.Time) Observation {
	if !t.Connected {
		return Observation{Status: ts.Dark, Prompt: ts.PromptUnknown, Reason: ts.NoAgent}
	}
	// Orca has no rendered screen for this terminal: the screen is missing, not
	// unmatched (cli#2677). Anything else unusable stays unrecognized below.
	if sc != nil && sc.Source == SourceScreenUnavailable {
		return Observation{Status: ts.Locked, Prompt: ts.PromptUnknown, Reason: ts.NoScreen}
	}
	// A terminal that cannot be typed into is never sendable.
	if !t.Writable || !sc.usable() {
		return held("")
	}
	// A usage-limit screen is its own state, and it outranks the composer
	// reads below: the harness may draw an empty composer under the limit line.
	if limited, resetAt, assumed := usageLimit(sc.Tail, now); limited {
		return Observation{Status: ts.Locked, Prompt: ts.PromptUnknown, Reason: ts.RateLimited,
			Family: ScreenAgentFamily(sc), ResetAt: resetAt, ResetAssumedLocal: assumed}
	}
	// Both families' chrome at once is ambiguity: Orca's identity must not
	// break the tie.
	if c, x := screenFamilies(sc); c && x {
		return held("")
	}
	shown, id := ScreenAgentFamily(sc), t.Identity()
	if shown == "" && id == "" {
		// A shell needs POSITIVE evidence, a shell prompt on the last line:
		// output nothing recognizes is not proof of a shell.
		if endsInShellPrompt(sc.Tail) {
			return Observation{Status: ts.Dark, Prompt: ts.PromptUnknown, Reason: ts.BareShell}
		}
		return held("")
	}
	// A dialog can hide the agent's chrome, so Orca's identity stands in;
	// two disagreeing signals hold.
	family := shown
	if family == "" {
		family = id
	}
	if id != "" && shown != "" && id != shown {
		return held(family)
	}
	obs := held(family)
	state, _ := ReadComposer(sc, family)
	live, _ := IdleProof(sc.Tail, family)
	switch state {
	case ComposerQuestion:
		obs.Prompt, obs.Reason, obs.Status = ts.PromptMenu, ts.MenuOrQuestion, ts.Locked
		return obs
	case ComposerEmpty:
		obs.Prompt = ts.PromptEmpty
		switch live {
		case Busy:
			obs.Status, obs.Reason = ts.Busy, ""
		case Idle:
			obs.Status, obs.Reason = ts.Idle, ""
		}
	case ComposerOwn, ComposerDraft:
		obs.Prompt = ts.PromptNotEmpty
		if live == Busy {
			obs.Status, obs.Reason = ts.Busy, ""
		}
	}
	obs.LastTurnAt = LastTurnAt(sc, family, now)
	if obs.Reason == ts.Unrecognized {
		if line := UnmatchedLine(sc.Tail, family); line != "" {
			obs.Detail = "could not match: " + line
		}
	}
	return obs
}

// shellPrompt: "user@host:~/dir$", "root@host:/# ", "host% ".
var shellPrompt = regexp.MustCompile(`^(?:[\w.-]+@[\w.-]+:\S*[$#]|[\w.-]+[%$#])$`)

func endsInShellPrompt(tail []string) bool {
	for i := len(tail) - 1; i >= 0; i-- {
		if s := squash(tail[i]); s != "" {
			return shellPrompt.MatchString(s)
		}
	}
	return false
}

// The clock time each harness draws for a finished turn: Claude Code's
// "✻ Worked for 1m 10s · done 3:54 PM", Codex's "Worked for 33s • 16:32" (or
// "• 3:49 AM"). Time of day only: LastTurnAt supplies the date.
var (
	claudeTurnStamp = regexp.MustCompile(`· done (\d{1,2}):(\d{2})(?: ?([AaPp][Mm]))?\s*$`)
	codexTurnStamp  = regexp.MustCompile(`Worked for [^•]*• (\d{1,2}):(\d{2})(?: ?([AaPp][Mm]))?\s*$`)
)

// LastTurnAt is when the harness shows its last turn finished, or nil when
// the screen does not show one: never guessed. The per-harness adapter is the
// regexp for the family; the newest stamp on screen (the lowest) wins. The
// stamp carries no date, so it is the most recent such time of day that is not
// in the future of now, in now's zone.
func LastTurnAt(sc *Screen, family string, now time.Time) *time.Time {
	var re *regexp.Regexp
	switch family {
	case "claude":
		re = claudeTurnStamp
	case "codex":
		re = codexTurnStamp
	default:
		return nil
	}
	if !sc.usable() {
		return nil
	}
	for i := len(sc.Tail) - 1; i >= 0; i-- {
		m := re.FindStringSubmatch(squash(sc.Tail[i]))
		if m == nil {
			continue
		}
		h, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		if m[3] != "" {
			if h < 1 || h > 12 {
				continue
			}
			h %= 12
			if strings.EqualFold(m[3], "pm") {
				h += 12
			}
		}
		if h > 23 || min > 59 {
			continue
		}
		at := time.Date(now.Year(), now.Month(), now.Day(), h, min, 0, 0, now.Location())
		if at.After(now) {
			at = at.AddDate(0, 0, -1)
		}
		return &at
	}
	return nil
}

// A usage-limit line, as a harness draws it: "429 Usage limit reached for 5
// hour ... reset 2026-11-20 16:45:32 (UTC+8)". Only the LAST usageLimitWindow
// non-blank lines count, so a limit line that has scrolled away above a live
// composer does not hold an agent that has resumed.
const usageLimitWindow = 8

var (
	// The phrase alone is not the banner: a transcript that merely says "usage
	// limit reached" must not hold an idle agent. A line counts when the phrase
	// carries the banner's "429" prefix or its reset clause (the line captured
	// in the cli#2676 report has both), so a variant without one still reads.
	usageLimitPhrase = regexp.MustCompile(`(?i)usage limit reached`)
	usageLimitLine   = regexp.MustCompile(`(?i)\b429 usage limit reached`)
	// The reset time: "reset YYYY-MM-DD HH:MM:SS", then an optional "(UTC+8)",
	// "(UTC-5)" or "(UTC+5:30)".
	usageLimitReset = regexp.MustCompile(`(?i)\breset (\d{4})-(\d{2})-(\d{2}) (\d{2}):(\d{2}):(\d{2})(?: \(UTC(?:([+-])(\d{1,2})(?::?(\d{2}))?)?\))?`)
)

// usageLimit reports whether the screen shows a usage-limit line and, when it
// also shows a parsable reset time, that time in UTC. A time with no offset is
// read in now's location (the host's local time) and assumedLocal says so; one
// that does not parse (an impossible date, an offset beyond +-14h) gives nil:
// never guessed.
func usageLimit(tail []string, now time.Time) (limited bool, resetAt *time.Time, assumedLocal bool) {
	seen := 0
	for i := len(tail) - 1; i >= 0 && seen < usageLimitWindow; i-- {
		s := squash(tail[i])
		if s == "" {
			continue
		}
		seen++
		m := usageLimitReset.FindStringSubmatch(s)
		if !usageLimitPhrase.MatchString(s) || (!usageLimitLine.MatchString(s) && m == nil) {
			continue
		}
		if m == nil {
			return true, nil, false
		}
		n := make([]int, 7)
		for j := 1; j <= 6; j++ {
			n[j], _ = strconv.Atoi(m[j])
		}
		loc, assumed := now.Location(), true
		if m[7] != "" {
			oh, _ := strconv.Atoi(m[8])
			om, _ := strconv.Atoi(m[9])
			if oh > 14 || om > 59 {
				return true, nil, false
			}
			secs := oh*3600 + om*60
			if m[7] == "-" {
				secs = -secs
			}
			loc, assumed = time.FixedZone("", secs), false
		} else if strings.Contains(strings.ToUpper(m[0]), "(UTC") {
			loc, assumed = time.UTC, false
		}
		at := time.Date(n[1], time.Month(n[2]), n[3], n[4], n[5], n[6], 0, loc)
		// time.Date normalises an impossible field (month 13, hour 99): reject it.
		if at.Year() != n[1] || int(at.Month()) != n[2] || at.Day() != n[3] || at.Hour() != n[4] || at.Minute() != n[5] || at.Second() != n[6] {
			return true, nil, false
		}
		u := at.UTC()
		// A reset already past means the limit is lifted: a banner left in the
		// scrollback must not hold the agent's new turn.
		if !u.After(now) {
			return false, nil, false
		}
		return true, &u, assumed
	}
	return false, nil, false
}
