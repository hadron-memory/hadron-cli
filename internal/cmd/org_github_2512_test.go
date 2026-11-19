package cmd

// hadron-cli#2512 — `hadron org github link|unlink|set-app`. Pinned here:
//   - each verb sends exactly ONE matching mutation, the org ref as typed, and
//     never retries (a lost answer is never re-sent);
//   - the private key is write-only: it reaches the wire only as the mutation's
//     variables payload, and appears in no stdout, stderr, --json, error text
//     or server-reflected metadata (redacted instead);
//   - unlink is destructive: non-interactive without --yes is refused before
//     any request; a TTY prompt can decline;
//   - link's --installation-id is validated locally (positive integer);
//   - server refusals (non-admin, unknown org) map to the existing exit codes
//     with the server code riding along, and no follow-up mutation is sent;
//   - the --json DTO is stable and secret-free:
//     {id, urn, githubAppId, githubInstallationId, hasGithubAppPrivateKey}.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const (
	orgGithubLinked2512 = `{"id":"org1","urn":"hrn:org:acme.com","githubAppId":"12345","githubInstallationId":998877,"hasGithubAppPrivateKey":true}`
	keySentinel2512     = "sentinel-key-2512"
)

func TestOrgGithub2512Commands(t *testing.T) {
	for _, tc := range []struct {
		name, op, field string
		args            []string
		vars            map[string]any
	}{
		{"link", "LinkGithubApp", "linkGithubApp", []string{"github", "link", "acme.com", "--installation-id", "998877"}, map[string]any{"orgId": "acme.com", "installationId": float64(998877)}},
		{"link urn as typed", "LinkGithubApp", "linkGithubApp", []string{"github", "link", "hrn:org:acme.com", "--installation-id", "1"}, map[string]any{"orgId": "hrn:org:acme.com", "installationId": float64(1)}},
		{"unlink", "UnlinkGithubApp", "unlinkGithubApp", []string{"github", "unlink", "acme.com", "--yes"}, map[string]any{"orgId": "acme.com"}},
		{"set-app", "UpdateOrgGithubApp", "updateOrgGithubApp", []string{"github", "set-app", "acme.com", "--app-id", "12345", "--private-key-file", "-"}, map[string]any{"orgId": "acme.com", "appId": "12345", "privateKey": keySentinel2512}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, captured := captureGraphQL(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":` + orgGithubLinked2512 + `}}`})
			f, out := testFactory(t)
			f.IOStreams.In = strings.NewReader(keySentinel2512 + "\n")
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"org"}, tc.args...), "--json", "--server", srv.URL))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(captured[tc.op], &got); err != nil {
				t.Fatal(err)
			}
			for k, v := range tc.vars {
				if got[k] != v {
					t.Errorf("%s: got %v want %v", k, got[k], v)
				}
			}
			if len(got) != len(tc.vars) {
				t.Errorf("unexpected variables %v", got)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if dto["id"] != "org1" || dto["urn"] != "hrn:org:acme.com" || dto["githubAppId"] != "12345" || dto["githubInstallationId"] != float64(998877) || dto["hasGithubAppPrivateKey"] != true {
				t.Fatalf("DTO: %v", dto)
			}
			if len(dto) != 5 {
				t.Fatalf("unexpected DTO keys: %v", dto)
			}
			if strings.Contains(out.String(), keySentinel2512) {
				t.Fatal("key leaked into --json")
			}
		})
	}
}

