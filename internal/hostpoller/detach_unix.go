//go:build !windows

package hostpoller

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

// detach starts the agent in a new session, so it outlives the poller and
// the poller's signals do not reach it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func realPath(p string) (string, error) { return filepath.EvalSymlinks(p) }

// keyFileTooOpen: any group or other permission bit.
func keyFileTooOpen(info os.FileInfo) bool { return info.Mode().Perm()&0o077 != 0 }

// processAlive: signal 0 probes without delivering; EPERM still means a
// process holds the pid.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// occupied scans /proc/<pid>/cwd for a process working inside path and names
// it by pid and argv (#1474: an operator must be able to tell WHICH process
// holds the tree — the comm name alone cannot). Only processes this user may
// inspect are seen, which covers agents the holder's user runs. Without /proc
// (macOS) it reports none.
func occupied(path string) (string, error) {
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir("/proc")
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	self := os.Getpid()
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		cwd, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if err != nil {
			continue // gone, or not ours to inspect
		}
		if cwd == root || strings.HasPrefix(cwd, root+string(os.PathSeparator)) {
			return fmt.Sprintf("pid %d, %s", pid, processArgv(e.Name())), nil
		}
	}
	return "", nil
}

// processArgv names a process by its command line (/proc/<pid>/cmdline,
// NUL-separated), falling back to comm for a process with no command line
// (a kernel thread) or an unreadable one. The line is rendered by
// renderArgv — quoted, redacted and bounded — because it leaves the host in
// a refusal reason and the logs (#1474).
func processArgv(dir string) string {
	raw, err := os.ReadFile(filepath.Join("/proc", dir, "cmdline"))
	if fields, ok := argvFromCmdline(raw); err == nil && ok {
		return renderArgv(fields)
	}
	comm, _ := os.ReadFile(filepath.Join("/proc", dir, "comm"))
	return strings.TrimSpace(string(comm))
}

// argvFromCmdline splits a /proc/<pid>/cmdline read: NUL-separated, ending
// in ONE terminator NUL. A further trailing NUL is an empty LAST argument,
// which is kept — an empty argv slot is not nothing (#1474 review; Jane D3:
// TrimRight ate every empty trailing slot). An empty read — kernel threads,
// zombies — is not an argv at all: false, so comm answers instead (#1474
// delta review P3-1: Split("") returns [""], which used to render as a bare
// quoted empty argument and starve the comm fallback).
func argvFromCmdline(raw []byte) ([]string, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00"), true
}

// argvDisplayLimit bounds how much of an occupant's command line leaves the
// host, in bytes. The pid, the executable and a short prefix of the rest are
// what an operator needs to tell WHICH process holds the tree (#1474); the
// rest of an arbitrary command line is not this host's to send or log.
const argvDisplayLimit = 200

// credentialName matches the NAME of an assignment (NAME=value,
// --flag=value) whose value looks like a credential. Over-matching is safe —
// the name alone still identifies the process; the value never leaves.
var credentialName = regexp.MustCompile(`(?i)(token|secret|password|passwd|passphrase|credential|api[-_]?key|private[-_]?key|session|authorization|cookie)`)

// credentialHeader matches an argument that IS a header whose name says it
// carries a credential ("Authorization: Bearer …", "Cookie: …",
// "X-API-Key: …", "X-Auth-Token: …", "Proxy-Authorization: …"): any header
// name containing auth, token, key, secret or cookie (#1474 delta review
// D2). Over-matching is safe — the name alone still identifies the header;
// the value never leaves.
var credentialHeader = regexp.MustCompile(`(?i)^[\w-]*(auth|token|key|secret|cookie)[\w-]*\s*:`)

// bareCredentialFlags are short flags whose NEXT argument is a credential
// (curl -u user:pass, sshpass -p secret). Redacting the value can over-reach
// — ssh's -p is a port — but over-matching is the safe direction: a port
// number never leaves instead of a password (#1474 delta review P3).
var bareCredentialFlags = map[string]bool{"-u": true, "-p": true}

// stripURLUserinfo removes user:password@ (or user@) from a URL-shaped
// argument: https://u:p@host/path renders as https://host/path (#1474 delta
// review P3). Only scheme://…@… shapes are touched; git@host:path and bare
// email addresses carry no scheme separator and pass through.
func stripURLUserinfo(arg string) string {
	i := strings.Index(arg, "://")
	if i < 0 {
		return arg
	}
	rest := arg[i+3:]
	at := strings.IndexByte(rest, '@')
	if at < 0 || strings.ContainsAny(rest[:at], "/?#") {
		return arg // no userinfo, or the @ belongs to a later component
	}
	return arg[:i+3] + rest[at+1:]
}

