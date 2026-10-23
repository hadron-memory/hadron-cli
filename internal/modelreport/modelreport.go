// Package modelreport reads the model a native agent session is running from
// that session's transcript file, for `hadron team session sync-model`
// (hadron-cli#995, #2252 slice B1).
//
// The reader is deliberately narrow. It never chooses a transcript: the only
// file it will read is the one the bound Hadron session already RECORDED as
// its transcriptPath, and the hook's path, native session id and cwd are
// claims it verifies against that file, not an association source. Anything
// it cannot prove returns ErrUnavailable, carrying a fixed reason and no path
// or transcript content, so a caller reports nothing rather than guess.
package modelreport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Read budgets. Over any of them the answer is "unavailable", never a partial
// guess.
const (
	maxHeader = 64 << 10
	maxTail   = 4 << 20
	maxRecord = 1 << 20
	maxModel  = 128
)

// ErrUnavailable is the one failure: no model could be proven. Reasons are
// fixed strings.
var ErrUnavailable = errors.New("model unavailable")

func unavailable(reason string) error { return errors.Join(ErrUnavailable, errors.New(reason)) }

// Claim is what the native tool's hook supplied on stdin.
type Claim struct {
	SessionID      string
	TranscriptPath string
	Cwd            string
}

// Options: Recorded is the bound session's server-recorded transcriptPath.
type Options struct {
	Recorded string
	Claim    Claim
	Roots    []string
}

// Test seams for the replacement races; nil in production.
var (
	afterCanonical func()
	afterRead      func()
)

// DefaultRoots are the transcript roots the supported tools write under.
func DefaultRoots() []string {
	home, _ := os.UserHomeDir()
	roots := []string{}
	claude := os.Getenv("CLAUDE_CONFIG_DIR")
	if claude == "" && home != "" {
		claude = filepath.Join(home, ".claude")
	}
	if claude != "" {
		roots = append(roots, filepath.Join(claude, "projects"))
	}
	codex := os.Getenv("CODEX_HOME")
	if codex == "" && home != "" {
		codex = filepath.Join(home, ".codex")
	}
	if codex != "" {
		roots = append(roots, filepath.Join(codex, "sessions"))
	}
	return roots
}

