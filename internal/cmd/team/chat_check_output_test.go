package team

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failedCheckFile struct {
	at     string
	closed bool
}

func (f *failedCheckFile) Write(p []byte) (int, error) {
	if f.at == "write" {
		return 0, errors.New("disk full")
	}
	if f.at == "shortnil" {
		return 1, nil
	}
	if f.at == "short" {
		return 1, io.ErrShortWrite
	}
	return len(p), nil
}
func (f *failedCheckFile) Sync() error {
	if f.at == "sync" {
		return errors.New("sync")
	}
	return nil
}
func (f *failedCheckFile) Close() error {
	f.closed = true
	if f.at == "close" {
		return errors.New("close")
	}
	return nil
}
func TestCheckArtifactFileFailures(t *testing.T) {
	for _, at := range []string{"write", "short", "shortnil", "sync", "close"} {
		t.Run(at, func(t *testing.T) {
			f := &failedCheckFile{at: at}
			if completeCheckFile(f, checkArtifact{}) == nil || !f.closed {
				t.Fatal("ignored failure or open handle")
			}
		})
	}
}
func TestCheckArtifactLongCompleteAndRetainedAfterPathFailure(t *testing.T) {
	cache := t.TempDir()
	a := checkArtifact{Version: 1, Messages: []teamChatMessageDTO{{Seq: 1, Body: strings.Repeat("body", 100000)}}}
	path, e := publishCheckArtifact(cache, a, checkBrokenWriter{})
	if e == nil || path == "" {
		t.Fatal(path, e)
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var got checkArtifact
	if json.Unmarshal(raw, &got) != nil || got.Messages[0].Body != a.Messages[0].Body {
		t.Fatal("truncated artifact")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 || entries[0].Name() != "messages.json" {
		t.Fatal(entries)
	}
}
func TestCheckArtifactPrivateCacheAndEmpty(t *testing.T) {
	var b bytes.Buffer
	path, e := publishCheckArtifact(t.TempDir(), checkArtifact{SampledHead: 10, Messages: []teamChatMessageDTO{}}, &b)
	if e != nil || path != "" || !strings.Contains(b.String(), "No tagged") {
		t.Fatal(path, e, b.String())
	}
	cache := filepath.Join(t.TempDir(), "notdir")
	_ = os.WriteFile(cache, []byte("x"), 0600)
	if _, e = publishCheckArtifact(cache, checkArtifact{Messages: []teamChatMessageDTO{{Seq: 1}}}, &b); e == nil {
		t.Fatal("accepted invalid cache")
	}
}

type checkFlushFailure struct{ bytes.Buffer }

func (*checkFlushFailure) Flush() error { return errors.New("flush failed") }
func TestCheckArtifactPathFlushFailure(t *testing.T) {
	w := &checkFlushFailure{}
	path, e := publishCheckArtifact(t.TempDir(), checkArtifact{Messages: []teamChatMessageDTO{{Seq: 1}}}, w)
	if e == nil || path == "" {
		t.Fatal(path, e)
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal(e)
	}
}
