package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#1899: an explicitly empty or blank --account is refused (exit 2,
// zero requests), not read as "omitted" (review:an-empty-flag-is-not-an-absent-flag).
// Through the real command with a stub server.

const emptyAccount1899 = "--account needs a label (it was given empty — an unset shell variable?): name one account, or omit --account to project every account the server lists"

func resp1899() map[string]string {
	return map[string]string{
		"AccountUsageAccounts":   acctsResponse(true, acctRupaliOrca1),
		"AccountUsageProjection": projResponse("team-rupali", projWinNoRunOut, ""),
	}
}

func TestUsageProject1899EmptyAccountRefusedBeforeAnyRequest(t *testing.T) {
	empties := map[string][]string{
		`--account ""`:   {"--account", ""},
		`--account=`:     {"--account="},
		`--account "  "`: {"--account", "  "},
	}
	others := map[string][]string{
		"alone":         {},
		"-m":            {"-m", "hrn:mem:example.com:team"},
		"--config":      {"--config", "/nonexistent/usage.yaml"},
		"--host":        {"--host", "orca1"},
		"--family":      {"--family", "claude"},
		"--add-workers": {"--add-workers", "1"},
		"--move-worker": {"--move-worker", "Ruth", "--to-account", "team-holger"},
	}
	for en, e := range empties {
		for on, o := range others {
			t.Run(en+"/"+on, func(t *testing.T) {
				calls, out, _, err := runProject1878(t, resp1899(), append(append([]string{}, e...), o...)...)
				if err == nil || exitOf(err) != exitcode.Usage || err.Error() != emptyAccount1899 {
					t.Fatalf("want exit 2 %q, got %v (%d)", emptyAccount1899, err, exitOf(err))
				}
				if len(*calls) != 0 || out != "" {
					t.Fatalf("made %d requests, printed %q", len(*calls), out)
				}
			})
		}
	}
}

// Controls: an omitted --account keeps every existing behaviour.
func TestUsageProject1899OmittedAccountUnchanged(t *testing.T) {
	calls, _, _, err := runProject1878(t, resp1899(), "--json")
	if err != nil {
		t.Fatalf("omitted --account must project the server list: %v", err)
	}
	if n := opCount1878(calls, "AccountUsageAccounts"); n != 1 || len(projCalls(calls)) != 1 {
		t.Fatalf("want one list and one projection, got %d and %d", n, len(projCalls(calls)))
	}
	refusals := map[string]struct {
		argv []string
		msg  string
	}{
		"-m":            {[]string{"-m", "hrn:mem:example.com:team"}, "--memory and --config choose a memory"},
		"--config":      {[]string{"--config", "/nonexistent/usage.yaml"}, "--memory and --config choose a memory"},
		"--host":        {[]string{"--host", "orca1"}, "--host and --family qualify one account"},
		"--family":      {[]string{"--family", "claude"}, "--host and --family qualify one account"},
		"--add-workers": {[]string{"--add-workers", "1"}, "a what-if applies to one account"},
	}
	for name, r := range refusals {
		t.Run(name, func(t *testing.T) {
			calls, _, _, err := runProject1878(t, resp1899(), r.argv...)
			if err == nil || exitOf(err) != exitcode.Usage || !strings.HasPrefix(err.Error(), r.msg) {
				t.Fatalf("want today's refusal %q, got %v (%d)", r.msg, err, exitOf(err))
			}
			if len(*calls) != 0 {
				t.Fatalf("made %d requests", len(*calls))
			}
		})
	}
}

// Control: a non-empty --account projects that one account, as today.
func TestUsageProject1899NonEmptyAccountUnchanged(t *testing.T) {
	calls, _, _, err := runProject1878(t, resp1899(), "--account", "team-rupali", "--json")
	if err != nil {
		t.Fatal(err)
	}
	got := projCalls(calls)
	if len(got) != 1 || got[0].Vars["account"] != "team-rupali" || opCount1878(calls, "AccountUsageAccounts") != 0 {
		t.Fatalf("projections %+v", got)
	}
}
