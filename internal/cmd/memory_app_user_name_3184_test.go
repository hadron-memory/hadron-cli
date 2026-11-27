package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3184 (C1 of the App user memories slice, server#5183): `memory set --app --agent
// --class personal|private` does not need --name. The server derives the slug and the
// display name and ignores a name for these classes; one per class per App is the
// server's rule. The CLI forwards what it was given, omits what it was not, and maps
// the server's refusals. Pins are at command level against a capturing fake.
const derivedMemory3184 = `{"id":"m9","urn":"hrn:mem:acme.com:coach-ada-personal","name":"coach-ada-personal","shortDescription":null,
	"class":"personal","visibility":null,"organizationId":"o1","isEncrypted":false,"maxRevCount":10,"updatedAt":"2026-11-26T00:00:00Z"}`

func runSet3184(t *testing.T, responses map[string]string, extra ...string) (vars map[string]json.RawMessage, called bool, out, errOut string, code int) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, o := testFactory(t)
	var stderr strings.Builder
	f.IOStreams.ErrOut = &stderr
	root := NewRootCmd(f)
	args := append([]string{"memory", "set", "--app", "hrn:app:acme.com:coach", "--agent", "hrn:agent:acme.com:a"}, extra...)
	args = append(args, "--server", gql.URL)
	root.SetArgs(args)
	err := root.Execute()
	if err != nil {
		code = renderFailure(f, args, err)
	}
	raw, called := captured["CreateMemoryInApp"]
	if called {
		_ = json.Unmarshal(raw, &vars)
	}
	return vars, called, o.String(), stderr.String(), code
}

func TestAppUserMemoryNeedsNoName3184(t *testing.T) {
	for _, class := range []string{"personal", "private"} {
		t.Run(class, func(t *testing.T) {
			resp := map[string]string{"CreateMemoryInApp": `{"data":{"createMemoryInApp":` + strings.Replace(derivedMemory3184, `"class":"personal"`, `"class":"`+class+`"`, 1) + `}}`}
			vars, called, out, errOut, code := runSet3184(t, resp, "--class", class, "--json")
			if code != 0 || !called {
				t.Fatalf("a %s create without --name must be sent and succeed: exit %d, sent %v\n%s%s", class, code, called, out, errOut)
			}
			if _, sent := vars["name"]; sent {
				t.Errorf("no --name given: the name variable must be omitted, not sent: %v", vars)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatalf("invalid JSON: %v\n%s", err, out)
			}
			if dto["urn"] != "hrn:mem:acme.com:coach-ada-personal" || dto["name"] != "coach-ada-personal" {
				t.Errorf("--json must carry the server's derived urn and name: %v", dto)
			}
			_, _, text, _, tcode := runSet3184(t, resp, "--class", class)
			if tcode != 0 || !strings.Contains(text, "coach-ada-personal") {
				t.Errorf("text output must show the derived name and urn (exit %d):\n%s", tcode, text)
			}
		})
	}
}

// --name is still accepted and sent for these classes, so an older server still uses
// it; the output shows the name the server answered with, not the one typed.
func TestAppUserMemoryNameIsStillSentWhenGiven3184(t *testing.T) {
	resp := map[string]string{"CreateMemoryInApp": `{"data":{"createMemoryInApp":` + derivedMemory3184 + `}}`}
	vars, called, out, _, code := runSet3184(t, resp, "--class", "personal", "--name", "My notes")
	if code != 0 || !called {
		t.Fatalf("exit %d, sent %v", code, called)
	}
	if string(vars["name"]) != `"My notes"` {
		t.Errorf("--name must still be sent: %v", vars)
	}
	if !strings.Contains(out, "coach-ada-personal") || strings.Contains(out, "My notes") {
		t.Errorf("the output shows the server's name, not the typed one:\n%s", out)
	}
}

// A class that names things still needs a name: the usage refusal is local and sends nothing.
func TestAppClassAgentCreateStillNeedsAName3184(t *testing.T) {
	_, called, _, errOut, code := runSet3184(t, map[string]string{}, "--class", "app")
	if code != exitcode.Usage || called {
		t.Errorf("--class app --agent without --name: exit %d (want 2), sent %v\n%s", code, called, errOut)
	}
}

