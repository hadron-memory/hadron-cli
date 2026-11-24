package team

import (
	"context"
	"errors"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller/hostyaml"
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
		{"wrapped unreadable configuration", fmt.Errorf("load: %w", exitcode.Newf(exitcode.Usage, "bad yaml")), "HOST_CONFIG_UNREADABLE"},
		{"wrapped absent key", fmt.Errorf("load: %w", exitcode.Newf(exitcode.AuthRequired, "no key")), "HOST_KEY_UNAVAILABLE"},
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
// cannot become a client, records the default code. The test first proves the
// premise (client construction itself fails, with an error that is neither a
// usage nor an auth one), so it cannot pass for the wrong reason. The reason is
// the constant alone, the run id is kept, and nothing the user sees (the
// reason, either stream) carries the URL, the key, the config path or the
// parse error's text.
func TestSweepRecordClientFailureReadsAsClientUnavailable2288(t *testing.T) {
	const apply = true
	for _, server := range []string{"://bad", "not a url"} {
		t.Run(server, func(t *testing.T) {
			dir := t.TempDir()
			key := filepath.Join(dir, "host.key")
			if err := os.WriteFile(key, []byte("private-host-key"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := filepath.Join(dir, "host.yaml")
			raw := fmt.Sprintf("server: %q\napp: test-app\nkey_file: %s\nworktrees_root: %s\nworkers:\n  Iris:\n    family: claude\n    launch: [echo]\n    worktrees: [iris]\n", server, key, dir)
			if err := os.WriteFile(cfg, []byte(hostyaml.Supervised(raw)), 0600); err != nil {
				t.Fatal(err)
			}
			io, out, errOut := output.Test()
			f := &cmdutil.Factory{IOStreams: io, ServerFlag: server}
			// The premise: building the client fails, and not as a usage or auth error.
			_, _, _, loadErr := loadHostPoller(f, cfg)
			if loadErr == nil {
				t.Fatalf("the client was built for %q; this case no longer reaches the default arm", server)
			}
			if code := exitcode.FromError(loadErr); code == exitcode.Usage || code == exitcode.AuthRequired {
				t.Fatalf("%q fails as exit %d (%v), which is not the default arm", server, code, loadErr)
			}
			got := recordSweepRun(context.Background(), f, cfg, "test-app", "orca1", "run:replay", time.Now(), apply, hostup.Result{SweepMode: "APPLY"})
			if got.Recorded || got.Reason != "HOST_CLIENT_UNAVAILABLE" || got.RunID != "run:replay" {
				t.Fatalf("an unusable client must read as HOST_CLIENT_UNAVAILABLE with the run id kept: %+v", got)
			}
			seen := got.Reason + out.String() + errOut.String()
			for _, leak := range []string{dir, "private-host-key", "missing protocol", "invalid URI", server} {
				if strings.Contains(seen, leak) {
					t.Fatalf("user-visible output leaked %q: %q", leak, seen)
				}
			}
		})
	}
}
