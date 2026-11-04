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
	if err == nil || exitcode.FromError(err) != exitcode.Usage || calls != 0 {
		t.Errorf("--email must not exist (usage, no call): code=%d calls=%d err=%v", exitcode.FromError(err), calls, err)
	}
	_, calls, _, err = runAppUserCreate(t, appUserResp)
	if err == nil || exitcode.FromError(err) != exitcode.Usage || calls != 0 {
		t.Errorf("--external-id is required (usage, no call): code=%d calls=%d err=%v", exitcode.FromError(err), calls, err)
	}
}

func TestAppUserCreateTextAndJSONAndSameUserTwice(t *testing.T) {
	stdout, _, _, err := runAppUserCreate(t, appUserResp, "--external-id", "ext-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"usr9", "ext-ada"} {
		if !strings.Contains(stdout, w) {
			t.Errorf("text output missing %q: %s", w, stdout)
		}
	}
	if strings.Contains(stdout, "created") || strings.Contains(stdout, "already existed") || strings.Contains(stdout, "existing") {
		t.Errorf("the response does not say created-vs-found, so the output must not either: %s", stdout)
	}
	var ids []string
	for i := 0; i < 2; i++ {
		out, _, _, err := runAppUserCreate(t, appUserResp, "--external-id", "ext-1", "--json")
		if err != nil {
			t.Fatal(err)
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
	if exitcode.FromError(err) != exitcode.Forbidden {
		t.Errorf("exit = %d, want Forbidden (%d): %v", exitcode.FromError(err), exitcode.Forbidden, err)
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
