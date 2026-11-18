package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type managerCase2448 struct {
	name, op, field, answer, vars string
	args                          []string
}

func managerCases2448() []managerCase2448 {
	return []managerCase2448{
		{name: "properties", op: "SetAppProperties", field: "setAppProperties", answer: `{"id":"` + app1594 + `","urn":"hrn:app:acme.com:eng-team","propertiesRevision":4,"properties":{"x":1}}`, vars: `{"appRef":"` + app1594 + `","properties":{"x":1},"expectedRevision":3}`, args: []string{"app", "properties", "set", app1594, `{"x":1}`, "--expected-revision", "3"}},
		{name: "model-set", op: "SetAppModelGroup", field: "setAppModelGroup", answer: `{"name":"frontier","patterns":["claude-*"],"updatedAt":"2026-11-18T00:00:00Z"}`, vars: `{"appRef":"` + app1594 + `","name":"frontier","patterns":["claude-*"]}`, args: []string{"app", "model-group", "set", app1594, "frontier", "--pattern", "claude-*"}},
		{name: "model-clear", op: "DeleteAppModelGroup", field: "deleteAppModelGroup", answer: `true`, vars: `{"appRef":"` + app1594 + `","name":"frontier"}`, args: []string{"app", "model-group", "clear", app1594, "frontier", "--yes"}},
		{name: "delivery", op: "TeamDeliverySettingsSet", field: "setAppDeliverySettings", answer: `{"batchWindowSeconds":120,"busyDelaySeconds":600,"stuckWindowSeconds":900,"checkPeriodSeconds":600,"darkAlarmWindowSeconds":1800,"failedChecksToOverdue":2}`, vars: `{"appRef":"` + app1594 + `","batchWindowSeconds":120}`, args: []string{"team", "delivery-settings", "set", "--batch-window-seconds", "120", "--app", app1594}},
		{name: "ticker", op: "TeamTickerSettingsSet", field: "setAppTickerSettings", answer: `{"minCadenceSeconds":120,"liveCap":10,"selfSubscribeCadenceSeconds":600,"dailyWakeBudget":100,"dailyWakeBudgetDerived":false}`, vars: `{"appRef":"` + app1594 + `","liveCap":10}`, args: []string{"team", "ticker", "settings", "set", "--live-cap", "10", "--app", app1594}},
		{name: "grant-rm", op: "TeamAppGrantRm", field: "revokeAppGrant", answer: `{"id":"grant1","name":"WORKERS_STAFF","grantedAt":"2026-11-17T00:00:00Z","revokedAt":"2026-11-18T00:00:00Z","app":{"id":"` + app1594 + `"},"worker":{"id":"wkr1"}}`, vars: `{"appRef":"` + app1594 + `","workerRef":"wkr1","name":"WORKERS_STAFF"}`, args: []string{"team", "grant", "rm", "wkr1", "WORKERS_STAFF", "--yes", "--app", app1594}},
	}
}

// Literal hashes pin the six plain (no-flag) wire documents. They were captured
// from the generated plain operations on main at 2efc8cb3's parent, before the
// instructed operations were added: each value is the SHA-256 of that
// operation's Query as the request carries it, i.e. the text of the
// gen.<Op>_Operation constant in internal/api/gen/generated.go. To recompute
// after a deliberate change to a plain document, hash that constant for each
// of these six operations on the new base and name the base in the commit.
// No-flag wire documents must not move otherwise.
//
// The call recorder (snapshotServer1614) appends from the httptest handler
// goroutine without a lock. That is safe here only because every test in this
// file issues its requests strictly one after another and reads the slice
// after the command returns; introduce parallel requests only together with a
// mutex around that append.
var managerPlainHashes2448 = map[string]string{
	"SetAppProperties":        "4f32f157f9fb5f7b63e0cae3497418d82fd6241cd746147052230a919f76ea6c",
	"SetAppModelGroup":        "82c77f7c4d337ee9ce020c357cfa8993f5e2f744d5ffc33cb98769894a0fe6a4",
	"DeleteAppModelGroup":     "964f2237788bbde7d3000aa4d958fbb03af048cd5835fb09975ab3bde2138ecd",
	"TeamDeliverySettingsSet": "d55b6270448967bfb84de9f57eef3433a0683f0788a166d07162d3a3ab241a8c",
	"TeamTickerSettingsSet":   "91b80c85e3a3a57a944dd07cadb1e79e23a330062a65902cc7a649eb5ddd0ed0",
	"TeamAppGrantRm":          "254ec644e01f887f32525351ab5765ae61a8312ac7ab46bf7168cebc367603cf",
}

