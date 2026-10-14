package teamrouter

// One writer at a time: a mkdir lock in the state directory recording its
// owner (pid, boot, start). Ported from router.ts with the owner record kept
// byte-compatible, so a Go run and a still-installed TS run exclude each
// other on a shared state directory during migration.
//
// Not flock(2), deliberately: the lock must stay visible to the TS router it
// replaces, and its stale-owner proofs (reboot, ESRCH, pid reuse) are the
// reviewed #1490 semantics this port preserves.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ownerlessLockGrace: a lock with no owner record is reclaimed once this old
// (a crash between the mkdir and the owner write).
const ownerlessLockGrace = 60 * time.Second

// bootToleranceMS: two readings of the boot time closer than this are one boot.
const bootToleranceMS = 120_000

// lockOwner is the owner record: {"pid","boot","startedAt"} in epoch ms.
type lockOwner struct {
	PID       int   `json:"pid"`
	Boot      int64 `json:"boot,omitempty"`
	StartedAt int64 `json:"startedAt"`
}

// ownRecord is this process's owner record. Boot is omitted when this host's
// boot time cannot be read: a guessed 0 would read as "rebooted since" to
// the next run and break a LIVE lock.
func ownRecord() string {
	rec := lockOwner{PID: os.Getpid(), StartedAt: time.Now().UnixMilli()}
	if boot, ok := bootEpochMS(); ok {
		rec.Boot = boot
	}
	b, _ := json.Marshal(rec)
	return string(b)
}

// readOwner is the lock's raw owner record; nil when there is none (yet).
func readOwner(lock string) *string {
	b, err := os.ReadFile(filepath.Join(lock, "owner"))
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

func sameOwner(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// lockHolderAlive is true unless the recorded owner is PROVABLY gone: the
// host rebooted since, no process has the pid, or the process with that pid
// started after the lock was taken (a reused pid, @codex P2 #1490). A live
// owner — another user's included (EPERM) — is never displaced. A record
// that cannot be parsed is not trusted to be alive.
func lockHolderAlive(lock string, raw *string) bool {
	if raw == nil {
		info, err := os.Stat(lock)
		return err == nil && time.Since(info.ModTime()) < ownerlessLockGrace
	}
	var obj map[string]json.RawMessage
	var pid float64
	var boot, startedAt *float64
	if json.Unmarshal([]byte(*raw), &obj) == nil && obj != nil {
		pid = numberOr(obj["pid"], math.NaN())
		if v := numberOr(obj["boot"], math.NaN()); !math.IsNaN(v) {
			boot = &v
		}
		if v := numberOr(obj["startedAt"], math.NaN()); !math.IsNaN(v) {
			startedAt = &v
		}
	} else {
		// An older, pid-only record: a bare number, or not JSON at all.
		pid = jsNumber(*raw)
	}
	if math.IsNaN(pid) || pid != math.Trunc(pid) || pid <= 0 || pid > math.MaxInt32 {
		return false
	}
	if boot != nil {
		if now, ok := bootEpochMS(); ok && math.Abs(*boot-float64(now)) > bootToleranceMS {
			return false
		}
	}
	if processGone(int(pid)) {
		return false
	}
	if startedAt != nil {
		// `ps` reports whole seconds: allow for the truncation.
		if started, ok := processStartedAtMS(int(pid)); ok && float64(started) > *startedAt+1000 {
			return false
		}
	}
	return true
}

func numberOr(raw json.RawMessage, def float64) float64 {
	var f float64
	if len(raw) == 0 || json.Unmarshal(raw, &f) != nil {
		return def
	}
	return f
}

// jsNumber is JavaScript's Number(s.trim()) for the forms an owner file can
// hold: "" is 0, a decimal is itself, anything else is NaN.
func jsNumber(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return f
}

// BreakStaleLock breaks a stale lock without ever deleting a LIVE one
// (@codex P2 and Chip QA, #1490). Check-then-delete races: another run can
// break and re-take the lock in between. So the lock is MOVED, atomically, to
// a unique tomb, and the tomb's owner record compared with the one judged
// dead: a match means the stale lock is gone ("retry" — mkdir again, also
// atomic); a mismatch means another run took it first, so its live lock is
// renamed straight back ("busy").
func BreakStaleLock(lock string, judged *string) string {
	tomb := fmt.Sprintf("%s.tomb-%d-%d-%x", lock, os.Getpid(), time.Now().UnixNano(), rand.Uint64())
	if err := os.Rename(lock, tomb); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "retry" // someone else broke it first: the path may be free
		}
		return "busy"
	}
	if sameOwner(readOwner(tomb), judged) {
		_ = os.RemoveAll(tomb)
		return "retry"
	}
	// Not the lock judged dead: put it back. If a third run took the path
	// meanwhile, the moved lock's owner sees it lost (stillOwner) and stops.
	if os.Rename(tomb, lock) != nil {
		_ = os.RemoveAll(tomb)
	}
	return "busy"
}

// withLock runs fn holding the state directory's writer lock. fn receives
// stillOwner: the poll re-checks it before every send and before adoption,
// so a run that somehow lost the lock stops instead of acting alongside
// another. Release removes the lock only while it still holds this run's
// record.
func withLock[T any](stateDir string, fn func(stillOwner func() bool) (T, error)) (T, error) {
	var zero T
	lock := filepath.Join(stateDir, ".writer-lock")
	busy := routerErrorf("another router run holds %s", lock)
	mine := ownRecord()
	acquire := func() bool {
		if os.Mkdir(lock, 0o700) != nil {
			return false
		}
		if os.WriteFile(filepath.Join(lock, "owner"), []byte(mine), 0o600) != nil {
			_ = os.RemoveAll(lock) // never leave an ownerless lock of our own making
			return false
		}
		return true
	}
	acquired := acquire()
	for attempt := 0; !acquired && attempt < 2; attempt++ {
		judged := readOwner(lock)
		if lockHolderAlive(lock, judged) {
			return zero, busy
		}
		if BreakStaleLock(lock, judged) == "busy" {
			return zero, busy
		}
		acquired = acquire()
	}
	if !acquired {
		return zero, busy
	}
	stillOwner := func() bool {
		o := readOwner(lock)
		return o != nil && *o == mine
	}
	defer func() {
		if stillOwner() {
			_ = os.RemoveAll(lock)
		}
	}()
	return fn(stillOwner)
}
