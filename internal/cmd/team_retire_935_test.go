package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#935 — `team attention`, `team attention switchover preview|apply` and
// `team router poll|loop|status` are retired (hadron-server#2027): each stays
// a discoverable command that returns the retirement error, Usage exit 2, and
// touches nothing — no config, credential or binding read, no request, no
// terminal, no state directory — and never echoes a --since/--proof value.

const (
	retiredAttentionMsg  = `hadron team attention is retired (hadron-server#2027). It has no replacement unread or token view. Read your own chat with "hadron team chat read".`
	retiredSwitchoverMsg = retiredAttentionMsg + ` No backlog was marked read.`
	retiredRouterMsg     = `hadron team router is retired (hadron-server#2027): the attention API it polled is being removed. Wakes come from tickers (hadron-server#2138). Nothing was polled or typed.`

	sinceSentinel = "SINCE-SENTINEL-935"
	proofSentinel = "PROOF-SENTINEL-935"
)

type trippedTransport struct{ t *testing.T }

func (tr trippedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.t.Errorf("retired command made a request: %s %s", r.Method, r.URL)
	return nil, errors.New("no network for a retired command")
}

// retiredFactory is a Factory whose every side-effect seam fails the test
// when touched: config load, token store, HTTP. The worktree binding path is
// a directory, so a binding read errors instead of passing silently; the
// router config named by the environment is not JSON, so a config read
// errors too; and HADRON_TOKEN is unset (missing credentials).
func retiredFactory(t *testing.T) (*cmdutil.Factory, *strings.Builder, *strings.Builder) {
	t.Helper()
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	t.Setenv("HADRON_TOKEN", "")
	f.HTTPClient = &http.Client{Transport: trippedTransport{t}}
	f.ConfigFn = func() (*config.Config, error) {
		t.Error("retired command loaded the CLI config")
		return nil, errors.New("tripped")
	}
	f.TokenStoreFn = func() store.Store {
		t.Error("retired command resolved the token store")
		return memStore{}
	}
	gitDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(gitDir, "hadron-team-session.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(team.GitDirEnv, gitDir)
	bad := filepath.Join(t.TempDir(), "router.json")
	if err := os.WriteFile(bad, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HADRON_ROUTER_CONFIG", bad)
	t.Setenv("ORCA_ROUTER_CONFIG", bad)
	return f, out, errOut
}

// sentinelRouterConfig is a VALID router config whose orca binary records any
// run and whose state directory does not exist yet, so a stub that read the
// config and went on to poll, type or write state leaves evidence.
func sentinelRouterConfig(t *testing.T) (path, marker, stateDir string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "orca-ran")
	orca := filepath.Join(dir, "orca")
	if err := os.WriteFile(orca, []byte("#!/bin/sh\ntouch "+marker+"\necho '{}'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	stateDir = filepath.Join(dir, "state")
	path = filepath.Join(dir, "router.json")
	cfg := `{"app": "hrn:app:acme.com:eng-team", "channel": "chan-team", "stateDir": ` + jsonString(stateDir) +
		`, "orcaCli": ` + jsonString(orca) + `, "busyWaitMs": 10, "submitWaitS": 1, "roster": [{"worker": "w-eli", "name": "Eli", "worktree": ` +
		jsonString(filepath.Join(dir, "eli")) + `}]}`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, marker, stateDir
}

func TestRetiredAttentionAndRouterStubs(t *testing.T) {
	cfgPath, marker, stateDir := sentinelRouterConfig(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"attention", []string{"team", "attention"}, retiredAttentionMsg},
		{"attention since", []string{"team", "attention", "--since", sinceSentinel}, retiredAttentionMsg},
		{"attention since now", []string{"team", "attention", "--since", "now"}, retiredAttentionMsg},
		{"attention empty since", []string{"team", "attention", "--since", ""}, retiredAttentionMsg},
		{"attention app", []string{"team", "attention", "--app", "hrn:app:acme.com:eng-team"}, retiredAttentionMsg},
		{"preview", []string{"team", "attention", "switchover", "preview"}, retiredSwitchoverMsg},
		{"apply", []string{"team", "attention", "switchover", "apply", "--proof", proofSentinel, "--yes"}, retiredSwitchoverMsg},
		{"apply no proof", []string{"team", "attention", "switchover", "apply"}, retiredSwitchoverMsg},
		{"poll", []string{"team", "router", "poll"}, retiredRouterMsg},
		{"poll config", []string{"team", "router", "poll", "--config", cfgPath}, retiredRouterMsg},
		{"loop", []string{"team", "router", "loop", "--config", cfgPath, "--interval", "1s"}, retiredRouterMsg},
		{"loop zero interval", []string{"team", "router", "loop", "--interval", "0s"}, retiredRouterMsg},
		{"status", []string{"team", "router", "status", "--config", cfgPath}, retiredRouterMsg},
		{"status env config", []string{"team", "router", "status"}, retiredRouterMsg},
	}
	for _, tc := range cases {
		for _, asJSON := range []bool{false, true} {
			name := tc.name
			args := append([]string{}, tc.args...)
			if asJSON {
				name += " json"
				args = append(args, "--json")
			}
			t.Run(name, func(t *testing.T) {
				f, out, errOut := retiredFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(args)
				err := root.Execute()
				if got := exitOf(err); got != exitcode.Usage {
					t.Fatalf("exit = %d (%v), want %d", got, err, exitcode.Usage)
				}
				if err.Error() != tc.want {
					t.Fatalf("message = %q\nwant      %q", err.Error(), tc.want)
				}
				if out.String() != "" {
					t.Fatalf("stdout before the error = %q, want empty", out.String())
				}
				if code := renderError(f, err); code != exitcode.Usage {
					t.Fatalf("renderError = %d", code)
				}
				printed := out.String() + errOut.String()
				for _, s := range []string{sinceSentinel, proofSentinel, cfgPath} {
					if strings.Contains(printed, s) {
						t.Errorf("output echoes %q:\n%s", s, printed)
					}
				}
				if asJSON {
					var env struct {
						Error map[string]any `json:"error"`
					}
					if err := json.Unmarshal([]byte(out.String()), &env); err != nil {
						t.Fatalf("stdout is not the JSON error envelope: %v\n%s", err, out.String())
					}
					if len(env.Error) != 2 || env.Error["code"] != float64(exitcode.Usage) || env.Error["message"] != tc.want {
						t.Fatalf("envelope = %v", env.Error)
					}
				} else if !strings.Contains(errOut.String(), tc.want) {
					t.Fatalf("stderr = %q, want the retirement message", errOut.String())
				}
				if _, err := os.Stat(marker); err == nil {
					t.Fatal("orca ran")
				}
				if _, err := os.Stat(stateDir); err == nil {
					t.Fatal("router state directory was created")
				}
			})
		}
	}
}

// Old valid flags parse so the retirement message is reached; an unknown flag
// is still an ordinary usage error, not the retirement message.
func TestRetiredStubsRejectUnknownFlagsAsUsage(t *testing.T) {
	for _, args := range [][]string{
		{"team", "attention", "--bogus"},
		{"team", "attention", "switchover", "apply", "--bogus"},
		{"team", "router", "poll", "--bogus"},
		{"team", "router", "loop", "--bogus"},
		{"team", "router", "status", "--bogus"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			f, _, _ := retiredFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(args)
			err := root.Execute()
			if got := exitOf(err); got != exitcode.Usage {
				t.Fatalf("exit = %d (%v), want %d", got, err, exitcode.Usage)
			}
			if !strings.Contains(err.Error(), "unknown flag: --bogus") || strings.Contains(err.Error(), "retired") {
				t.Fatalf("message = %q, want cobra's unknown-flag error", err.Error())
			}
		})
	}
}

// The help says the command is retired, keeps the flags old scripts pass, and
// does not offer a replacement view.
func TestRetiredStubsHelp(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		flags []string
	}{
		{[]string{"team", "attention"}, []string{"--since"}},
		{[]string{"team", "attention", "switchover", "preview"}, nil},
		{[]string{"team", "attention", "switchover", "apply"}, []string{"--proof", "--yes"}},
		{[]string{"team", "router", "poll"}, []string{"--config"}},
		{[]string{"team", "router", "loop"}, []string{"--config", "--interval"}},
		{[]string{"team", "router", "status"}, []string{"--config"}},
	} {
		t.Run(strings.Join(tc.args[1:], " "), func(t *testing.T) {
			f, out, _ := retiredFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{}, tc.args...), "--help"))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			help := out.String()
			if !strings.Contains(help, "retired (hadron-server#2027)") {
				t.Errorf("help does not say retired:\n%s", help)
			}
			for _, fl := range tc.flags {
				if !strings.Contains(help, fl) {
					t.Errorf("help lost the %s flag:\n%s", fl, help)
				}
			}
			for _, bad := range []string{"--since now", "adopt", "nudge each", "Orca terminal"} {
				if strings.Contains(help, bad) {
					t.Errorf("help still describes the retired behaviour (%q):\n%s", bad, help)
				}
			}
		})
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
