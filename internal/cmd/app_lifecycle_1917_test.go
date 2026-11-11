package cmd

import (
	"encoding/json"
	"errors"
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
// The server answers true for a real move and for a repeat alike, so the
// output states the resulting state, never a transition.

const (
	lifecycleID  = "capp100000000000000000000"
	lifecycleURN = "hrn:app:acme.com:support"
)

type lifecycleCase struct {
	name   string
	args   []string // without the ref and --server
	op     string
	result string
	status string
}

var lifecycleCases = []lifecycleCase{
	{"app delete", []string{"app", "delete"}, "DeleteApp", `{"data":{"deleteApp":true}}`, "deleted"},
	{"app rm", []string{"app", "rm"}, "DeleteApp", `{"data":{"deleteApp":true}}`, "deleted"},
	{"app uninstall", []string{"app", "uninstall"}, "UninstallApp", `{"data":{"uninstallApp":true}}`, "uninstalled"},
	{"app reinstall", []string{"app", "reinstall"}, "ReinstallApp", `{"data":{"reinstallApp":true}}`, "installed"},
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
func withYes(args ...string) []string {
	if args[1] == "reinstall" {
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
			_, captured, err := runLifecycle(t, allLifecycleResponses(), withYes(append(append([]string{}, c.args...), ref)...)...)
			if err != nil {
				t.Fatalf("%s %s: %v", c.name, ref, err)
			}
			if len(captured) != 1 {
				t.Fatalf("%s %s: want exactly one request (%s, no preflight read), got %v", c.name, ref, c.op, keys(captured))
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
		args := append(withYes(append(append([]string{}, c.args...), lifecycleID)...), "--json")
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
		want       []string
	}{
		{[]string{"app", "uninstall"}, "UninstallApp", `{"data":{"uninstallApp":true}}`,
			[]string{"is uninstalled", "read-only", "handle is free", "app reinstall"}},
		{[]string{"app", "delete"}, "DeleteApp", `{"data":{"deleteApp":true}}`,
			[]string{"is deleted", "no longer serving", "no undelete"}},
		{[]string{"app", "reinstall"}, "ReinstallApp", `{"data":{"reinstallApp":true}}`,
			[]string{"is installed", "same App"}},
	}
	for _, c := range cases {
		out, _, err := runLifecycle(t, map[string]string{c.op: c.result}, withYes(append(append([]string{}, c.args...), lifecycleID)...)...)
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
	}{
		{[]string{"app", "reinstall"}, refusal("ReinstallApp", "APP_URN_IN_USE", "Another installed App holds this App's handle; reinstall refused."), exitcode.Conflict, "handle", "APP_URN_IN_USE"},
		{[]string{"app", "reinstall"}, refusal("ReinstallApp", "APP_DELETED", "This App is deleted."), exitcode.Conflict, "deleted", "APP_DELETED"},
		{[]string{"app", "uninstall"}, refusal("UninstallApp", "APP_DELETED", "This App is deleted."), exitcode.Conflict, "deleted", "APP_DELETED"},
		// A missing App and a refused caller get one identical FORBIDDEN; the CLI
		// renders it once and never reads first to tell them apart.
		{[]string{"app", "uninstall"}, refusal("UninstallApp", "FORBIDDEN", "Forbidden"), exitcode.Forbidden, "Forbidden", "FORBIDDEN"},
		{[]string{"app", "reinstall"}, refusal("ReinstallApp", "FORBIDDEN", "Forbidden"), exitcode.Forbidden, "Forbidden", "FORBIDDEN"},
		// deleteApp resolves its ref without deleted Apps: a repeat delete, like
		// a missing App, is the server's APP_NOT_FOUND (Celine's stack F3).
		{[]string{"app", "delete"}, refusal("DeleteApp", "APP_NOT_FOUND", "App not found."), exitcode.NotFound, "not found", "APP_NOT_FOUND"},
	}
	for _, c := range cases {
		_, captured, err := runLifecycle(t, c.resp, withYes(append(append([]string{}, c.args...), lifecycleURN)...)...)
		if err == nil || exitCodeFor(err) != c.wantExit {
			t.Errorf("%v %v: err %v exit %d, want exit %d", c.args, c.args, err, exitCodeFor(err), c.wantExit)
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