// The server's refusals reach the caller with the server's own message and code. --name is
// given so this isolates the mapping from the name rule (a current server ignores it).
func TestAppUserMemoryRefusalsMapToStableExits3184(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		exit       int
	}{
		{"already holds one", "APP_USER_MEMORY_ALREADY_EXISTS", exitcode.Conflict},
		{"retry exhausted, no URN", "MEMORY_URN_CONFLICT", exitcode.Conflict},
		{"not a member", "FORBIDDEN", exitcode.Forbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := `{"errors":[{"message":"the server says no","extensions":{"code":"` + tc.code + `","memoryClass":"personal"}}],"data":null}`
			_, called, out, errOut, code := runSet3184(t, map[string]string{"CreateMemoryInApp": reply}, "--class", "personal", "--name", "x", "--json")
			if !called || code != tc.exit {
				t.Fatalf("%s: exit %d, want %d (sent %v)\n%s%s", tc.code, code, tc.exit, called, out, errOut)
			}
			all := out + errOut
			if !strings.Contains(all, "the server says no") {
				t.Errorf("the server's message must reach the caller:\n%s", all)
			}
			if !strings.Contains(all, `"serverCode": "`+tc.code+`"`) && !strings.Contains(all, `"serverCode":"`+tc.code+`"`) {
				t.Errorf("--json errors must carry serverCode %s:\n%s", tc.code, all)
			}
		})
	}
}

// An older server (name still required) refuses the document; the CLI shows the existing
// version-skew answer and never invents a name.
func TestAppUserMemoryOmittedNameAgainstAnOlderServer3184(t *testing.T) {
	reply := `{"errors":[{"message":"Field \"createMemoryInApp\" argument \"name\" of type \"String!\" is required, but it was not provided.","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}],"data":null}`
	_, called, out, errOut, code := runSet3184(t, map[string]string{"CreateMemoryInApp": reply}, "--class", "personal")
	if !called || code != exitcode.Usage {
		t.Fatalf("exit %d, want 2 through the schema-skew mapping (sent %v)\n%s%s", code, called, out, errOut)
	}
	all := out + errOut
	for _, want := range []string{"the CLI and server schema versions are out of sync", "Update whichever is behind", "`hadron version` shows the CLI build", `argument "name"`} {
		if !strings.Contains(all, want) {
			t.Errorf("the skew advice (cli#3280) must say %q:\n%s", want, all)
		}
	}
}

// cli#3280 item 2: the create sends what it was given and nothing it was not.

// --class app with --name still sends the name variable (the optional(name) change touched this path too).
func TestAppClassCreateWithNameStillSendsTheName3280(t *testing.T) {
	resp := map[string]string{"CreateMemoryInApp": `{"data":{"createMemoryInApp":` + derivedMemory3184 + `}}`}
	vars, called, _, _, code := runSet3184(t, resp, "--class", "app", "--name", "Runbook")
	if code != 0 || !called {
		t.Fatalf("exit %d, sent %v", code, called)
	}
	if string(vars["name"]) != `"Runbook"` || string(vars["memoryClass"]) != `"app"` {
		t.Errorf("--class app sends its name and class: %v", vars)
	}
}

// memoryClass travels for the classes the server derives names for, as typed.
func TestMemoryClassIsForwardedForPersonalAndPrivate3280(t *testing.T) {
	resp := map[string]string{"CreateMemoryInApp": `{"data":{"createMemoryInApp":` + derivedMemory3184 + `}}`}
	for _, class := range []string{"personal", "private"} {
		vars, called, _, _, code := runSet3184(t, resp, "--class", class)
		if code != 0 || !called || string(vars["memoryClass"]) != `"`+class+`"` {
			t.Errorf("%s: exit %d, sent %v, memoryClass %s", class, code, called, vars["memoryClass"])
		}
	}
}

// --slug is refused for an App-scoped create whatever the class, before any request: the server names it.
func TestSlugIsRefusedForAnAppScopedCreate3280(t *testing.T) {
	for _, class := range []string{"app", "personal", "private"} {
		args := []string{"--class", class, "--slug", "mine"}
		if class == "app" {
			args = append(args, "--name", "Runbook")
		}
		_, called, out, errOut, code := runSet3184(t, map[string]string{}, args...)
		if code != exitcode.Usage || called || !strings.Contains(out+errOut, "--slug cannot be used with App-scoped creation") {
			t.Errorf("%s: exit %d (want 2), sent %v:\n%s%s", class, code, called, out, errOut)
		}
	}
}
