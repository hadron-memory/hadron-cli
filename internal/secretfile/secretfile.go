// Package secretfile creates a file that is going to hold a secret, so that
// the file is never wider than 0600 and never reached through a symlink as its
// final component (cli#1223). The destination is opened BEFORE the secret exists, and the
// caller commits or aborts afterwards.
package secretfile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Rename, Write and Remove are seams so a test can force the late failures
// (the ones that happen after the secret was minted); production never changes
// them.
var (
	Rename = os.Rename
	Remove = os.Remove
	Write  = func(f *os.File, b []byte) error { _, err := f.Write(b); return err }
)

// ErrExists reports a destination that already exists (a file, a directory or
// a symlink, dangling or not) and --force was not given.
var ErrExists = errors.New("path already exists")

// ErrIsDir reports a destination that is a directory, which --force never
// replaces.
var ErrIsDir = errors.New("path is a directory")

// LeftBehindError reports that cleanup could not remove the file Open created,
// so it may still hold part or all of the secret. Path is the file to delete.
type LeftBehindError struct {
	Path string
	Err  error
}

func (e *LeftBehindError) Error() string {
	return fmt.Sprintf("could not remove %s, which may hold key material; delete it yourself", e.Path)
}

func (e *LeftBehindError) Unwrap() error { return e.Err }

// remove deletes the file Open created; an already-missing file is success.
func (p *Pending) remove() error {
	if err := Remove(p.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return &LeftBehindError{Path: p.path, Err: err}
	}
	return nil
}

// Pending is an opened, still empty destination.
type Pending struct {
	final string // the path the caller asked for
	path  string // the file that was opened: final, or a same-dir temp under force
	f     *os.File
	force bool
}

// Open creates the destination: O_CREAT|O_EXCL|O_NOFOLLOW at 0600 on the path
// itself, or — with force — on a fresh temp file in the same directory, which
// Commit renames over the path. The final path component is never followed if it is a symlink (O_NOFOLLOW;
// on windows, CREATE_NEW with FILE_FLAG_OPEN_REPARSE_POINT — see noFollow);
// symlinked parent directories are followed like any other path. On windows the
// 0600 mode only clears the read-only bit: the file takes its directory's
// inherited ACL.
func Open(final string, force bool) (*Pending, error) {
	p := &Pending{final: final, path: final, force: force}
	flags := os.O_CREATE | os.O_EXCL | os.O_WRONLY | noFollow
	if force {
		if fi, err := os.Lstat(final); err == nil && fi.IsDir() {
			return nil, fmt.Errorf("%w: %s", ErrIsDir, final)
		}
		var suffix [6]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, err
		}
		p.path = filepath.Join(filepath.Dir(final), "."+filepath.Base(final)+".tmp-"+hex.EncodeToString(suffix[:]))
	}
	f, err := os.OpenFile(p.path, flags, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) && !force {
			return nil, fmt.Errorf("%w: %s", ErrExists, final)
		}
		return nil, err
	}
	p.f = f
	return p, nil
}

// Abort closes and removes the file Open created. Safe to call after a failed
// Commit and more than once. Under force an existing destination is untouched.
// A *LeftBehindError means the file could not be removed and the caller must
// tell the user where it is.
func (p *Pending) Abort() error {
	if p.f != nil {
		_ = p.f.Close()
		p.f = nil
	}
	return p.remove()
}

// Commit writes the secret and, under force, renames the temp over the
// destination. On any failure the file Open created is removed; if that removal
// fails too the error is a *LeftBehindError naming the file. The error never contains the secret unless the
// underlying write error does, so callers must not print it raw.
func (p *Pending) Commit(secret []byte) error {
	if p.f == nil {
		return errors.New("secretfile: already closed")
	}
	err := Write(p.f, secret)
	if cerr := p.f.Close(); err == nil {
		err = cerr
	}
	p.f = nil
	if err == nil && p.force {
		err = Rename(p.path, p.final)
	}
	if err != nil {
		if rerr := p.remove(); rerr != nil {
			return rerr
		}
		return errors.New("could not write the key file")
	}
	return nil
}
