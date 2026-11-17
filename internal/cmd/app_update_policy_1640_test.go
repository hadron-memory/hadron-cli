package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// cli#1640 (server#3423 / #3425): `app update --policy-file <path>` sends the
// App's action-policy link, `--clear-policy` sends an explicit null, neither
// flag leaves `policy` out of the request AND out of the document (an older
// server rejects a document that declares it). The two flags are mutually
// exclusive and every local refusal sends nothing.

func policyFile1640(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAppUpdatePolicyFileSendsThePolicy1640(t *testing.T) {
	srv, calls := appSettingsServer(t)
	out, err := appSettingsRun(t, srv, "update", "--policy-file", policyFile1640(t, "{\n  \"allow\": [\"run.execute\", \"comm.outbound\"]\n}\n"), "--yes")
	if err != nil {
		t.Fatal(err)
	}
	c := appSettingsMutation(t, *calls)
	var got, want any
	if err := json.Unmarshal(c.Vars["policy"], &got); err != nil {
		t.Fatalf("policy var missing or not JSON: %v vars=%v", err, c.Vars)
	}
	_ = json.Unmarshal([]byte(`{"allow":["run.execute","comm.outbound"]}`), &want)
	if !reflect.DeepEqual(got, want) || len(c.Vars) != 2 {
		t.Fatalf("vars=%v", c.Vars)
	}
	if !strings.Contains(c.Query, "policy: $policy") {
		t.Fatalf("document does not pass the policy: %s", c.Query)
	}
	var dto struct {
		Changed []string `json:"changed"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil || !reflect.DeepEqual(dto.Changed, []string{"policy"}) {
		t.Fatalf("changed=%v err=%v\n%s", dto.Changed, err, out)
	}
}

func TestAppUpdateClearPolicySendsExplicitNull1640(t *testing.T) {
	srv, calls := appSettingsServer(t)
	out, err := appSettingsRun(t, srv, "update", "--clear-policy", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	c := appSettingsMutation(t, *calls)
	if string(c.Vars["policy"]) != "null" || len(c.Vars) != 2 {
		t.Fatalf("vars=%v", c.Vars)
	}
	var dto struct {
		Changed []string `json:"changed"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil || !reflect.DeepEqual(dto.Changed, []string{"policy (cleared)"}) {
		t.Fatalf("changed=%v err=%v\n%s", dto.Changed, err, out)
	}
}

func TestAppUpdatePolicyTravelsWithOtherSettingsInOneRequest1640(t *testing.T) {
	srv, calls := appSettingsServer(t)
	if _, err := appSettingsRun(t, srv, "update", "--name", "Support Desk", "--role", "READER", "--policy-file", policyFile1640(t, `{"allow":[]}`), "--yes"); err != nil {
		t.Fatal(err)
	}
	c := appSettingsMutation(t, *calls)
	if string(c.Vars["name"]) != `"Support Desk"` || string(c.Vars["role"]) != `"READER"` || string(c.Vars["policy"]) != `{"allow":[]}` {
		t.Fatalf("vars=%v", c.Vars)
	}
}

// Without either flag the request and the document carry no policy at all, so
// a server that predates the argument keeps accepting the command.
func TestAppUpdateWithoutPolicyFlagsSendsNoPolicy1640(t *testing.T) {
	for _, flags := range [][]string{{"--name", "X"}, {"--role", "READER"}, {"--surface", "chat"}} {
		srv, calls := appSettingsServer(t)
		if _, err := appSettingsRun(t, srv, "update", append(flags, "--yes")...); err != nil {
			t.Fatal(err)
		}
		c := appSettingsMutation(t, *calls)
		if _, present := c.Vars["policy"]; present || strings.Contains(c.Query, "policy") {
			t.Fatalf("flags=%v policy leaked into the request: %+v", flags, c)
		}
	}
}

func TestAppUpdatePolicyLocalRefusalsSendNothing1640(t *testing.T) {
	good := policyFile1640(t, `{"allow":["run.execute"]}`)
	for name, flags := range map[string][]string{
		"both flags":      {"--policy-file", good, "--clear-policy"},
		"invalid JSON":    {"--policy-file", policyFile1640(t, `{"allow": [`)},
		"empty file":      {"--policy-file", policyFile1640(t, " \n")},
		"null would wipe": {"--policy-file", policyFile1640(t, "null")},
		"missing file":    {"--policy-file", filepath.Join(t.TempDir(), "nope.json")},
		"blank path":      {"--policy-file", ""},
		"trailing data":   {"--policy-file", policyFile1640(t, `{"allow":[]} {"x":1}`)},
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := appSettingsServer(t)
			_, err := appSettingsRun(t, srv, "update", append(flags, "--yes")...)
			if exitCodeFor(err) != 2 || len(*calls) != 0 {
				t.Fatalf("err=%v calls=%+v", err, *calls)
			}
		})
	}
}