// renderArgv renders a command line for the occupied-tree refusal: an
// argument that would not survive a re-split on spaces is %q-quoted, a
// credential-shaped value keeps only its name, and the whole line is
// bounded (#1474 review P2-4: name WHICH process holds the tree, never its
// secrets, in the reason and in the logs).
func renderArgv(fields []string) string {
	out := make([]string, 0, len(fields))
	redactNext := false
	for _, f := range fields {
		f = stripURLUserinfo(f)
		switch {
		case redactNext:
			// The previous argument was a bare credential flag (--token,
			// -u, -p); this one is its value.
			out = append(out, "<redacted>")
			redactNext = false
		case credentialHeader.MatchString(f):
			name, _, _ := strings.Cut(f, ":")
			out = append(out, name+": <redacted>")
		default:
			name, _, hasValue := strings.Cut(f, "=")
			if hasValue && credentialName.MatchString(name) {
				out = append(out, name+"=<redacted>")
				continue
			}
			if !hasValue && strings.HasPrefix(f, "-") {
				if bareCredentialFlags[f] {
					out = append(out, f)
					redactNext = true
					continue
				}
				// An attached short-flag value (mysql -pSECRET, curl
				// -uuser:pass): the flag stays, the value never leaves.
				// This is checked BEFORE the credential-name branch — the
				// value attached to -p can itself contain "secret".
				if len(f) > 2 && (strings.HasPrefix(f, "-p") || strings.HasPrefix(f, "-u")) {
					out = append(out, f[:2]+"<redacted>")
					continue
				}
				if credentialName.MatchString(f) {
					out = append(out, f)
					redactNext = true
					continue
				}
			}
			out = append(out, quoteArg(f))
		}
	}
	line := strings.Join(out, " ")
	if len(line) > argvDisplayLimit {
		// Never slice a rune in half: back the cut off to a rune boundary.
		// Every argument is valid UTF-8 by now (quoteArg escapes invalid
		// bytes), so this back-off is bounded by one rune's length.
		cut := argvDisplayLimit
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		line = line[:cut] + "…"
	}
	return line
}

// quoteArg keeps an argument unambiguous in the joined line: only arguments
// that would not survive being re-split on spaces — or that carry invalid
// UTF-8, which would otherwise both render raw and make the byte-bound
// truncation back off past them (#1474 delta review P3) — are quoted, so a
// plain `sleep 30` stays readable while `sh -c "sleep 30 & wait"` keeps its
// shape and an invalid byte arrives as an escape, never raw.
func quoteArg(arg string) string {
	if arg == "" || !utf8.ValidString(arg) || strings.IndexFunc(arg, func(r rune) bool {
		return unicode.IsSpace(r) || r == '"' || r == '\\' || !unicode.IsPrint(r)
	}) >= 0 {
		return strconv.Quote(arg)
	}
	return arg
}

// signalGroup and probeGroup are the group primitives terminateGroup uses,
// as package vars so tests can make them fail (#1474 review: the
// signal-fails and does-not-exit paths are otherwise unreachable on a
// healthy host).
var (
	signalGroup = func(pgid int, sig syscall.Signal) error {
		return syscall.Kill(-pgid, sig)
	}
	probeGroup = func(pgid int) error {
		return syscall.Kill(-pgid, 0)
	}
)

// groupPollInterval is how often terminateGroup probes a group it is
// waiting on. It is far below any sane grace, so the probe cost bounds the
// latency of noticing the group emptied.
var groupPollInterval = 20 * time.Millisecond

// postKillWait bounds the wait after SIGKILL. It is deliberately NOT
// TerminateGrace again (the review's double-grace): SIGKILL cannot be
// caught or ignored, so only an unkillable (D-state) member can outlive it,
// and that is reported rather than waited on twice.
var postKillWait = 2 * time.Second

// groupGone reports whether the process group no longer exists: an ESRCH
// answer to a signal-0 probe of the group.
func groupGone(pgid int) bool {
	return errors.Is(probeGroup(pgid), syscall.ESRCH)
}

// groupGoneWithin polls the group until it is seen gone or wait passes;
// true when the group was seen gone.
func groupGoneWithin(pgid int, wait time.Duration) bool {
	if groupGone(pgid) {
		return true
	}
	ticker := time.NewTicker(groupPollInterval)
	defer ticker.Stop()
	timeout := time.NewTimer(wait)
	defer timeout.Stop()
	for {
		select {
		case <-ticker.C:
			if groupGone(pgid) {
				return true
			}
		case <-timeout.C:
			return groupGone(pgid)
		}
	}
}

// stopGuard is the Unix Terminate gate: this launch's process group was
// already seen EMPTY before the leader was reaped (noteGroupBeforeReap), so
// nothing of the launch is running and the group id has been reusable since
// the reap — probing or signalling it again could reach whatever holds the
// number now (#1474 delta review P2-1, Jane D1). Until then the id stays
// allocated while any member lives, and Terminate probes and signals as
// before.
func (a *execAgent) stopGuard() bool { return a.groupEmpty.Load() }

// terminateGroup stops the process group a launch created, and confirms the
// WHOLE group is gone — the leader being reaped is not the group being gone:
// a wrapper's child can outlive its leader (#1474). A launched agent runs as
// its own session leader (detach sets Setsid), so its group id is its pid,
// and that id cannot be reused while any member lives — the kernel keeps a
// pgid allocated until its group is empty, and an empty group answers ESRCH
// — so probing and signalling the group stays safe after the leader's exit,
// reaped or not. SIGTERM, a bounded grace, SIGKILL, then a short fixed wait;
// stopped is false whenever the group could not be confirmed empty.
func terminateGroup(pid int, grace time.Duration) (stopped bool, detail string) {
	if groupGone(pid) {
		// The group is already gone: nothing was running, nothing was
		// signalled, and the stop needs no words.
		return true, ""
	}
	if err := signalGroup(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return false, fmt.Sprintf("could not stop the launched process group (pid %d): %v", pid, err)
	}
	if groupGoneWithin(pid, grace) {
		return true, fmt.Sprintf("terminated the launched process group (pid %d)", pid)
	}
	if err := signalGroup(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return false, fmt.Sprintf("could not stop the launched process group (pid %d): it ignored SIGTERM for %s and SIGKILL failed: %v", pid, grace, err)
	}
	if groupGoneWithin(pid, postKillWait) {
		return true, fmt.Sprintf("killed the launched process group (pid %d) after it ignored SIGTERM for %s", pid, grace)
	}
	return false, fmt.Sprintf("the launched process group (pid %d) did not exit within %s of SIGKILL", pid, postKillWait)
}
