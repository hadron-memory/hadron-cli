package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1058: `app update` — name, description, systemPrompt, sessionTimeoutSeconds
// only. The load-bearing property is the WIRE: omitted fields must be absent
// from the variables, a clear must be an explicit null, and "" must stay "".

const appUpdateOK1058 = `{"data":{"updateApp":{"id":"capp1","urn":"hrn:app:acme.com:support","name":"Support","sessionTimeoutSeconds":3600}}}`

const appRef1058 = "hrn:app:acme.com:support"

// runAppUpdate1058 runs `app update <ref> <args…>` with --yes unless the
// caller asks otherwise, and returns the captured UpdateApp variables as raw
// JSON values keyed by variable name (nil map = the mutation was never sent).
func runAppUpdate1058(t *testing.T, args ...string) (map[string]json.RawMessage, string, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{"UpdateApp": appUpdateOK1058})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"app", "update", appRef1058}, args...), "--server", gql.URL))
	err := root.Execute()
	var vars map[string]json.RawMessage
	if raw, ok := captured["UpdateApp"]; ok {
		if e := json.Unmarshal(raw, &vars); e != nil {
			t.Fatal(e)
		}
	}
	return vars, out.String(), err
}

func keys1058(m map[string]json.RawMessage) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

func TestAppUpdateSendsOnlyThePassedFields(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantKeys string
	}{
		{"name only", []string{"--name", "Desk", "--yes"}, "name,ref"},
		{"description only", []string{"--description", "d", "--yes"}, "description,ref"},
		{"system prompt only", []string{"--system-prompt", "p", "--yes"}, "ref,systemPrompt"},
		{"timeout only", []string{"--session-timeout-seconds", "900", "--yes"}, "ref,sessionTimeoutSeconds"},
		{"all four", []string{"--name", "n", "--description", "d", "--system-prompt", "p", "--session-timeout-seconds", "60", "--yes"}, "description,name,ref,sessionTimeoutSeconds,systemPrompt"},
		{"two", []string{"--name", "n", "--session-timeout-seconds", "60", "--yes"}, "name,ref,sessionTimeoutSeconds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vars, _, err := runAppUpdate1058(t, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if got := keys1058(vars); got != tc.wantKeys {
				t.Errorf("variable keys = %q, want %q (an omitted field must be absent, never null)", got, tc.wantKeys)
			}
			if string(vars["ref"]) != `"`+appRef1058+`"` {
				t.Errorf("ref = %s", vars["ref"])
			}
		})
	}
}

func TestAppUpdateSeparatesEmptyStringFromClear(t *testing.T) {
	vars, _, err := runAppUpdate1058(t, "--description", "", "--system-prompt", "", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if string(vars["description"]) != `""` || string(vars["systemPrompt"]) != `""` {
		t.Errorf("empty string must be sent as \"\": description=%s systemPrompt=%s", vars["description"], vars["systemPrompt"])
	}

	vars, _, err = runAppUpdate1058(t, "--clear-description", "--clear-system-prompt", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if string(vars["description"]) != "null" || string(vars["systemPrompt"]) != "null" {
		t.Errorf("clear must be an explicit null: description=%s systemPrompt=%s", vars["description"], vars["systemPrompt"])
	}
	if _, ok := vars["name"]; ok {
		t.Errorf("name must stay absent on a clear: %v", keys1058(vars))
	}
}

func TestAppUpdateTimeoutIsPassedThroughUnclamped(t *testing.T) {
	vars, _, err := runAppUpdate1058(t, "--session-timeout-seconds", "0", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if string(vars["sessionTimeoutSeconds"]) != "0" {
		t.Errorf("an explicit 0 must reach the server for it to judge, got %s", vars["sessionTimeoutSeconds"])
	}
	vars, _, err = runAppUpdate1058(t, "--name", "n", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := vars["sessionTimeoutSeconds"]; ok {
		t.Error("an unpassed timeout must not get a default")
	}
}

func TestAppUpdateSystemPromptFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "prompt.md")
	body := "line one\n\"quoted\" line two\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	vars, _, err := runAppUpdate1058(t, "--system-prompt-file", p, "--yes")
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := json.Unmarshal(vars["systemPrompt"], &got); err != nil || got != body {
		t.Errorf("systemPrompt = %q (%v), want %q", got, err, body)
	}
	if _, _, err := runAppUpdate1058(t, "--system-prompt-file", filepath.Join(t.TempDir(), "missing"), "--yes"); exitCodeFor(err) != exitcode.Usage {
		t.Errorf("a missing file is a usage error, got %v", err)
	}
}

func TestAppUpdateRefusesWithoutFlagsAndOnConflicts(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p.md")
	_ = os.WriteFile(p, []byte("x"), 0o600)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no flags", []string{"--yes"}},
		{"only --yes and --json", []string{"--yes", "--json"}},
		{"description + clear", []string{"--description", "d", "--clear-description", "--yes"}},
		{"prompt + clear", []string{"--system-prompt", "p", "--clear-system-prompt", "--yes"}},
		{"prompt file + clear", []string{"--system-prompt-file", p, "--clear-system-prompt", "--yes"}},
		{"prompt + prompt file", []string{"--system-prompt", "p", "--system-prompt-file", p, "--yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vars, _, err := runAppUpdate1058(t, tc.args...)
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("want Usage, got %v", err)
			}
			if vars != nil {
				t.Errorf("a refusal must send no mutation, sent %v", vars)
			}
		})
	}
}