// A refusal for the new argument (an older server) is shown as the server
// returned it and the mutation is not retried or widened.
func TestAppUpdatePolicyOlderServerRefusalIsNotRetried1640(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"Unknown argument \"policy\" on field \"Mutation.updateApp\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`))
	}))
	defer srv.Close()
	_, err := appSettingsRun(t, srv, "update", "--policy-file", policyFile1640(t, `{"allow":[]}`), "--yes")
	if err == nil || n != 1 {
		t.Fatalf("err=%v requests=%d", err, n)
	}
	// The server's own words survive; nothing generic replaces them.
	if !strings.Contains(err.Error(), `Unknown argument "policy" on field "Mutation.updateApp"`) {
		t.Fatalf("the server's refusal was not preserved: %v", err)
	}
}

// A success reply that carries no App (updateApp null, or data without it) is
// a malformed answer, not a result: the request was sent once, whether it
// applied is unknown, and nothing is claimed or printed as updated.
func TestAppUpdateReplyWithoutAnAppIsAnUnknownOutcome1640(t *testing.T) {
	policy := policyFile1640(t, `{"allow":[]}`)
	for name, flags := range map[string][]string{
		"policy file":  {"--policy-file", policy},
		"clear policy": {"--clear-policy"},
		"settings":     {"--role", "READER"},
		"basic":        {"--name", "X"},
	} {
		for reply, body := range map[string]string{"null": `{"data":{"updateApp":null}}`, "missing": `{"data":{}}`} {
			t.Run(name+"/"+reply, func(t *testing.T) {
				var n int
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n++
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(body))
				}))
				defer srv.Close()
				out, err := appSettingsRun(t, srv, "update", append(flags, "--yes")...)
				if exitCodeFor(err) != 7 || n != 1 || !strings.Contains(err.Error(), "outcome unknown") || strings.Contains(out, "updated") {
					t.Fatalf("err=%v requests=%d stdout=%s", err, n, out)
				}
			})
		}
	}
}

// The policy's shape is the server's: any single JSON value that is not null
// goes through, including numbers a float64 cannot hold; the size limit is
// exact (1 MiB passes, one byte more is refused before any request).
func TestAppUpdatePolicyFileValueAndSizeBoundaries1640(t *testing.T) {
	const limit = 1 << 20
	exact := `{"allow":["` + strings.Repeat("a", limit-len(`{"allow":[""]}`)) + `"]}`
	if len(exact) != limit {
		t.Fatalf("fixture is %d bytes, want %d", len(exact), limit)
	}
	for name, content := range map[string]string{"huge exponent": `{"n":1e400}`, "array": `["run.execute"]`, "exactly the limit": exact} {
		t.Run("accepted/"+name, func(t *testing.T) {
			srv, calls := appSettingsServer(t)
			if _, err := appSettingsRun(t, srv, "update", "--policy-file", policyFile1640(t, content), "--yes"); err != nil {
				t.Fatal(err)
			}
			if c := appSettingsMutation(t, *calls); len(c.Vars["policy"]) == 0 {
				t.Fatalf("policy not sent: %v", c.Vars)
			}
		})
	}
	t.Run("refused/one byte over", func(t *testing.T) {
		srv, calls := appSettingsServer(t)
		_, err := appSettingsRun(t, srv, "update", "--policy-file", policyFile1640(t, exact[:len(exact)-1]+` ]}`), "--yes")
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Fatalf("err=%v calls=%+v", err, *calls)
		}
	})
}

// The confirmation names the policy change, never its value, and declining
// sends nothing.
func TestAppUpdatePolicyConfirmationNamesTheChangeNotTheValue1640(t *testing.T) {
	for _, tc := range []struct {
		flags []string
		want  string
	}{
		{[]string{"--policy-file", policyFile1640(t, `{"allow":["private-sentinel-value"]}`)}, "Update App " + appRef1058 + ": policy?"},
		{[]string{"--clear-policy"}, "Update App " + appRef1058 + ": policy (cleared)?"},
	} {
		srv, calls := appSettingsServer(t)
		f, _, prompt := testFactoryTTY(t, "n\n")
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{"app", "update", appRef1058}, tc.flags...), "--server", srv.URL))
		err := root.Execute()
		if exitCodeFor(err) != 6 || len(*calls) != 0 {
			t.Fatalf("declined: %v calls=%+v", err, *calls)
		}
		if !strings.Contains(prompt.String(), tc.want) || strings.Contains(prompt.String(), "private-sentinel-value") {
			t.Fatalf("prompt=%q want %q without the value", prompt.String(), tc.want)
		}
	}
}
