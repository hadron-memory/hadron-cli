package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1627: `app user create` over createAppUser (App key callers only).

const appUserResp = `{"data":{"createAppUser":{"id":"usr9","handle":"ext-ada","name":"Ada","urn":"hrn:user:ext-ada","externalId":"ext-1"}}}`

func runAppUserCreate(t *testing.T, resp string, args ...string) (stdout string, calls int, vars json.RawMessage, err error) {
	t.Helper()
	srv, count, varsOf := countingGraphQL(t, map[string]string{"CreateAppUser": resp})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"app", "user", "create", "--server", srv.URL}, args...))
	err = root.Execute()
	return out.String(), count("CreateAppUser"), varsOf("CreateAppUser"), err
}

func TestAppUserCreateSendsOnlyWhatWasGiven(t *testing.T) {
	_, calls, vars, err := runAppUserCreate(t, appUserResp, "--external-id", "ext-1")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("exactly one mutation, got %d", calls)
	}
	var m map[string]any
	_ = json.Unmarshal(vars, &m)
	if m["externalId"] != "ext-1" {
		t.Errorf("externalId: %v", m)
	}
	for _, k := range []string{"handle", "name", "email"} {
		if _, present := m[k]; present {
			t.Errorf("%q must be OMITTED (preserve), never sent: %s", k, vars)
		}
	}
	_, _, vars, err = runAppUserCreate(t, appUserResp, "--external-id", "ext-1", "--handle", "ext-ada", "--name", "Ada")
	if err != nil {
		t.Fatal(err)
	}
	m = map[string]any{}
	_ = json.Unmarshal(vars, &m)
	if m["handle"] != "ext-ada" || m["name"] != "Ada" {
		t.Errorf("flags must reach the wire: %s", vars)
	}
}

func TestAppUserCreateHasNoEmailFlagAndRequiresExternalID(t *testing.T) {
	_, calls, _, err := runAppUserCreate(t, appUserResp, "--external-id", "x", "--email", "a@b.c")
	if err == nil || exitCodeFor(err) != exitcode.Usage || calls != 0 {
		t.Errorf("--email must not exist (usage, no call): code=%d calls=%d err=%v", exitCodeFor(err), calls, err)
	}
	_, calls, _, err = runAppUserCreate(t, appUserResp)
	if err == nil || exitCodeFor(err) != exitcode.Usage || calls != 0 {
		t.Errorf("--external-id is required (usage, no call): code=%d calls=%d err=%v", exitCodeFor(err), calls, err)
	}
}

