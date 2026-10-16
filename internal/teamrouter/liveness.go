package teamrouter

// The #1826 liveness pass: a coordinator or lifecycle watch that a passive
// wait can strand. A finished turn with unread NON-mention chat generates no
// mention, so nothing wakes it; flagged workers are nudged to catch up after
// strictly more than CoordinatorWatchMin minutes of continuous idle with the
// chat past their read watermark.
//
// Two layers, deliberately:
//   - a CHEAP per-poll probe (short idle wait + prompt-empty proof) whose only
//     job is the durable clock — any non-idle evidence resets it;
//   - the FULL guard chain — fresh resolution, the busy-wait idle gate, the
//     empty-prompt gate, a fresh head>cursor recompute, the fail-closed
//     recheck — only once the clock is OWED a send. A hold from the chain
//     holds the token exactly like a held mention nudge.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"slices"
)

func (r *router) livenessPass(ctx context.Context, st *State, out *Outcome, flagged []RosterEntry,
	terminals []Terminal, mentionHandles map[string]string, mustOwn func() error) error {
	if len(flagged) == 0 {
		if _, err := os.Stat(st.livenessPath()); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
	}
	liveness := st.Liveness()
	saveAll := func() error {
		if err := st.saveLiveness(liveness); err != nil {
			return routerErrorf("liveness write failed: %v", err)
		}
		return nil
	}
	// A removed flag resets the watch: a re-flagged worker starts fresh.
	pruned := false
	for w := range liveness {
		if !slices.ContainsFunc(flagged, func(e RosterEntry) bool { return e.Worker == w }) {
			delete(liveness, w)
			pruned = true
		}
	}
	if pruned {
		if err := saveAll(); err != nil {
			return err
		}
	}
	if len(flagged) == 0 {
		return nil
	}

	// The mention pass touched some flagged workers: a delivered nudge
	// COLLAPSES the liveness send (one send per wake window, the clock
	// restarting at it); a held or dropped one observed the terminal or the
	// worker reading — interrupted idle, clock reset.
	touched := map[string]bool{}
	nameTouched := func(n string) {
		for _, e := range r.cfg.Roster {
			if e.Name == n {
				touched[e.Worker] = true
			}
		}
	}
	for _, n := range out.Nudged {
		nameTouched(n)
	}
	for n := range out.Held {
		nameTouched(n)
	}
	for _, n := range out.Dropped {
		nameTouched(n)
	}

	notes := st.livenessNotes()
	note := func(e RosterEntry, reason string) error {
		if notes[e.Worker] == reason {
			return nil
		}
		notes[e.Worker] = reason
		if err := st.saveLivenessNotes(notes); err != nil {
			return routerErrorf("liveness notes write failed: %v", err)
		}
		r.log(logPrefix + e.Name + ": liveness: " + reason)
		return nil
	}
	head := r.channelHead(ctx) // one query per poll; fail-closed per worker below

	for _, entry := range flagged {
		rec := liveness[entry.Worker]
		save := func() error {
			liveness[entry.Worker] = rec
			return saveAll()
		}
		resetClock := func() error {
			if rec.IdleSince != nil || rec.Handle != "" {
				rec.IdleSince, rec.Handle = nil, ""
				return save()
			}
			return nil
		}
		hold := func(reason string) error {
			out.Held[entry.Name] = "liveness: " + reason
			return resetClock()
		}
		if touched[entry.Worker] {
			if slices.Contains(out.Nudged, entry.Name) {
				// The mention nudge's own gates proved the terminal idle just
				// now: the episode restarts at that send, ON its terminal.
				t := r.now()
				rec.IdleSince = &t
				if h, ok := mentionHandles[entry.Worker]; ok {
					rec.Handle = h
				}
				if err := save(); err != nil {
					return err
				}
			} else if err := resetClock(); err != nil {
				return err
			}
			continue
		}

		// 1) The cheap probe. provesIdle returns a non-empty reason ONLY on
		// its untracked screen-fallback success, which proves the prompt
		// empty too — so the extra prompt check runs exactly where tui-idle
		// alone was the evidence (a human typing reads idle, #5473).
		handle, family, _, rerr := r.resolveTerminal(ctx, entry, terminals)
		if rerr != nil {
			if err := resetClock(); err != nil { // unresolvable — no episode to measure
				return err
			}
			continue
		}
		idle, via := r.provesIdle(ctx, handle, family, lastOutputOf(terminals, handle), recheckIdleTimeoutMS)
		continuous := idle
		if idle && via == "" {
			continuous, _ = r.promptIsEmpty(ctx, handle, family)
		}
		if !continuous {
			if err := resetClock(); err != nil { // busy, draft, unreadable — interrupted idle
				return err
			}
			continue
		}
		if rec.Handle != handle || rec.IdleSince == nil {
			t := r.now() // first sighting, or a REPLACED terminal
			rec.IdleSince, rec.Handle = &t, handle
			if err := save(); err != nil {
				return err
			}
		}
		// 2) The liveness source, fail-closed on BOTH sides: unknown is no
		// candidate this poll — logged once per reason, the clock kept aging,
		// never a reset and never a failed poll.
		if head == nil {
			if err := note(entry, "source unreadable: team channel head"); err != nil {
				return err
			}
			continue
		}
		cursor := r.readCursor(ctx, entry.Worker)
		if cursor == nil {
			if err := note(entry, "source unreadable: worker read state"); err != nil {
				return err
			}
			continue
		}
		if *head <= *cursor {
			if err := resetClock(); err != nil { // observed current: it read the chat
				return err
			}
			continue
		}
		// 3) Owed a send? Strictly more than N minutes of continuous idle,
		// chat past the cursor and past the delivered frontier.
		owed := rec.IdleSince != nil &&
			float64(r.now()-*rec.IdleSince) > r.cfg.CoordinatorWatchMin*60_000 &&
			(rec.CoveredFrontier == nil || *head > *rec.CoveredFrontier)
		if !owed {
			continue
		}
		// 4) The full guard chain, same gates and order as a mention nudge.
		fresh, err := r.listTerminals(ctx)
		if err != nil {
			return err
		}
		sendHandle, sendFamily, _, rerr := r.resolveTerminal(ctx, entry, fresh)
		if rerr != nil {
			if err := hold(rerr.Error()); err != nil {
				return err
			}
			continue
		}
		if sendHandle != rec.Handle {
			// REPLACED between the probe and the chain (#1829 local P2-4): the
			// accumulated minutes belong to the OLD terminal's episode.
			if err := resetClock(); err != nil {
				return err
			}
			continue
		}
		if gate, why := r.provesIdle(ctx, sendHandle, sendFamily, lastOutputOf(fresh, sendHandle), r.cfg.BusyWaitMS); !gate {
			if err := hold(why); err != nil {
				return err
			}
			continue
		}
		if empty, why := r.promptIsEmpty(ctx, sendHandle, sendFamily); !empty {
			if err := hold("prompt not empty (" + why + ")"); err != nil {
				return err
			}
			continue
		}
		if err := mustOwn(); err != nil {
			return err
		}
		// The recompute immediately before the send (#1616's analog): a
		// failure fails the poll rather than send a stale claim, and a worker
		// that caught up in the gate window is dropped.
		nowCursor := r.readCursor(ctx, entry.Worker)
		nowHead := r.channelHead(ctx)
		if nowCursor == nil || nowHead == nil {
			return routerErrorf("liveness recompute failed: read state or channel head unreadable (token unchanged)")
		}
		if *nowHead <= *nowCursor {
			if err := resetClock(); err != nil {
				return err
			}
			out.LivenessDropped = append(out.LivenessDropped, entry.Name)
			continue
		}
		relisted, err := r.listTerminals(ctx)
		if err != nil {
			return err
		}
		if again, why := r.provesIdle(ctx, sendHandle, sendFamily, lastOutputOf(relisted, sendHandle), recheckIdleTimeoutMS); !again {
			if err := hold("busy again after the recompute (" + why + ")"); err != nil {
				return err
			}
			continue
		}
		if stillEmpty, why := r.promptIsEmpty(ctx, sendHandle, sendFamily); !stillEmpty {
			if err := hold("prompt not empty on recheck (" + why + ")"); err != nil {
				return err
			}
			continue
		}
		if err := mustOwn(); err != nil {
			return err
		}
		ok, how := r.sendNudge(ctx, sendHandle, LivenessNudgeText(*nowCursor, *nowHead))
		if !ok {
			// A failed send holds the token; the clock stands — retried next poll.
			out.Held[entry.Name] = "liveness: " + how
			continue
		}
		// Persisted immediately after the send (#1829 local P1-2). The clock
		// restarts AT the send; the frontier covers through the head it named.
		t, frontier := r.now(), *nowHead
		rec = LivenessRecord{IdleSince: &t, Handle: sendHandle, CoveredFrontier: &frontier}
		if err := save(); err != nil {
			return err
		}
		out.Liveness = append(out.Liveness, entry.Name)
	}
	return nil
}
