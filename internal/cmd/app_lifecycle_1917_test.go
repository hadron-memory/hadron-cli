package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1917 (hadron-server#3774, cor:dmo:050:21; Holger's option A, R4): three
// App lifecycle commands, each bound to exactly one mutation.
//
//	app delete    (alias rm) -> deleteApp     state "deleted"
//	app uninstall (no alias) -> uninstallApp  state "uninstalled"
//	app reinstall            -> reinstallApp  state "installed"
//
// uninstall/reinstall answer true for a real move and for a repeat alike, so the
// output states the resulting state, never a transition.

const (
	lifecycleID  = "capp100000000000000000000"
	lifecycleURN = "hrn:app:acme.com:support"
)

type lifecycleCase struct {
	name     string
	args     []string // without the ref and --server
	op       string
	result   string
	status   string
	needsYes bool
}

var lifecycleCases = []lifecycleCase{
	{"app delete", []string{"app", "delete"}, "DeleteApp", `{"data":{"deleteApp":true}}`, "deleted", true},
	{"app rm", []string{"app", "rm"}, "DeleteApp", `{"data":{"deleteApp":true}}`, "deleted", true},
	{"app uninstall", []string{"app", "uninstall"}, "UninstallApp", `{"data":{"uninstallApp":true}}`, "uninstalled", true},
	{"app reinstall", []string{"app", "reinstall"}, "ReinstallApp", `{"data":{"reinstallApp":true}}`, "installed", false},
}

// allLifecycleResponses answers all three mutations, so a command that calls
// the WRONG one is caught by the captured operation name, not by a fake that
// refuses it.
func allLifecycleResponses() map[string]string {
	return map[string]string{
		"DeleteApp":    `{"data":{"deleteApp":true}}`,
		"UninstallApp": `{"data":{"uninstallApp":true}}`,
		"ReinstallApp": `{"data":{"reinstallApp":true}}`,
	}
}

// withYes appends --yes where the command confirms (delete, rm, uninstall);
// reinstall restores the same App and has no confirmation or --yes.
func withYes(needsYes bool, args ...string) []string {
	if !needsYes {
		return args
	}
	return append(args, "--yes")
}

func runLifecycle(t *testing.T, responses map[string]string, args ...string) (string, map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", gql.URL))
	err := root.Execute()
	return out.String(), captured, err
}

func TestAppLifecycle1917EachCommandCallsExactlyItsMutationWithTheRefUnchanged(t *testing.T) {
	for _, c := range lifecycleCases {
		for _, ref := range []string{lifecycleID, lifecycleURN} {
			_, captured, calls, err := runLifecycleOrdered(t, allLifecycleResponses(), withYes(c.needsYes, append(append([]string{}, c.args...), ref)...)...)
			if err != nil {
				t.Fatalf("%s %s: %v", c.name, ref, err)
			}
			if len(calls) != 1 {
				t.Fatalf("%s %s: want exactly one request (%s, no preflight read), got %v", c.name, ref, c.op, calls)
			}
			raw, ok := captured[c.op]
			if !ok {
				t.Fatalf("%s %s: called %v, want %s", c.name, ref, keys(captured), c.op)
			}
			var vars map[string]any
			_ = json.Unmarshal(raw, &vars)
			if vars["ref"] != ref {
				t.Errorf("%s: ref %v, want %q unchanged", c.name, vars["ref"], ref)
			}
		}
	}
}

func TestAppLifecycle1917JSONStatusIsTheResultingStateAndARepeatReadsTheSame(t *testing.T) {
	for _, c := range lifecycleCases {
		args := append(withYes(c.needsYes, append(append([]string{}, c.args...), lifecycleID)...), "--json")
		first, _, err := runLifecycle(t, map[string]string{c.op: c.result}, args...)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(first), &got); err != nil {
			t.Fatalf("%s: not JSON: %v\n%s", c.name, err, first)
		}
		if got["app"] != lifecycleID || got["status"] != c.status {
			t.Errorf("%s: %v, want app %s status %s", c.name, got, lifecycleID, c.status)
		}
		repeat, _, _ := runLifecycle(t, map[string]string{c.op: c.result}, args...)
		if repeat != first {
			t.Errorf("%s: a repeat reads differently:\n%s\nvs\n%s", c.name, first, repeat)
		}
	}
}

