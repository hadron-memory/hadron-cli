package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// cli#1628 (the rest): `app install --policy-file <path>` sends createApp's
// `policy`: the App-layer action-policy link, which can only narrow (cor:acl:040:02).
// The reader is the one `app update --policy-file` uses (#1640). Install has no
// clear flag: an install that wants no link just leaves the flag out.

func TestAppInstallPolicyFileSendsThePolicy1628(t *testing.T) {
	srv, calls := appSettingsServer(t)
	out, err := appSettingsRun(t, srv, "install", "--policy-file", policyFile1640(t, "{\n \"allow\": [\"run.execute\"]\n}\n"), "--yes")
	if err != nil {
		t.Fatal(err)
	}
	c := appSettingsMutation(t, *calls)
	var got, want any
	if err := json.Unmarshal(c.Vars["policy"], &got); err != nil {
		t.Fatalf("policy var missing or not JSON: %v vars=%v", err, c.Vars)
	}
	_ = json.Unmarshal([]byte(`{"allow":["run.execute"]}`), &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("policy = %v", got)
	}
	// Only the install's own variables ride along: nothing else is invented.
	for k := range c.Vars {
		if k != "policy" && k != "orgRef" && k != "agentRef" && k != "name" {
			t.Fatalf("unrequested variable %q: %v", k, c.Vars)
		}
	}
	if !strings.Contains(c.Query, "policy: $policy") {
		t.Fatalf("document does not pass the policy: %s", c.Query)
	}
	// The receipt is the same seven-field App receipt, with these values.
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	wantReceipt := map[string]any{"id": "capp1", "urn": "hrn:app:acme.com:support", "name": "Support", "appType": "AGENT", "agentId": "a1", "memberCount": float64(1), "createdAt": "2026-01-01T00:00:00Z"}
	if !reflect.DeepEqual(dto, wantReceipt) {
		t.Fatalf("receipt = %v, want %v", dto, wantReceipt)
	}
}

// installServer1628 answers every request with the given createApp payload.
func installServer1628(t *testing.T, createApp string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"createApp":` + createApp + `}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A withheld member count stays null in the receipt (distinct from a known 0).
func TestAppInstallPolicyFileKeepsWithheldMemberCountNull1628(t *testing.T) {
	srv := installServer1628(t, `{"id":"capp1","urn":"hrn:app:acme.com:support","name":"Support","appType":"AGENT","agentId":"a1","memberCount":null,"createdAt":"2026-01-01T00:00:00Z"}`)
	out, err := appSettingsRun(t, srv, "install", "--policy-file", policyFile1640(t, `{"allow":[]}`), "--yes")
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if v, ok := dto["memberCount"]; !ok || v != nil {
		t.Fatalf("memberCount = %v (present %v), want null", v, ok)
	}
}

// Jibril's P2: a reply with no error and a null createApp is a refusal, never
// a nil dereference, on every install document.
func TestAppInstallNullCreateAppIsARefusal1628(t *testing.T) {
	for name, flags := range map[string][]string{
		"policy":   {"--policy-file", policyFile1640(t, `{"allow":[]}`), "--yes"},
		"settings": {"--role", "READER", "--yes"},
		"legacy":   nil,
	} {
		t.Run(name, func(t *testing.T) {
			srv := installServer1628(t, `null`)
			_, err := appSettingsRun(t, srv, "install", flags...)
			if err == nil || exitCodeFor(err) != 1 {
				t.Fatalf("err=%v (exit %d), want a refusal, exit 1", err, exitCodeFor(err))
			}
		})
	}
}

// Jibril's P3: the interactive prompt names the setting, never its content,
// and declining sends no mutation.
func TestAppInstallPolicyFilePromptNamesTheSettingNotTheContent1628(t *testing.T) {
	srv, calls := appSettingsServer(t)
	f, _, prompt := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "install", "--org", "acme.com", "--agent", "a1", "--name", "Support", "--policy-file", policyFile1640(t, `{"allow":["private-sentinel-value"]}`), "--server", srv.URL})
	err := root.Execute()
	if exitCodeFor(err) != 6 || len(*calls) != 0 {
		t.Fatalf("declined: %v calls=%+v", err, *calls)
	}
	if !strings.Contains(prompt.String(), "policy") || strings.Contains(prompt.String(), "private-sentinel-value") {
		t.Fatalf("prompt = %q", prompt.String())
	}
}

