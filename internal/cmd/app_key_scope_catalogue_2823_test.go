package cmd

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2823: `hadron app key scope catalogue` reads Query.appKeyScopeCatalogue,
// the server's closed list of App key scopes (cor:aut:010:03). The CLI keeps
// no copy of that list: whatever the server sends is what prints, in order.

const catalogue2823 = `{"data":{"appKeyScopeCatalogue":[` +
	`{"scope":"attention:deliver","description":"Read and acknowledge due wakes.","requiresHostId":false},` +
	`{"scope":"host:operate","description":"Act as one host.","requiresHostId":true}]}}`

func runCatalogue2823(t *testing.T, srv *httptest.Server, args ...string) keyRun {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"app", "key", "scope", "catalogue"}, args...), "--server", srv.URL))
	err := root.Execute()
	return keyRun{out: out.String(), errOut: errOutString2523(t, f.IOStreams), err: err}
}

func countingServer2823(t *testing.T, resp string, calls *int) *httptest.Server {
	t.Helper()
	srv, _ := captureGraphQLFunc(t, func(op string) string {
		if op == "AppKeyScopeCatalogue" {
			*calls++
		}
		return resp
	})
	return srv
}

func TestAppKeyScopeCatalogue2823JSONOrderAndOneCall(t *testing.T) {
	calls := 0
	r := runCatalogue2823(t, countingServer2823(t, catalogue2823, &calls), "--json")
	if r.err != nil {
		t.Fatalf("unexpected error: %v", r.err)
	}
	if calls != 1 {
		t.Fatalf("want exactly one AppKeyScopeCatalogue call, got %d", calls)
	}
	var got []struct {
		Scope          string `json:"scope"`
		Description    string `json:"description"`
		RequiresHostID bool   `json:"requiresHostId"`
	}
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatalf("--json is not an array of scopes: %v\n%s", err, r.out)
	}
	if len(got) != 2 || got[0].Scope != "attention:deliver" || got[0].RequiresHostID || got[1].Scope != "host:operate" || !got[1].RequiresHostID || got[1].Description != "Act as one host." {
		t.Fatalf("server order and fields must pass through: %+v", got)
	}
}

func TestAppKeyScopeCatalogue2823TextTable(t *testing.T) {
	calls := 0
	r := runCatalogue2823(t, countingServer2823(t, catalogue2823, &calls))
	if r.err != nil {
		t.Fatalf("unexpected error: %v", r.err)
	}
	for _, want := range []string{"SCOPE", "NEEDS HOST", "WHAT IT PERMITS", "attention:deliver", "host:operate", "Act as one host."} {
		if !strings.Contains(r.out, want) {
			t.Fatalf("table lacks %q:\n%s", want, r.out)
		}
	}
	if strings.Index(r.out, "attention:deliver") > strings.Index(r.out, "host:operate") {
		t.Fatalf("rows must keep server order:\n%s", r.out)
	}
}

func TestAppKeyScopeCatalogue2823EmptyList(t *testing.T) {
	calls := 0
	srv := countingServer2823(t, `{"data":{"appKeyScopeCatalogue":[]}}`, &calls)
	r := runCatalogue2823(t, srv, "--json")
	if r.err != nil || strings.TrimSpace(r.out) != "[]" {
		t.Fatalf("empty list must be [] not null: err=%v out=%q", r.err, r.out)
	}
	r = runCatalogue2823(t, srv)
	if r.err != nil || !strings.Contains(r.out, "no scopes") {
		t.Fatalf("empty text must say no scopes: err=%v out=%q", r.err, r.out)
	}
}

func TestAppKeyScopeCatalogue2823RefusalKeepsServerCode(t *testing.T) {
	calls := 0
	srv := countingServer2823(t, `{"errors":[{"message":"You must be logged in","extensions":{"code":"UNAUTHENTICATED"}}],"data":null}`, &calls)
	r := runCatalogue2823(t, srv)
	if r.err == nil || exitCodeFor(r.err) != exitcode.AuthRequired {
		t.Fatalf("UNAUTHENTICATED must exit %d, got err=%v code=%d", exitcode.AuthRequired, r.err, exitCodeFor(r.err))
	}
	if !strings.Contains(r.err.Error(), "You must be logged in") || r.out != "" {
		t.Fatalf("server sentence passes through, nothing on stdout: err=%v out=%q", r.err, r.out)
	}
}