func TestAppLifecycle1917TextStatesTheStateInTheSpecsWords(t *testing.T) {
	cases := []struct {
		args       []string
		op, result string
		needsYes   bool
		want       []string
	}{
		{[]string{"app", "uninstall"}, "UninstallApp", `{"data":{"uninstallApp":true}}`,
			true, []string{"is uninstalled", "read-only", "handle is free", "app reinstall"}},
		{[]string{"app", "delete"}, "DeleteApp", `{"data":{"deleteApp":true}}`,
			true, []string{"is deleted", "no longer serving", "no undelete"}},
		{[]string{"app", "reinstall"}, "ReinstallApp", `{"data":{"reinstallApp":true}}`,
			false, []string{"is installed", "same App"}},
	}
	for _, c := range cases {
		out, _, err := runLifecycle(t, map[string]string{c.op: c.result}, withYes(c.needsYes, append(append([]string{}, c.args...), lifecycleID)...)...)
		if err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%v: output %q lacks %q", c.args, out, w)
			}
		}
		// Never a transition claim (a repeat answers true too), never "reinstalled",
		// never a new App, and never the old "✓ Uninstalled App" for a deletion.
		for _, bad := range []string{"reinstalled", "Uninstalled App", " now", "new App", "created"} {
			if strings.Contains(out, bad) {
				t.Errorf("%v: output %q claims %q", c.args, out, bad)
			}
		}
	}
}

func TestAppLifecycle1917ConfirmationGatesUninstallAndDeleteButNotReinstall(t *testing.T) {
	for _, args := range [][]string{{"app", "uninstall", lifecycleID}, {"app", "delete", lifecycleID}, {"app", "rm", lifecycleID}} {
		_, captured, err := runLifecycle(t, allLifecycleResponses(), args...)
		if exitCodeFor(err) != exitcode.Usage || len(captured) != 0 {
			t.Errorf("%v without --yes, non-interactive: err %v (exit %d), requests %v; want a usage refusal and no request",
				args, err, exitCodeFor(err), keys(captured))
		}
	}
	if _, captured, err := runLifecycle(t, allLifecycleResponses(), "app", "reinstall", lifecycleID); err != nil || len(captured) != 1 {
		t.Errorf("reinstall restores the same App and needs no --yes: %v %v", err, keys(captured))
	}
}

func TestAppLifecycle1917DecliningThePromptSendsNothingAndUninstallIsNotCalledIrreversible(t *testing.T) {
	cases := []struct {
		args      []string
		prompt    []string
		notPrompt string
	}{
		{[]string{"app", "uninstall", lifecycleID}, []string{"Uninstall App " + lifecycleID, "read-only", "app reinstall"}, "cannot be undone"},
		{[]string{"app", "delete", lifecycleID}, []string{"Delete App " + lifecycleID, "cannot be undone"}, ""},
		{[]string{"app", "delete", lifecycleURN}, []string{"Delete App " + lifecycleURN, "installed App currently holding this URN", "cannot be undone"}, ""},
	}
	for _, c := range cases {
		gql, captured := captureGraphQL(t, allLifecycleResponses())
		f, _, errOut := testFactoryTTY(t, "n\n")
		root := NewRootCmd(f)
		root.SetArgs(append(c.args, "--server", gql.URL))
		err := root.Execute()
		if exitCodeFor(err) != exitcode.Cancelled || len(captured) != 0 {
			t.Errorf("%v declined: err %v, requests %v; want Cancelled and no request", c.args, err, keys(captured))
		}
		for _, w := range c.prompt {
			if !strings.Contains(errOut.String(), w) {
				t.Errorf("%v: prompt %q lacks %q", c.args, errOut.String(), w)
			}
		}
		if c.notPrompt != "" && strings.Contains(errOut.String(), c.notPrompt) {
			t.Errorf("%v: prompt %q says %q of a reversible uninstall", c.args, errOut.String(), c.notPrompt)
		}
	}
}

