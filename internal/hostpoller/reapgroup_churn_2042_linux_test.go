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
	state   byte
	threads int
}

// churnProc models /proc for one scan call: pids is the listing, stats the
// answers, and gone(pass, pid) says whether pid has died by the time pass
// `pass` reads its stat (listed, then vanished).
type churnProc struct {
	pids  []int
	stats map[int]churnStat
	gone  func(pass, pid int) bool
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
	readGroupStatFn = func(path string) (int, byte, int, error) {
		pid, _ := strconv.Atoi(path[len("/proc/") : len(path)-len("/stat")])
		if f.gone != nil && f.gone(f.pass, pid) {
			return 0, 0, 0, fs.ErrNotExist
		}
		st, ok := f.stats[pid]
		if !ok {
			return 0, 0, 0, fs.ErrNotExist
		}
		return st.pgrp, st.state, st.threads, nil
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
		churnPgid:   {churnPgid, 'Z', 1}, // the leader: a held zombie
		churnMember: {churnPgid, 'Z', 1}, // a dead member nobody reaped
	}}
	f.pids = []int{churnPgid, churnMember}
	for i := 0; i < unrelated; i++ {
		pid := 200 + i
		f.pids = append(f.pids, pid)
		f.stats[pid] = churnStat{churnOther, 'S', 1}
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

// A pid proven outside the group in pass 0 that is STILL there later is read
// as before, and a running member is found at once whatever churn surrounds it.
func TestARunningMemberIsFoundAmidChurn2042(t *testing.T) {
	f := churnHost(20)
	f.pids = append(f.pids, 150)
	f.stats[150] = churnStat{churnPgid, 'S', 1} // a running member
	f.gone = func(pass, pid int) bool { return pid >= 200 && pid < 205 }
	f.install(t)
	if !groupHasOtherMembers(churnPgid, churnPgid) {
		t.Fatal("a running member of the group must read as undrained")
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
