package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#1895: `team usage accounts` and `team usage project` share one
// too-old mapper. It matches only the exact unknown-field shape for its own
// field, keeps each command's own too-old prefix, and appends the server's
// message; every other error keeps api.MapError. Through the real commands
// with a stub server.

const (
	accountsTooOld1895   = "this hadron-server is too old to list usage accounts (no accountUsageAccounts field; needs hadron-server #3853) — upgrade the server"
	projectionTooOld1895 = "this hadron-server is too old to project account usage (no accountUsageProjection field; needs hadron-server #3820) — upgrade the server"
)

func gqlMessage1895(msg string) string {
	b, err := json.Marshal(map[string]any{"errors": []map[string]string{{"message": msg}}})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// usageCase1895 runs one command against one error on its own operation.
type usageCase1895 struct {
	name, op, field, prefix string
	run                     func(t *testing.T, resp map[string]string) error
}

func usageCases1895() []usageCase1895 {
	return []usageCase1895{
		{"accounts", "AccountUsageAccounts", "accountUsageAccounts", accountsTooOld1895, func(t *testing.T, resp map[string]string) error {
			_, _, _, err := runAccounts(t, resp)
			return err
		}},
		{"project", "AccountUsageProjection", "accountUsageProjection", projectionTooOld1895, func(t *testing.T, resp map[string]string) error {
			_, _, _, err := runProject1878(t, resp, "--account", "team-rupali")
			return err
		}},
		// #1901 BLIND P3-7a: project without --account lists first; a server
		// without the list field gets the accounts too-old text there too.
		{"project (no --account) list", "AccountUsageAccounts", "accountUsageAccounts", accountsTooOld1895, func(t *testing.T, resp map[string]string) error {
			_, _, _, err := runProject1878(t, resp)
			return err
		}},
	}
}

// The exact unknown-field shape for the command's own field: that command's
// prefix, unchanged and first, then the server's message; exit 1.
func TestUsage1895TooOldKeepsPrefixAndServerMessage(t *testing.T) {
	for _, c := range usageCases1895() {
		t.Run(c.name, func(t *testing.T) {
			server := `Cannot query field "` + c.field + `" on type "Query".`
			err := c.run(t, map[string]string{c.op: gqlMessage1895(server)})
			if err == nil || exitOf(err) != exitcode.Error {
				t.Fatalf("want exit 1, got %v (%d)", err, exitOf(err))
			}
			// The documented join, exactly: prefix + " (server said: " + the
			// server's error + ")" (#1901 BLIND P3-6).
			want := c.prefix + " (server said: " + gqlerror.List{{Message: server}}.Error() + ")"
			if err.Error() != want {
				t.Fatalf("too-old text:\n got %q\nwant %q", err.Error(), want)
			}
		})
	}
}

// Negative controls: none of these is "too old"; each keeps api.MapError.
func TestUsage1895OtherErrorsAreNotTooOld(t *testing.T) {
	for _, c := range usageCases1895() {
		controls := map[string]string{
			"unknown argument":      gqlMessage1895(`Unknown argument "appRef" on field "Query.` + c.field + `".`),
			"different field":       gqlMessage1895(`Cannot query field "asOfX" on type "Query". Did you mean "` + c.field + `"?`),
			"unrelated":             gqlMessage1895("database is on fire"),
			"APP_NOT_FOUND by code": gqlErrorJSON("APP_NOT_FOUND"),
		}
		for name, body := range controls {
			t.Run(c.name+"/"+name, func(t *testing.T) {
				err := c.run(t, map[string]string{c.op: body})
				if err == nil {
					t.Fatal("want an error")
				}
				if strings.Contains(err.Error(), "too old") {
					t.Fatalf("mapped to too old: %v", err)
				}
				if name == "APP_NOT_FOUND by code" && !strings.Contains(err.Error(), "APP_NOT_FOUND") {
					t.Fatalf("APP_NOT_FOUND not passed through: %v", err)
				}
			})
		}
	}
}

// The null envelope is the CLI's defensive guard: exit 1, its own message.
func TestUsage1895AccountsNullEnvelope(t *testing.T) {
	_, out, _, err := runAccounts(t, map[string]string{"AccountUsageAccounts": `{"data":{"accountUsageAccounts":null}}`})
	if err == nil || exitOf(err) != exitcode.Error || err.Error() != "server returned no account list" {
		t.Fatalf("got %v (%d)", err, exitOf(err))
	}
	if out != "" {
		t.Fatalf("printed %q", out)
	}
}

// No resolvable team App (no --app, no App context, no binding): exit 2 and
// zero GraphQL requests, for both commands.
func TestUsage1895NoAppExitsTwoWithoutRequests(t *testing.T) {
	for _, argv := range [][]string{{"accounts"}, {"project", "--account", "team-rupali"}, {"project"}} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			f, _ := testFactory(t) // empty isolated App config
			t.Chdir(t.TempDir())
			t.Setenv(team.GitDirEnv, t.TempDir()) // a worktree with no binding
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer srv.Close()
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"--server", srv.URL, "team", "usage"}, argv...))
			err := root.Execute()
			if err == nil || exitOf(err) != exitcode.Usage || !strings.Contains(err.Error(), "no team App") {
				t.Fatalf("want exit 2 no team App, got %v (%d)", err, exitOf(err))
			}
			if n := requests.Load(); n != 0 {
				t.Fatalf("made %d requests", n)
			}
		})
	}
}

