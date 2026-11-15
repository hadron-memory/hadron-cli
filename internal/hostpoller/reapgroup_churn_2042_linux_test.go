//go:build linux

package hostpoller

import (
	"io/fs"
	"os"
	"strconv"
	"testing"
)

// cli#2042: a busy host kills unrelated processes through every scan pass. A
// listed pid that vanishes before its stat read voided the pass even when an
// earlier pass of the same call had already proved that pid was NOT in the
// group, so on a churning host every pass was void and the scan answered
// "members" forever (the drain watcher then never reaped). These tests drive
// the scan with a modelled /proc: no sleeps, no timing, no real churn.

type churnEntry struct{ name string }

func (e churnEntry) Name() string               { return e.name }
func (e churnEntry) IsDir() bool                { return true }
func (e churnEntry) Type() fs.FileMode          { return fs.ModeDir }
func (e churnEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrNotExist }

type churnStat struct {
	pgrp    int
	session int
	state   byte
	threads int
}

// churnProc models /proc for one scan call: pids is the listing, stats the
// answers, and gone(pass, pid) says whether pid has died by the time pass
// `pass` reads its stat (listed, then vanished).
type churnProc struct {
	pids  []int
	stats map[int]churnStat
	// dyn, when it answers, overrides stats for that pass (a process that
	// changes group between passes).
	dyn  func(pass, pid int) (churnStat, bool)
	gone func(pass, pid int) bool
	// unlistedFrom: a pid stops being LISTED from this pass on (reaped for good).
	unlistedFrom map[int]int
	pass         int
	lists        int
}

func (f *churnProc) install(t *testing.T) {
	t.Helper()
	oldDir, oldStat := readProcDir, readGroupStatFn
	t.Cleanup(func() { readProcDir, readGroupStatFn = oldDir, oldStat })
	readProcDir = func(string) ([]os.DirEntry, error) {
		f.lists++
		f.pass = f.lists - 1
		out := make([]os.DirEntry, 0, len(f.pids))
		for _, p := range f.pids {
			if from, ok := f.unlistedFrom[p]; ok && f.pass >= from {
				continue
			}
			out = append(out, churnEntry{strconv.Itoa(p)})
		}
		return out, nil
	}
	readGroupStatFn = func(path string) (int, int, byte, int, error) {
		pid, _ := strconv.Atoi(path[len("/proc/") : len(path)-len("/stat")])
		if f.gone != nil && f.gone(f.pass, pid) {
			return 0, 0, 0, 0, fs.ErrNotExist
		}
		if f.dyn != nil {
			if st, ok := f.dyn(f.pass, pid); ok {
				return st.pgrp, st.session, st.state, st.threads, nil
			}
		}
		st, ok := f.stats[pid]
		if !ok {
			return 0, 0, 0, 0, fs.ErrNotExist
		}
		return st.pgrp, st.session, st.state, st.threads, nil
	}
}

const (
	churnPgid   = 100
	churnMember = 101
	churnOther  = 7
)

// A drained group (only zombies) on a host where, in EVERY pass, a few
// unrelated processes die between the listing and their stat read.
func churnHost(unrelated int) *churnProc {
	f := &churnProc{stats: map[int]churnStat{
		// The launched agent is a session leader (detach sets Setsid): its group
		// id is its session id.
		churnPgid:   {churnPgid, churnPgid, 'Z', 1}, // the leader: a held zombie
		churnMember: {churnPgid, churnPgid, 'Z', 1}, // a dead member nobody reaped
	}}
	f.pids = []int{churnPgid, churnMember}
	for i := 0; i < unrelated; i++ {
		pid := 200 + i
		f.pids = append(f.pids, pid)
		f.stats[pid] = churnStat{churnOther, churnOther, 'S', 1} // another session entirely
	}
	return f
}

