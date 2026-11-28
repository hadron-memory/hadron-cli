package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#3242 over hadron-server#5235 (merged afaa9da1, 2026-11-26): updateMemory with a `urn` on an
// App personal or private memory answers APP_MEMORY_SLUG_FIXED (extension memoryClass) and applies NOTHING in
// the call; the display name can still be changed in a call of its own. The exit class (2) and the passed-through
// message are cli#3280's; this pins what #3242 adds: the nothing-applied note, serverCode and memoryClass in the
// --json envelope, and a --slug usage line that no longer says "renames the memory".

const slugFixedServerText3242 = "A personal or private memory in an App keeps its slug. Its display name can be changed."

const slugFixedReply3242 = `{"errors":[{"message":"` + slugFixedServerText3242 + `","extensions":{"code":"APP_MEMORY_SLUG_FIXED","memoryClass":"personal"}}],"data":null}`

// runSlugFixed3242 runs `memory set` against a fake that refuses the update, and returns what the caller sees: the
// exit code, the rendered text (stdout+stderr) and the update's variables.
func runSlugFixed3242(t *testing.T, args ...string) (int, string, map[string]any) {
	t.Helper()
	return runSlugFixedWith3242(t, slugFixedReply3242, args...)
}

// runSlugFixedWith3242 is runSlugFixed3242 with the server's UpdateMemory reply given.
func runSlugFixedWith3242(t *testing.T, reply string, args ...string) (int, string, map[string]any) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{"GetMemory": appMemoryRead3280, "UpdateMemory": reply})
	f, out := testFactory(t)
	var stderr strings.Builder
	f.IOStreams.ErrOut = &stderr
	root := NewRootCmd(f)
	full := append(append([]string{"memory", "set"}, args...), "--server", gql.URL)
	root.SetArgs(full)
	err := root.Execute()
	if err == nil {
		t.Fatal("the refusal must fail the command")
	}
	code := renderFailure(f, full, err)
	var vars map[string]any
	if raw, ok := captured["UpdateMemory"]; ok {
		_ = json.Unmarshal(raw, &vars)
	}
	return code, out.String() + stderr.String(), vars
}

// The refusal says what the server's sentence does not: nothing in the call was applied (so a --name passed with
// --slug is not saved), and --name alone changes the display name. The server's own text still reaches the caller.
func TestSlugRefusalSaysNothingInTheCallWasApplied3242(t *testing.T) {
	code, seen, vars := runSlugFixed3242(t, "hrn:mem:acme.com:coach-ada-personal", "--slug", "other")
	if code != exitcode.Usage {
		t.Errorf("exit %d, want 2", code)
	}
	for _, want := range []string{slugFixedServerText3242, "Nothing in this call was applied", "--name"} {
		if !strings.Contains(seen, want) {
			t.Errorf("the refusal must say %q:\n%s", want, seen)
		}
	}
	if vars["urn"] != "other" {
		t.Errorf("the CLI predicts nothing by class: --slug is sent as typed, got %v", vars)
	}
}

// The mixed call: --name with --slug is refused whole, and the note says so, because the caller may have expected the
// name to land. The CLI does not split the call.
func TestSlugRefusalOnAMixedCallNamesTheUnsavedName3242(t *testing.T) {
	code, seen, vars := runSlugFixed3242(t, "hrn:mem:acme.com:coach-ada-personal", "--name", "Coach Ada", "--slug", "other")
	if code != exitcode.Usage {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(seen, "not saved") {
		t.Errorf("a --name passed with --slug is not saved, and the note must say so:\n%s", seen)
	}
	if vars["name"] != "Coach Ada" || vars["urn"] != "other" {
		t.Errorf("one call carrying both, as typed: %v", vars)
	}
}

// --json: the envelope carries the server's code and the memory class, additively beside the usual fields.
func TestSlugRefusalJSONCarriesServerCodeAndMemoryClass3242(t *testing.T) {
	code, seen, _ := runSlugFixed3242(t, "hrn:mem:acme.com:coach-ada-personal", "--slug", "other", "--json")
	if code != exitcode.Usage {
		t.Errorf("exit %d, want 2", code)
	}
	var env struct {
		Error struct {
			Code        int    `json:"code"`
			ServerCode  string `json:"serverCode"`
			MemoryClass string `json:"memoryClass"`
			Message     string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(seen)), &env); err != nil {
		t.Fatalf("not one JSON envelope: %v\n%s", err, seen)
	}
	if env.Error.ServerCode != "APP_MEMORY_SLUG_FIXED" || env.Error.MemoryClass != "personal" || env.Error.Code != exitcode.Usage {
		t.Errorf("serverCode and memoryClass must be carried: %+v\n%s", env.Error, seen)
	}
	if !strings.Contains(env.Error.Message, slugFixedServerText3242) {
		t.Errorf("the server's message stays in the envelope: %s", env.Error.Message)
	}
}

// A class outside the two the server names is not echoed (the field is a fixed set, never free server text), while the
// envelope still prints, with the server's code, at exit 2.
func TestSlugRefusalJSONDropsAnUnknownMemoryClass3242(t *testing.T) {
	reply := `{"errors":[{"message":"x","extensions":{"code":"APP_MEMORY_SLUG_FIXED","memoryClass":"\u001b[31mevil"}}],"data":null}`
	code, seen, _ := runSlugFixedWith3242(t, reply, "hrn:mem:acme.com:coach-ada-personal", "--slug", "other", "--json")
	if code != exitcode.Usage {
		t.Errorf("exit %d, want 2", code)
	}
	var env struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(seen)), &env); err != nil {
		t.Fatalf("not one JSON envelope: %v\n%s", err, seen)
	}
	if env.Error["serverCode"] != "APP_MEMORY_SLUG_FIXED" {
		t.Errorf("the envelope must still carry the server's code: %v", env.Error)
	}
	if _, has := env.Error["memoryClass"]; has {
		t.Errorf("an unknown class must not be echoed: %v", env.Error)
	}
	if strings.Contains(seen, "evil") {
		t.Errorf("the unknown class text leaked:\n%s", seen)
	}
}

