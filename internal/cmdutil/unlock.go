package cmdutil

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// UnlockFlags is the root's --unlock state for one invocation (hadron-cli#1234).
//
// Ruling 0129 (Holger, "(a)"): a passphrase is supplied per invocation only,
// from a no-echo prompt, a mode-checked file or stdin. It is never an argv
// value and never an environment variable, and nothing is cached between
// invocations. There is deliberately no flag that takes the passphrase itself.
type UnlockFlags struct {
	Memories []string // --unlock <memory-ref>, repeatable up to api.MaxMemoryKeys
	Files    []string // --unlock-passphrase-file <path>, paired with --unlock in order
	Stdin    bool     // --unlock-passphrase-stdin, one memory only
	// FilesSet / StdinSet record that the flag was given at all, so an empty
	// --unlock-passphrase-file= is refused instead of read as absent.
	FilesSet bool
	StdinSet bool
}

// unlockPrompt reads one passphrase with no echo. Replaced in tests, which
// cannot drive a real terminal.
var unlockPrompt = promptNoEcho

// SetUnlockPromptForTest replaces the no-echo prompt and returns a restore func.
func SetUnlockPromptForTest(fn func(*output.IOStreams, string) (string, error)) func() {
	prev := unlockPrompt
	unlockPrompt = fn
	return func() { unlockPrompt = prev }
}

func promptNoEcho(ios *output.IOStreams, label string) (string, error) {
	input, ok := ios.In.(*os.File)
	if !ok {
		return "", exitcode.Newf(exitcode.Usage, "the --unlock passphrase prompt needs a terminal on stdin; use --unlock-passphrase-file or --unlock-passphrase-stdin. Nothing was sent")
	}
	if _, err := fmt.Fprintf(ios.ErrOut, "%s: ", label); err != nil {
		return "", err
	}
	data, err := term.ReadPassword(int(input.Fd()))
	fmt.Fprintln(ios.ErrOut)
	if err != nil {
		return "", exitcode.Newf(exitcode.Usage, "reading the --unlock passphrase failed. Nothing was sent")
	}
	return string(data), nil
}

// ResolveUnlock turns the --unlock flags into the invocation's keys, reading
// every passphrase BEFORE any request is sent; every refusal here is a usage
// error with nothing sent. No error ever contains a passphrase or a path's
// content.
func ResolveUnlock(ios *output.IOStreams, u UnlockFlags) ([]api.MemoryKey, error) {
	if len(u.Memories) == 0 {
		if u.FilesSet || u.StdinSet {
			return nil, exitcode.Newf(exitcode.Usage, "--unlock-passphrase-file and --unlock-passphrase-stdin need --unlock <memory-ref>. Nothing was sent")
		}
		return nil, nil
	}
	if len(u.Memories) > api.MaxMemoryKeys {
		return nil, exitcode.Newf(exitcode.Usage, "at most %d --unlock memories per invocation (got %d). Nothing was sent", api.MaxMemoryKeys, len(u.Memories))
	}
	refs := make([]string, 0, len(u.Memories))
	seen := map[string]bool{}
	for _, m := range u.Memories {
		if strings.TrimSpace(m) == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--unlock was given an empty memory ref. Nothing was sent")
		}
		ref := CanonicalMemoryRef(m)
		if seen[ref] {
			return nil, exitcode.Newf(exitcode.Usage, "--unlock names the same memory twice (the ref given is not shown). Nothing was sent")
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	if u.Stdin && u.FilesSet {
		return nil, exitcode.Newf(exitcode.Usage, "--unlock-passphrase-stdin and --unlock-passphrase-file are mutually exclusive. Nothing was sent")
	}
	passes := make([]string, len(refs))
	switch {
	case u.Stdin:
		if len(refs) != 1 {
			return nil, exitcode.Newf(exitcode.Usage, "--unlock-passphrase-stdin supplies ONE memory's passphrase (got %d --unlock); use one --unlock-passphrase-file per memory, or the prompt. Nothing was sent", len(refs))
		}
		data, err := io.ReadAll(ios.In)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "reading the --unlock passphrase from stdin failed. Nothing was sent")
		}
		passes[0] = trimPassphrase(data)
	case u.FilesSet:
		if len(u.Files) != len(refs) {
			return nil, exitcode.Newf(exitcode.Usage, "give one --unlock-passphrase-file per --unlock, in the same order (got %d files for %d memories). Nothing was sent", len(u.Files), len(refs))
		}
		for i, path := range u.Files {
			p, err := readPassphraseFile(path, i+1)
			if err != nil {
				return nil, err
			}
			passes[i] = p
		}
	default:
		if !ios.IsInputTerminal() {
			return nil, exitcode.Newf(exitcode.Usage, "--unlock needs a passphrase: run on a terminal to be prompted, or pass --unlock-passphrase-file or --unlock-passphrase-stdin. There is no argument or environment variable for it. Nothing was sent")
		}
		for i, ref := range refs {
			p, err := unlockPrompt(ios, "Passphrase for "+ref)
			if err != nil {
				return nil, err
			}
			passes[i] = strings.TrimRight(p, "\r\n")
		}
	}
	keys := make([]api.MemoryKey, len(refs))
	for i, ref := range refs {
		if passes[i] == "" {
			return nil, exitcode.Newf(exitcode.Usage, "the passphrase for --unlock number %d is empty (the ref given is not shown). Nothing was sent", i+1)
		}
		keys[i] = api.MemoryKey{Memory: ref, Passphrase: passes[i]}
	}
	return keys, nil
}