func TestDrainedGroupReadsDrainedWhileUnrelatedProcessesDieEveryPass2042(t *testing.T) {
	f := churnHost(40)
	// Pass p: the three unrelated pids 200+3p.. die before their stat read, so
	// EVERY pass has vanishings (the busy-host shape: 2-8 per pass).
	f.gone = func(pass, pid int) bool { return pid >= 200+3*pass && pid < 200+3*pass+3 }
	f.install(t)
	if groupHasOtherMembers(churnPgid, churnPgid) {
		t.Fatalf("a group of zombies read as undrained after %d listings: unrelated /proc churn starved the scan", f.lists)
	}
	if f.lists >= maxGroupScanPasses {
		t.Fatalf("it should settle well inside the pass cap, used %d of %d", f.lists, maxGroupScanPasses)
	}
}

// The relaxation is only for pids PROVEN outside the group. A pid never seen
// before that vanishes could have been a member (or forked one), so a pass
// with one still proves nothing, however many proven-unrelated pids also
// die: a fresh unknown vanishing in every pass exhausts the cap and answers
// "members", the conservative direction.
func TestAnUnseenVanishedPidStillVoidsThePass2042(t *testing.T) {
	f := churnHost(10)
	fresh := func(pass int) int { return 5000 + pass }
	f.gone = func(pass, pid int) bool {
		// known-unrelated churn AND one never-before-seen pid per pass
		return (pid >= 200 && pid < 203) || pid == fresh(pass)
	}
	f.install(t)
	base := f.pids
	// Each pass lists a fresh pid that has already died: only listed, never stat-able.
	oldDir := readProcDir
	readProcDir = func(d string) ([]os.DirEntry, error) {
		ents, err := oldDir(d)
		ents = append(ents, churnEntry{strconv.Itoa(fresh(f.pass))})
		return ents, err
	}
	_ = base
	if !groupHasOtherMembers(churnPgid, churnPgid) {
		t.Fatal("an unseen pid vanishing every pass proves nothing: the scan must answer members, not drained")
	}
	if f.lists != maxGroupScanPasses {
		t.Fatalf("the scan must use every pass before giving up, used %d of %d", f.lists, maxGroupScanPasses)
	}
}

// A running member is found at once, amid the churn.
func TestARunningMemberIsFoundAmidChurn2042(t *testing.T) {
	f := churnHost(20)
	f.pids = append(f.pids, 150)
	f.stats[150] = churnStat{churnPgid, churnPgid, 'S', 1} // a running member
	f.gone = func(pass, pid int) bool { return pid >= 200 && pid < 205 }
	f.install(t)
	if !groupHasOtherMembers(churnPgid, churnPgid) {
		t.Fatal("a running member of the group must read as undrained")
	}
}

// Jibril P3: a process OUTSIDE the group in pass 0 that is a running member by a
// later pass is detected on the later pass. (Only a same-session process can
// move into the group, so the model uses one.)
func TestAProcessThatJoinsTheGroupLaterIsDetected2042(t *testing.T) {
	f := churnHost(5)
	f.pids = append(f.pids, 150)
	f.dyn = func(pass, pid int) (churnStat, bool) {
		if pid != 150 {
			return churnStat{}, false
		}
		if pass == 0 {
			return churnStat{churnOther, churnPgid, 'S', 1}, true // same session, other group
		}
		return churnStat{churnPgid, churnPgid, 'S', 1}, true // joined, running
	}
	f.install(t)
	if !groupHasOtherMembers(churnPgid, churnPgid) {
		t.Fatalf("a process that joined the group and runs must read as undrained (after %d listings)", f.lists)
	}
}

