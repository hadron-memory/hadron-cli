package team

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#2369: a relative delivery.orca_bin is refused by the sweep, as the
// status read refuses it. Before the fix the sweep stat'ed the name against the
// working directory, replaced a missing one with "orca", and left a present
// one to be resolved on PATH at every exec.

// relativeOrcaBin2369 runs `team health sweep` on a host config (delivery off,
// the only place the gap is reachable) naming orcaBin, with every way the
// sweep could touch the world wired to fail: the server, the status read and
// the Orca runner. It returns the touch counts and the command's error.
func relativeOrcaBin2369(t *testing.T, orcaBin string) (requests, reads, runners int32, err error) {
	t.Helper()
	oldRunner := orcaRunner
	orcaRunner = func(string) orcaterm.Runner {
		atomic.AddInt32(&runners, 1)
		return func(context.Context, []string) orcaterm.Result { return orcaterm.Result{Code: 1} }
	}
	t.Cleanup(func() { orcaRunner = oldRunner })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	for n, s := range map[string]string{"key": "SYNTHETIC_HOST_KEY_2369", "state.json": `{"hostId":"own"}`} {
		if e := os.WriteFile(filepath.Join(dir, n), []byte(s), 0o600); e != nil {
			t.Fatal(e)
		}
	}
	cfg := fmt.Sprintf("server: %q\napp: hrn:app:test:team\nkey_file: key\nstate_file: state.json\nworktrees_root: %q\nworkers:\n  w1:\n    family: codex\n    launch: [fake]\n    worktrees: [iris]\ndelivery:\n  orca_bin: %q\n", srv.URL, filepath.Join(dir, "wt-root"), orcaBin)
	path := filepath.Join(dir, "host.yaml")
	if e := os.WriteFile(path, []byte(cfg), 0o600); e != nil {
		t.Fatal(e)
	}
	f := cmdutil.NewFactory()
	streams, _, _ := output.Test()
	f.IOStreams = streams
	c := newCmdTeamHealthWithReader(f, func(context.Context, string, *hostpoller.Config) ([]byte, int, error) {
		atomic.AddInt32(&reads, 1)
		return []byte("[]"), 0, nil
	})
	c.SetArgs([]string{"sweep", "--config", path, "--dry-run=false"})
	err = c.Execute()
	return atomic.LoadInt32(&requests), atomic.LoadInt32(&reads), atomic.LoadInt32(&runners), err
}

func TestHealthRefusesRelativeOrcaBin2369(t *testing.T) {
	for name, tc := range map[string]struct {
		bin     string
		present bool // a file of that name exists in the working directory
	}{
		"bare name, file present in the working directory": {"orca-beta", true},
		"bare name, absent from the working directory":     {"orca-beta", false},
		"dot-relative path":              {"./orca", true},
		"relative path with a separator": {"bin/orca", false},
	} {
		t.Run(name, func(t *testing.T) {
			wd := t.TempDir()
			t.Chdir(wd)
			if tc.present {
				p := filepath.Join(wd, tc.bin)
				if e := os.MkdirAll(filepath.Dir(p), 0o755); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o700); e != nil {
					t.Fatal(e)
				}
			}
			requests, reads, runners, err := relativeOrcaBin2369(t, tc.bin)
			if err == nil || exitcode.FromError(err) != exitcode.Usage {
				t.Fatalf("want a Usage refusal (exit 2), got %v", err)
			}
			if !strings.Contains(err.Error(), "delivery.orca_bin must be an absolute path") || !strings.Contains(err.Error(), "nothing written") {
				t.Fatalf("refusal wording: %v", err)
			}
			if requests != 0 || reads != 0 || runners != 0 {
				t.Fatalf("the refusal must come before any server request, status read or Orca exec: requests=%d reads=%d runners=%d", requests, reads, runners)
			}
		})
	}
}
