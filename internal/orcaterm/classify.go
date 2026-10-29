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
}

func held(prompt ts.Prompt, family string) Observation {
	return Observation{Status: ts.Locked, Prompt: prompt, Reason: ts.Unrecognized, Family: family}
}

// Classify judges terminal t showing sc. now resolves the clock time a
// harness prints for its last finished turn.
func Classify(t Terminal, sc *Screen, now time.Time) Observation {
	if !t.Connected {
		return Observation{Status: ts.Dark, Prompt: ts.PromptUnknown, Reason: ts.NoAgent}
	}
	if !sc.usable() {
		return held(ts.PromptUnknown, "")
	}
	shown, id := ScreenAgentFamily(sc), t.Identity()
	if shown == "" && id == "" {
		return Observation{Status: ts.Dark, Prompt: ts.PromptUnknown, Reason: ts.BareShell}
	}
	// A dialog can hide the agent's chrome, so Orca's identity stands in;
	// two disagreeing signals hold.
	family := shown
	if family == "" {
		family = id
	}
	if id != "" && shown != "" && id != shown {
		return held(ts.PromptUnknown, family)
	}
	obs := held(ts.PromptUnknown, family)
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
	return obs
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
