//go:build linux

package auth

// hadron-cli#3180: the terminal read behind the code prompt, on a real
// pseudo-terminal. A fake reader cannot see these: whether echo is off while
// waiting and restored after, and whether an abandoned wait leaves the line the
// next reader needs unconsumed.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func openPTY3180(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal here: %v", err)
	}
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("pty unlock: %v", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Skipf("pty number: %v", err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("pty slave: %v", err)
	}
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	return master, slave
}

func echoOn(t *testing.T, f *os.File) bool {
	t.Helper()
	tio, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	return tio.Lflag&unix.ECHO != 0
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A pasted line is read, echo is off while waiting and on again afterwards.
func TestReadHiddenLineEchoOffThenRestored_3180(t *testing.T) {
	master, slave := openPTY3180(t)
	if !echoOn(t, slave) {
		t.Fatal("the pty should start with echo on")
	}
	type res struct {
		line string
		err  error
	}
	done := make(chan res, 1)
	go func() {
		l, err := readHiddenLine(context.Background(), int(slave.Fd()))
		done <- res{l, err}
	}()
	waitFor(t, "echo to go off while the prompt waits", func() bool { return !echoOn(t, slave) })
	if _, err := master.WriteString("the-pasted-code\n"); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil || r.line != "the-pasted-code" {
		t.Fatalf("got %q, %v", r.line, r.err)
	}
	if !echoOn(t, slave) {
		t.Errorf("echo must be restored after a normal read")
	}
}

// Ending the wait (the redirect won, the timeout, Ctrl+C) returns at once,
// restores echo, and consumes nothing: a line typed afterwards reaches the
// next reader intact.
func TestReadHiddenLineAbandonedLeavesInputAlone_3180(t *testing.T) {
	master, slave := openPTY3180(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := readHiddenLine(ctx, int(slave.Fd()))
		done <- err
	}()
	waitFor(t, "echo to go off while the prompt waits", func() bool { return !echoOn(t, slave) })
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want the context's error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("an abandoned wait must return promptly")
	}
	if !echoOn(t, slave) {
		t.Errorf("echo must be restored when the wait is abandoned")
	}
	// The install-skills prompt reads next: it must see its own answer.
	if _, err := master.WriteString("y\n"); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(slave).ReadString('\n')
		got <- line
	}()
	select {
	case line := <-got:
		if line != "y\n" {
			t.Errorf("the next reader got %q, want %q (the abandoned read swallowed input)", line, "y\n")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the next reader never saw its line")
	}
}
