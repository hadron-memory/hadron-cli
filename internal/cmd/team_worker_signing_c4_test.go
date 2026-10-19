package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#916 C4 (hadron-server#2120, cor:agt:040:04): per-worker signature and
// trailer templates on `team worker update` and `team worker get`.

// signedWorkerJSON is a worker whose templates the caller may read.
const signedWorkerJSON = `{"id":"wkr1","urn":"hrn:worker:acme.com:eng-team:iris","slug":"iris",
	"appId":"capp100000000000000000000","agentId":"agt1","name":"Iris","role":"backend-engineer",
	"prompt":"You are Iris.","promptOverride":null,
	"signature":"— [{{worker.name}}, {{worker.role}}]({{worker.url}}) · {{session.llmModel}}","trailer":"",
	"memoryId":"mw1","retiredAt":null,"retiredBy":null,
	"createdAt":"2026-08-14T00:00:00Z","createdBy":"u-holger"}`

func signingStubs(resp string) map[string]string {
	return map[string]string{
		"GetWorker":           `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"UpdateWorkerSigning": resp,
		"TeamAppIdentity":     teamAppIdentityJSON,
	}
}

const signingOK = `{"data":{"updateWorker":` + signedWorkerJSON + `}}`

func runWorkerUpdate(t *testing.T, responses map[string]string, flags ...string) (string, *[]attnCall, error) {
	t.Helper()
	srv, calls := attnServer(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"team", "worker", "update", "hrn:worker:acme.com:eng-team:iris"}, flags...), "--server", srv.URL))
	err := root.Execute()
	return out.String(), calls, err
}

func callsFor(calls *[]attnCall, op string) []attnCall {
	var out []attnCall
	for _, c := range *calls {
		if c.Op == op {
			out = append(out, c)
		}
	}
	return out
}

// Pin 1: the ACTUAL serialized variables, read from the request body (a
// decode cannot tell an absent key from a null one). Omitted is absent, ""
// is an explicit empty string, text is text, and null never appears.
func TestC4SigningUpdateSerializesPresenceExactly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		flags   []string
		want    []string
		without []string
	}{
		{"signature empty", []string{"--signature", ""}, []string{`"signature":""`}, []string{`"trailer"`, `"promptOverride"`}},
		{"signature text", []string{"--signature", "by {{worker.name}}"}, []string{`"signature":"by {{worker.name}}"`}, []string{`"trailer"`, `"promptOverride"`}},
		{"trailer empty", []string{"--trailer", ""}, []string{`"trailer":""`}, []string{`"signature"`, `"promptOverride"`}},
		{"trailer text", []string{"--trailer", "Worker: {{worker.name}}"}, []string{`"trailer":"Worker: {{worker.name}}"`}, []string{`"signature"`, `"promptOverride"`}},
		{"both", []string{"--signature", "s", "--trailer", "t"}, []string{`"signature":"s"`, `"trailer":"t"`}, []string{`"promptOverride"`}},
		{"with override", []string{"--signature", "s", "--prompt-override", "p"}, []string{`"signature":"s"`, `"promptOverride":"p"`}, []string{`"trailer"`}},
		{"with cleared override", []string{"--trailer", "t", "--clear-prompt-override"}, []string{`"trailer":"t"`, `"promptOverride":""`}, []string{`"signature"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, calls, err := runWorkerUpdate(t, signingStubs(signingOK), tc.flags...)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			got := callsFor(calls, "UpdateWorkerSigning")
			if len(got) != 1 {
				t.Fatalf("want exactly one UpdateWorkerSigning, got %d (%+v)", len(got), *calls)
			}
			raw := string(got[0].RawVars)
			for _, w := range tc.want {
				if !strings.Contains(raw, w) {
					t.Errorf("variables must contain %s:\n%s", w, raw)
				}
			}
			for _, w := range tc.without {
				if strings.Contains(raw, w) {
					t.Errorf("variables must omit %s:\n%s", w, raw)
				}
			}
			if strings.Contains(raw, "null") {
				t.Errorf("a signing update never sends null:\n%s", raw)
			}
		})
	}
}

// Pin 2: a prompt-override-only update keeps today's operation and wire,
// explicit-null clear included (TestWorkerUpdateClearSendsAnExplicitNull).
func TestC4PromptOnlyUpdateKeepsItsOperation(t *testing.T) {
	r := signingStubs(signingOK)
	r["UpdateWorker"] = `{"data":{"updateWorker":` + updatedWorkerJSON + `}}`
	_, calls, err := runWorkerUpdate(t, r, "--prompt-override", "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(callsFor(calls, "UpdateWorkerSigning")) != 0 || len(callsFor(calls, "UpdateWorker")) != 1 {
		t.Errorf("a prompt-only update must use UpdateWorker alone: %+v", *calls)
	}
}