func TestAppUpdateConfirmation(t *testing.T) {
	const secret = "TOP-SECRET-PROMPT-TEXT"
	// Non-interactive without --yes: refused, nothing sent.
	vars, _, err := runAppUpdate1058(t, "--system-prompt", secret)
	if exitCodeFor(err) != exitcode.Usage || vars != nil {
		t.Fatalf("non-interactive without --yes must refuse unsent: err=%v vars=%v", err, vars)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("refusal echoes the system prompt: %v", err)
	}

	// TTY: "n" cancels with nothing sent; the prompt names the target and field NAMES only.
	for answer, wantSent := range map[string]bool{"n\n": false, "y\n": true} {
		gql, captured := captureGraphQL(t, map[string]string{"UpdateApp": appUpdateOK1058})
		f, _, errOut := testFactoryTTY(t, answer)
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "update", appRef1058, "--name", "Desk", "--system-prompt", secret, "--server", gql.URL})
		err := root.Execute()
		_, sent := captured["UpdateApp"]
		if sent != wantSent {
			t.Errorf("answer %q: sent=%v want %v (err %v)", answer, sent, wantSent, err)
		}
		if !wantSent && exitCodeFor(err) != exitcode.Cancelled {
			t.Errorf("a declined confirmation exits Cancelled, got %v", err)
		}
		prompt := errOut.String()
		for _, want := range []string{appRef1058, "name", "systemPrompt"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("confirmation lacks %q: %q", want, prompt)
			}
		}
		if strings.Contains(prompt, secret) || strings.Contains(prompt, "Desk") {
			t.Errorf("confirmation must name fields, not values: %q", prompt)
		}
	}
}

func TestAppUpdateReceipts(t *testing.T) {
	const secret = "TOP-SECRET-PROMPT-TEXT"
	_, text, err := runAppUpdate1058(t, "--system-prompt", secret, "--clear-description", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, appRef1058) || !strings.Contains(text, "systemPrompt") || !strings.Contains(text, "description (cleared)") {
		t.Errorf("text receipt: %q", text)
	}
	if strings.Contains(text, secret) {
		t.Errorf("text receipt echoes the system prompt: %q", text)
	}

	_, js, err := runAppUpdate1058(t, "--system-prompt", secret, "--name", "Support", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		App     string   `json:"app"`
		ID      string   `json:"id"`
		Status  string   `json:"status"`
		Changed []string `json:"changed"`
	}
	if err := json.Unmarshal([]byte(js), &dto); err != nil {
		t.Fatalf("%v in %s", err, js)
	}
	if dto.App != appRef1058 || dto.ID != "capp1" || dto.Status != "updated" || strings.Join(dto.Changed, ",") != "name,systemPrompt" {
		t.Errorf("json receipt: %+v", dto)
	}
	if strings.Contains(js, secret) {
		t.Errorf("json receipt echoes the system prompt: %s", js)
	}
}

func TestAppUpdatePassesServerRefusalThrough(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{"UpdateApp": `{"errors":[{"message":"not allowed to update this App","extensions":{"code":"FORBIDDEN"}}],"data":null}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "update", appRef1058, "--name", "x", "--yes", "--server", gql.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Forbidden {
		t.Fatalf("want Forbidden, got %v", err)
	}
	if !strings.Contains(err.Error(), "not allowed to update this App") {
		t.Errorf("the server's sentence must pass through: %v", err)
	}
}

// A request that never gets an answer leaves the outcome unknown: one attempt,
// no retry, no success claim.
func TestAppUpdateLostResponseIsOutcomeUnknownAndNotRetried(t *testing.T) {
	var posts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		atomic.AddInt32(&posts, 1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close() // the connection drops after the request arrived
		}
	}))
	t.Cleanup(srv.Close)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "update", appRef1058, "--name", "x", "--yes", "--server", srv.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Unavailable {
		t.Fatalf("want Unavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "outcome unknown") {
		t.Errorf("must say the outcome is unknown: %v", err)
	}
	if n := atomic.LoadInt32(&posts); n != 1 {
		t.Errorf("a write must be sent exactly once, sent %d", n)
	}
	if strings.Contains(out.String(), "Updated") {
		t.Errorf("no success claim on a lost response: %q", out.String())
	}
}
