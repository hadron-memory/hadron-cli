//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package auth

import (
	"context"

	"golang.org/x/term"
)

// readHiddenLine is the portable read for platforms without a pollable
// terminal descriptor (Windows). term.ReadPassword cannot be interrupted, so
// when ctx ends first the read is abandoned and the console mode is put back;
// on those platforms a line typed afterwards may still be taken by it.
func readHiddenLine(ctx context.Context, fd int) (string, error) {
	saved, _ := term.GetState(fd)
	type result struct {
		line []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		b, err := term.ReadPassword(fd)
		done <- result{b, err}
	}()
	select {
	case r := <-done:
		return string(r.line), r.err
	case <-ctx.Done():
		if saved != nil {
			_ = term.Restore(fd, saved)
		}
		return "", ctx.Err()
	}
}
