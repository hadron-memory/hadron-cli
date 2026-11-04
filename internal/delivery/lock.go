package delivery

// One deliverer per worker at a time, across processes: the host poller and
// C2's posting CLI (#908) both type into the same terminals. A mkdir lock
// per worker recording its owner (pid, boot, start), copied from
// internal/teamrouter's lock.go (the reviewed #1490 stale-owner semantics),
// generalized from one state-directory lock to one lock per worker. Proc
// probes are in proc_unix.go / proc_windows.go, copied likewise.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ErrBusy is a lock another live deliverer holds. A deliverer that finds it
// skips the worker this time and never waits: a post must not stall behind
// the poller.
var ErrBusy = errors.New("another deliverer holds this worker's lock")

// ownerlessLockGrace: a lock with no owner record is reclaimed once this old
// (a crash between the mkdir and the owner write).
const ownerlessLockGrace = 60 * time.Second

// bootToleranceMS: two readings of the boot time closer than this are one boot.
const bootToleranceMS = 120_000

type lockOwner struct {
	PID       int   `json:"pid"`
	Boot      int64 `json:"boot,omitempty"`
	StartedAt int64 `json:"startedAt"`
}

func ownRecord() string {
	rec := lockOwner{PID: os.Getpid(), StartedAt: time.Now().UnixMilli()}
	if boot, ok := bootEpochMS(); ok {
		rec.Boot = boot
	}
	b, _ := json.Marshal(rec)
	// A nonce keeps two deliverers in ONE process (the poller's workers run
	// concurrently) from reading each other's record as their own.
	return fmt.Sprintf("%s\n%x", b, rand.Uint64())
}

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
// started after the lock was taken. A record that cannot be parsed is not
// trusted to be alive.
func lockHolderAlive(lock string, raw *string) bool {
	if raw == nil {
		info, err := os.Stat(lock)
		return err == nil && time.Since(info.ModTime()) < ownerlessLockGrace
	}
	first, _, _ := strings.Cut(*raw, "\n")
	var rec struct {
		PID       *float64 `json:"pid"`
		Boot      *float64 `json:"boot"`
		StartedAt *float64 `json:"startedAt"`
	}
	if json.Unmarshal([]byte(first), &rec) != nil || rec.PID == nil {
		return false
	}
	pid := *rec.PID
	if pid != math.Trunc(pid) || pid <= 0 || pid > math.MaxInt32 {
		return false
	}
	if rec.Boot != nil {
		if now, ok := bootEpochMS(); ok && math.Abs(*rec.Boot-float64(now)) > bootToleranceMS {
			return false
		}
	}
	if processGone(int(pid)) {
		return false
	}
	if rec.StartedAt != nil {
		if started, ok := processStartedAtMS(int(pid)); ok && float64(started) > *rec.StartedAt+1000 {
			return false
		}
	}
	return true
}

// breakStaleLock moves a lock judged dead to a unique tomb and compares the
// tomb's owner with the one judged: a match means the stale lock is gone; a
// mismatch means another deliverer took it first, so it goes straight back.
func breakStaleLock(lock string, judged *string) bool {
	tomb := fmt.Sprintf("%s.tomb-%d-%d-%x", lock, os.Getpid(), time.Now().UnixNano(), rand.Uint64())
	if err := os.Rename(lock, tomb); err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	if sameOwner(readOwner(tomb), judged) {
		_ = os.RemoveAll(tomb)
		return true
	}
	if os.Rename(tomb, lock) != nil {
		_ = os.RemoveAll(tomb)
	}
	return false
}

// workerIDRE is the shape a worker id must have to name a lock or record
// path: a server id, never a path.
var workerIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func checkWorkerID(id string) error {
	if !workerIDRE.MatchString(id) {
		return fmt.Errorf("worker id %q cannot name a delivery path", id)
	}
	return nil
}

// WithWorkerLock runs fn holding workerID's lock under dir. fn receives
// stillOwner, re-checked before every send and every write, so a deliverer
// that somehow lost the lock stops instead of acting alongside another.
func WithWorkerLock(dir, workerID string, fn func(stillOwner func() bool) error) error {
	if err := checkWorkerID(workerID); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock := filepath.Join(dir, workerID+".lock")
	mine := ownRecord()
	acquire := func() bool {
		if os.Mkdir(lock, 0o700) != nil {
			return false
		}
		if os.WriteFile(filepath.Join(lock, "owner"), []byte(mine), 0o600) != nil {
			_ = os.RemoveAll(lock)
			return false
		}
		return true
	}
	acquired := acquire()
	for attempt := 0; !acquired && attempt < 2; attempt++ {
		judged := readOwner(lock)
		if lockHolderAlive(lock, judged) || !breakStaleLock(lock, judged) {
			return ErrBusy
		}
		acquired = acquire()
	}
	if !acquired {
		return ErrBusy
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