func TestAppInstallPolicyFileWithOtherSettings1628(t *testing.T) {
	srv, calls := appSettingsServer(t)
	_, err := appSettingsRun(t, srv, "install", "--role", "READER", "--surface", "chat", "--policy-file", policyFile1640(t, `{"allow":[]}`), "--yes")
	if err != nil {
		t.Fatal(err)
	}
	c := appSettingsMutation(t, *calls)
	for _, k := range []string{"role", "surfaces", "policy"} {
		if _, ok := c.Vars[k]; !ok {
			t.Fatalf("%s missing: %v", k, c.Vars)
		}
	}
	if string(c.Vars["policy"]) != `{"allow":[]}` {
		t.Fatalf("policy = %s", c.Vars["policy"])
	}
}

// The reader's refusals are local usage errors on install too: nothing is sent.
func TestAppInstallPolicyFileLocalRefusals1628(t *testing.T) {
	cases := map[string]string{
		"invalid json": "{not json",
		"two values":   `{} {}`,
		"empty":        "  \n",
		"null":         "null",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			srv, calls := appSettingsServer(t)
			_, err := appSettingsRun(t, srv, "install", "--policy-file", policyFile1640(t, content), "--yes")
			if exitCodeFor(err) != 2 || len(*calls) != 0 {
				t.Fatalf("err=%v calls=%+v", err, *calls)
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		srv, calls := appSettingsServer(t)
		_, err := appSettingsRun(t, srv, "install", "--policy-file", "/nonexistent/policy.json", "--yes")
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Fatalf("err=%v calls=%+v", err, *calls)
		}
	})
}

// A policy is a setting: with no terminal and no --yes the install is refused
// before anything is sent, like every other new setting.
func TestAppInstallPolicyFileNeedsConfirmation1628(t *testing.T) {
	srv, calls := appSettingsServer(t)
	_, err := appSettingsRun(t, srv, "install", "--policy-file", policyFile1640(t, `{"allow":[]}`))
	if err == nil || len(*calls) != 0 {
		t.Fatalf("err=%v calls=%+v", err, *calls)
	}
}

func TestAppInstallHasPolicyFileButNoClearFlag1628(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "install", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--policy-file <path>") {
		t.Fatalf("help does not describe --policy-file:\n%s", out.String())
	}
	srv, calls := appSettingsServer(t)
	f2, _ := testFactory(t)
	r2 := NewRootCmd(f2)
	r2.SetArgs([]string{"app", "install", "--org", "acme.com", "--agent", "a1", "--name", "S", "--clear-policy", "--yes", "--server", srv.URL})
	if err := r2.Execute(); exitCodeFor(err) != 2 || len(*calls) != 0 {
		t.Fatalf("install accepted --clear-policy: err=%v calls=%d", err, len(*calls))
	}
}

// #2319: install has no --clear-policy, so its empty/null refusals must not
// advise it; update keeps the advice.
func TestAppPolicyFileRefusalAdviceMatchesTheCommand2319(t *testing.T) {
	for name, content := range map[string]string{"empty": "  \n", "null": "null"} {
		t.Run("install "+name, func(t *testing.T) {
			srv, calls := appSettingsServer(t)
			_, err := appSettingsRun(t, srv, "install", "--policy-file", policyFile1640(t, content), "--yes")
			if exitCodeFor(err) != 2 || len(*calls) != 0 || err == nil {
				t.Fatalf("err=%v calls=%+v", err, *calls)
			}
			if strings.Contains(err.Error(), "--clear-policy") || !strings.Contains(err.Error(), "leave --policy-file out") {
				t.Fatalf("install advice = %q", err)
			}
		})
		t.Run("update "+name, func(t *testing.T) {
			srv, calls := appSettingsServer(t)
			_, err := appSettingsRun(t, srv, "update", "--policy-file", policyFile1640(t, content), "--yes")
			if exitCodeFor(err) != 2 || len(*calls) != 0 || err == nil || !strings.Contains(err.Error(), "--clear-policy") {
				t.Fatalf("update err=%v calls=%+v", err, *calls)
			}
		})
	}
}
