package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1999: polish follow-ups to #1996. Selected behavior (gate, Xavier): the
// --app flag and the context ref go to the server RAW; trimming only decides
// whether a ref is absent.

// runActions1999 is runActions1996 with a hook that runs after the factory
// has isolated the config dir, to stage an App context or a binding.
func runActions1999(t *testing.T, bound bool, pre func(t *testing.T, serverURL string), responses map[string]string, argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	if bound {
		writeTeamBinding(t)
	} else {
		teamGitDir(t)
	}
	srv, calls := attnServer(t, responses)
	if bound {
		setTeamBindingServer(t, srv.URL)
	}
	f, out := testFactory(t)
	if pre != nil {
		pre(t, srv.URL)
	}
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

func contextApp1999(app string) func(*testing.T, string) {
	return func(t *testing.T, _ string) {
		p, err := config.File()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("app = "+strconv.Quote(app)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// Item 1: the ref is sent byte for byte; padded refs are one more refused arm.
func TestTeamGrantActions1999PaddedRefSentRaw(t *testing.T) {
	const padded = "  hrn:app:acme.com:team \t"
	calls, out, errOut, err := runActions1999(t, false, nil, map[string]string{"GrantActions": emptyActions1996}, "team", "grant", "actions", "--app", padded)
	if err != nil {
		t.Fatal(err)
	}
	got := actionCalls1996(calls)
	if len(got) != 1 || got[0].Vars["appRef"] != padded {
		t.Fatalf("padded ref must be sent as typed, calls = %+v", got)
	}
	// Refusal text is the unpadded arm's, apart from the echoed ref.
	const plainRef = "hrn:app:acme.com:plain-ref"
	_, plain, plainStderr, _ := runActions1999(t, false, nil, map[string]string{"GrantActions": emptyActions1996}, "team", "grant", "actions", "--app", plainRef)
	if strings.ReplaceAll(out, padded, "<REF>") != strings.ReplaceAll(plain, plainRef, "<REF>") || errOut != plainStderr {
		t.Errorf("padded arm differs: %q vs %q", out, plain)
	}
	calls, _, _, err = runActions1999(t, false, nil, map[string]string{"GrantAction": `{"data":{"action":null}}`}, "team", "grant", "action", "a.b", "--app", padded)
	if err == nil || exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
	if got := actionCalls1996(calls); len(got) != 1 || got[0].Vars["appRef"] != padded {
		t.Fatalf("action: padded ref must be sent as typed, calls = %+v", got)
	}
}

// A whitespace-only ref is ABSENT: it never reaches the server as a ref.
func TestTeamGrantActions1999BlankRefIsAbsent(t *testing.T) {
	calls, _, _, err := runActions1999(t, false, nil, map[string]string{"GrantActions": emptyActions1996}, "team", "grant", "actions", "--app", "   ")
	if err == nil || exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
		t.Fatalf("blank --app is absent: err=%v calls=%+v", err, *calls)
	}
}

func TestTeamGrantActions1999ContextRefRawAndLabelled(t *testing.T) {
	calls, out, _, err := runActions1999(t, false, contextApp1999(" hrn:app:acme.com:ctx "), map[string]string{"GrantActions": emptyActions1996}, "team", "grant", "actions")
	if err != nil {
		t.Fatal(err)
	}
	got := actionCalls1996(calls)
	if len(got) != 1 || got[0].Vars["appRef"] != " hrn:app:acme.com:ctx " {
		t.Fatalf("context ref must be sent raw: %+v", got)
	}
	if !strings.Contains(out, "App:  hrn:app:acme.com:ctx  (from the App context)") {
		t.Errorf("source label missing: %q", out)
	}
}

// Item 2: count-only text names the App and where it came from.
func TestTeamGrantActions1999CountOnlyTextHeader(t *testing.T) {
	resp := map[string]string{"GrantActions": `{"data":{"grantActions":{"items":[],"total":9}}}`}
	_, out, _, err := runActions1999(t, false, nil, resp, "team", "grant", "actions", "--app", "a1", "--limit", "0")
	if err != nil || out != "App: a1 (from --app)\ntotal: 9\n" {
		t.Fatalf("flag source: err=%v out=%q", err, out)
	}
	_, out, _, err = runActions1999(t, false, contextApp1999("a2"), resp, "team", "grant", "actions", "--limit", "0")
	if err != nil || out != "App: a2 (from the App context)\ntotal: 9\n" {
		t.Fatalf("context source: err=%v out=%q", err, out)
	}
	_, out, _, _ = runActions1999(t, false, nil, resp, "team", "grant", "actions", "--app", "a1", "--limit", "0", "--json")
	if compact1996(out) != `{"total":9}` {
		t.Errorf("--json count-only must stay {total}: %q", out)
	}
}

// Items 3 and 6: help wording.
func TestTeamGrantActions1999HelpWording(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	for _, path := range [][]string{{"team", "grant", "action"}, {"team", "grant", "actions"}} {
		c, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(c.Long, "as the server sent them") {
			t.Errorf("%v: the DTO drops unknown fields; help must not promise them", path)
		}
		if !strings.Contains(c.Long, "the known fields, values unchanged") {
			t.Errorf("%v: help must say 'the known fields, values unchanged'", path)
		}
		if !strings.Contains(c.Long, "takes precedence over the person credential") || !strings.Contains(c.Long, "another App gets the empty answer") {
			t.Errorf("%v: help must state worker-binding precedence", path)
		}
	}
	usage, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(usage), "takes precedence over the person credential") {
		t.Error("agentic-usage.md must state worker-binding precedence")
	}
	if !strings.Contains(string(usage), "--app is absent") {
		t.Error("agentic-usage.md must say a blank --app is absent")
	}
	for _, path := range [][]string{{"team", "grant", "action"}, {"team", "grant", "actions"}} {
		c, _, _ := root.Find(path)
		if !strings.Contains(c.Long, "--app is absent") {
			t.Errorf("%v: help must say a blank --app is absent", path)
		}
	}
}

// Item 4: a null element (a schema-contract violation) is skipped, not a panic.
func TestTeamGrantActions1999NilItemSkipped(t *testing.T) {
	resp := map[string]string{"GrantActions": `{"data":{"grantActions":{"items":[null,` + actionJSON1996 + `],"total":2}}}`}
	_, out, _, err := runActions1999(t, false, nil, resp, "team", "grant", "actions", "--app", "a1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if e := json.Unmarshal([]byte(out), &page); e != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0]["name"] != "schedule.create" {
		t.Errorf("one action, total as sent: err=%v %s", e, out)
	}
}

// Item 5: arms #1998 left unpinned.
func TestTeamGrantActions1999BindingForOtherServerUsesPerson(t *testing.T) {
	other := func(t *testing.T, _ string) { setTeamBindingServer(t, "http://other.invalid") }
	calls, _, _, err := runActions1999(t, true, other, map[string]string{"GrantActions": emptyActions1996}, "team", "grant", "actions", "--app", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if got := actionCalls1996(calls); len(got) != 1 || got[0].Session != "" {
		t.Fatalf("a binding for another server must not send its session: %+v", got)
	}
}

func TestTeamGrantActions1999Bounds(t *testing.T) {
	bad := [][]string{{"--limit", "-1"}, {"--limit", "201"}, {"--offset", "-1"}, {"--offset", "2147483648"}}
	for _, a := range bad {
		calls, _, _, err := runActions1999(t, false, nil, map[string]string{"GrantActions": emptyActions1996}, append([]string{"team", "grant", "actions", "--app", "a1"}, a...)...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
			t.Errorf("%v: want Usage and no call, got %v %+v", a, err, *calls)
		}
	}
	good := []struct {
		args          []string
		limit, offset float64
	}{{[]string{"--limit", "0"}, 0, 0}, {[]string{"--limit", "200"}, 200, 0}, {[]string{"--offset", "2147483647"}, 20, 2147483647}}
	for _, g := range good {
		calls, _, _, err := runActions1999(t, false, nil, map[string]string{"GrantActions": emptyActions1996}, append([]string{"team", "grant", "actions", "--app", "a1"}, g.args...)...)
		got := actionCalls1996(calls)
		if err != nil || len(got) != 1 || got[0].Vars["limit"] != g.limit || got[0].Vars["offset"] != g.offset {
			t.Errorf("%v: err=%v calls=%+v", g.args, err, got)
		}
	}
}

func TestTeamGrantActions1999NilPageIsAnError(t *testing.T) {
	_, _, _, err := runActions1999(t, false, nil, map[string]string{"GrantActions": `{"data":{"grantActions":null}}`}, "team", "grant", "actions", "--app", "a1")
	if err == nil || exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), "server returned no grant actions page") {
		t.Fatalf("nil page: %v", err)
	}
}

// cli#2001: the precedence sentence's own scenario. A bound worker (binding for
// this server) naming an App other than the binding's still reads with the
// worker session; whether the person participates there is the server's call.
func TestTeamGrantActions2001BoundWorkerOtherAppKeepsSession(t *testing.T) {
	const other = "hrn:app:acme.com:not-the-bound-app" // the binding's appId is capp100000000000000000000
	for _, argv := range [][]string{
		{"team", "grant", "actions", "--app", other},
		{"team", "grant", "action", "a.b", "--app", other},
	} {
		calls, _, _, _ := runActions1999(t, true, nil, map[string]string{"GrantActions": emptyActions1996, "GrantAction": `{"data":{"action":null}}`}, argv...)
		got := actionCalls1996(calls)
		if len(got) != 1 || got[0].Session != "s-new" || got[0].Vars["appRef"] != other {
			t.Errorf("%v: want one call with the binding's session and the typed ref, got %+v", argv, got)
		}
	}
}