// Joaquin's P2 and Jibril's P1: a SAME-SESSION process outside the group can
// setpgid into it, fork, and die before its stat read; the child first appears
// in the NEXT listing. Such a vanishing must void its pass, never be treated as
// "proven outside the group", or the scan answers drained before the child is
// listed.
func TestASameSessionOutsiderThatVanishesVoidsThePass2042(t *testing.T) {
	f := churnHost(1)
	f.pids = append(f.pids, 300)
	f.stats[300] = churnStat{churnOther, churnPgid, 'S', 1} // outside the group, INSIDE its session
	f.stats[301] = churnStat{churnPgid, churnPgid, 'S', 1}  // the late child: a running member
	f.gone = func(pass, pid int) bool { return pid == 300 && pass >= 1 }
	f.install(t)
	list := readProcDir
	readProcDir = func(path string) ([]os.DirEntry, error) {
		es, err := list(path)
		if f.pass >= 2 {
			es = append(es, churnEntry{"301"}) // first listed after 300 forked it and died
		}
		return es, err
	}
	if !groupHasOtherMembers(churnPgid, churnPgid) {
		t.Fatalf("answered drained after %d listings, before the late child was listed", f.lists)
	}
}

// Jibril P2: a pid that was an outsider in pass 0 and is seen INSIDE the group in a
// later pass loses its outsider status: its later vanishing voids that pass.
func TestACachedOutsiderSeenInsideTheGroupLosesItsStatus2042(t *testing.T) {
	f := churnHost(1)
	f.pids = append(f.pids, 150)
	f.dyn = func(pass, pid int) (churnStat, bool) {
		if pid != 150 {
			return churnStat{}, false
		}
		switch pass {
		case 0:
			return churnStat{churnOther, churnOther, 'S', 1}, true // another session: cached
		case 1:
			return churnStat{churnPgid, churnPgid, 'Z', 1}, true // a zombie member: status cleared
		}
		return churnStat{}, false
	}
	f.gone = func(pass, pid int) bool { return pid == 150 && pass == 2 }
	f.unlistedFrom = map[int]int{150: 3}
	f.install(t)
	if groupHasOtherMembers(churnPgid, churnPgid) {
		t.Fatal("only zombies remain: drained once a clean pass exists")
	}
	// 0: first Z sighting of 101 (void). 1: first Z sighting of 150 (void).
	// 2: 150 vanished: no longer an outsider, so it voids. 3: clean.
	if f.lists != 4 {
		t.Fatalf("a pid seen inside the group must lose its outsider status: answered after %d listings, want 4", f.lists)
	}
}

// A vanished pid that WAS a group member (a zombie someone reaped) is not
// "proven outside the group": it still voids that pass, so the scan needs a
// further clean listing before it answers drained.
func TestAVanishedGroupMemberIsNotLaunderedAsAnOutsider2042(t *testing.T) {
	f := churnHost(5)
	// Listed in pass 1 but dead before its stat read; reaped for good by pass 2.
	f.gone = func(pass, pid int) bool { return pass == 1 && pid == churnMember }
	f.unlistedFrom = map[int]int{churnMember: 2}
	f.install(t)
	if groupHasOtherMembers(churnPgid, churnPgid) {
		t.Fatal("only the leader remains once the zombie member is reaped: drained")
	}
	// pass 0: first sighting of the Z member (void); pass 1: it vanished (void,
	// it was a member); pass 2: clean.
	if f.lists != 3 {
		t.Fatalf("a vanished member must void its pass: answered after %d listings, want 3", f.lists)
	}
}

// Without the leader's own entry the group's session is unknown, so nothing may
// be proven to be outside it: churn voids every pass and the scan answers
// members (the conservative direction), never drained.
func TestNothingIsProvenOutsideWhenTheLeadersSessionIsUnknown2042(t *testing.T) {
	f := churnHost(40)
	f.pids = f.pids[1:] // the leader is not listed ...
	delete(f.stats, churnPgid)
	f.gone = func(pass, pid int) bool { return pid >= 200+3*pass && pid < 200+3*pass+3 }
	f.install(t)
	if !groupHasOtherMembers(churnPgid, churnPgid) {
		t.Fatal("with no leader session nothing is proven outside the group: the scan must stay conservative")
	}
	if f.lists != maxGroupScanPasses {
		t.Fatalf("every pass must be void, used %d of %d", f.lists, maxGroupScanPasses)
	}
}
