package hostpoller

import "os"

// trustWalk is the filesystem the reload trust check reads (Unix; the Windows
// stub takes it for the shared signature). The zero value uses the real one;
// the poller carries it (Poller.trust) so a test substitutes a probe on its
// own poller — a package-level variable would be shared by every test at
// once, so tests that override it could not run in parallel (cli#2653 item 3).
type trustWalk struct {
	// lstatLink reads a followed symlink's own metadata; a test can report a
	// link another user owns (a test cannot create one).
	lstatLink func(string) (os.FileInfo, error)
	// readlink reads a followed symlink's target; a test can make the read
	// fail on a link the walk has already listed (cli#2653 item 7).
	readlink func(string) (string, error)
}

func (w trustWalk) lstat(name string) (os.FileInfo, error) {
	if w.lstatLink != nil {
		return w.lstatLink(name)
	}
	return os.Lstat(name)
}

func (w trustWalk) readLink(name string) (string, error) {
	if w.readlink != nil {
		return w.readlink(name)
	}
	return os.Readlink(name)
}