func TestAppUserCreateTextAndJSONShape(t *testing.T) {
	stdout, _, _, err := runAppUserCreate(t, appUserResp, "--external-id", "ext-1")
	if err != nil {
		t.Fatal(err)
	}
	// The whole line, so each value is pinned to its own column: the URN
	// contains the handle as a substring, so per-value Contains checks could
	// not tell a dropped or swapped handle from a correct one.
	if want := "user usr9  handle ext-ada  name Ada  urn hrn:user:ext-ada  external id ext-1\n"; stdout != want {
		t.Errorf("text output:\n got %q\nwant %q", stdout, want)
	}
	if strings.Contains(stdout, "created") || strings.Contains(stdout, "already existed") || strings.Contains(stdout, "existing") {
		t.Errorf("the response does not say created-vs-found, so the output must not either: %s", stdout)
	}
	// The same-externalId-same-user guarantee is the server's (the identity
	// key); against a fixed mock this loop pins only that the CLI sends the id
	// unchanged and prints what comes back, twice.
	var ids []string
	for i := 0; i < 2; i++ {
		out, _, vars, err := runAppUserCreate(t, appUserResp, "--external-id", "ext-1", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var sent map[string]any
		if e := json.Unmarshal(vars, &sent); e != nil || sent["externalId"] != "ext-1" {
			t.Errorf("run %d must send the external id unchanged: %s", i, vars)
		}
		var got map[string]any
		if e := json.Unmarshal([]byte(out), &got); e != nil {
			t.Fatalf("json: %v: %s", e, out)
		}
		for _, k := range []string{"id", "handle", "name", "urn", "externalId"} {
			if _, ok := got[k]; !ok {
				t.Errorf("json key %q missing: %s", k, out)
			}
		}
		if _, ok := got["created"]; ok {
			t.Errorf("no fabricated created flag: %s", out)
		}
		ids = append(ids, got["id"].(string))
	}
	if ids[0] != ids[1] {
		t.Errorf("same externalId returns the same user id: %v", ids)
	}
}

func TestAppUserCreateNullHandleAndNameStayNull(t *testing.T) {
	out, _, _, err := runAppUserCreate(t, `{"data":{"createAppUser":{"id":"u1","handle":null,"name":null,"urn":null,"externalId":"e"}}}`, "--external-id", "e", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	for _, k := range []string{"handle", "name", "urn"} {
		if v, ok := got[k]; !ok || v != nil {
			t.Errorf("%s must be present and null: %s", k, out)
		}
	}
}

// A person token (or any caller the server refuses) comes back as the server's
// typed error: nonzero exit, the server's code, no pre-judging, one call.
func TestAppUserCreatePassesServerRefusalThrough(t *testing.T) {
	refusal := memberGQLError("createAppUser is for App Key callers only", "FORBIDDEN")
	stdout, calls, _, err := runAppUserCreate(t, refusal, "--external-id", "ext-1")
	if err == nil {
		t.Fatal("a refusal must be an error")
	}
	if exitCodeFor(err) != exitcode.Forbidden {
		t.Errorf("exit = %d, want Forbidden (%d): %v", exitCodeFor(err), exitcode.Forbidden, err)
	}
	if calls != 1 {
		t.Errorf("one call, never retried: %d", calls)
	}
	if !strings.Contains(err.Error(), "App Key callers only") {
		t.Errorf("server message must surface: %v", err)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("nothing on stdout on refusal: %s", stdout)
	}
}

// Sentinel: the credential never appears in stdout or stderr, on success,
// on refusal, or in the --json error envelope.
func TestAppUserCreateNeverPrintsTheCredential(t *testing.T) {
	const sentinel = "hdr_app_SENTINEL_1627_do_not_print"
	for _, tc := range []struct {
		name, resp string
		args       []string
	}{
		{"success text", appUserResp, nil},
		{"success json", appUserResp, []string{"--json"}},
		{"refusal text", memberGQLError("refused", "FORBIDDEN"), nil},
		{"refusal json", memberGQLError("refused", "FORBIDDEN"), []string{"--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, _ := countingGraphQL(t, map[string]string{"CreateAppUser": tc.resp})
			f, out, errOut := testFactoryTTY(t, "")
			t.Setenv("HADRON_TOKEN", sentinel)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"app", "user", "create", "--server", srv.URL, "--external-id", "e"}, tc.args...))
			err := root.Execute()
			all := out.String() + errOut.String()
			if err != nil {
				all += err.Error()
			}
			if strings.Contains(all, sentinel) {
				t.Errorf("credential leaked: %s", all)
			}
		})
	}
}

// An answer without a user is a failure, never a silent success with empty output.
func TestAppUserCreateEmptyAnswerIsAnError(t *testing.T) {
	stdout, _, _, err := runAppUserCreate(t, `{"data":{"createAppUser":null}}`, "--external-id", "e")
	if err == nil || exitCodeFor(err) != exitcode.Unavailable {
		t.Errorf("a missing user must exit Unavailable (%d): code=%d %v", exitcode.Unavailable, exitCodeFor(err), err)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("nothing on stdout: %s", stdout)
	}
}

func TestAppUserCreateRefusesWhitespaceIdAndEmptyOptionals(t *testing.T) {
	for _, args := range [][]string{
		{"--external-id", " ext-1"}, {"--external-id", "ext-1 "},
		{"--external-id", "e", "--handle", ""}, {"--external-id", "e", "--name", ""},
	} {
		_, calls, _, err := runAppUserCreate(t, appUserResp, args...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || calls != 0 {
			t.Errorf("%v: want usage error and zero calls, got code=%d calls=%d err=%v", args, exitCodeFor(err), calls, err)
		}
	}
}

// Text mode shows a field the server does not return as "-", in its own column.
func TestAppUserCreateTextShowsDashForAbsentFields(t *testing.T) {
	out, _, _, err := runAppUserCreate(t, `{"data":{"createAppUser":{"id":"u1","handle":null,"name":null,"urn":null,"externalId":null}}}`, "--external-id", "e")
	if err != nil {
		t.Fatal(err)
	}
	if want := "user u1  handle -  name -  urn -  external id -\n"; out != want {
		t.Errorf("text output:\n got %q\nwant %q", out, want)
	}
}
