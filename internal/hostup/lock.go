package hostup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// ErrLocked is another run holding the host's lock.
var ErrLocked = errors.New("another host up run holds this host's lock")

// Lock takes the per-host run lock in dir, without waiting: two applying runs
// on one host never overlap (a timer's sweep and a hand run, or two sweeps
// when one runs long), and the second leaves at once with ErrLocked rather
// than queueing behind the first. It is an OS file lock, so a run that dies
// leaves nothing stale behind. The returned func releases it.
func Lock(dir, hostID string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(hostID))
	l := flock.New(filepath.Join(dir, "host-up."+hex.EncodeToString(sum[:8])+".lock"))
	ok, err := l.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrLocked
	}
	return func() { _ = l.Unlock() }, nil
}
