package cmd

import (
	"encoding/json"
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
	// The receipt is the same seven-field App receipt.
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil || len(dto) != 7 {
		t.Fatalf("receipt = %v err=%v", dto, err)
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