// #1898 BLIND F1 (folded in per the change manager, #98023): project's
// omitted-account path guards a null list the same way, exit 1, no projection.
func TestUsage1895ProjectNullEnvelope(t *testing.T) {
	calls, out, _, err := runProject1878(t, map[string]string{"AccountUsageAccounts": `{"data":{"accountUsageAccounts":null}}`})
	if err == nil || exitOf(err) != exitcode.Error || err.Error() != "server returned no account list" {
		t.Fatalf("got %v (%d)", err, exitOf(err))
	}
	if out != "" || len(projCalls(calls)) != 0 {
		t.Fatalf("printed %q, made %d projections", out, len(projCalls(calls)))
	}
}

// #1901 LOCAL F1/F2: "too old" only when EVERY error in the envelope is the
// exact unknown-field rejection for the command's own field; a mixed envelope
// (that rejection beside APP_NOT_FOUND, or beside an unrelated error) and each
// single control are exactly api.MapError's answer — same exit, same message.
func TestUsage1895NonTooOldIsExactlyMapError(t *testing.T) {
	for _, c := range usageCases1895() {
		own := &gqlerror.Error{Message: `Cannot query field "` + c.field + `" on type "Query".`}
		appNF := &gqlerror.Error{Message: "refused: APP_NOT_FOUND", Extensions: map[string]any{"code": "APP_NOT_FOUND"}}
		controls := map[string]gqlerror.List{
			"unknown argument":        {{Message: `Unknown argument "appRef" on field "Query.` + c.field + `".`}},
			"different field":         {{Message: `Cannot query field "asOfX" on type "Query". Did you mean "` + c.field + `"?`}},
			"unrelated":               {{Message: "database is on fire"}},
			"APP_NOT_FOUND by code":   {appNF},
			"mixed own+APP_NOT_FOUND": {own, appNF},
			"mixed APP_NOT_FOUND+own": {appNF, own},
			"mixed own+unrelated":     {own, {Message: "database is on fire"}},
		}
		for name, list := range controls {
			t.Run(c.name+"/"+name, func(t *testing.T) {
				body, err := json.Marshal(map[string]any{"errors": list})
				if err != nil {
					t.Fatal(err)
				}
				got := c.run(t, map[string]string{c.op: string(body)})
				want := api.MapError(list)
				if got == nil || exitOf(got) != exitOf(want) || got.Error() != want.Error() {
					t.Fatalf("not api.MapError:\n got %d %q\nwant %d %q", exitOf(got), got, exitOf(want), want)
				}
			})
		}
	}
}