// ValidModel bounds a model string before it goes on the wire.
func ValidModel(s string) bool {
	if s == "" || len(s) > maxModel || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

func canonical(p string) (string, error) {
	if p == "" {
		return "", errors.New("empty")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func underRoot(p string, roots []string) bool {
	for _, r := range roots {
		cr, err := canonical(r)
		if err != nil {
			continue
		}
		if p == cr {
			continue
		}
		if strings.HasPrefix(p, strings.TrimRight(cr, string(filepath.Separator))+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Read returns the model of the native session, or ErrUnavailable.
func Read(o Options) (string, error) {
	if o.Recorded == "" || o.Claim.TranscriptPath == "" || o.Claim.SessionID == "" {
		return "", unavailable("no recorded transcript association")
	}
	rec, err := canonical(o.Recorded)
	if err != nil {
		return "", unavailable("recorded transcript not resolvable")
	}
	hook, err := canonical(o.Claim.TranscriptPath)
	if err != nil || hook != rec {
		return "", unavailable("hook transcript is not the recorded transcript")
	}
	if !underRoot(rec, o.Roots) {
		return "", unavailable("transcript outside the allowed roots")
	}
	pre, err := os.Lstat(rec)
	if err != nil || !pre.Mode().IsRegular() {
		return "", unavailable("transcript is not a regular file")
	}
	if afterCanonical != nil {
		afterCanonical()
	}
	f, err := openNoFollow(rec)
	if err != nil {
		return "", unavailable("transcript not readable")
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || !os.SameFile(pre, fi) {
		return "", unavailable("transcript identity changed")
	}
	size := fi.Size()

	kind, err := identify(f, size, o.Claim)
	if err != nil {
		return "", err
	}
	model, err := newestModel(f, size, kind, o.Claim.SessionID)
	if err != nil {
		return "", err
	}
	if afterRead != nil {
		afterRead()
	}
	// Identity must still hold at the final read check: the fd is unchanged
	// in size, and the recorded path still names the file we read.
	post, err := f.Stat()
	if err != nil || post.Size() < size {
		return "", unavailable("transcript truncated during read")
	}
	again, err := canonical(o.Recorded)
	if err != nil || again != rec {
		return "", unavailable("transcript path changed during read")
	}
	now, err := os.Stat(again)
	if err != nil || !os.SameFile(now, fi) {
		return "", unavailable("transcript replaced during read")
	}
	if !ValidModel(model) {
		return "", unavailable("model string not acceptable")
	}
	return model, nil
}

type adapter int

const (
	claudeAdapter adapter = iota + 1
	codexAdapter
)

func lines(b []byte) [][]byte { return bytes.Split(b, []byte("\n")) }

// identify reads the bounded header and proves the file's own native
// identity equals the hook's session id, by typed envelope field.
func identify(f *os.File, size int64, c Claim) (adapter, error) {
	n := int64(maxHeader)
	if size < n {
		n = size
	}
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
		return 0, unavailable("transcript header unreadable")
	}
	ls := lines(buf)
	if n == int64(maxHeader) && size > n {
		ls = ls[:len(ls)-1] // the last header line may be cut
	}
	for _, l := range ls {
		var rec struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			Payload   struct {
				ID  string `json:"id"`
				Cwd string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(l, &rec) != nil {
			continue
		}
		if rec.Type == "session_meta" {
			if rec.Payload.ID != c.SessionID {
				return 0, unavailable("native session id mismatch")
			}
			if c.Cwd == "" || rec.Payload.Cwd != c.Cwd {
				return 0, unavailable("native session cwd mismatch")
			}
			return codexAdapter, nil
		}
		if rec.SessionID != "" {
			if rec.SessionID != c.SessionID {
				return 0, unavailable("native session id mismatch")
			}
			return claudeAdapter, nil
		}
	}
	return 0, unavailable("no native session identity in transcript header")
}

// newestModel scans the bounded tail newest-first for the latest model
// record of the adapter's typed envelope.
func newestModel(f *os.File, size int64, kind adapter, nativeID string) (string, error) {
	start := size - maxTail
	if start < 0 {
		start = 0
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return "", unavailable("transcript tail unreadable")
	}
	i := bytes.LastIndexByte(buf, '\n')
	if i < 0 {
		return "", unavailable("no complete record in the read window")
	}
	// A partial final line is dropped (the writer may be mid-append) — but one
	// already over the record budget is oversized input, not a partial write,
	// and an older model must not be reported past it.
	if len(buf)-i-1 > maxRecord {
		return "", unavailable("record over budget")
	}
	buf = buf[:i]
	ls := lines(buf)
	if start > 0 {
		ls = ls[1:] // the first line of a mid-file window may be cut
	}
	for i := len(ls) - 1; i >= 0; i-- {
		l := ls[i]
		if len(l) > maxRecord {
			return "", unavailable("record over budget")
		}
		if m, ok := modelOf(l, kind, nativeID); ok {
			return m, nil
		}
	}
	return "", unavailable("no model record in the read window")
}

func modelOf(l []byte, kind adapter, nativeID string) (string, bool) {
	var rec struct {
		Type        string `json:"type"`
		SessionID   string `json:"sessionId"`
		IsSidechain bool   `json:"isSidechain"`
		Message     struct {
			Model string `json:"model"`
		} `json:"message"`
		Payload struct {
			Model string `json:"model"`
		} `json:"payload"`
	}
	if json.Unmarshal(l, &rec) != nil {
		return "", false
	}
	switch kind {
	case claudeAdapter:
		if rec.Type != "assistant" || rec.IsSidechain || rec.Message.Model == "" || rec.Message.Model == "<synthetic>" {
			return "", false
		}
		if rec.SessionID != "" && rec.SessionID != nativeID {
			return "", false
		}
		return rec.Message.Model, true
	case codexAdapter:
		if rec.Type != "turn_context" || rec.Payload.Model == "" {
			return "", false
		}
		return rec.Payload.Model, true
	}
	return "", false
}