func TestAppKeyScopeCatalogue2823TakesNoArguments(t *testing.T) {
	calls := 0
	r := runCatalogue2823(t, countingServer2823(t, catalogue2823, &calls), "extra")
	if r.err == nil || exitCodeFor(r.err) != exitcode.Usage || calls != 0 {
		t.Fatalf("an argument is a usage error before any call: err=%v calls=%d", r.err, calls)
	}
}

// A null list is a malformed answer, not "the server lists no scopes": exit 1, nothing on
// stdout (Joran's BLIND P2). An empty list stays "no scopes" / [] (tested above).
func TestAppKeyScopeCatalogue2823NullListIsAnError(t *testing.T) {
	for name, resp := range map[string]string{
		"null field": `{"data":{"appKeyScopeCatalogue":null}}`,
		"null data":  `{"data":null}`,
	} {
		for _, args := range [][]string{{}, {"--json"}} {
			calls := 0
			r := runCatalogue2823(t, countingServer2823(t, resp, &calls), args...)
			if r.err == nil || exitCodeFor(r.err) != exitcode.Error || r.out != "" {
				t.Fatalf("%s %v: want exit %d and no stdout, got err=%v out=%q", name, args, exitcode.Error, r.err, r.out)
			}
			if !strings.Contains(r.err.Error(), "no scope catalogue") {
				t.Fatalf("%s %v: error should name the missing catalogue: %v", name, args, r.err)
			}
		}
	}
}

func TestAppKeyScopeCatalogue2823NullElementIsAnErrorNotAPanic(t *testing.T) {
	calls := 0
	r := runCatalogue2823(t, countingServer2823(t, `{"data":{"appKeyScopeCatalogue":[null]}}`, &calls))
	if r.err == nil || exitCodeFor(r.err) != exitcode.Error || r.out != "" {
		t.Fatalf("a null element must be a clean exit %d: err=%v out=%q", exitcode.Error, r.err, r.out)
	}
}

// NEEDS HOST reads yes for the scope that needs a host and no for the one that does not:
// inverting the mapping must fail this.
func TestAppKeyScopeCatalogue2823NeedsHostMapping(t *testing.T) {
	calls := 0
	r := runCatalogue2823(t, countingServer2823(t, catalogue2823, &calls))
	for _, line := range strings.Split(r.out, "\n") {
		switch {
		case strings.HasPrefix(line, "host:operate"):
			if !strings.Contains(line, "yes") || strings.Contains(line, " no ") {
				t.Fatalf("host:operate needs a host, row: %q", line)
			}
		case strings.HasPrefix(line, "attention:deliver"):
			if !strings.Contains(line, "no") || strings.Contains(line, "yes") {
				t.Fatalf("attention:deliver needs no host, row: %q", line)
			}
		}
	}
}

// Server-supplied cells are sanitized (output.SafeCell): an escape, a newline and a tab in a
// scope or description never reach the terminal as control characters.
func TestAppKeyScopeCatalogue2823HostileCellsAreSanitized(t *testing.T) {
	calls := 0
	hostile := `{"data":{"appKeyScopeCatalogue":[{"scope":"x\u001b[31m:evil","description":"a\nb\tc‮d","requiresHostId":false}]}}`
	r := runCatalogue2823(t, countingServer2823(t, hostile, &calls))
	if r.err != nil {
		t.Fatalf("unexpected error: %v", r.err)
	}
	if strings.ContainsAny(r.out, "\x1b\t‮") {
		t.Fatalf("control characters reached stdout: %q", r.out)
	}
	if strings.Count(r.out, "\n") != 2 { // header + one row, no injected line
		t.Fatalf("a newline in a cell injected a line: %q", r.out)
	}
}
