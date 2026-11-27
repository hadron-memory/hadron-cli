package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/filterreport"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// TestFindFilteredNoTerminalRowAgentTree2483 pins F9: the fallback row for a
// named worker with no terminal claims no agent_tree — nothing was scanned,
// so "no_wrapper" would be a diagnosis of nothing.
func TestFindFilteredNoTerminalRowAgentTree2483(t *testing.T) {
	streams, out, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: streams, JSON: true, AppFlag: "hrn:app:acme.com:team", ServerFlag: "https://test"}
	source := filterSources{procRoot: t.TempDir(), targets: func(context.Context, string, []string) ([]filterreport.Target, error) {
		return []filterreport.Target{{ID: "w1", Name: "Iris"}}, nil
	}, runner: func() (orcaterm.Runner, bool) {
		return func(context.Context, []string) orcaterm.Result {
			return orcaterm.Result{Stdout: `{"ok":true,"result":{"terminals":[]}}`}
		}, true
	}}
	cmd := newCmdWorkerFindFilteredWithSources(f, source)
	cmd.SetArgs([]string{"iris"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("json %v %s", err, out)
	}
	if rows[0]["classification"] != "UNREADABLE" || rows[0]["note"] != "no_terminal" {
		t.Fatal(rows)
	}
	if agent, ok := rows[0]["agent_tree"].(string); !ok || agent != "" {
		t.Fatalf("no-terminal row claims agent_tree %q; nothing was scanned", rows[0]["agent_tree"])
	}
}

// TestFindFilteredMalformedList2483 pins F12: an Orca that answers ok with a
// payload the reader cannot trust is a failed scan (exit 1, fixed code), not
// an off-Orca refusal (exit 2). An Orca that never answered, or answered with
// its own error envelope, keeps the documented exit-2 refusal.
func TestFindFilteredMalformedList2483(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result orcaterm.Result
		want   int
		text   string
	}{
		{"terminals not an array", orcaterm.Result{Stdout: `{"ok":true,"result":{"terminals":{"oops":true}}}`}, 1, "terminal_list_malformed"},
		{"row without handle", orcaterm.Result{Stdout: `{"ok":true,"result":{"terminals":[{"worktreePath":"/tmp"}]}}`}, 1, "terminal_list_malformed"},
		{"unparsable payload", orcaterm.Result{Stdout: `not json at all`}, 2, findFilteredHostRefusal},
		{"orca error envelope", orcaterm.Result{Stdout: `{"ok":false,"error":{"code":"unreachable"}}`}, 2, findFilteredHostRefusal},
		{"never answered", orcaterm.Result{Code: 1}, 2, findFilteredHostRefusal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			streams, out, _ := output.Test()
			f := &cmdutil.Factory{IOStreams: streams, AppFlag: "hrn:app:acme.com:team"}
			source := filterSources{targets: func(context.Context, string, []string) ([]filterreport.Target, error) {
				t.Fatal("roster read after a failed list")
				return nil, errors.New("unreachable")
			}, runner: func() (orcaterm.Runner, bool) {
				return func(context.Context, []string) orcaterm.Result { return tc.result }, true
			}}
			cmd := newCmdWorkerFindFilteredWithSources(f, source)
			cmd.SetArgs(nil)
			err := cmd.Execute()
			if err == nil || exitcode.FromError(err) != tc.want || err.Error() != tc.text || out.Len() != 0 {
				t.Fatalf("got %v (exit %d, out %q), want exit %d %q", err, exitcode.FromError(err), out.String(), tc.want, tc.text)
			}
		})
	}
}

