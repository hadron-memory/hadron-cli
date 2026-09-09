package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #540: an App ref that cannot name an App is refused HERE, before any round
// trip, as a usage error that names the argument the caller typed and lists
// only the v2 forms. The server's refusal for the same input named the
// GraphQL field the ref landed in ("workers URN …"), carried genqlient's
// `input:3:` location prefix, taught the retired `::` grammar in its example,
// and said "a app". This walks every surface that hands an App ref to the
// server, so a new site that forwards a ref unchecked shows up as a missing
// row rather than as a regression somebody meets in prd.
func TestAppRefRefusedClientSideBeforeAnyRoundTrip(t *testing.T) {
	const bare = "hadron-dev-team"
	cases := []struct {
		name   string
		args   []string
		source string // what the message must lead with: the flag or positional the user typed
	}{
		{"team worker list", []string{"team", "worker", "list", "--app", bare}, "--app"},
		{"team worker cast", []string{"team", "worker", "cast", "--app", bare, "--role", "qa", "--name", "Uma"}, "--app"},
		{"team session list", []string{"team", "session", "list", "--pr", "hadron-memory/hadron-cli#371", "--app", bare}, "--app"},
		{"run list", []string{"run", "list", "--app", bare}, "--app"},
		{"run trigger", []string{"run", "trigger", "--app", bare, "--entry", "hrn:node:acme.com:ops:tasks:x"}, "--app"},
		{"schedule list", []string{"schedule", "list", "--app", bare}, "--app"},
		{"webhook list", []string{"webhook", "list", "--app", bare}, "--app"},
		{"task run", []string{"task", "run", "hrn:node:acme.com:kb:tasks:x", "--app", bare}, "--app"},
		{"node import --task", []string{"node", "import", "--url", "https://ex.com/p", "-m", "acme.com:kb", "--loc", "clips:p",
			"--task", "hrn:node:acme.com:kb:tasks:distill", "--app", bare}, "--app"},
		{"memory attach", []string{"memory", "attach", "hrn:mem:acme.com:kb", "--app", bare, "--agent", "hrn:agent:acme.com:x"}, "--app"},
		{"memory set (App-scoped create)", []string{"memory", "set", "--app", bare, "--agent", "hrn:agent:acme.com:x", "--class", "app", "--name", "Runbook"}, "--app"},
		{"connection grant create", []string{"connection", "grant", "create", "--connection", "conn_123", "--app", bare, "--scopes", "mail.read"}, "--app"},
		{"ai-config create", []string{"ai-config", "create", "--app", bare, "--name", "x", "--provider", "p", "--model", "m"}, "--app"},
		{"ai-config list", []string{"ai-config", "list", "--app", bare}, "--app"},
		{"ticket mint", []string{"ticket", "mint", "--org", "acme.com", "--app", bare, "--action", "comm.outbound", "--count", "1"}, "--app"},
		{"app agent list positional", []string{"app", "agent", "list", bare}, "<app-ref>"},
		{"app agent add", []string{"app", "agent", "add", bare, "hrn:agent:acme.com:iris"}, "<app>"},
		{"app agent remove", []string{"app", "agent", "remove", bare, "hrn:agent:acme.com:iris", "--yes"}, "<app>"},
		{"app uninstall", []string{"app", "uninstall", bare, "--yes"}, "<app-ref>"},
		{"app set-active", []string{"app", "set-active", bare}, "<app-ref>"},
		{"config set app", []string{"config", "set", "app", bare}, "<value>"},
		{"agent create --install-into", []string{"agent", "create", "--org", "acme.com", "--name", "Iris", "--install-into", bare}, "--install-into"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// No responses at all: any operation reaching the fake server is
			// itself a failure ("unexpected operation"), and `captured` says
			// which one.
			gql, captured := captureGraphQL(t, map[string]string{})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			err := root.Execute()
			if code := exitCodeFor(err); code != exitcode.Usage {
				t.Fatalf("exit code = %d, want Usage; err: %v", code, err)
			}
			if len(captured) != 0 {
				t.Errorf("the refusal must cost no round trip; sent: %v", captured)
			}
			msg := err.Error()
			// Names what was typed and where, then the forms — and NOT the
			// GraphQL field, the wire location, or the retired grammar.
			for _, want := range []string{tc.source + ` "` + bare + `"`, "hrn:app:<root>:<slug>", "<root>:<slug>", "an App id"} {
				if !strings.Contains(msg, want) {
					t.Errorf("message lacks %q:\n%s", want, msg)
				}
			}
			for _, banned := range []string{"::", "input:", "workers", "a app", "not fully qualified"} {
				if strings.Contains(msg, banned) {
					t.Errorf("message carries %q:\n%s", banned, msg)
				}
			}
		})
	}
}

// Every spelling the CLI accepts reaches the server as the canonical v2 URN —
// emit v2, accept everything — and an id passes through as itself, since the
// server dispatches PKs by shape.
func TestAppRefIsCanonicalOnTheWire(t *testing.T) {
	const id = "capp100000000000000000000"
	for in, want := range map[string]string{
		"acme.com:eng-team":          "hrn:app:acme.com:eng-team",
		"acme.com::eng-team":         "hrn:app:acme.com:eng-team", // legacy separator: accepted, never advertised
		"urn:app:acme.com::eng-team": "hrn:app:acme.com:eng-team", // v1 URN
		"hrn:app:acme.com:eng-team":  "hrn:app:acme.com:eng-team",
		id:                           id,
	} {
		gql, captured := captureGraphQL(t, map[string]string{"WorkersRoster": staffJSON})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "worker", "list", "--app", in, "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("--app %q: %v", in, err)
		}
		var vars map[string]any
		_ = json.Unmarshal(captured["WorkersRoster"], &vars)
		if vars["appRef"] != want {
			t.Errorf("--app %q reached the server as %v, want %q", in, vars["appRef"], want)
		}
	}
}

// `app set-active` stores the canonical form, so what later commands send —
// and what `config get app` shows — is one spelling regardless of what was
// typed; and it refuses what cannot name an App rather than storing a value
// that would fail every later command.
func TestAppSetActiveStoresTheCanonicalForm(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "set-active", "acme.com::eng-team", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("set-active: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil || got["app"] != "hrn:app:acme.com:eng-team" {
		t.Errorf("set-active should echo the stored canonical form, got %s (err %v)", out.String(), err)
	}
	cfg, err := f.Config()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.App() != "hrn:app:acme.com:eng-team" {
		t.Errorf("stored app = %q, want the canonical form", cfg.App())
	}
}

// A malformed value can still reach the config by hand (the CLI's own writers
// now refuse one). The refusal then names the SOURCE — nobody typed --app —
// and the remedy, where the server's message named a GraphQL field.
func TestConfiguredAppContextRefusalNamesTheSourceAndTheRemedy(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	cfg, err := f.Config()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("app", "hadron-dev-team"); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "list", "--server", gql.URL})
	err = root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit code = %d, want Usage; err: %v", code, err)
	}
	if len(captured) != 0 {
		t.Errorf("no round trip expected; sent: %v", captured)
	}
	for _, want := range []string{`the configured App context "hadron-dev-team"`, "hadron app set-active"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q:\n%s", want, err)
		}
	}
	if strings.Contains(err.Error(), "--app") {
		t.Errorf("nobody typed --app; the message must not say they did:\n%s", err)
	}
}
