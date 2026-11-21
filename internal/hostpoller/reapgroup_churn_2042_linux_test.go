//go:build linux

package hostpoller

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
)

// cli#2042: on a busy host a process born and dead between a listing and its
// own stat read voids a scan pass, and a host spawning short-lived processes
// leaves most passes void, so a bound of eight passes answered "members" far
// too often. The scan's SAFETY rule is unchanged and pinned here: every
// vanished pid voids its pass, whoever it was (it could have been a member, or
// joined the group and forked). These tests drive the scan with a modelled
// /proc: no sleeps, no timing, no real churn.

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
// answers, gone(pass, pid) says whether pid has died by the time pass `pass`
// reads its stat (listed, then vanished), and dyn overrides stats per pass (a
// process that changes group between passes).
type churnProc struct {
	pids         []int
	stats        map[int]churnStat
	gone         func(pass, pid int) bool
	dyn          func(pass, pid int) (churnStat, bool)
	unlistedFrom map[int]int // a pid stops being LISTED from this pass on
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
		if f.dyn != nil {
			if st, ok := f.dyn(f.pass, pid); ok {
				return st.pgrp, st.state, st.threads, nil
			}
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

// churnHost is a drained group (a held zombie leader and one dead, unreaped
// member) on a host with `unrelated` processes of other groups.
func churnHost(unrelated int) *churnProc {
	f := &churnProc{stats: map[int]churnStat{
		churnPgid:   {churnPgid, 'Z', 1},
		churnMember: {churnPgid, 'Z', 1},
	}}
	f.pids = []int{churnPgid, churnMember}
	for i := 0; i < unrelated; i++ {
		pid := 200 + i
		f.pids = append(f.pids, pid)
		f.stats[pid] = churnStat{churnOther, 'S', 1}
	}
	return f
}

// withFreshVanisher adds, from pass 0 up to (excluding) `until`, one pid per pass
// that is listed and dead before its stat read: the process born and dead inside
// a pass, which nothing can rule out.
func withFreshVanisher(f *churnProc, until int) {
	list := readProcDir
	readProcDir = func(path string) ([]os.DirEntry, error) {
		es, err := list(path)
		if f.pass < until {
			es = append(es, churnEntry{strconv.Itoa(9000 + f.pass)})
		}
		return es, err
	}
}

// The failure of #2042: a host that keeps spawning short-lived processes leaves
// MANY passes void in a row. The scan must keep going well past the old bound of
// eight and settle on the first clean pass. (Red at a bound of eight.)
func TestALongRunOfVoidPassesStillSettlesOnTheFirstCleanPass2042(t *testing.T) {
	const voidPasses = 20
	f := churnHost(3)
	f.install(t)
	withFreshVanisher(f, voidPasses)
	if groupHasOtherMembers(churnPgid, churnPgid) {
		t.Fatalf("answered members after %d listings; a clean pass exists at %d", f.lists, voidPasses+1)
	}
	if f.lists != voidPasses+1 {
		t.Fatalf("must settle on the first clean pass: %d listings, want %d", f.lists, voidPasses+1)
	}
	if maxGroupScanPasses <= voidPasses {
		t.Fatalf("the bound (%d) must exceed a realistic run of void passes (%d)", maxGroupScanPasses, voidPasses)
	}
}

// A /proc that never holds still still ends at the bound with "members", the
// conservative direction, after using every pass.
func TestEndlessChurnStillEndsAtTheBoundAsMembers2042(t *testing.T) {
	f := churnHost(3)
	f.install(t)
	withFreshVanisher(f, 1<<30)
	if !groupHasOtherMembers(churnPgid, churnPgid) {
		t.Fatal("a /proc that never holds still proves nothing: the scan must answer members")
	}
	if f.lists != maxGroupScanPasses {
		t.Fatalf("it must use every pass before giving up, used %d of %d", f.lists, maxGroupScanPasses)
	}
}

// The safety rule, pinned from the review of this change (Joaquin, Jibril,
// Colin, Rook): a process outside the group can setpgid into it (same session),
// fork a runnable child and die before its stat read, so EVERY vanished pid voids
// its pass, however the host looks. Here pid 300 is outside the group in every
// stat read that succeeds, and dies before its stat read in pass 1; its late child
// is first LISTED in pass 2. Were the vanished pid ignored, pass 1 would be clean
// and the scan would answer drained before the child is listed.
func TestAVanishedPidAlwaysVoidsItsPassEvenIfItWasOutsideTheGroup2042(t *testing.T) {
	f := churnHost(1)
	f.pids = append(f.pids, 300)
	f.stats[300] = churnStat{churnOther, 'S', 1} // outside the group whenever it can be read
	f.stats[301] = churnStat{churnPgid, 'S', 1}  // the late child: a running member
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
	if f.lists != 3 {
		t.Fatalf("the scan must reach the listing that shows the child: %d listings, want 3 (pass 0: first Z, pass 1: vanish, pass 2: child)", f.lists)
	}
}

// The same holds when the pid was seen outside the group in EVERY earlier pass
// and its number could even have been reused: nothing is remembered between passes.
func TestNothingSeenInAnEarlierPassIsTrustedWhenAPidVanishes2042(t *testing.T) {
	f := churnHost(40)
	// Three different unrelated pids die in EVERY pass (wrapping), each already
	// read successfully in an earlier pass.
	f.gone = func(pass, pid int) bool {
		if pid < 200 {
			return false
		}
		return ((pid-200)-3*pass%40+40)%40 < 3
	}
	f.install(t)
	if !groupHasOtherMembers(churnPgid, churnPgid) {
		t.Fatal("every pass has a vanished pid: no pass is clean, so the scan must answer members")
	}
	if f.lists != maxGroupScanPasses {
		t.Fatalf("every pass must be void, used %d of %d", f.lists, maxGroupScanPasses)
	}
}

// A vanished GROUP member voids its pass too, so one more clean listing is needed.
func TestAVanishedGroupMemberVoidsItsPass2042(t *testing.T) {
	f := churnHost(5)
	f.gone = func(pass, pid int) bool { return pass == 1 && pid == churnMember }
	f.unlistedFrom = map[int]int{churnMember: 2}
	f.install(t)
	if groupHasOtherMembers(churnPgid, churnPgid) {
		t.Fatal("only the leader remains once the zombie member is reaped: drained")
	}
	// pass 0: first sighting of the Z member (void); pass 1: it vanished (void); pass 2: clean.
	if f.lists != 3 {
		t.Fatalf("a vanished member must void its pass: answered after %d listings, want 3", f.lists)
	}
}

// A running member is found at once, amid the churn.
func TestARunningMemberIsFoundAmidChurn2042(t *testing.T) {
	f := churnHost(20)
	f.pids = append(f.pids, 150)
	f.stats[150] = churnStat{churnPgid, 'S', 1}
	f.gone = func(pass, pid int) bool { return pid >= 200 && pid < 205 }
	f.install(t)
	if !groupHasOtherMembers(churnPgid, churnPgid) {
		t.Fatal("a running member of the group must read as undrained")
	}
	if f.lists != 1 {
		t.Fatalf("a running member is found at once, not by exhausting the passes: %d listings", f.lists)
	}
}

// A process that joins the group after the first pass is detected on a later one.
func TestAProcessThatJoinsTheGroupLaterIsDetected2042(t *testing.T) {
	f := churnHost(5)
	f.pids = append(f.pids, 150)
	f.dyn = func(pass, pid int) (churnStat, bool) {
		if pid != 150 {
			return churnStat{}, false
		}
		if pass == 0 {
			return churnStat{churnOther, 'S', 1}, true
		}
		return churnStat{churnPgid, 'S', 1}, true
	}
	f.install(t)
	if !groupHasOtherMembers(churnPgid, churnPgid) {
		t.Fatalf("a process that joined the group and runs must read as undrained (after %d listings)", f.lists)
	}
}

// hadron-cli#2142: the reason CODE of a "members" answer comes from the pass that
// decided it, never from text read from /proc.
func TestMembershipReportsTheReasonCodeOfTheDecidingPass2142(t *testing.T) {
	why := func(f *churnProc) (bool, drainWhy) {
		f.install(t)
		return groupMembership(churnPgid, churnPgid)
	}
	// a running member
	f := churnHost(2)
	f.pids = append(f.pids, 150)
	f.stats[150] = churnStat{churnPgid, 'S', 1}
	if m, w := why(f); !m || w != whyLiveMember {
		t.Errorf("running member: %v %q", m, w)
	}
	// a never-seen pid vanishing in every pass
	f = churnHost(2)
	f.install(t)
	withFreshVanisher(f, 1<<30)
	if m, w := groupMembership(churnPgid, churnPgid); !m || w != whyUnseenPid {
		t.Errorf("unseen pid: %v %q", m, w)
	}
	// a Z member seen for the first time on every pass (a new one each pass)
	f = churnHost(2)
	f.dyn = func(pass, pid int) (churnStat, bool) {
		if pid >= 600 && pid < 700 {
			return churnStat{churnPgid, 'Z', 1}, true
		}
		return churnStat{}, false
	}
	f.install(t)
	list := readProcDir
	readProcDir = func(path string) ([]os.DirEntry, error) {
		es, err := list(path)
		es = append(es, churnEntry{strconv.Itoa(600 + f.pass)})
		return es, err
	}
	if m, w := groupMembership(churnPgid, churnPgid); !m || w != whyLateFork {
		t.Errorf("late fork: %v %q", m, w)
	}
	// an unreadable /proc
	f = churnHost(1)
	f.install(t)
	readProcDir = func(string) ([]os.DirEntry, error) { return nil, fs.ErrPermission }
	if m, w := groupMembership(churnPgid, churnPgid); !m || w != whyUnreadable {
		t.Errorf("unreadable: %v %q", m, w)
	}
}

// A group member seen as a zombie that VANISHES while the pass is void for no
// other reason is a vanished MEMBER, not an unseen pid.
func TestAVanishedZombieMemberIsCodedAsAVanishedMember2142(t *testing.T) {
	// In pass 1 the vanished pid WAS in the previous pass's Z set: that is the member code.
	f2 := churnHost(1)
	f2.gone = func(pass, pid int) bool { return pid == churnMember && pass == 1 }
	f2.install(t)
	_, _, zs, _ := scanGroupOnce(churnPgid, churnPgid, nil, readProcDir)
	_, void, _, why := scanGroupOnce(churnPgid, churnPgid, zs, readProcDir)
	if !void || why != whyVanishedMember {
		t.Fatalf("void=%v why=%q, want vanished-member", void, why)
	}
}

// cli#2744: when the host's unrelated churn outlasts the scan's bound, EVERY pass is void
// (why=unseen-pid) and the stop reports a drained group as "not exited within 2s of
// SIGKILL": modelled here with a fresh vanisher on every pass, past maxGroupScanPasses.
// The FIRST assertion pins the production scan's conservative answer under unbounded
// churn as a KNOWN LIMITATION tracked as #2853, not as the desired behavior: it is here
// so that a future fix of #2853 changes this line on purpose. The held-exit tests give
// their launch a group-only lister (groupOnlyProc2744) so that churn from OTHER groups
// never reaches their scan; the second half pins that the lister settles.
func TestUnrelatedChurnPastTheBoundVoidsTheScanAndAGroupOnlyListerKeepsItOut2744(t *testing.T) {
	f := churnHost(3)
	f.install(t)
	withFreshVanisher(f, 1<<30) // a new short-lived process on every listing, forever
	if members, why := groupMembership(churnPgid, churnPgid); !members || why != whyUnseenPid {
		t.Fatalf("the safe answer under unbounded churn is members/unseen-pid (known limitation, #2853): %v %s", members, why)
	}
	var pg atomic.Int64
	pg.Store(churnPgid)
	f.lists, f.pass = 0, 0
	members, why := groupMembershipWith(churnPgid, churnPgid, groupOnlyProc2744(&pg))
	if members {
		t.Fatalf("a group-only lister must settle on the group's own drained state, got members (%s)", why)
	}
	// Two listings settle it: the Z member is first seen on pass 0 (void: first sighting),
	// and the second pass sees it again.
	if f.lists != 2 {
		t.Errorf("settles in exactly two listings, not by exhausting the bound (%d): %d", maxGroupScanPasses, f.lists)
	}
}

// The group-only lister keeps the leader and its members and drops everything else,
// including a pid gone before its stat read that was never seen as a member; a pid that
// WAS seen as a member and then vanishes is kept (so the scan voids over it); a /proc
// that cannot be listed passes its error through; with no pgid yet it lists all.
func TestGroupOnlyListerKeepsOnlyTheGroup2744(t *testing.T) {
	f := churnHost(2)
	f.install(t)
	withFreshVanisher(f, 1<<30)
	var pg atomic.Int64
	all, err := groupOnlyProc2744(&pg)("/proc")
	if err != nil || len(all) != len(f.pids)+1 {
		t.Fatalf("before the pgid is known the lister must list everything: %d entries, err %v", len(all), err)
	}
	pg.Store(churnPgid)
	list := groupOnlyProc2744(&pg)
	kept, err := list("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range kept {
		names = append(names, e.Name())
	}
	if len(kept) != 2 || names[0] != strconv.Itoa(churnPgid) || names[1] != strconv.Itoa(churnMember) {
		t.Fatalf("only the leader and its member are kept: %v", names)
	}
	// The member was seen; now it vanishes before its stat read: it is still listed.
	f.gone = func(_, pid int) bool { return pid == churnMember }
	kept, _ = list("/proc")
	names = names[:0]
	for _, e := range kept {
		names = append(names, e.Name())
	}
	if len(names) != 2 || names[1] != strconv.Itoa(churnMember) {
		t.Fatalf("a known member that vanished must stay listed so the scan voids over it: %v", names)
	}
	// An unlistable /proc is the caller's problem, not an empty group.
	readProcDir = func(string) ([]os.DirEntry, error) { return nil, fs.ErrPermission }
	if _, err := groupOnlyProc2744(&pg)("/proc"); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("a listing error passes through: %v", err)
	}
}
