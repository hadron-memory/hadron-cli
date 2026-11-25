//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package auth

import (
	"bytes"
	"context"
	"errors"

	"golang.org/x/sys/unix"
)

// pollInterval is how often a waiting read looks at ctx.
const pollInterval = 100 // milliseconds

const maxPastedLine = 8192

// readHiddenLine reads one line from the terminal fd with echo off. It never
// blocks inside read(2): it polls the descriptor and reads only what is
// already there, so when ctx ends (the redirect arrived first, the timeout
// fired, Ctrl+C) it returns at once with the terminal's settings restored and
// without having consumed a byte meant for whatever reads next.
func readHiddenLine(ctx context.Context, fd int) (string, error) {
	saved, err := unix.IoctlGetTermios(fd, termiosGet)
	if err != nil {
		return "", err
	}
	quiet := *saved
	quiet.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, termiosSet, &quiet); err != nil {
		return "", err
	}
	defer func() { _ = unix.IoctlSetTermios(fd, termiosSet, saved) }()

	var buf []byte
	chunk := make([]byte, 512)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, pollInterval)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return "", err
		}
		if n == 0 {
			continue
		}
		m, err := unix.Read(fd, chunk)
		if err != nil {
			if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
				continue
			}
			return "", err
		}
		if m == 0 { // end of input (Ctrl+D)
			if len(buf) > 0 {
				return string(buf), nil
			}
			return "", errors.New("end of input")
		}
		buf = append(buf, chunk[:m]...)
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			return string(bytes.TrimRight(buf[:i], "\r")), nil
		}
		if len(buf) > maxPastedLine {
			return "", errors.New("input too long")
		}
	}
}
