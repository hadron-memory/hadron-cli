package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	authflow "github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func runLogin3180(t *testing.T, args ...string) (authflow.LoginOptions, int, error) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	old := browserLogin
	t.Cleanup(func() { browserLogin = old })
	var seen authflow.LoginOptions
	calls := 0
	browserLogin = func(_ context.Context, opts authflow.LoginOptions) (*authflow.Token, error) {
		seen, calls = opts, calls+1
		return nil, errors.New("stop before storing")
	}
	streams, _, _ := output.Test()
	f := cmdutil.NewFactory()
	f.IOStreams = streams
	f.ServerFlag = "http://127.0.0.1:1"
	cmd := newCmdLogin(f)
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	return seen, calls, cmd.Execute()
}

// --no-browser reaches the flow as code-entry mode; without it the flow gets
// the default (loopback, with the client-side fallback).
func TestLoginNoBrowserEntersCodeEntry_3180(t *testing.T) {
	if opts, calls, _ := runLogin3180(t, "--no-browser"); calls != 1 || !opts.CodeEntry {
		t.Errorf("--no-browser must ask the flow for code entry, calls=%d CodeEntry=%v", calls, opts.CodeEntry)
	}
	if opts, calls, _ := runLogin3180(t); calls != 1 || opts.CodeEntry {
		t.Errorf("the default must not be code entry, calls=%d CodeEntry=%v", calls, opts.CodeEntry)
	}
}

// --with-token and --no-browser name different sources of the credential.
func TestLoginNoBrowserExcludesWithToken_3180(t *testing.T) {
	_, calls, err := runLogin3180(t, "--no-browser", "--with-token")
	if err == nil || !strings.Contains(err.Error(), "no-browser") {
		t.Fatalf("want a mutual-exclusion refusal, got %v", err)
	}
	if calls != 0 {
		t.Errorf("no login may start")
	}
}
