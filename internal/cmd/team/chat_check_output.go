package team

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type checkArtifact struct {
	Version            int                  `json:"version"`
	WorkerID           string               `json:"workerId"`
	ChannelID          string               `json:"channelId"`
	ChannelIncarnation int                  `json:"channelIncarnation"`
	SampledCursor      int                  `json:"sampledCursor"`
	SampledHead        int                  `json:"sampledHead"`
	Messages           []teamChatMessageDTO `json:"messages"`
}

// Each artifact gets a fresh exclusive private directory. No shared predictable
// file or existing directory can redirect a write; the completed file is retained
// if printing its path or the later mark fails.
func publishCheckArtifact(cache string, a checkArtifact, notice io.Writer) (path string, err error) {
	if len(a.Messages) == 0 {
		return "", writeCheckNotice(notice, fmt.Sprintf("No tagged messages in (%d, %d]; preparing mark through #%d (including untagged messages).\n", a.SampledCursor, a.SampledHead, a.SampledHead))
	}
	if !filepath.IsAbs(cache) || len(cache) > 1024 {
		return "", fmt.Errorf("private artifact cache path is not an absolute bounded path")
	}
	if err = os.MkdirAll(cache, 0700); err != nil {
		return "", fmt.Errorf("cannot create artifact cache")
	}
	dir, err := os.MkdirTemp(cache, "hadron-chat-check-")
	if err != nil {
		return "", fmt.Errorf("cannot create private artifact directory")
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(dir)
		}
	}()
	tmp := filepath.Join(dir, "messages.partial")
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("cannot create private message artifact")
	}
	if err = completeCheckFile(file, a); err != nil {
		return "", err
	}
	path = filepath.Join(dir, "messages.json")
	if err = os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("cannot finish message artifact")
	}
	handle, err := os.Open(dir)
	if err != nil {
		return "", fmt.Errorf("cannot open artifact directory")
	}
	syncErr := handle.Sync()
	closeErr := handle.Close()
	if syncErr != nil || closeErr != nil {
		return "", fmt.Errorf("cannot sync artifact directory")
	}
	complete = true
	// JSON quoting keeps even an operator-supplied cache path on one diagnostic line.
	if err = writeCheckNotice(notice, fmt.Sprintf("Tagged messages written to %q; open this file to read them. Preparing mark through #%d (including untagged messages).\n", path, a.SampledHead)); err != nil {
		return path, err
	}
	return path, nil
}
func writeCheckNotice(w io.Writer, s string) error {
	n, err := io.WriteString(w, s)
	if err == nil && n != len(s) {
		return io.ErrShortWrite
	}
	if err == nil {
		if f, ok := w.(interface{ Flush() error }); ok {
			return f.Flush()
		}
	}
	return err
}

// Separate the content/close boundary for short-write, disk-full and close-error
// tests. Production always supplies the exclusive private os.File above.
type checkArtifactFile interface {
	io.Writer
	Sync() error
	Close() error
}

func completeCheckFile(file checkArtifactFile, a checkArtifact) error {
	if err := writeCheckJSON(file, a); err != nil {
		_ = file.Close()
		return fmt.Errorf("cannot write complete message artifact")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("cannot sync message artifact")
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("cannot close message artifact")
	}
	return nil
}

func writeCheckJSON(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}

type checkFullWriter struct{ io.Writer }

func (w checkFullWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}