// Pin 3: nothing to update is refused before any write, and the message
// names every field.
func TestC4NothingToUpdateNamesAllFields(t *testing.T) {
	_, calls, err := runWorkerUpdate(t, signingStubs(signingOK))
	if exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("exit = %d, want Usage", exitCodeFor(err))
	}
	for _, flag := range []string{"--signature", "--trailer", "--prompt-override"} {
		if !strings.Contains(err.Error(), flag) {
			t.Errorf("the refusal must name %s: %v", flag, err)
		}
	}
	if len(callsFor(calls, "UpdateWorkerSigning"))+len(callsFor(calls, "UpdateWorker")) != 0 {
		t.Error("no write may be sent")
	}
}

// Pin 4: from a bound worktree, ANY signature/trailer flag sends the
// worktree's session, so the server applies its bound-session rule (H26,
// provisional). A prompt-only update sends none, as today.
func TestC4BoundWorktreeAttachesSessionForSigningOnly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		op    string
		want  string
	}{
		{"signature", []string{"--signature", "s"}, "UpdateWorkerSigning", "s-new"},
		{"trailer", []string{"--trailer", ""}, "UpdateWorkerSigning", "s-new"},
		{"mixed", []string{"--trailer", "t", "--prompt-override", "p"}, "UpdateWorkerSigning", "s-new"},
		{"prompt only", []string{"--prompt-override", "p"}, "UpdateWorker", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTeamBinding(t)
			r := signingStubs(signingOK)
			r["UpdateWorker"] = `{"data":{"updateWorker":` + updatedWorkerJSON + `}}`
			srv, calls := attnServer(t, r)
			setTeamBindingServer(t, srv.URL)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"team", "worker", "update", "hrn:worker:acme.com:eng-team:iris"}, tc.flags...), "--server", srv.URL))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			got := callsFor(calls, tc.op)
			if len(got) != 1 || got[0].Session != tc.want {
				t.Fatalf("%s session = %+v, want %q", tc.op, got, tc.want)
			}
			for _, c := range *calls {
				if c.Op != tc.op && c.Session != "" {
					t.Errorf("only the write may carry the session: %+v", c)
				}
			}
		})
	}
}

// Pin 5: FORBIDDEN keeps the server's sentence whole. Only when this CLI sent
// the edit through a bound session does it add a hedged supplement, and it
// never names a rule it can't see.
func TestC4ForbiddenKeepsServerTextAndHedgesOnlyWhenBound(t *testing.T) {
	const serverText = "You do not have permission to manage this App's workers."
	forbidden := `{"errors":[{"message":"` + serverText + `","extensions":{"code":"FORBIDDEN"}}]}`
	for _, bound := range []bool{true, false} {
		if bound {
			writeTeamBinding(t)
		} else {
			teamGitDir(t)
		}
		srv, _ := attnServer(t, signingStubs(forbidden))
		if bound {
			setTeamBindingServer(t, srv.URL)
		}
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "worker", "update", "hrn:worker:acme.com:eng-team:iris", "--signature", "s", "--server", srv.URL})
		err := root.Execute()
		if exitCodeFor(err) != exitcode.Forbidden {
			t.Fatalf("bound=%v: exit = %d, want Forbidden", bound, exitCodeFor(err))
		}
		msg := err.Error()
		if !strings.Contains(msg, serverText) {
			t.Errorf("bound=%v: the server's sentence must survive whole:\n%s", bound, msg)
		}
		if strings.Contains(msg, "H26") {
			t.Errorf("bound=%v: the CLI must not name the rule:\n%s", bound, msg)
		}
		hedged := strings.Contains(msg, "bound worker session")
		if hedged != bound {
			t.Errorf("bound=%v: hedged supplement present=%v:\n%s", bound, hedged, msg)
		}
	}
}

// Pin 6: the receipt names what this call changed, and nothing else.
func TestC4ReceiptNamesOnlyChangedFields(t *testing.T) {
	out, _, err := runWorkerUpdate(t, signingStubs(signingOK), "--trailer", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "trailer") {
		t.Errorf("the receipt must name the trailer:\n%s", out)
	}
	for _, not := range []string{"signature template", "prompt override"} {
		if strings.Contains(strings.ToLower(out), not) {
			t.Errorf("the receipt must not claim %q:\n%s", not, out)
		}
	}
}