func managerRun2448(t *testing.T, c managerCase2448, extra []string, state string, hook func(snapshotCall1614), refused bool) (*[]snapshotCall1614, string, string, error) {
	t.Helper()
	teamGitDir(t)
	srv, calls := snapshotServer1614(t, func(call snapshotCall1614) string {
		if hook != nil {
			hook(call)
		}
		if call.OperationName == "GetBoundSessionEnd" {
			return `{"data":{"session":{"id":"s-new","endedAt":null,"endReason":null}}}`
		}
		if call.OperationName == "TeamAppIdentity" {
			return teamAppIdentityJSON
		}
		if call.OperationName != c.op && call.OperationName != c.op+"Instructed" {
			t.Errorf("unexpected operation %s", call.OperationName)
			return `{}`
		}
		if refused {
			return `{"errors":[{"message":"manager instruction refused","extensions":{"code":"FORBIDDEN"}}]}`
		}
		return `{"data":{"` + c.field + `":` + c.answer + `}}`
	})
	writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
	path := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
	switch state {
	case "none":
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	case "corrupt":
		if err := os.WriteFile(path, []byte(`{broken`), 0600); err != nil {
			t.Fatal(err)
		}
	case "incomplete":
		rewriteBinding1592(t, func(b map[string]any) { delete(b, "sessionId") })
	case "other-server":
		rewriteBinding1592(t, func(b map[string]any) { b["server"] = "https://other.invalid" })
	case "other-app":
		rewriteBinding1592(t, func(b map[string]any) { b["appId"] = otherApp1594 })
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	argv := append(append([]string{}, c.args...), "--json", "--server", srv.URL)
	argv = append(argv, extra...)
	root.SetArgs(argv)
	err := root.Execute()
	return calls, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}
func managerWrite2448(t *testing.T, c managerCase2448, calls *[]snapshotCall1614, op string) snapshotCall1614 {
	t.Helper()
	var found []snapshotCall1614
	for _, call := range *calls {
		switch call.OperationName {
		case op:
			found = append(found, call)
		case c.op, c.op + "Instructed":
			t.Fatalf("unexpected alternate mutation: %+v", call)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one %s call, got %+v", op, *calls)
	}
	return found[0]
}
func TestAppManagerInstructionWire2448(t *testing.T) {
	for _, c := range managerCases2448() {
		t.Run(c.name, func(t *testing.T) {
			plain, out, _, err := managerRun2448(t, c, nil, "", nil, false)
			if err != nil {
				t.Fatal(err)
			}
			base := managerWrite2448(t, c, plain, c.op)
			sum := sha256.Sum256([]byte(base.Query))
			if hex.EncodeToString(sum[:]) != managerPlainHashes2448[c.op] {
				t.Fatalf("no-flag document changed: %s", base.Query)
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(c.vars), &want); err != nil {
				t.Fatal(err)
			}
			wantCalls := 1
			if strings.HasPrefix(c.op, "Team") {
				wantCalls++
			}
			if base.Session != "" || !reflect.DeepEqual(base.Variables, want) || len(*plain) != wantCalls {
				t.Fatalf("no-flag wire changed: %+v", *plain)
			}
			calls, instrOut, _, err := managerRun2448(t, c, []string{"--instruction", "live-chat:client: 42 "}, "", nil, false)
			if err != nil {
				t.Fatal(err)
			}
			call := managerWrite2448(t, c, calls, c.op+"Instructed")
			if call.Session != "s-new" {
				t.Fatalf("verified session missing: %+v", call)
			}
			if !strings.Contains(call.Query, "instruction: $instruction") {
				t.Fatalf("instruction not bound in mutation: %s", call.Query)
			}
			claim := map[string]any{"kind": "LIVE_CHAT", "ref": "client: 42 "}
			if !reflect.DeepEqual(call.Variables["instruction"], claim) {
				t.Fatalf("claim changed: %#v", call.Variables)
			}
			rest := map[string]any{}
			for k, v := range call.Variables {
				if k != "instruction" {
					rest[k] = v
				}
			}
			if !reflect.DeepEqual(rest, want) || out != instrOut {
				t.Fatalf("instruction changed other args/output: vars=%v out=%q vs %q", call.Variables, out, instrOut)
			}
		})
	}
}
func TestAppManagerInstructionBadFlag2448(t *testing.T) {
	for _, c := range managerCases2448() {
		for _, bad := range []string{"", "bad", "wrong:ref", "task:  "} {
			t.Run(c.name+"/"+bad, func(t *testing.T) {
				calls, _, _, err := managerRun2448(t, c, []string{"--instruction", bad}, "", nil, false)
				if err == nil || exitCodeFor(err) != exitcode.Usage || strings.Contains(err.Error(), "unknown flag") || len(*calls) != 0 {
					t.Fatalf("bad claim must refuse before requests: err=%v calls=%+v", err, *calls)
				}
			})
		}
	}
}
func TestAppManagerInstructionFallback2448(t *testing.T) {
	for _, c := range managerCases2448() {
		for _, state := range []string{"none", "corrupt", "incomplete", "other-server", "other-app"} {
			t.Run(c.name+"/"+state, func(t *testing.T) {
				calls, _, note, err := managerRun2448(t, c, []string{"--instruction", "task:one"}, state, nil, false)
				if err != nil {
					t.Fatal(err)
				}
				call := managerWrite2448(t, c, calls, c.op+"Instructed")
				if call.Session != "" || call.Variables["instruction"] == nil {
					t.Fatalf("must send claim as person: %+v", call)
				}
				if state != "none" && !strings.Contains(note, "signed-in person") {
					t.Fatalf("unusable binding note missing: %q", note)
				}
				if state == "none" && note != "" {
					t.Fatalf("unbound uses instructedAsPerson quiet fallback: %q", note)
				}
			})
		}
	}
}
func TestAppManagerInstructionBindingChange2448(t *testing.T) {
	for _, original := range managerCases2448() {
		t.Run(original.name, func(t *testing.T) {
			c := original
			c.args = append([]string{}, original.args...)
			for i, v := range c.args {
				if v == app1594 {
					c.args[i] = "hrn:app:acme.com:eng-team"
				}
			}
			changed := false
			calls, _, note, err := managerRun2448(t, c, []string{"--instruction", "task:one"}, "", func(call snapshotCall1614) {
				if call.OperationName == "TeamAppIdentity" {
					changed = true
					rewriteBinding1592(t, func(b map[string]any) { b["sessionId"] = "successor" })
				}
			}, false)
			if err != nil {
				t.Fatal(err)
			}
			call := managerWrite2448(t, c, calls, c.op+"Instructed")
			if !changed || call.Session != "" || !strings.Contains(note, "changed") {
				t.Fatalf("must reject successor attribution: changed=%t note=%q call=%+v", changed, note, call)
			}
			for _, read := range *calls {
				if read.OperationName != c.op+"Instructed" && (read.Session != "" || read.Variables["instruction"] != nil) {
					t.Fatalf("lookup claimed worker authority: %+v", read)
				}
			}
		})
	}
}
func TestAppManagerInstructionRefusalNoRetry2448(t *testing.T) {
	for _, c := range managerCases2448() {
		t.Run(c.name, func(t *testing.T) {
			calls, out, _, err := managerRun2448(t, c, []string{"--instruction", "task:one"}, "", nil, true)
			if err == nil || exitCodeFor(err) != exitcode.Forbidden || out != "" || !strings.Contains(err.Error(), "manager instruction refused") {
				t.Fatalf("refusal changed: %v out=%q", err, out)
			}
			call := managerWrite2448(t, c, calls, c.op+"Instructed")
			wantCalls := 1
			if strings.HasPrefix(c.op, "Team") {
				wantCalls++
			}
			if call.Session != "s-new" || len(*calls) != wantCalls {
				t.Fatalf("refusal retried/downgraded: %+v", *calls)
			}
		})
	}
}

// A prompt is a local boundary too: no lookup may precede consent, and a
// binding replaced while the person answers must never attribute the successor.
type managerPromptReader2448 struct {
	before func()
	answer io.Reader
}

func (r *managerPromptReader2448) Read(p []byte) (int, error) {
	if r.before != nil {
		r.before()
		r.before = nil
	}
	return r.answer.Read(p)
}
func TestAppManagerInstructionConfirmation2448(t *testing.T) {
	for _, c := range managerCases2448() {
		if c.name != "model-clear" && c.name != "grant-rm" {
			continue
		}
		for _, accept := range []bool{false, true} {
			t.Run(c.name+"/"+map[bool]string{false: "decline", true: "binding-changed"}[accept], func(t *testing.T) {
				teamGitDir(t)
				srv, calls := snapshotServer1614(t, func(call snapshotCall1614) string {
					if call.OperationName == "GetBoundSessionEnd" {
						return `{"data":{"session":{"id":"successor","endedAt":null,"endReason":null}}}`
					}
					if call.OperationName != c.op+"Instructed" {
						t.Errorf("unexpected lookup %s", call.OperationName)
					}
					return `{"data":{"` + c.field + `":` + c.answer + `}}`
				})
				writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
				f, _ := testFactory(t)
				answer := "n\n"
				if accept {
					answer = "y\n"
				}
				streams, _, stderr := output.TestTTY(answer)
				streams.In = &managerPromptReader2448{answer: streams.In, before: func() {
					if len(*calls) != 0 {
						t.Fatalf("request before consent: %+v", *calls)
					}
					if accept {
						rewriteBinding1592(t, func(b map[string]any) { b["sessionId"] = "successor" })
					}
				}}
				f.IOStreams = streams
				root := NewRootCmd(f)
				args := []string{}
				for _, arg := range c.args {
					if arg != "--yes" {
						args = append(args, arg)
					}
				}
				root.SetArgs(append(args, "--server", srv.URL, "--instruction", "task:one", "--json"))
				err := root.Execute()
				if !strings.Contains(stderr.String(), app1594) {
					t.Fatalf("prompt omits target App: %q", stderr.String())
				}
				if !accept {
					if err == nil || len(*calls) != 0 {
						t.Fatalf("decline err=%v calls=%+v", err, *calls)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				call := managerWrite2448(t, c, calls, c.op+"Instructed")
				if call.Session != "" || !strings.Contains(stderr.String(), "changed") {
					t.Fatalf("successor attributed: %+v stderr=%q", call, stderr.String())
				}
			})
		}
	}
}
func TestAppManagerInstructionScope2448(t *testing.T) {
	for _, args := range [][]string{{"app", "properties", "clear"}, {"app", "properties", "get"}, {"app", "model-group", "list"}, {"team", "grant", "add"}, {"team", "delivery-settings", "get"}, {"team", "ticker", "settings", "get"}} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		cmd, _, err := root.Find(args)
		if err != nil {
			t.Fatal(err)
		}
		if cmd.Flags().Lookup("instruction") != nil {
			t.Fatalf("instruction leaked to %v", args)
		}
	}
}
