package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3189 item 1: #3181 removed the typed value from these `team` refusals but gave each site no pin
// of its own (the positional guard sees positionals only). Every row puts a token-shaped canary where
// the refusal used to echo it and fails if any piece of it comes back, in text or --json, so putting
// the `%q`/`%s` back into the message fails the row.
func TestFlagRefusalsInTeamNeverShowTheTypedValue3189(t *testing.T) {
	p := pastedUpper3029()
	low := strings.ToLower(p)
	app := []string{"--app", "hrn:app:acme.com:ops"}
	cat := func(parts ...[]string) []string {
		var out []string
		for _, s := range parts {
			out = append(out, s...)
		}
		return out
	}
	for _, tc := range []struct {
		name string
		argv []string
		says string
		exit int
	}{
		{"stall-schedule range", cat([]string{"team", "stall-schedule", "set", "--host-report-timeout-minutes", p}, app), "whole number of minutes", exitcode.Usage},
		{"stall-schedule count", cat([]string{"team", "stall-schedule", "set", "--host-lapse-check-ins", p}, app), "whole number", exitcode.Usage},
		{"usage install interval", []string{"team", "usage", "install", "--config", "c", "--interval", p}, "not a duration", exitcode.Usage},
		{"host import host", cat([]string{"team", "host", "import", "--from", "h.yaml", "--host", "bad " + p}, app), "host id", exitcode.Usage},
		{"task hand-back code", cat([]string{"team", "task", "hand-back", "AT1.2", "--code", p}, app), "--code is required and must be one of", exitcode.Usage},
		{"task list offset", cat([]string{"team", "task", "list", "--offset", "-73519"}, app), "--offset takes a non-negative", exitcode.Usage},
		{"task list limit", cat([]string{"team", "events", "--limit", "987654"}, app), "--limit takes 1 to", exitcode.Usage},
		{"wakes first", cat([]string{"team", "wakes", "due", "--first", "-73519"}, app), "--first must be at least 1", exitcode.Usage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen, code := renderedNoEcho3029(t, nil, tc.argv...)
			if code != tc.exit || !strings.Contains(seen, tc.says) {
				t.Errorf("exit %d, want %d naming %q: %s", code, tc.exit, tc.says, seen)
			}
			for _, leak := range []string{low, low[:8], "987654", "73519"} {
				if strings.Contains(strings.ToLower(seen), leak) {
					t.Errorf("shows the value %q: %s", leak, seen)
				}
			}
		})
	}
}

// The NotFound refusals that name no id or ref any more: the server answers "nothing", the CLI says so
// without the ref the person typed.
func TestNotFoundRefusalsInTeamNeverShowTheRef3189(t *testing.T) {
	p := pastedUpper3029()
	low := strings.ToLower(p)
	for _, tc := range []struct {
		name      string
		responses map[string]string
		argv      []string
		says      string
	}{
		{"signing request get", map[string]string{"TeamGrantSigningRequest": `{"data":{"grantSigningRequest":null}}`}, []string{"team", "grant", "signing-request", "get", p}, "signing request not found"},
		{"change-agent worker", map[string]string{"GetWorker": `{"data":{"worker":null}}`}, []string{"team", "worker", "change-agent", "hrn:worker:acme.com:ops:" + low, "--agent", "hrn:agent:acme.com:a", "--yes"}, "no worker has that ref"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen, code := renderedNoEcho3029(t, tc.responses, tc.argv...)
			if code != exitcode.NotFound || !strings.Contains(seen, tc.says) || !strings.Contains(seen, "not shown") {
				t.Errorf("exit %d, want NotFound naming %q and that the value is not shown: %s", code, tc.says, seen)
			}
			if strings.Contains(strings.ToLower(seen), low) || strings.Contains(seen, p[:8]) {
				t.Errorf("shows the ref: %s", seen)
			}
		})
	}
}

// task create --batch-file carries runSettings per item: the refusal names the item and field, never
// the value.
func TestBatchRunSettingsRefusalsNeverShowTheValue3189(t *testing.T) {
	p := pastedUpper3029()
	for name, item := range map[string]string{
		"family": `{"task":{"title":"x","runSettings":{"family":"` + p + ` y"}}}`,
		"model":  `{"task":{"title":"x","runSettings":{"model":"` + p + ` y"}}}`,
		"tier":   `{"task":{"title":"x","runSettings":{"tier":"` + p + `"}}}`,
		"effort": `{"task":{"title":"x","runSettings":{"effort":"` + p + `"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "batch.json")
			if err := os.WriteFile(path, []byte("["+item+"]"), 0o600); err != nil {
				t.Fatal(err)
			}
			seen, code := renderedNoEcho3029(t, nil, "team", "task", "create", "--batch-file", path, "--app", "hrn:app:acme.com:ops")
			if code != exitcode.Usage || !strings.Contains(seen, name) {
				t.Errorf("exit %d, want a Usage refusal naming %s: %s", code, name, seen)

			}
			if strings.Contains(seen, p) || strings.Contains(strings.ToLower(seen), strings.ToLower(p[:8])) {
				t.Errorf("shows the value: %s", seen)
			}
		})
	}
}

// `worker compile|approve --only` names the missing entries by position, never by the typed ref
// (the compile batch test pins one plain name; this pins a token-shaped one on both verbs).
func TestOnlyRefusalsNeverShowTheTypedRefs3189(t *testing.T) {
	p := pastedUpper3029()
	check := func(t *testing.T, err error, out string) {
		t.Helper()
		if err == nil || exitCodeFor(err) != exitcode.NotFound || !strings.Contains(err.Error(), "--only value(s) 2") {
			t.Fatalf("want NotFound naming value 2: %v", err)
		}
		if all := err.Error() + out; strings.Contains(all, p) || strings.Contains(strings.ToLower(all), strings.ToLower(p[:8])) {
			t.Errorf("shows the typed ref: %q", all)
		}
	}
	t.Run("compile", func(t *testing.T) {
		_, out, _, err := runBatch(t, batchTeam(), "--only", "ann,"+p, "--apply")
		check(t, err, out)
	})
	t.Run("approve", func(t *testing.T) {
		_, out, err := runApprove(t, approveTeam(), "--only", "Ann,"+p, "--apply")
		check(t, err, out)
	})
}

// mark-read says the App is unknown without the ref that was typed.
func TestMarkReadAppNotFoundNeverShowsTheRef3189(t *testing.T) {
	p := pastedUpper3029()
	writeTeamBinding(t)
	srv, _ := attnServer(t, map[string]string{"TeamDefaultChannel": `{"data":{"app":null}}`})
	setTeamBindingServer(t, srv.URL)
	for _, jsonMode := range []bool{false, true} {
		f, out := testFactory(t)
		var stderr strings.Builder
		f.IOStreams.ErrOut = &stderr
		root := NewRootCmd(f)
		args := []string{"team", "chat", "mark-read", "--through", "1", "--server", srv.URL, "--app", "hrn:app:acme.com:" + strings.ToLower(p)}
		if jsonMode {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		err := root.Execute()
		if err == nil {
			t.Fatal("want a refusal")
		}
		code := renderFailure(f, args, err)
		seen := out.String() + stderr.String()
		if code != exitcode.NotFound || !strings.Contains(seen, "App not found") || !strings.Contains(seen, "not shown") {
			t.Errorf("exit %d, want NotFound naming the App and that the ref is not shown: %s", code, seen)
		}
		if strings.Contains(strings.ToLower(seen), strings.ToLower(p[:8])) {
			t.Errorf("shows the ref: %s", seen)
		}
	}
}
