package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#1878: with --account omitted, `team usage project` takes the
// account list from the server (accountUsageAccounts, cor:agt:140:05) and
// projects each entry as returned, instead of deriving labels from
// account-usage objects on the client (cor:api:240:01). Through the real
// command with a stub server.

const incompleteWarning1878 = "warning: the server's account list is incomplete; some accounts may be missing from this projection.\n"

func runProject1878(t *testing.T, resp map[string]string, argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	writeTeamBinding(t)
	srv, calls := attnServer(t, resp)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL, "team", "usage", "project"}, argv...))
	err := root.Execute()
	return calls, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

func opCount1878(calls *[]attnCall, op string) int {
	n := 0
	for _, c := range *calls {
		if c.Op == op {
			n++
		}
	}
	return n
}

// Acceptance 1 + 2: each listed entry is projected once, in the server's order
// (not re-sorted), one label from two hosts is two projections, account/host/
// family are passed back unchanged, and no object is read for discovery.
func TestUsageProject1878ProjectsServerListInOrder(t *testing.T) {
	resp := map[string]string{
		"AccountUsageAccounts":   acctsResponse(true, acctRupaliOrca1, acctHolgerCodex, acctRupaliOrca2),
		"AccountUsageProjection": projResponse("x", projWinNoRunOut, ""),
	}
	calls, _, _, err := runProject1878(t, resp, "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := projCalls(calls)
	want := [][3]string{{"team-rupali", "orca1", "claude"}, {"team-holger", "orca1", "codex"}, {"team-rupali", "orca2", "claude"}}
	if len(got) != len(want) {
		t.Fatalf("want %d projections, got %+v", len(want), got)
	}
	for i, w := range want {
		v := got[i].Vars
		if v["account"] != w[0] || v["host"] != w[1] || v["family"] != w[2] {
			t.Fatalf("projection %d: want %v, got %v", i, w, v)
		}
	}
	if n := opCount1878(calls, "AccountUsageAccounts"); n != 1 {
		t.Fatalf("want one accountUsageAccounts call, got %d", n)
	}
	if n := opCount1878(calls, "FindObjects"); n != 0 {
		t.Fatalf("discovery read %d object pages; the server lists the accounts", n)
	}
}

// Ask "JSON shape (exact)": complete sits after accounts, carries the server's
// value, and nothing else in the DTO changes.
func TestUsageProject1878JSONCarriesCompleteAfterAccounts(t *testing.T) {
	resp := map[string]string{
		"AccountUsageAccounts":   acctsResponse(true, acctRupaliOrca1),
		"AccountUsageProjection": projResponse("team-rupali", projWinNoRunOut, ""),
	}
	_, out, _, err := runProject1878(t, resp, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(squash1837(out), `}],"complete":true,"whatIf":null}`) {
		t.Fatalf("complete must follow accounts:\n%s", out)
	}
}

// Acceptance 3: three empty/incomplete combinations, text and --json, all exit 0
// (non-empty + complete is covered by the order and shape tests above).
func TestUsageProject1878EmptyAndIncomplete(t *testing.T) {
	cases := map[string]struct {
		resp     string
		complete string
		warn     bool
	}{
		"empty complete":       {acctsResponse(true), "true", false},
		"empty incomplete":     {acctsResponse(false), "false", true},
		"non-empty incomplete": {acctsResponse(false, acctRupaliOrca1), "false", true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			resp := map[string]string{"AccountUsageAccounts": c.resp, "AccountUsageProjection": projResponse("team-rupali", projWinNoRunOut, "")}
			_, js, _, err := runProject1878(t, resp, "--json")
			if err != nil {
				t.Fatalf("json: %v", err)
			}
			if !strings.Contains(squash1837(js), `"complete":`+c.complete+`,`) {
				t.Fatalf("json lacks complete %s:\n%s", c.complete, js)
			}
			if strings.HasPrefix(name, "empty") {
				var head struct {
					App string `json:"app"`
				}
				if err := json.Unmarshal([]byte(js), &head); err != nil || head.App == "" {
					t.Fatalf("no app in %s (%v)", js, err)
				}
				wantJS := `{"app":"` + head.App + `","accounts":[],"complete":` + c.complete + `,"whatIf":null}`
				if squash1837(js) != wantJS {
					t.Fatalf("empty json\n got %s\nwant %s", squash1837(js), wantJS)
				}
			}
			_, out, errOut, err := runProject1878(t, resp)
			if err != nil {
				t.Fatalf("text: %v", err)
			}
			if c.warn && errOut != incompleteWarning1878 {
				t.Fatalf("stderr %q, want exactly %q", errOut, incompleteWarning1878)
			}
			if !c.warn && errOut != "" {
				t.Fatalf("complete list wrote stderr %q", errOut)
			}
			if strings.Contains(out, "incomplete") {
				t.Fatalf("the warning leaked to stdout: %q", out)
			}
		})
	}
}

// Acceptance 3: APP_NOT_FOUND from the list is the error it is, not an empty projection.
func TestUsageProject1878AppNotFoundIsAnError(t *testing.T) {
	calls, out, _, err := runProject1878(t, map[string]string{"AccountUsageAccounts": gqlErrorJSON("APP_NOT_FOUND")}, "--json")
	if err == nil || exitOf(err) == exitcode.OK || !strings.Contains(err.Error(), "APP_NOT_FOUND") || strings.Contains(err.Error(), "too old") {
		t.Fatalf("want APP_NOT_FOUND through api.MapError, got %v (%d)", err, exitOf(err))
	}
	if out != "" || len(projCalls(calls)) != 0 {
		t.Fatalf("a refusal printed %q and made %d projections", out, len(projCalls(calls)))
	}
}

// Acceptance 4: a server without accountUsageAccounts says too old, exit 1.
func TestUsageProject1878OldServerSaysTooOld(t *testing.T) {
	old := `{"errors":[{"message":"Cannot query field \"accountUsageAccounts\" on type \"Query\"."}]}`
	_, _, _, err := runProject1878(t, map[string]string{"AccountUsageAccounts": old})
	if err == nil || exitOf(err) != exitcode.Error || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("got %v (%d)", err, exitOf(err))
	}
}