// Pin 7: `worker get` — a template is a string, "" stays "" (printed as
// "(empty)"), and a masked field is null (printed as nothing).
func TestC4WorkerGetShowsTemplatesAndWithheld(t *testing.T) {
	get := func(worker string, json bool) string {
		gql, _ := captureGraphQL(t, map[string]string{"GetWorker": `{"data":{"worker":` + worker + `}}`, "TeamAppIdentity": teamAppIdentityJSON})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		args := []string{"team", "worker", "get", "hrn:worker:acme.com:eng-team:iris", "--server", gql.URL}
		if json {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	var dto struct {
		Signature *string `json:"signature"`
		Trailer   *string `json:"trailer"`
	}
	if err := json.Unmarshal([]byte(get(signedWorkerJSON, true)), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Signature == nil || !strings.Contains(*dto.Signature, "{{worker.name}}") || dto.Trailer == nil || *dto.Trailer != "" {
		t.Errorf("--json must carry the raw templates, \"\" as \"\": %+v", dto)
	}
	masked := get(irisWorkerJSON, true)
	if !strings.Contains(masked, `"signature": null`) || !strings.Contains(masked, `"trailer": null`) {
		t.Errorf("a withheld template must be null in --json:\n%s", masked)
	}
	human := get(signedWorkerJSON, false)
	if !strings.Contains(human, "Signature template:") || !strings.Contains(human, "Trailer template:\n(empty)") {
		t.Errorf("human output must show both templates, empty as (empty):\n%s", human)
	}
	if strings.Contains(get(irisWorkerJSON, false), "template:") {
		t.Error("a withheld template prints nothing")
	}
}

// Pin 8 (#946 BLIND P2, Joost): a bound worktree's signing edit never falls
// back to the person. The binding is resolved BEFORE the write: the same
// endpoint under another spelling still carries the session, and a binding
// that can't be verified for this request refuses with nothing written.
// Unbound shells are unaffected (pin 4).
func TestC4BoundSigningEditNeverDowngradesToThePerson(t *testing.T) {
	for _, tc := range []struct {
		name    string
		server  func(url string) string // the binding's recorded server
		app     string                  // --app, when set
		mutate  func(b map[string]any)
		session string // "" means refused before any write
	}{
		{name: "same endpoint, trailing slash", server: func(u string) string { return u + "/" }, session: "s-new"},
		{name: "another server", server: func(string) string { return "https://elsewhere.example.test" }},
		{name: "another App", server: func(u string) string { return u }, app: "capp200000000000000000000"},
		{name: "incomplete binding", server: func(u string) string { return u }, mutate: func(b map[string]any) { b["workerId"] = "" }},
		{name: "binding without a session", server: func(u string) string { return u }, mutate: func(b map[string]any) { b["sessionId"] = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTeamBinding(t)
			r := signingStubs(signingOK)
			r["TeamAppIdentity"] = teamAppIdentityJSON
			if tc.app != "" {
				r["TeamAppIdentity"] = strings.ReplaceAll(teamAppIdentityJSON, "capp100000000000000000000", tc.app)
			}
			srv, calls := attnServer(t, r)
			setTeamBindingServer(t, tc.server(srv.URL))
			if tc.mutate != nil {
				p := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
				raw, _ := os.ReadFile(p)
				var b map[string]any
				_ = json.Unmarshal(raw, &b)
				tc.mutate(b)
				raw, _ = json.Marshal(b)
				if err := os.WriteFile(p, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"team", "worker", "update", "hrn:worker:acme.com:eng-team:iris", "--signature", "s", "--server", srv.URL}
			if tc.app != "" {
				args = append(args, "--app", tc.app)
			}
			root.SetArgs(args)
			err := root.Execute()
			writes := callsFor(calls, "UpdateWorkerSigning")
			if tc.session != "" {
				if err != nil || len(writes) != 1 || writes[0].Session != tc.session {
					t.Fatalf("want one write carrying %q; err=%v writes=%+v", tc.session, err, writes)
				}
				return
			}
			if len(writes) != 0 {
				t.Fatalf("an unverifiable binding must refuse before writing; sent %+v", writes)
			}
			if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "nothing was written") {
				t.Errorf("want a Usage refusal saying nothing was written, got %d: %v", exitCodeFor(err), err)
			}
		})
	}
}