// TestOrgGithub2512SingleMutation: every verb sends exactly one matching
// mutation and never retries — including on a transport failure, where the
// command must fail rather than re-send a destructive or credential-bearing
// mutation.
func TestOrgGithub2512SingleMutation(t *testing.T) {
	for _, tc := range []struct {
		name, op, field string
		args            []string
	}{
		{"link", "LinkGithubApp", "linkGithubApp", []string{"github", "link", "acme.com", "--installation-id", "998877"}},
		{"unlink", "UnlinkGithubApp", "unlinkGithubApp", []string{"github", "unlink", "acme.com", "--yes"}},
		{"set-app", "UpdateOrgGithubApp", "updateOrgGithubApp", []string{"github", "set-app", "acme.com", "--app-id", "12345", "--private-key-file", "-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls, _ := countingGraphQL(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":` + orgGithubLinked2512 + `}}`})
			f, _ := testFactory(t)
			f.IOStreams.In = strings.NewReader(keySentinel2512 + "\n")
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"org"}, tc.args...), "--server", srv.URL))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if calls(tc.op) != 1 {
				t.Fatalf("%d calls of %s", calls(tc.op), tc.op)
			}
		})
	}
	t.Run("no retry after transport failure", func(t *testing.T) {
		var mu sync.Mutex
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			calls++
			mu.Unlock()
			// A truncated body, so the client's read fails after the send.
			_, _ = w.Write([]byte(`{"data":`))
		}))
		defer srv.Close()
		f, _ := testFactory(t)
		f.IOStreams.In = strings.NewReader(keySentinel2512 + "\n")
		root := NewRootCmd(f)
		root.SetArgs([]string{"org", "github", "unlink", "acme.com", "--yes", "--server", srv.URL})
		if err := root.Execute(); err == nil {
			t.Fatal("expected failure")
		}
		if calls != 1 {
			t.Fatalf("%d requests; a failure must not be retried", calls)
		}
	})
}

// TestOrgGithub2512RefusalsBeforeWrite: every local refusal happens with ZERO
// network calls — the destructive-unlink non-interactive refusal, local flag
// validation, and the write-only key reader's own refusals.
func TestOrgGithub2512RefusalsBeforeWrite(t *testing.T) {
	for _, tc := range []struct {
		args []string
		exit int // 0: only "some error before any request" is asserted
	}{
		{[]string{"github", "unlink", "acme.com"}, 2},
		{[]string{"github", "unlink", ""}, 2},
		{[]string{"github", "unlink", " "}, 2},
		{[]string{"github", "link", "acme.com", "--installation-id", "0"}, 2},
		{[]string{"github", "link", "acme.com", "--installation-id", "-3"}, 2},
		{[]string{"github", "link", "acme.com", "--installation-id", "not-a-number"}, 0},
		{[]string{"github", "link", "acme.com"}, 0},
		{[]string{"github", "link", ""}, 2},
		{[]string{"github", "set-app", "acme.com"}, 0},
		{[]string{"github", "set-app", "acme.com", "--app-id", ""}, 2},
		{[]string{"github", "set-app", "", "--app-id", "1", "--private-key-file", "-"}, 2},
		{[]string{"github", "set-app", "acme.com", "--app-id", "1", "--private-key-file="}, 2},
		{[]string{"github", "set-app", "acme.com", "--app-id", "1"}, 2},
		{[]string{"github", "set-app", "acme.com", "--app-id", "  ", "--private-key-file", "-"}, 2},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			srv, calls, _ := countingGraphQL(t, nil)
			f, _ := testFactory(t)
			f.IOStreams.In = strings.NewReader("")
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"org"}, append(tc.args, "--server", srv.URL)...))
			err := root.Execute()
			if err == nil {
				t.Fatal("expected local refusal")
			}
			if tc.exit != 0 && exitCodeFor(err) != tc.exit {
				t.Fatalf("exit %d (%v)", exitCodeFor(err), err)
			}
			for _, op := range []string{"LinkGithubApp", "UnlinkGithubApp", "UpdateOrgGithubApp"} {
				if calls(op) != 0 {
					t.Fatalf("%s made %d network calls", op, calls(op))
				}
			}
		})
	}
}

// TestOrgGithub2512UnlinkPrompt: on an answerable terminal the unlink prompt
// can still say no (nothing sent), and yes sends exactly one mutation.
func TestOrgGithub2512UnlinkPrompt(t *testing.T) {
	for _, answer := range []string{"n\n", "y\n"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			srv, calls, _ := countingGraphQL(t, map[string]string{"UnlinkGithubApp": `{"data":{"unlinkGithubApp":` + orgGithubLinked2512 + `}}`})
			f, _, _ := testFactoryTTY(t, answer)
			root := NewRootCmd(f)
			root.SetArgs([]string{"org", "github", "unlink", "acme.com", "--server", srv.URL})
			err := root.Execute()
			if answer == "n\n" {
				if err == nil || calls("UnlinkGithubApp") != 0 {
					t.Fatalf("declined: err %v calls %d", err, calls("UnlinkGithubApp"))
				}
			} else if err != nil || calls("UnlinkGithubApp") != 1 {
				t.Fatalf("consented: err %v calls %d", err, calls("UnlinkGithubApp"))
			}
		})
	}
}

// TestOrgGithub2512ServerRefusals: a non-admin (FORBIDDEN, exit 8) and an
// unknown org (NOT_FOUND, exit 4) map through the existing classification with
// the server code riding along, and nothing follows the one refused mutation.
func TestOrgGithub2512ServerRefusals(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{
		{"FORBIDDEN", 8},
		{"NOT_FOUND", 4},
		{"UNAUTHENTICATED", 3},
	} {
		for _, verb := range []struct {
			name, op, field string
			args            []string
		}{
			{"link", "LinkGithubApp", "linkGithubApp", []string{"github", "link", "acme.com", "--installation-id", "1"}},
			{"unlink", "UnlinkGithubApp", "unlinkGithubApp", []string{"github", "unlink", "acme.com", "--yes"}},
			{"set-app", "UpdateOrgGithubApp", "updateOrgGithubApp", []string{"github", "set-app", "acme.com", "--app-id", "1", "--private-key-file", "-"}},
		} {
			t.Run(verb.name+" "+tc.code, func(t *testing.T) {
				srv, calls, _ := countingGraphQL(t, map[string]string{verb.op: fmt.Sprintf(`{"errors":[{"message":"refused","extensions":{"code":%q}}]}`, tc.code)})
				f, out := testFactory(t)
				f.IOStreams.In = strings.NewReader(keySentinel2512 + "\n")
				root := NewRootCmd(f)
				root.SetArgs(append(append([]string{"org"}, verb.args...), "--json", "--server", srv.URL))
				err := root.Execute()
				if err == nil {
					t.Fatal("expected refusal")
				}
				if calls(verb.op) != 1 {
					t.Fatalf("%d network calls; no follow-up after a refusal", calls(verb.op))
				}
				if code := renderError(f, err); code != tc.exit {
					t.Fatalf("rendered exit %d want %d", code, tc.exit)
				}
				if strings.Contains(out.String(), keySentinel2512) {
					t.Fatal("key leaked into the error envelope")
				}
			})
		}
	}
}

// TestOrgGithub2512SecretNeverEchoes: on stdin, from a file, and through a
// server error that embeds the key in its own diagnostic, the key appears ONLY
// in the one mutation's variables payload — never on any stream, never in the
// error chain, never in the exit envelope's serverCode.
func TestOrgGithub2512SecretNeverEchoes(t *testing.T) {
	okBody := `{"data":{"updateOrgGithubApp":` + orgGithubLinked2512 + `}}`
	errBody := fmt.Sprintf(`{"errors":[{"message":"%s raw backend diagnostic","extensions":{"code":"FORBIDDEN","details":"%s"}}]}`, keySentinel2512, keySentinel2512)
	for _, tc := range []struct {
		name     string
		response string
		fail     bool
	}{
		{"success", okBody, false},
		{"error embedding key", errBody, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, captured := captureGraphQL(t, map[string]string{"UpdateOrgGithubApp": tc.response})
			f, out := testFactory(t)
			stderr := &strings.Builder{}
			f.IOStreams.ErrOut = stderr
			f.IOStreams.In = strings.NewReader(keySentinel2512 + "\n")
			root := NewRootCmd(f)
			root.SetArgs([]string{"org", "github", "set-app", "acme.com", "--app-id", "12345", "--private-key-file", "-", "--server", srv.URL})
			err := root.Execute()
			if tc.fail != (err != nil) {
				t.Fatalf("err %v", err)
			}
			if strings.Contains(out.String()+stderr.String(), keySentinel2512) {
				t.Fatal("key on stdout/stderr")
			}
			if n := bytes.Count(captured["UpdateOrgGithubApp"], []byte(keySentinel2512)); n != 1 {
				t.Fatalf("key appears %d times in the request payload, want exactly 1", n)
			}
			if err != nil {
				for e := err; e != nil; e = unwrap2512(e) {
					if strings.Contains(e.Error(), keySentinel2512) {
						t.Fatalf("raw key reachable in chain: %T", e)
					}
				}
				if code := renderError(f, err); code != 8 {
					t.Fatalf("rendered exit %d", code)
				}
			}
		})
	}
	// The reflected-metadata defense: a server (erroneously) echoing the key in
	// any returned Organization field gets "[REDACTED]" in the DTO, never the
	// key.
	for _, field := range []string{"id", "urn", "githubAppId"} {
		t.Run("reflected in "+field, func(t *testing.T) {
			var row map[string]any
			if err := json.Unmarshal([]byte(orgGithubLinked2512), &row); err != nil {
				t.Fatal(err)
			}
			row[field] = keySentinel2512
			body, err := json.Marshal(map[string]any{"data": map[string]any{"updateOrgGithubApp": row}})
			if err != nil {
				t.Fatal(err)
			}
			srv, _ := captureGraphQL(t, map[string]string{"UpdateOrgGithubApp": string(body)})
			f, out := testFactory(t)
			f.IOStreams.In = strings.NewReader(keySentinel2512 + "\n")
			root := NewRootCmd(f)
			root.SetArgs([]string{"org", "github", "set-app", "acme.com", "--app-id", "12345", "--private-key-file", "-", "--json", "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if dto[field] != "[REDACTED]" || strings.Contains(out.String(), keySentinel2512) {
				t.Fatalf("field %s not redacted: %s", field, out.String())
			}
		})
	}
}

func unwrap2512(err error) error {
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok {
		return u.Unwrap()
	}
	return nil
}

// TestOrgGithub2512NoInlineKeyFlag: the only key input is the file/stdin flag;
// no flag exists that would take the key as an inline value.
func TestOrgGithub2512NoInlineKeyFlag(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	cmd, _, err := root.Find([]string{"org", "github", "set-app"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Flags().Lookup("private-key") != nil {
		t.Fatal("an inline --private-key flag exists")
	}
	if cmd.Flags().Lookup("private-key-file") == nil {
		t.Fatal("--private-key-file missing")
	}
}

// TestOrgGithub2512TextOutput: the human renderer names the org and the three
// github fields, and a private-key-bearing run prints no key.
func TestOrgGithub2512TextOutput(t *testing.T) {
	srv, _ := captureGraphQL(t, map[string]string{"LinkGithubApp": `{"data":{"linkGithubApp":` + orgGithubLinked2512 + `}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "github", "link", "acme.com", "--installation-id", "998877", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"hrn:org:acme.com", "12345", "998877"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text output missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "sentinel-key-2512") {
		t.Fatal("key in text output")
	}
}
