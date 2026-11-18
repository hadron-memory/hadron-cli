package team

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostup"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#2288 (the §10 review's third survivor on #2274): the default arm of
// hostLoadFailureCode. HOST_CLIENT_UNAVAILABLE is a CLI-local closed code for a
// failure that is neither an unreadable configuration (Usage) nor an absent
// key (AuthRequired); it never reaches the server (cor:agt:070:09 governs only
// the run the server keeps) and carries no path, key or message.

func TestHostLoadFailureCodeIsClosedAndTheDefaultIsClientUnavailable2288(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"unreadable configuration", exitcode.Newf(exitcode.Usage, "bad yaml"), "HOST_CONFIG_UNREADABLE"},
		{"absent key", exitcode.Newf(exitcode.AuthRequired, "no key"), "HOST_KEY_UNAVAILABLE"},
		{"plain error", errors.New("parse \"://bad\": missing protocol scheme"), "HOST_CLIENT_UNAVAILABLE"},
		{"generic exit", exitcode.Newf(exitcode.Error, "boom"), "HOST_CLIENT_UNAVAILABLE"},
		{"unavailable exit", exitcode.Newf(exitcode.Unavailable, "down"), "HOST_CLIENT_UNAVAILABLE"},
		{"wrapped plain", fmt.Errorf("client: %w", errors.New("x")), "HOST_CLIENT_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := hostLoadFailureCode(tc.err)
			if got != tc.want {
				t.Fatalf("hostLoadFailureCode = %q, want %q", got, tc.want)
			}
			if !sweepReasonCode.MatchString(got) {
				t.Fatalf("%q is not a closed reason code", got)
			}
		})
	}
}

// End to end: a configuration that loads and has its key, but whose server
// cannot become a client (an unparsable URL), records the default code. The
// reason is the constant alone: the parse error's text, the URL, the key and
// the config path never appear in it, and the reconcile result is not touched.
func TestSweepRecordClientFailureReadsAsClientUnavailable2288(t *testing.T) {
	for _, server := range []string{"://bad", "not a url"} {
		t.Run(server, func(t *testing.T) {
			dir := t.TempDir()
			key := filepath.Join(dir, "host.key")
			if err := os.WriteFile(key, []byte("private-host-key"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := filepath.Join(dir, "host.yaml")
			raw := fmt.Sprintf("server: %q\napp: test-app\nkey_file: %s\nworktrees_root: %s\nworkers:\n  Iris:\n    family: claude\n    launch: [echo]\n    worktrees: [iris]\n", server, key, dir)
			if err := os.WriteFile(cfg, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			io, _, _ := output.Test()
			f := &cmdutil.Factory{IOStreams: io, ServerFlag: server}
			got := recordSweepRun(context.Background(), f, cfg, "test-app", "orca1", "run:replay", time.Now(), true, hostup.Result{SweepMode: "APPLY"})
			if got.Recorded || got.Reason != "HOST_CLIENT_UNAVAILABLE" || got.RunID != "run:replay" {
				t.Fatalf("an unusable client must read as HOST_CLIENT_UNAVAILABLE with the run id kept: %+v", got)
			}
			for _, leak := range []string{dir, "private-host-key", "missing protocol", "invalid URI", server} {
				if strings.Contains(got.Reason, leak) {
					t.Fatalf("the code leaked %q: %q", leak, got.Reason)
				}
			}
		})
	}
}