// Acceptance 5: an explicit --account run never lists, has no complete key,
// and -m/--config beside it change nothing.
func TestUsageProject1878ExplicitAccountUnchanged(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "usage.yaml")
	if err := os.WriteFile(cfg, []byte("memory: hrn:mem:example.com:other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp := map[string]string{
		"AccountUsageAccounts":   acctsResponse(true, acctRupaliOrca1),
		"AccountUsageProjection": projResponse("team-rupali", projWinNoRunOut, ""),
	}
	var base string
	for i, extra := range [][]string{{}, {"-m", "hrn:mem:example.com:team"}, {"--config", cfg}} {
		calls, out, _, err := runProject1878(t, resp, append([]string{"--account", "team-rupali", "--json"}, extra...)...)
		if err != nil {
			t.Fatalf("%v: %v", extra, err)
		}
		if strings.Contains(out, `"complete"`) {
			t.Fatalf("%v: explicit --account must omit complete:\n%s", extra, out)
		}
		if n := opCount1878(calls, "AccountUsageAccounts"); n != 0 {
			t.Fatalf("%v: explicit --account listed accounts %d times", extra, n)
		}
		if n := opCount1878(calls, "FindObjects"); n != 0 {
			t.Fatalf("%v: explicit --account read objects", extra)
		}
		if got := projCalls(calls); len(got) != 1 || got[0].Vars["account"] != "team-rupali" {
			t.Fatalf("%v: projections %+v", extra, got)
		}
		if i == 0 {
			base = out
		} else if out != base {
			t.Fatalf("%v changed the output:\n%s\nvs\n%s", extra, out, base)
		}
	}
}

// Acceptance 6: --memory or --config with --account omitted is refused before
// any network call, exit 2, with the exact reason.
func TestUsageProject1878MemoryAndConfigRefusedWithoutAccount(t *testing.T) {
	const msg = "--memory and --config choose a memory to read account labels from; the server now lists the accounts from the team App's team-space memory, so they do not apply: drop the flag, or name one account with --account"
	for name, argv := range map[string][]string{
		"memory":       {"--memory", "hrn:mem:example.com:team"},
		"m":            {"-m", "hrn:mem:example.com:team"},
		"config":       {"--config", "/nonexistent/usage.yaml"},
		"both, --json": {"-m", "hrn:mem:example.com:team", "--config", "/nonexistent/usage.yaml", "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			calls, out, _, err := runProject1878(t, map[string]string{"AccountUsageAccounts": acctsResponse(true, acctRupaliOrca1)}, argv...)
			if err == nil || exitOf(err) != exitcode.Usage || err.Error() != msg {
				t.Fatalf("want exit 2 %q, got %v (%d)", msg, err, exitOf(err))
			}
			if len(*calls) != 0 || out != "" {
				t.Fatalf("refusal made %d requests, printed %q", len(*calls), out)
			}
		})
	}
}

// Flag help states the new rule for --memory/--config.
func TestUsageProject1878FlagHelp(t *testing.T) {
	_, out, _, err := runProject1878(t, map[string]string{}, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "rejected when --account is omitted; ignored with --account") != 2 {
		t.Fatalf("--memory and --config help must state the rule:\n%s", out)
	}
	if strings.Contains(out, "to list accounts") {
		t.Fatalf("stale 'to list accounts' wording:\n%s", out)
	}
}
