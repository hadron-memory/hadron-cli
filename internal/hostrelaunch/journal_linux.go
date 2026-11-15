//go:build linux

package hostrelaunch

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const journalLimit = 4 << 20

type journalState struct {
	Version    int               `json:"version"`
	Generation uint64            `json:"generation"`
	Records    map[string]Record `json:"records"`
}

// FileJournal holds the run lock until Close. The caller must close it only
// after all Runner calls return. Linux is the Orca host platform for C5.
// Applying opens claim a generation before returning, including empty sweeps.
// Read-only opens never create directories, locks or files.
type FileJournal struct {
	dir    *os.File
	lock   *os.File
	state  journalState
	dry    bool
	closed bool
}

func OpenJournal(path string, dry bool) (*FileJournal, error) {
	return openJournal(path, dry, !dry)
}

// OpenJournalHeld takes the run lock as an applying open does but claims no
// generation (cli#2161): an exit pass observes one worker, not a sweep, so it
// must not break the absence continuity of the workers it never looked at.
// Its Sweep is the last sweep's generation, 0 when no sweep ever ran.
func OpenJournalHeld(path string) (*FileJournal, error) {
	return openJournal(path, false, false)
}

func openJournal(path string, dry, advance bool) (_ *FileJournal, err error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("relaunch journal path must be absolute")
	}
	s := &FileJournal{dry: dry, state: journalState{Version: 1, Records: map[string]Record{}}}
	defer func() {
		if err != nil {
			_ = s.Close()
		}
	}()
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(e, unix.ENOENT) {
		if dry {
			return s, nil
		}
		if e = os.Mkdir(path, 0700); e != nil && !errors.Is(e, os.ErrExist) {
			return nil, e
		}
		fd, e = unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	}
	if e != nil {
		return nil, e
	}
	s.dir = os.NewFile(uintptr(fd), path)
	if e = privateOwned(s.dir, true); e != nil {
		return nil, e
	}
	if !dry {
		s.lock, e = s.open("run.lock", unix.O_RDWR|unix.O_CREAT)
		if e != nil {
			return nil, e
		}
		if e = unix.Flock(int(s.lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
			return nil, fmt.Errorf("relaunch journal busy: %w", e)
		}
	}
	f, e := s.open("state.json", unix.O_RDONLY)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	if e == nil {
		defer func() { _ = f.Close() }()
		info, statErr := f.Stat()
		if statErr != nil {
			return nil, statErr
		}
		if info.Size() > journalLimit {
			return nil, errors.New("relaunch journal exceeds limit")
		}
		decoder := json.NewDecoder(io.LimitReader(f, journalLimit+1))
		decoder.DisallowUnknownFields()
		if e = decoder.Decode(&s.state); e != nil {
			return nil, fmt.Errorf("invalid relaunch journal: %w", e)
		}
		var extra any
		if e = decoder.Decode(&extra); e != io.EOF {
			return nil, errors.New("relaunch journal has trailing content")
		}
		if e = s.validate(); e != nil {
			return nil, e
		}
	}
	if advance {
		if s.state.Generation == math.MaxUint64 {
			return nil, errors.New("relaunch generation exhausted")
		}
		s.state.Generation++
		if e = s.persist(); e != nil {
			return nil, e
		}
	}
	return s, nil
}

func privateOwned(f *os.File, directory bool) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() != directory || (!directory && !info.Mode().IsRegular()) || info.Mode().Perm()&0077 != 0 {
		return errors.New("relaunch journal must be private and regular")
	}
	var st unix.Stat_t
	if err = unix.Fstat(int(f.Fd()), &st); err != nil {
		return err
	}
	if !directory && st.Nlink != 1 {
		return errors.New("relaunch journal file has multiple links")
	}
	if st.Uid != uint32(os.Geteuid()) {
		return errors.New("relaunch journal owner mismatch")
	}
	return nil
}
func (s *FileJournal) open(name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(int(s.dir.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	if err = privateOwned(f, false); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
func (s *FileJournal) validate() error {
	if s.state.Version != 1 || s.state.Records == nil || len(s.state.Records) > 10000 {
		return errors.New("unsupported or corrupt relaunch journal")
	}
	for key, rec := range s.state.Records {
		if strings.TrimSpace(key) == "" || len(rec.Attempts) > 10000 || rec.AbsentSweep > s.state.Generation {
			return errors.New("invalid relaunch record")
		}
		for _, at := range rec.Attempts {
			if at.IsZero() {
				return errors.New("invalid relaunch timestamp")
			}
		}
	}
	return nil
}
func (s *FileJournal) Sweep() uint64 { return s.state.Generation }
func (s *FileJournal) Load(worker string) (Record, error) {
	if s.closed {
		return Record{}, os.ErrClosed
	}
	rec := s.state.Records[worker]
	rec.Attempts = append([]time.Time(nil), rec.Attempts...)
	return rec, nil
}
func (s *FileJournal) Save(worker string, rec Record) error {
	if s.closed {
		return os.ErrClosed
	}
	if s.dry {
		return errors.New("dry-run relaunch journal is read-only")
	}
	old, exists := s.state.Records[worker]
	rec.Attempts = append([]time.Time(nil), rec.Attempts...)
	s.state.Records[worker] = rec
	if err := s.persist(); err != nil {
		if exists {
			s.state.Records[worker] = old
		} else {
			delete(s.state.Records, worker)
		}
		return err
	}
	return nil
}
func (s *FileJournal) persist() error {
	if err := s.validate(); err != nil {
		return err
	}
	data, err := json.Marshal(s.state)
	if err != nil {
		return err
	}
	if len(data) > journalLimit {
		return errors.New("relaunch journal exceeds limit")
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	name := "state-" + hex.EncodeToString(nonce[:]) + ".tmp"
	f, err := s.open(name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = unix.Unlinkat(int(s.dir.Fd()), name, 0) }()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = unix.Renameat(int(s.dir.Fd()), name, int(s.dir.Fd()), "state.json"); err != nil {
		return err
	}
	return s.dir.Sync()
}
func (s *FileJournal) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	var errs []error
	if s.lock != nil {
		errs = append(errs, s.lock.Close())
	}
	if s.dir != nil {
		errs = append(errs, s.dir.Close())
	}
	return errors.Join(errs...)
}
