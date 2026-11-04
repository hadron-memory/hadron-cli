package cmdutil

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

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
			return nil, exitcode.Newf(exitcode.Usage, "--unlock names %s twice. Nothing was sent", ref)
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
			return nil, exitcode.Newf(exitcode.Usage, "the passphrase for --unlock %s is empty. Nothing was sent", ref)
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

// readPassphraseFile reads one passphrase file under the rule ssh applies to a
// private key: a file any group or other user can access is refused, before it
// is read and before any request. The mode is checked on the OPENED file, so a
// swap between check and read cannot slip a different file in. Errors name the
// file by position, never echo the path (a passphrase pasted in place of a
// path must not be printed back, #1490).
func readPassphraseFile(path string, n int) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", exitcode.Newf(exitcode.Usage, "--unlock-passphrase-file #%d is empty: name a file. Nothing was sent", n)
	}
	if runtime.GOOS == "windows" {
		return "", exitcode.Newf(exitcode.Usage, "--unlock-passphrase-file is not supported on Windows: its file permissions cannot be checked. Use the prompt or --unlock-passphrase-stdin. Nothing was sent")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", exitcode.Newf(exitcode.Usage, "reading --unlock-passphrase-file #%d: %s. Nothing was sent", n, readFailureClass(err))
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", exitcode.Newf(exitcode.Usage, "reading --unlock-passphrase-file #%d: the file could not be read. Nothing was sent", n)
	}
	if !info.Mode().IsRegular() {
		return "", exitcode.Newf(exitcode.Usage, "--unlock-passphrase-file #%d is not a regular file. Nothing was sent", n)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return "", exitcode.Newf(exitcode.Usage, "--unlock-passphrase-file #%d is accessible by group or others (mode %04o); run chmod 600 on it. Nothing was sent", n, perm)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", exitcode.Newf(exitcode.Usage, "reading --unlock-passphrase-file #%d: the file could not be read. Nothing was sent", n)
	}
	return trimPassphrase(data), nil
}
