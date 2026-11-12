package cmd

import (
	"encoding/json"
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

// hadron-cli#1904: under --json the refusal is the standard error envelope on
// stdout (#334: nothing else was written, so stdout is the one JSON document),
// exit code 2, never duplicated onto stderr, and no request was made.
func TestUsageProject1904EmptyAccountJSONEnvelope(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, resp1899())
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	errOut := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "usage", "project", "--account", "", "--json"})
	err := root.Execute()
	if err == nil || exitOf(err) != exitcode.Usage {
		t.Fatalf("want exit 2, got %v (%d)", err, exitOf(err))
	}
	if code := renderError(f, err); code != exitcode.Usage {
		t.Fatalf("rendered exit code %d, want %d", code, exitcode.Usage)
	}
	var env struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	dec := json.NewDecoder(strings.NewReader(out.String()))
	dec.DisallowUnknownFields()
	if derr := dec.Decode(&env); derr != nil || dec.More() {
		t.Fatalf("stdout must be exactly one envelope: %v\n%q", derr, out.String())
	}
	if env.Error.Code != exitcode.Usage || env.Error.Message != emptyAccount1899 {
		t.Fatalf("envelope %+v", env.Error)
	}
	if got := errOut(); got != "" {
		t.Fatalf("stderr must stay empty, got %q", got)
	}
	if len(*calls) != 0 {
		t.Fatalf("made %d requests", len(*calls))
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