func TestAppLifecycle1917RefusalsAreTypedAndNeverPreflighted(t *testing.T) {
	refusal := func(op, code, msg string) map[string]string {
		return map[string]string{op: `{"errors":[{"message":"` + msg + `","extensions":{"code":"` + code + `","appId":"` + lifecycleID + `","urn":"` + lifecycleURN + `"}}],"data":null}`}
	}
	cases := []struct {
		args     []string
		resp     map[string]string
		wantExit int
		wantText string
		code     string
		needsYes bool
	}{
		{[]string{"app", "reinstall"}, refusal("ReinstallApp", "APP_URN_IN_USE", "Another installed App holds this App's handle; reinstall refused."), exitcode.Conflict, "handle", "APP_URN_IN_USE", false},
		{[]string{"app", "reinstall"}, refusal("ReinstallApp", "APP_DELETED", "This App is deleted."), exitcode.Conflict, "deleted", "APP_DELETED", false},
		{[]string{"app", "uninstall"}, refusal("UninstallApp", "APP_DELETED", "This App is deleted."), exitcode.Conflict, "deleted", "APP_DELETED", true},
		// A missing App and a refused caller get one identical FORBIDDEN; the CLI
		// renders it once and never reads first to tell them apart.
		{[]string{"app", "uninstall"}, refusal("UninstallApp", "FORBIDDEN", "Forbidden"), exitcode.Forbidden, "Forbidden", "FORBIDDEN", true},
		{[]string{"app", "reinstall"}, refusal("ReinstallApp", "FORBIDDEN", "Forbidden"), exitcode.Forbidden, "Forbidden", "FORBIDDEN", false},
		// deleteApp after hadron-server#3975: the canned refusal maps to its exit
		// and serverCode. This pins the code-to-exit mapping only; the fake
		// cannot express the server's id-vs-URN resolution (by id a repeat is
		// APP_DELETED, by URN FORBIDDEN while no installed App holds the handle).
		{[]string{"app", "delete"}, refusal("DeleteApp", "FORBIDDEN", "Forbidden"), exitcode.Forbidden, "Forbidden", "FORBIDDEN", true},
		{[]string{"app", "delete"}, refusal("DeleteApp", "APP_DELETED", "This App is deleted."), exitcode.Conflict, "deleted", "APP_DELETED", true},
	}
	for _, c := range cases {
		_, captured, err := runLifecycle(t, c.resp, withYes(c.needsYes, append(append([]string{}, c.args...), lifecycleURN)...)...)
		if err == nil || exitCodeFor(err) != c.wantExit {
			t.Errorf("%v %v: err %v exit %d, want exit %d", c.args, lifecycleURN, err, exitCodeFor(err), c.wantExit)
			continue
		}
		if !strings.Contains(err.Error(), c.wantText) {
			t.Errorf("%v: refusal %q lacks the server's %q", c.args, err.Error(), c.wantText)
		}
		if len(captured) != 1 {
			t.Errorf("%v: %d requests %v, want the one mutation", c.args, len(captured), keys(captured))
		}
		// The --json envelope's serverCode is the branchable signal (Celine's stack F4).
		var coded interface{ ServerCode() string }
		if !errors.As(err, &coded) || coded.ServerCode() != c.code {
			t.Errorf("%v: err %v must carry serverCode %s", c.args, err, c.code)
		}
	}
}

func TestAppLifecycle1917HelpNamesTheBreakingChangeAndTheIDRule(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	find := func(path ...string) (string, []string) {
		c, _, err := root.Find(path)
		if err != nil || c == nil || c.Name() != path[len(path)-1] {
			t.Fatalf("%v: no such command (%v)", path, err)
		}
		return c.Short + "\n" + c.Long, c.Aliases
	}
	help, aliases := find("app", "uninstall")
	for _, w := range []string{"no longer deletes", "app delete", "read-only", "handle", "app reinstall"} {
		if !strings.Contains(help, w) {
			t.Errorf("app uninstall help lacks %q:\n%s", w, help)
		}
	}
	if len(aliases) != 0 {
		t.Errorf("app uninstall has aliases %v, want none (rm moved to app delete)", aliases)
	}
	help, aliases = find("app", "delete")
	if strings.Join(aliases, ",") != "rm" {
		t.Errorf("app delete aliases %v, want [rm]", aliases)
	}
	for _, w := range []string{"by its id", "no undelete", "app uninstall"} {
		if !strings.Contains(help, w) {
			t.Errorf("app delete help lacks %q:\n%s", w, help)
		}
	}
	help, _ = find("app", "reinstall")
	for _, w := range []string{"same App", "APP_URN_IN_USE"} {
		if !strings.Contains(help, w) {
			t.Errorf("app reinstall help lacks %q:\n%s", w, help)
		}
	}
	// The neighbouring "uninstall" names keep their meaning.
	if c, _, err := root.Find([]string{"app", "agent", "uninstall"}); err != nil || c.Name() != "remove" {
		t.Errorf("app agent uninstall no longer reaches app agent remove: %v %v", c, err)
	}
	for _, p := range [][]string{{"team", "health", "uninstall"}, {"team", "usage", "uninstall"}} {
		if c, _, err := root.Find(p); err != nil || c.Name() != "uninstall" {
			t.Errorf("%v moved: %v %v", p, c, err)
		}
	}
}

