//go:build unix

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// A FIFO and a device are refused (not hung on, not read), with the usage
// code and nothing written.
func TestSessionShedNoteFileRefusesFIFOAndDeviceWithoutBlocking(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "n.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no mkfifo:", err)
	}
	for name, path := range map[string]string{"fifo": fifo, "device": "/dev/null"} {
		t.Run(name, func(t *testing.T) {
			if _, err := os.Stat(path); err != nil {
				t.Skip("no such node:", err)
			}
			bindWorktree(t)
			url, rig := shedServer(t, nil)
			type res struct {
				out, errOut string
				err         error
			}
			done := make(chan res, 1)
			go func() {
				o, e, err := runShed(t, url, "--note-file", path)
				done <- res{o, e, err}
			}()
			select {
			case r := <-done:
				if exitCodeFor(r.err) != exitcode.Usage || r.err == nil || !strings.Contains(r.err.Error(), "not a regular file") || strings.Contains(r.out+r.errOut+r.err.Error(), path) {
					t.Errorf("want a usage refusal that does not name the path, got %v / %q", r.err, r.errOut)
				}
			case <-time.After(5 * time.Second):
				if fh, err := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
					_ = syscall.Close(fh)
				}
				t.Fatal("shed blocked on a non-regular notes file")
			}
			if rig.count("CreateNode") != 0 || rig.count("CreateResumeClaim") != 0 || shedStampExists() {
				t.Errorf("nothing written: %v", rig.calls)
			}
		})
	}
}
