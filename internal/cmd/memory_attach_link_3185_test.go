package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#3185 (C2): attach is a LINK now (hadron-server#5185, cor:dmo:060:20
// "An attached memory"): any readable memory but `system`, unchanged, several Apps,
// read-only for the App key, lapses with the attacher's read. Refusal codes are the
// A3 table's.
const attachedKnowledge3185 = `{"id":"m3","urn":"hrn:mem:acme.com:kb","name":"Knowledge","shortDescription":null,
	"class":"knowledge","visibility":null,"organizationId":"o1",
	"isEncrypted":false,"maxRevCount":10,"updatedAt":"2026-07-14T00:00:00Z"}`

func attachArgs3185(extra ...string) []string {
	return append([]string{"memory", "attach", "hrn:mem:acme.com:kb", "--app", "hrn:app:acme.com:coach"}, extra...)
}

// Both the help and agentic-usage describe the link law and none of the retired
// bind wording.
func TestMemoryAttachHelpStatesTheLinkLaw3185(t *testing.T) {
	for _, args := range [][]string{{"memory", "attach", "--help"}, {"agentic-usage"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetOut(f.IOStreams.Out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		if args[0] == "agentic-usage" {
			i := strings.Index(text, "`memory attach")
			if i < 0 {
				t.Fatal("agentic-usage has no memory attach entry")
			}
			text = text[i:]
			if j := strings.Index(text, "\n- "); j > 0 {
				text = text[:j]
			}
		}
		flat := strings.Join(strings.Fields(text), " ")
		for _, banned := range []string{"free-standing", "not belong to an App yet", "App's organization", "as if you had created", "your memory in that App", "must be caller-owned", "already-scoped", "cross-org"} {
			if strings.Contains(flat, banned) {
				t.Errorf("%v still carries the retired bind wording %q:\n%s", args, banned, flat)
			}
		}
		for _, want := range []string{"any memory you can read", "system", "unchanged", "more than one App", "read-only", "stop reading", "owner is the App's owner", "memory detach"} {
			if !strings.Contains(flat, want) {
				t.Errorf("%v missing %q:\n%s", args, want, flat)
			}
		}
	}
}

// A class other than personal/private is sent with no client-side refusal; the
// output shows the memory's own URN, unchanged; --agent is optional and omitted
// from the request when not given, sent as given when it is.
func TestMemoryAttachSendsAnyClassAndAgentIsOptional3185(t *testing.T) {
	for name, c := range map[string]struct {
		extra     []string
		wantAgent any
	}{
		"no agent":   {nil, nil},
		"with agent": {[]string{"--agent", "agent1"}, "agent1"},
	} {
		t.Run(name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{"AttachMemoryToApp": `{"data":{"attachMemoryToApp":` + attachedKnowledge3185 + `}}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(attachArgs3185(c.extra...), "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatalf("a knowledge memory must be sent, not refused client-side: %v", err)
			}
			var vars map[string]any
			_ = json.Unmarshal(captured["AttachMemoryToApp"], &vars)
			if got, sent := vars["agentRef"]; (c.wantAgent == nil && sent) || (c.wantAgent != nil && got != c.wantAgent) {
				t.Errorf("agentRef = %v (sent=%v), want %v", got, sent, c.wantAgent)
			}
			text := out.String()
			if !strings.Contains(text, "hrn:mem:acme.com:kb") || strings.Contains(text, " via ") {
				t.Errorf("text must show the memory's own URN and no agent hop:\n%s", text)
			}
		})
	}
}

// The A3 refusal table: each code maps to its stable exit, the server's message
// reaches the output, and the --json error carries the server code.
func TestMemoryAttachRefusalCodes3185(t *testing.T) {
	for name, c := range map[string]struct {
		code, message string
		ext           string
		exit          int
	}{
		"no user":         {"UNAUTHENTICATED", "Authentication required", "", exitcode.AuthRequired},
		"key, run, etc":   {"FORBIDDEN", "Forbidden", "", exitcode.Forbidden},
		"not readable":    {"MEMORY_NOT_FOUND", "Memory not found", "", exitcode.NotFound},
		"system memory":   {"UNSUPPORTED_MEMORY_CLASS", "A system memory cannot be attached to an App.", `,"memoryClass":"system"`, exitcode.Usage},
		"App uninstalled": {"APP_UNINSTALLED", "The App is uninstalled.", "", exitcode.Conflict},
	} {
		t.Run(name, func(t *testing.T) {
			reply := `{"data":null,"errors":[{"message":"` + c.message + `","path":["attachMemoryToApp"],"extensions":{"code":"` + c.code + `"` + c.ext + `}}]}`
			gql, _ := captureGraphQL(t, map[string]string{"AttachMemoryToApp": reply})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(attachArgs3185("--json"), "--server", gql.URL))
			err := root.Execute()
			if err == nil {
				t.Fatal("a refusal must fail")
			}
			if got := renderError(f, err); got != c.exit {
				t.Errorf("exit %d, want %d", got, c.exit)
			}
			var env struct {
				Error struct {
					Message    string `json:"message"`
					ServerCode string `json:"serverCode"`
				} `json:"error"`
			}
			if e := json.Unmarshal([]byte(out.String()), &env); e != nil {
				t.Fatalf("%v\n%s", e, out.String())
			}
			if !strings.Contains(env.Error.Message, c.message) {
				t.Errorf("the server's message must reach the output: %s", out.String())
			}
			if env.Error.ServerCode != c.code {
				t.Errorf("serverCode = %q, want %s: %s", env.Error.ServerCode, c.code, out.String())
			}
		})
	}
}

// A server that predates the link (agentRef still required) answers a missing
// argument with a validation error: it reaches the schema-skew mapping, not a raw
// error. The narrow bind keeps working when --agent is given.
func TestMemoryAttachOlderServerSkew3185(t *testing.T) {
	reply := `{"data":null,"errors":[{"message":"Field \"attachMemoryToApp\" argument \"agentRef\" of type \"ID!\" is required, but it was not provided.","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	gql, _ := captureGraphQL(t, map[string]string{"AttachMemoryToApp": reply})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(attachArgs3185(), "--server", gql.URL))
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "out of sync") || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("want the schema-skew line (exit 2), got %v", err)
	}
	if !strings.Contains(err.Error(), "pass --agent") {
		t.Errorf("without --agent the skew line must say to pass it: %v", err)
	}
	// With --agent given the plain skew line stands: the hint would be noise.
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs(append(attachArgs3185("--agent", "agent1"), "--server", gql.URL))
	if err2 := root2.Execute(); err2 == nil || strings.Contains(err2.Error(), "pass --agent") {
		t.Errorf("no hint when --agent was given: %v", err2)
	}
}

// No client-side rule decides what may be attached: the memory ref is sent as
// canonicalised and nothing else is read first.
func TestMemoryAttachDecidesNothingLocally3185(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"AttachMemoryToApp": `{"data":{"attachMemoryToApp":` + attachedKnowledge3185 + `}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(attachArgs3185(), "--server", gql.URL))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 {
		t.Errorf("attach must make exactly one call, made %d: %v", len(captured), captured)
	}
}