// noCredentialFactory2564 is a real factory that can reach no credential:
// no HADRON_TOKEN, a config/file-store dir of its own, and an empty in-memory
// keyring in place of the OS keychain (cli#2564 item 8: clearing only the env
// and XDG left the keychain, a source the factory reads, to leak a developer's
// stored login into the "unauthenticated" case).
func noCredentialFactory2564(t *testing.T) *cmdutil.Factory {
	t.Helper()
	t.Setenv("HADRON_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	// A fresh mock REPLACES the provider, discarding any earlier plant on purpose
	// (the pin above proves a plant is otherwise read). The provider is
	// process-global and not restored; every test that touches it re-inits.
	keyring.MockInit()
	return cmdutil.NewFactory()
}

// TestNoCredentialFactoryDropsPlantedKeychainLogin2564 pins the helper with a
// positive control. keyring.MockInit installs a fresh, empty provider, so the
// helper's own MockInit deliberately discards anything planted before it; that
// only proves isolation if the plant was legible to the factory in the first
// place. Step 1 shows it was (same service/host key, the factory dials out
// instead of reporting no credential); step 2 shows the helper drops it.
func TestNoCredentialFactoryDropsPlantedKeychainLogin2564(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close() // connection refused: a found credential ends in unreachable
	host := auth.Host(server.URL)
	run := func(f *cmdutil.Factory) error {
		f.ServerFlag = server.URL
		f.HTTPClient = server.Client()
		_, err := findFilteredTargets(context.Background(), f, "hrn:app:test.com:team", nil)
		return err
	}
	// Positive control: plant, change nothing else, and the outcome changes.
	keyring.MockInit()
	if err := (store.Keyring{}).Set(host, "planted2564"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HADRON_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	if err := run(cmdutil.NewFactory()); err == nil || err.Error() != "team_roster_unreachable" {
		t.Fatalf("control: planted login not read by the factory: got %v, want team_roster_unreachable", err)
	}
	// Same plant, then the helper: the login is gone and nothing was sent.
	if err := (store.Keyring{}).Set(host, "planted2564"); err != nil {
		t.Fatal(err)
	}
	err := run(noCredentialFactory2564(t))
	if err == nil || err.Error() != "team_roster_unauthenticated" || exitcode.FromError(err) != exitcode.Error {
		t.Fatalf("got %v, want fixed team_roster_unauthenticated", err)
	}
}

// TestFindFilteredTargetErrorCodes2483 pins F11: target-read failures carry
// distinct fixed codes — unauthenticated, unreachable transport — instead of
// every failure collapsing into team_roster_unreadable. No raw backend text.
// The error KIND decides the code: only a transport failure (never answered)
// is team_roster_unreachable; an HTTP status or a GraphQL errors envelope is
// the server answering with something that is not a usable roster —
// team_roster_unreadable.
func TestFindFilteredTargetErrorCodes2483(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		f := noCredentialFactory2564(t)
		f.ServerFlag = "https://test.example"
		_, err := findFilteredTargets(context.Background(), f, "hrn:app:test.com:team", nil)
		if err == nil || err.Error() != "team_roster_unauthenticated" || exitcode.FromError(err) != exitcode.Error {
			t.Fatalf("got %v, want fixed team_roster_unauthenticated", err)
		}
	})
	t.Run("transport unreachable", func(t *testing.T) {
		t.Setenv("HADRON_TOKEN", "fake2483")
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		server.Close() // connection refused from here on
		f := cmdutil.NewFactory()
		f.ServerFlag = server.URL
		f.HTTPClient = server.Client()
		_, err := findFilteredTargets(context.Background(), f, "hrn:app:test.com:team", nil)
		if err == nil || err.Error() != "team_roster_unreachable" || exitcode.FromError(err) != exitcode.Error {
			t.Fatalf("got %v, want fixed team_roster_unreachable", err)
		}
		if strings.Contains(err.Error(), server.URL) {
			t.Fatal("raw transport text in the fixed code", err)
		}
	})
	// Answered failures keep the third code: whatever came back — an HTTP
	// status carrying an answer, a GraphQL errors envelope — is not a usable
	// roster, and saying "unreachable" about a server that answered would
	// hide the difference. The one exception is a 5xx with no GraphQL body,
	// gateway or not: indistinguishable from a lost answer, so unreachable.
	// The exact-match assertion on the fixed code is also the no-leak pin:
	// the message IS the code, so backend text cannot be present.
	// A 401 is unreadable, not unauthenticated: the credential existed and
	// was refused — "unauthenticated" is reserved for no credential at all.
	for _, tc := range []struct {
		name, contentType, body, wantCode string
		status                            int
	}{
		{"401 with GraphQL body", "application/json", `{"errors":[{"message":"bad credential","extensions":{"code":"FORBIDDEN"}}]}`, "team_roster_unreadable", 401},
		{"500 with GraphQL errors body", "application/json", `{"errors":[{"message":"resolver exploded","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, "team_roster_unreadable", 500},
		{"200 GraphQL errors envelope", "application/json", `{"data":null,"errors":[{"message":"refused","extensions":{"code":"FORBIDDEN"}}]}`, "team_roster_unreadable", 200},
		{"404 proxy page", "text/plain", `no such upstream`, "team_roster_unreadable", 404},
		{"502 gateway page", "text/html", `<html>502 Bad Gateway</html>`, "team_roster_unreachable", 502},
		{"500 non-gateway, no GraphQL body", "text/plain", `internal error`, "team_roster_unreachable", 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HADRON_TOKEN", "fake2483")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			f := cmdutil.NewFactory()
			f.ServerFlag = server.URL
			f.HTTPClient = server.Client()
			_, err := findFilteredTargets(context.Background(), f, "hrn:app:test.com:team", nil)
			if err == nil || err.Error() != tc.wantCode || exitcode.FromError(err) != exitcode.Error {
				t.Fatalf("got %v, want fixed %s", err, tc.wantCode)
			}
		})
	}
	t.Run("coded errors surface from the command", func(t *testing.T) {
		streams, out, _ := output.Test()
		f := &cmdutil.Factory{IOStreams: streams, AppFlag: "hrn:app:acme.com:team"}
		source := filterSources{targets: func(context.Context, string, []string) ([]filterreport.Target, error) {
			return nil, exitcode.Newf(exitcode.Error, "team_roster_unauthenticated")
		}, runner: func() (orcaterm.Runner, bool) {
			return func(context.Context, []string) orcaterm.Result {
				return orcaterm.Result{Stdout: `{"ok":true,"result":{"terminals":[]}}`}
			}, true
		}}
		cmd := newCmdWorkerFindFilteredWithSources(f, source)
		cmd.SetArgs(nil)
		err := cmd.Execute()
		if err == nil || err.Error() != "team_roster_unauthenticated" || exitcode.FromError(err) != exitcode.Error || out.Len() != 0 {
			t.Fatalf("coded target error did not surface: %v %q", err, out.String())
		}
	})
}

// TestFindFilteredRosterPageBound2483 pins F14: a server that continually
// raises its total cannot keep the offset pager looping forever; the read
// fails closed inside a fixed page budget instead.
func TestFindFilteredRosterPageBound2483(t *testing.T) {
	t.Setenv("HADRON_TOKEN", "fake2483")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n > 70 {
			w.WriteHeader(500)
			return
		}
		var req struct {
			Variables struct {
				Offset int `json:"offset"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		// One row per page, and the total always stays two ahead.
		_, _ = fmt.Fprintf(w, `{"data":{"workers":{"total":%d,"items":[{"id":"w%d","name":"W%d"}]}}}`, int(n)+2, req.Variables.Offset, req.Variables.Offset)
	}))
	defer server.Close()
	f := cmdutil.NewFactory()
	f.ServerFlag = server.URL
	f.HTTPClient = server.Client()
	_, err := findFilteredTargets(context.Background(), f, "hrn:app:test.com:team", nil)
	if err == nil {
		t.Fatal("endlessly growing roster paged as if complete")
	}
	if calls.Load() > 66 {
		t.Fatalf("roster paging unbounded: %d calls", calls.Load())
	}
}

