package cmdutil

import (
	"io"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// unreadPipeWait bounds the one-byte peek RefuseUnreadStdinPipe makes. A pipe
// whose writer is alive but silent (an agent harness or cron wrapper that
// leaves stdin open and never closes it) would block a plain read forever, and
// a guard must never turn a working invocation into a hang. A pipe that has
// bytes offers them at once (`cat f | …`, a heredoc), so a short wait loses
// nothing. This is a deliberate RACE between the writer and the clock: a writer
// whose first byte arrives after the wait is not caught, which is the accepted
// edge of a best-effort guard (the failure is the pre-guard behaviour, not a
// new one). A var so tests can shorten it.
var unreadPipeWait = 250 * time.Millisecond

// RefuseUnreadStdinPipe refuses a NON-EMPTY stdin that no flag was told to read
// (#1450). `cat body.md | hadron node create …` with no `--content -` used to
// exit 0 with an empty body: the pipe was never read, the success line looked
// the same, and a scripted caller lost its document. A warning would still
// store the empty body, so only a refusal closes the trap.
//
// The caller invokes it only on the path where NO flag reads stdin; a pipe
// that a flag consumes must never reach it. Fires only when stdin is not a
// terminal and a one-byte peek returns a byte. An empty pipe or /dev/null
// (EOF), a silent open pipe (the wait above), and a read error all pass: the
// guard exists for a pipe that is offering input, not to police stdin. The
// peeked byte of a non-seekable pipe cannot be restored; the refusal ends the
// command, so only a caller that shares one stdin across several commands (a
// `while read` loop) can observe it, and the message names `< /dev/null` for
// that case. A seekable stdin (a file redirect) is repaired by seeking back.
//
// flags names the flags that would read the pipe (the remedy) and consequence
// what would otherwise happen, so each command's message is specific to it.
// Exit 2, the family of the terminal refusal and #1035's zero-byte refusal.
func RefuseUnreadStdinPipe(stdin io.Reader, stdinIsTerminal bool, flags, consequence string) error {
	if stdin == nil || stdinIsTerminal {
		return nil
	}
	type peek struct {
		n   int
		err error
	}
	ch := make(chan peek, 1)
	go func() {
		var b [1]byte
		for {
			n, err := stdin.Read(b[:])
			if n > 0 || err != nil {
				ch <- peek{n, err}
				return
			}
		}
	}()
	select {
	case p := <-ch:
		if p.n == 0 {
			return nil
		}
		// Undo the peek where the stream allows it, so a refusal inside a
		// `while read …; done < file` loop does not also eat a byte of the
		// loop's next line.
		if sk, ok := stdin.(io.Seeker); ok {
			_, _ = sk.Seek(-1, io.SeekCurrent)
		}
	case <-time.After(unreadPipeWait):
		return nil
	}
	return exitcode.Newf(exitcode.Usage,
		"stdin has input but no flag reads it — pass %s to read a pipe (or redirect stdin from /dev/null if it is not meant for this command); refusing rather than %s", flags, consequence)
}