// --name alone on the same memory still succeeds, and sends no urn; --slug on a call the server accepts behaves as
// before and is sent as typed (the fake accepts it here, as the server does for a knowledge or group memory). No class
// check lives in the CLI.
func TestNameAloneAndSlugOnOtherMemoriesBehaveAsBefore3242(t *testing.T) {
	vars, _, err := runMemorySet(t, "hrn:mem:acme.com:coach-ada-personal", "--name", "Coach Ada")
	if err != nil {
		t.Fatalf("--name alone must still succeed: %v", err)
	}
	if _, has := vars["urn"]; has || vars["name"] != "Coach Ada" {
		t.Errorf("--name alone sends the name and no urn: %v", vars)
	}
	vars, _, err = runMemorySet(t, "hrn:mem:acme.com:coach-ada-personal", "--slug", "renamed")
	if err != nil {
		t.Fatalf("a server that accepts --slug (a knowledge or group memory) must still succeed: %v", err)
	}
	if vars["urn"] != "renamed" {
		t.Errorf("--slug is sent as typed, the server decides: %v", vars)
	}
}

// The --slug usage line no longer promises a rename for every memory.
func TestMemorySetSlugUsageNamesTheAppMemoryExemption3242(t *testing.T) {
	h := flatHelp3208(t, "memory", "set", "--help")
	if strings.Contains(h, "on update, renames the memory") {
		t.Errorf("--slug still promises a rename on update:\n%s", h)
	}
	if !strings.Contains(h, "keeps its slug") {
		t.Errorf("the --slug usage must say a personal or private App memory keeps its slug:\n%s", h)
	}
}

// agentic-usage states the nothing-applied rule next to the exit-2 entry.
func TestAgenticUsageStatesTheSlugRefusalAppliesNothing3242(t *testing.T) {
	u := flatHelp3208(t, "agentic-usage")
	if !strings.Contains(u, "APP_MEMORY_SLUG_FIXED") || !strings.Contains(u, "nothing in the call is applied") {
		t.Error("agentic-usage must say APP_MEMORY_SLUG_FIXED applies nothing in the call (cli#3242)")
	}
}

// Only APP_MEMORY_SLUG_FIXED gets the note, the code and the class: any other failure of the update (another server
// refusal, an unreachable server) is reported as before, so it never claims nothing was applied or carries this code.
func TestOtherUpdateFailuresGetNoSlugFixedNote3242(t *testing.T) {
	for name, reply := range map[string]string{
		"another refusal": `{"errors":[{"message":"not allowed","extensions":{"code":"FORBIDDEN","memoryClass":"personal"}}],"data":null}`,
		"a gateway fault": `{"errors":[{"message":"upstream","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`,
	} {
		code, seen, _ := runSlugFixedWith3242(t, reply, "hrn:mem:acme.com:coach-ada-personal", "--slug", "other", "--json")
		if strings.Contains(seen, "Nothing in this call was applied") || strings.Contains(seen, "APP_MEMORY_SLUG_FIXED") || strings.Contains(seen, `"memoryClass"`) {
			t.Errorf("%s: no slug-fixed note, code or class for another failure (exit %d):\n%s", name, code, seen)
		}
	}
}