// TestFindFilteredUsesReadOnlyClient2483 pins §10 F2: the command constructs
// the read-only terminal client, never the delivery or sweep client.
func TestFindFilteredUsesReadOnlyClient2483(t *testing.T) {
	f, err := os.ReadFile("worker_find_filtered.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "worker_find_filtered.go", f, 0)
	if err != nil {
		t.Fatal(err)
	}
	readOnly, wider := 0, 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "orcaterm" {
			switch sel.Sel.Name {
			case "NewReadOnlyClient":
				readOnly++
			case "NewClient", "NewSweepClient":
				wider++
			}
		}
		return true
	})
	if readOnly != 1 || wider != 0 {
		t.Fatalf("orcaterm client construction: %d read-only, %d wider", readOnly, wider)
	}
}

// TestFindFilteredHelpNamesConservativeCodes2483 pins F7: the help says what
// the conservative agent_tree codes cover, so an operator reading a
// cwd_outside_worktree or no_wrapper row is not misled about its meaning.
func TestFindFilteredHelpNamesConservativeCodes2483(t *testing.T) {
	cmd := newCmdWorkerFindFilteredWithSources(&cmdutil.Factory{}, filterSources{})
	for _, want := range []string{
		"below the worktree root",
		"not the wrapper's child",
		"empty when no terminal was scanned",
		"malformed terminal-list",
	} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("Long help does not say %q", want)
		}
	}
}