// trimPassphrase drops trailing newlines exactly as `memory encrypt
// --passphrase-file` does, so the file that encrypted a memory unlocks it.
func trimPassphrase(data []byte) string {
	return strings.TrimRight(string(data), "\r\n")
}

// afterPassphraseStat is a test seam: it runs between the pre-open Stat and
// the open, where a swap would land.
var afterPassphraseStat = func(string) {}

// openNoBlock opens path read-only with O_NONBLOCK, so a FIFO swapped in after
// the Stat cannot block the open waiting for a writer. A regular file ignores
// the flag. Windows never reaches this (the file source is refused there).
func openNoBlock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

// readPassphraseFile reads one passphrase file under the rule ssh applies to a
// private key: a file any group or other user can access is refused, before it
// is read and before any request. See readPrivateFile for the checks.
func readPassphraseFile(path string, n int) (string, error) {
	who := fmt.Sprintf("--unlock-passphrase-file #%d", n)
	data, err := readPrivateFile(path, who,
		who+" is empty: name a file. Nothing was sent",
		"--unlock-passphrase-file is not supported on Windows: its file permissions cannot be checked. Use the prompt or --unlock-passphrase-stdin. Nothing was sent")
	if err != nil {
		return "", err
	}
	return trimPassphrase(data), nil
}

// ReadSecretFile reads a file holding one secret (a data key) under the same
// rule and the same wording as readPassphraseFile, so the two secrets share one
// check: refused before any request when a group or other user can access it,
// when it is not a regular file, or when it changed while it was opened. flag is
// the flag's name as the user typed it (for example "--data-key-file"); errors
// name the flag, never the path, so a secret pasted in place of a path is never
// printed back (#1490). It reads regular files only: "-" (stdin) is the
// caller's to handle. The bytes are returned untrimmed.
func ReadSecretFile(path, flag string) ([]byte, error) {
	return readPrivateFile(path, flag,
		flag+" is empty: name a file, or - for stdin. Nothing was sent",
		flag+" is not supported on Windows: its file permissions cannot be checked. Pipe the key and use "+flag+" -. Nothing was sent")
}

// readPrivateFile is the shared body: who names the flag in every message,
// emptyMsg and windowsMsg are the two flag-specific refusals.
func readPrivateFile(path, who, emptyMsg, windowsMsg string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, exitcode.Newf(exitcode.Usage, "%s", emptyMsg)
	}
	if runtime.GOOS == "windows" {
		return nil, exitcode.Newf(exitcode.Usage, "%s", windowsMsg)
	}
	// Refuse a FIFO, device or directory BEFORE opening it: opening a FIFO
	// for reading blocks until a writer appears. The Stat alone cannot
	// guarantee that (a FIFO can replace the file between it and the open,
	// PR #1616 BLIND P2-2), so the open is non-blocking too and the
	// descriptor checks below refuse whatever it reached.
	before, err := os.Stat(path)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "reading %s: %s. Nothing was sent", who, readFailureClass(err))
	}
	if !before.Mode().IsRegular() {
		return nil, exitcode.Newf(exitcode.Usage, "%s is not a regular file. Nothing was sent", who)
	}
	afterPassphraseStat(path)
	file, err := openNoBlock(path)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "reading %s: %s. Nothing was sent", who, readFailureClass(err))
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "reading %s: the file could not be read. Nothing was sent", who)
	}
	// The checks below are on the OPENED file; a swap after the Stat above
	// is refused rather than read.
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return nil, exitcode.Newf(exitcode.Usage, "%s changed while it was opened. Nothing was sent", who)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return nil, exitcode.Newf(exitcode.Usage, "%s is accessible by group or others (mode %04o); run chmod 600 on it. Nothing was sent", who, perm)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "reading %s: the file could not be read. Nothing was sent", who)
	}
	return data, nil
}
