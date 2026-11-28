// Package hostcred is the host credential store (hadron-cli#3239, slice C1 of
// hadron-server#5209): the App keys that make this machine a host, one entry
// per server and App, in <config dir>/host-credentials.json.
//
// The store holds a credential and the identity it was issued for, never a
// setting (the epic's test 3; ruling RC1 B, Holger 2026-11-26): each entry is
// the server's base URL, the App's id, the host's id and the key. `hadron team
// host join` writes an entry from the server's own answer and `hadron team host
// leave` removes it; no command lets a user set a field, and the ids grant
// nothing: the key alone authorizes, and the host checks the ids against the
// server.
//
// Boundaries:
//   - The file is 0600 and its directory 0700. On Unix a file or an existing
//     directory with any group or world bit is refused, as is a symlink at the
//     file's path. On Windows the Go mode means nothing: the file is written
//     with an owner-only access list (internal/secretfile) and the mode checks
//     are skipped, as the host key file's check is.
//   - Every read-modify-write runs under its own lock file,
//     <config dir>/host-credentials.lock, so a write never interleaves with
//     another and a join's network call never holds up `hadron auth`.
//   - No error this package returns contains a key or any of the file's
//     content: a malformed file is named by its path and a fixed reason.
//
// The package imports nothing of the host poller, so `host run` (C3,
// hadron-cli#3237) can import it.
package hostcred

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/hostid"
	"github.com/hadron-memory/hadron-cli/internal/secretfile"
)

// FileName and LockName are the store's file and its lock, in the config dir.
const (
	FileName = "host-credentials.json"
	LockName = "host-credentials.lock"
	// Version is the one file format this CLI reads and writes.
	Version = 1
	// maxFileBytes bounds a read: a store is a handful of short entries.
	maxFileBytes = 1 << 20
)

// Entry is one host credential: the key that makes this machine the host
// HostID of the App AppID on Server.
type Entry struct {
	// Server is the server's base URL, as the CLI was pointed at when it
	// joined. Entries are matched by its host (auth.Host), as tokens are.
	Server string `json:"server"`
	AppID  string `json:"appId"`
	HostID string `json:"hostId"`
	// Key is the raw App key. Never print it, log it or put it in an error.
	Key string `json:"key"`
}

// Matches reports whether e is the entry for server and appID.
func (e Entry) Matches(server, appID string) bool {
	return auth.Host(e.Server) == auth.Host(server) && e.AppID == appID
}

type document struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// Error is a refusal of the store's file or directory. Its text names the path
// and a fixed reason, never content.
type Error struct {
	Path   string
	Reason string
}

func (e *Error) Error() string { return fmt.Sprintf("host credential store %s: %s", e.Path, e.Reason) }

// ErrBadEntry refuses an entry Put was given that is not a whole one.
var ErrBadEntry = errors.New("a host credential needs a server, an App id, a host id and a key")

// Path returns the store's file path. It creates nothing.
func Path() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Load reads every entry, without taking the lock. A missing file is no
// entries. Use Lock for a read that a write depends on.
func Load() ([]Entry, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	if err := checkDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return read(path)
}

// Keys returns the entries' keys, for scrubbing them from a child process's
// environment (hostpoller.ScrubbedEnv drops any value that contains a key).
func Keys(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Key)
	}
	return out
}

// lockWait bounds how long Lock waits for a lock another process holds (hadron-cli#3288 F11); lockRetry is the
// interval between attempts.
var lockWait = 30 * time.Second

const lockRetry = 100 * time.Millisecond

// Store is the store held under its lock. Unlock releases it.
type Store struct {
	path    string
	lock    *flock.Flock
	entries []Entry
}

// Lock creates the config directory if it is missing (0700), refuses one that
// is too open, takes the store's lock and reads the entries. The caller must
// Unlock, which a crash does too (the OS drops the lock with the process).
func Lock() (*Store, error) {
	dir, err := config.EnsureDir()
	if err != nil {
		return nil, err
	}
	if err := checkDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, FileName)
	lockPath := filepath.Join(dir, LockName)
	// The lock file is opened without following a symlink at its path, as the store file is (#3288 F13).
	lock := flock.New(lockPath, flock.SetFlag(os.O_CREATE|os.O_RDWR|lockNoFollow), flock.SetPermissions(0o600))
	if err := refuseSymlinkedLock(lockPath); err != nil {
		return nil, err
	}
	// A bounded wait (#3288 F11): join and leave hold the lock across a server call, so a stuck one must not
	// hold every later one forever.
	ctx, cancel := context.WithTimeout(context.Background(), lockWait)
	defer cancel()
	ok, err := lock.TryLockContext(ctx, lockRetry)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		if isSymlinkErr(err) {
			return nil, &Error{Path: lockPath, Reason: "is a symlink; refusing to lock through it"}
		}
		return nil, &Error{Path: lockPath, Reason: "cannot take the store's lock"}
	}
	if !ok {
		return nil, &Error{Path: lockPath, Reason: fmt.Sprintf("another hadron process has held the store's lock for over %s (a `team host join` or `leave` waiting on its server); let it finish, then run again", lockWait)}
	}
	entries, err := read(path)
	if err != nil {
		_ = lock.Unlock()
		return nil, err
	}
	return &Store{path: path, lock: lock, entries: entries}, nil
}