// Record every request in order: a map alone hides duplicate mutations.
func runLifecycleOrdered(t *testing.T, responses map[string]string, args ...string) (string, map[string]json.RawMessage, []string, error) {
	t.Helper()
	captured := map[string]json.RawMessage{}
	calls := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string
			Variables     json.RawMessage
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		calls = append(calls, req.OperationName)
		captured[req.OperationName] = req.Variables
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responses[req.OperationName]))
	}))
	defer srv.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", srv.URL))
	err := root.Execute()
	if err != nil {
		renderError(f, err)
	}
	return out.String(), captured, calls, err
}

func TestAppLifecycle1934CanonicalRefAndURNHint(t *testing.T) {
	for _, c := range lifecycleCases {
		_, captured, calls, err := runLifecycleOrdered(t, allLifecycleResponses(), withYes(c.needsYes, append(append([]string{}, c.args...), "acme.com:support")...)...)
		if err != nil || len(calls) != 1 {
			t.Fatalf("%s: %v calls %v", c.name, err, calls)
		}
		var vars map[string]any
		if err := json.Unmarshal(captured[c.op], &vars); err != nil {
			t.Fatal(err)
		}
		if vars["ref"] != lifecycleURN {
			t.Fatalf("%s: canonical ref %v", c.name, vars)
		}
	}
	out, _, _, err := runLifecycleOrdered(t, allLifecycleResponses(), "app", "uninstall", lifecycleURN, "--yes")
	if err != nil || !strings.Contains(out, "hadron app reinstall <its id>") || strings.Contains(out, "hadron app reinstall "+lifecycleURN) {
		t.Fatalf("URN reinstall hint: %q %v", out, err)
	}
}

func TestAppLifecycle1934JSONRefusalEnvelope(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{{"APP_DELETED", exitcode.Conflict}, {"APP_URN_IN_USE", exitcode.Conflict}, {"FORBIDDEN", exitcode.Forbidden}} {
		response := `{"errors":[{"message":"refused","extensions":{"code":"` + tc.code + `","appId":"` + lifecycleID + `","urn":"` + lifecycleURN + `"}}],"data":null}`
		out, _, calls, err := runLifecycleOrdered(t, map[string]string{"ReinstallApp": response}, "app", "reinstall", lifecycleID, "--json")
		if exitCodeFor(err) != tc.exit || len(calls) != 1 {
			t.Fatalf("%s: %v calls %v", tc.code, err, calls)
		}
		var envelope struct {
			Error struct {
				Code       int
				ServerCode string
				Message    string
			}
		}
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Error.Code != tc.exit || envelope.Error.ServerCode != tc.code || !strings.Contains(envelope.Error.Message, "refused") {
			t.Fatalf("%s: %s", tc.code, out)
		}
	}
}

func TestAppLifecycle1934HelpReferences(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	for _, name := range []string{"delete", "uninstall", "reinstall"} {
		command, _, err := root.Find([]string{"app", name})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(command.Long, "org:slug") {
			t.Errorf("%s: help must document org:slug normalization", name)
		}
		for _, line := range strings.Split(command.Example, "\n") {
			fields := strings.Fields(line)
			if len(fields) > 3 && !strings.Contains(fields[3], ":") && !cmdutil.IsAppID(fields[3]) {
				t.Errorf("%s: invalid example App id %q", name, fields[3])
			}
		}
	}
}