// Unlock releases the lock. Safe to call more than once.
func (s *Store) Unlock() {
	if s.lock != nil {
		_ = s.lock.Unlock()
		s.lock = nil
	}
}

// Path is the store's file.
func (s *Store) Path() string { return s.path }

// Entries returns a copy of the entries read under the lock.
func (s *Store) Entries() []Entry { return append([]Entry{}, s.entries...) }

// Find returns the entry for server and appID.
func (s *Store) Find(server, appID string) (Entry, bool) {
	for _, e := range s.entries {
		if e.Matches(server, appID) {
			return e, true
		}
	}
	return Entry{}, false
}

// Put stores e, replacing the entry for the same server and App if there is
// one, and writes the file.
func (s *Store) Put(e Entry) error {
	if !wholeEntry(e) {
		return ErrBadEntry
	}
	next := make([]Entry, 0, len(s.entries)+1)
	for _, old := range s.entries {
		if !old.Matches(e.Server, e.AppID) {
			next = append(next, old)
		}
	}
	next = append(next, e)
	if err := s.write(next); err != nil {
		return err
	}
	s.entries = next
	return nil
}

// Remove deletes the entry for server and appID and writes the file; the file
// itself is deleted when no entry is left. It reports whether there was one.
func (s *Store) Remove(server, appID string) (bool, error) {
	next := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		if !e.Matches(server, appID) {
			next = append(next, e)
		}
	}
	if len(next) == len(s.entries) {
		return false, nil
	}
	if len(next) == 0 {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, &Error{Path: s.path, Reason: "cannot delete the file"}
		}
	} else if err := s.write(next); err != nil {
		return false, err
	}
	s.entries = next
	return true, nil
}

func (s *Store) write(entries []Entry) error {
	if fi, err := os.Lstat(s.path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return &Error{Path: s.path, Reason: "is a symlink; refusing to write through it"}
	}
	body, err := json.MarshalIndent(document{Version: Version, Entries: entries}, "", "  ")
	if err != nil {
		return &Error{Path: s.path, Reason: "cannot encode the entries"}
	}
	pending, err := secretfile.Open(s.path, true)
	if err != nil {
		return &Error{Path: s.path, Reason: "cannot create the file"}
	}
	if err := pending.Commit(append(body, '\n')); err != nil {
		var lb *secretfile.LeftBehindError
		if errors.As(err, &lb) {
			return &Error{Path: s.path, Reason: "cannot write the file, and its temporary file " + lb.Path + " may hold key material: delete it"}
		}
		return &Error{Path: s.path, Reason: "cannot write the file"}
	}
	return nil
}

// checkDir refuses a config directory with any group or world bit (Unix only):
// MkdirAll does not tighten a directory that already exists, and whoever may
// write the directory may replace the file.
func checkDir(dir string) error {
	fi, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return &Error{Path: dir, Reason: "cannot read the directory"}
	}
	if tooOpen(fi) {
		return &Error{Path: dir, Reason: fmt.Sprintf("directory is accessible to group or others (mode %o); run chmod 700 %s", fi.Mode().Perm(), dir)}
	}
	return nil
}

// read loads and checks the file at path. A missing file is no entries.
func read(path string) ([]Entry, error) {
	f, err := openNoFollow(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		if isSymlinkErr(err) {
			return nil, &Error{Path: path, Reason: "is a symlink; refusing to read through it"}
		}
		return nil, &Error{Path: path, Reason: "cannot open the file"}
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, &Error{Path: path, Reason: "cannot read the file"}
	}
	if !fi.Mode().IsRegular() {
		return nil, &Error{Path: path, Reason: "is not a regular file"}
	}
	if tooOpen(fi) {
		return nil, &Error{Path: path, Reason: fmt.Sprintf("is accessible to group or others (mode %o); run chmod 600 %s", fi.Mode().Perm(), path)}
	}
	body, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, &Error{Path: path, Reason: "cannot read the file"}
	}
	if len(body) > maxFileBytes {
		return nil, &Error{Path: path, Reason: "is too large to be a host credential store"}
	}
	return parse(path, body)
}

// parse decodes the document strictly: one JSON object of the one version,
// no unknown key, every entry whole, no two for one server and App. The
// reasons are fixed text: a decoder's own message can quote content.
func parse(path string, body []byte) ([]Entry, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var doc document
	if err := dec.Decode(&doc); err != nil {
		return nil, &Error{Path: path, Reason: "is not a host credential store (malformed JSON or an unknown field)"}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, &Error{Path: path, Reason: "is not a host credential store (data after the document)"}
	}
	if doc.Version != Version {
		return nil, &Error{Path: path, Reason: fmt.Sprintf("has an unknown version (this CLI reads version %d)", Version)}
	}
	entries := make([]Entry, 0, len(doc.Entries))
	for i, e := range doc.Entries {
		if !wholeEntry(e) {
			return nil, &Error{Path: path, Reason: fmt.Sprintf("entry %d is not a whole host credential", i+1)}
		}
		for _, prev := range entries {
			if prev.Matches(e.Server, e.AppID) {
				return nil, &Error{Path: path, Reason: fmt.Sprintf("entry %d repeats a server and App", i+1)}
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func wholeEntry(e Entry) bool {
	return strings.TrimSpace(e.Server) != "" && strings.TrimSpace(e.AppID) != "" &&
		hostid.Valid(e.HostID) && strings.TrimSpace(e.Key) != "" && !strings.ContainsAny(e.Key, " \t\r\n")
}
