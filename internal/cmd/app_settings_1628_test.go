package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type appSettingsCall struct {
	Operation string                     `json:"operationName"`
	Query     string                     `json:"query"`
	Vars      map[string]json.RawMessage `json:"variables"`
}

func appSettingsServer(t *testing.T) (*httptest.Server, *[]appSettingsCall) {
	t.Helper()
	calls := &[]appSettingsCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c appSettingsCall
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			t.Error(err)
			return
		}
		*calls = append(*calls, c)
		w.Header().Set("Content-Type", "application/json")
		if c.Operation == "GetAppMetadata" {
			_, _ = w.Write([]byte(`{"data":{"app":{"id":"capp1","urn":"hrn:app:acme.com:support","name":"Support","appType":"AGENT"}}}`))
			return
		}
		if strings.HasPrefix(c.Operation, "UpdateApp") {
			_, _ = w.Write([]byte(appUpdateOK1058))
			return
		}
		if strings.HasPrefix(c.Operation, "CreateApp") {
			_, _ = w.Write([]byte(`{"data":{"createApp":{"id":"capp1","urn":"hrn:app:acme.com:support","name":"Support","appType":"AGENT","agentId":"a1","memberCount":1,"createdAt":"2026-01-01T00:00:00Z"}}}`))
			return
		}
		t.Errorf("unexpected operation %s", c.Operation)
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}
func appSettingsRun(t *testing.T, srv *httptest.Server, mode string, flags ...string) (string, error) {
	t.Helper()
	f, out := testFactory(t)
	r := NewRootCmd(f)
	args := []string{"app", mode}
	if mode == "install" {
		args = append(args, "--org", "acme.com", "--agent", "a1", "--name", "Support")
	} else {
		args = append(args, appRef1058)
	}
	args = append(args, flags...)
	r.SetArgs(append(args, "--server", srv.URL, "--json"))
	err := r.Execute()
	if err != nil && strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("gated flag absent: %v", err)
	}
	return out.String(), err
}
func appSettingsMutation(t *testing.T, calls []appSettingsCall) appSettingsCall {
	t.Helper()
	var muts []appSettingsCall
	for _, c := range calls {
		if strings.HasPrefix(c.Operation, "CreateApp") || strings.HasPrefix(c.Operation, "UpdateApp") {
			muts = append(muts, c)
		}
	}
	if len(muts) != 1 {
		t.Fatalf("mutations=%d calls=%+v", len(muts), calls)
	}
	return muts[0]
}
func TestAppSettings1628WireEachFlag(t *testing.T) {
	for _, mode := range []string{"install", "update"} {
		for _, tc := range []struct {
			flag, key, value        string
			installOnly, updateOnly bool
		}{
			{"--role", "role", `"READER"`, false, false},
			{"--create-user-permission", "createUserPermission", `"DENY"`, false, false},
			{"--identify-user-method", "identifyUserMethod", `"SECRET"`, false, false},
			{"--session-timeout-seconds", "sessionTimeoutSeconds", "90", false, false},
			{"--session-dormancy-days", "sessionDormancyDays", "7", false, true},
			{"--agent-sessions-expire=false", "agentSessionsExpire", "false", false, true},
			{"--anonymous-ttl-days", "anonymousTtlDays", "0", false, false},
			{"--expires-at", "expiresAt", `"2027-02-01T00:00:00Z"`, false, false},
			{"--training-mode=false", "trainingMode", "false", false, false},
			{"--agent-tool", "agentTools", `["calendar.list"]`, false, false},
			{"--surface", "surfaces", `["chat"]`, false, false},
			{"--install-optional", "installOptional", `["hrn:agent:acme.com:helper"]`, true, false},
			{"--type", "appType", `"CHATBOT"`, false, false},
		} {
			if (mode == "install" && tc.updateOnly) || (mode == "update" && tc.installOnly) {
				continue
			}
			t.Run(mode+tc.flag, func(t *testing.T) {
				srv, calls := appSettingsServer(t)
				flags := []string{tc.flag}
				if !strings.Contains(tc.flag, "=") {
					var value any
					if err := json.Unmarshal([]byte(tc.value), &value); err != nil {
						t.Fatal(err)
					}
					switch v := value.(type) {
					case string:
						flags = append(flags, v)
					case []any:
						flags = append(flags, v[0].(string))
					default:
						flags = append(flags, tc.value)
					}
				}
				_, err := appSettingsRun(t, srv, mode, append(flags, "--yes")...)
				if err != nil {
					t.Fatal(err)
				}
				call := appSettingsMutation(t, *calls)
				if string(call.Vars[tc.key]) != tc.value {
					t.Fatalf("%s=%s want%s", tc.key, call.Vars[tc.key], tc.value)
				}
				want := []string{tc.key, "ref"}
				if mode == "install" {
					want = []string{tc.key, "agentRef", "name", "orgRef"}
				}
				keys := []string{}
				for k := range call.Vars {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				sort.Strings(want)
				if !reflect.DeepEqual(keys, want) {
					t.Fatalf("unpassed values present: %v want%v", keys, want)
				}
			})
		}
	}
}
func TestAppSettings1628ClearAndReplace(t *testing.T) {
	for _, tc := range []struct {
		flags      []string
		key, value string
	}{
		{[]string{"--clear-expires-at"}, "expiresAt", "null"},
		{[]string{"--clear-agent-tools"}, "agentTools", "[]"},
		{[]string{"--clear-surfaces"}, "surfaces", "[]"},
		{[]string{"--surface", "chat", "--surface", "voice"}, "surfaces", `["chat","voice"]`},
		{[]string{"--agent-tool", "a,b", "--agent-tool", "c"}, "agentTools", `["a,b","c"]`},
	} {
		t.Run(strings.Join(tc.flags, " "), func(t *testing.T) {
			srv, calls := appSettingsServer(t)
			_, err := appSettingsRun(t, srv, "update", append(tc.flags, "--yes")...)
			if err != nil {
				t.Fatal(err)
			}
			v := appSettingsMutation(t, *calls).Vars
			if string(v[tc.key]) != tc.value || len(v) != 2 {
				t.Fatalf("vars=%v", v)
			}
		})
	}
}
func TestAppSettings1628LocalRefusals(t *testing.T) {
	for _, flags := range [][]string{{"--role", "boss"}, {"--create-user-permission", ""}, {"--identify-user-method", "email"}, {"--type", "bogus"}, {"--surface", "chat", "--clear-surfaces"}, {"--agent-tool", "x", "--clear-agent-tools"}, {"--expires-at", "x", "--clear-expires-at"}} {
		srv, calls := appSettingsServer(t)
		_, err := appSettingsRun(t, srv, "update", append(flags, "--yes")...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Fatalf("flags=%v err=%v calls=%+v", flags, err, *calls)
		}
	}
	srv, calls := appSettingsServer(t)
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{not JSON}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := appSettingsRun(t, srv, "install", "--policy-file", path, "--yes")
	if exitCodeFor(err) != 2 || len(*calls) != 0 {
		t.Fatalf("bad policy sent: %v %+v", err, *calls)
	}
}
func TestAppSettings1628PolicyAndRename(t *testing.T) {
	srv, calls := appSettingsServer(t)
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"allow":["run.execute"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := appSettingsRun(t, srv, "install", "--policy-file", path, "--yes")
	if err != nil {
		t.Fatal(err)
	}
	v := appSettingsMutation(t, *calls).Vars
	if string(v["policy"]) != `{"allow":["run.execute"]}` {
		t.Fatal(v)
	}
	var dto map[string]any
	if err = json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto) != 7 || dto["id"] != "capp1" {
		t.Fatal(out)
	}
	srv, calls = appSettingsServer(t)
	f, _, errOut := testFactoryTTY(t, "n\n")
	r := NewRootCmd(f)
	r.SetArgs([]string{"app", "update", appRef1058, "--urn", "renamed", "--surface", "chat", "--server", srv.URL})
	err = r.Execute()
	if exitCodeFor(err) != 6 {
		t.Fatalf("declined rename: %v", err)
	}
	for _, c := range *calls {
		if strings.HasPrefix(c.Operation, "UpdateApp") {
			t.Fatal("declined mutation sent")
		}
	}
	for _, s := range []string{appRef1058, "hrn:app:acme.com:renamed", "surfaces"} {
		if !strings.Contains(errOut.String(), s) {
			t.Fatalf("prompt lacks %s: %s", s, errOut.String())
		}
	}
}
func TestAppSettings1628ConfirmationAndLegacy(t *testing.T) {
	for _, mode := range []string{"install", "update"} {
		srv, calls := appSettingsServer(t)
		_, err := appSettingsRun(t, srv, mode, "--surface", "chat")
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Fatalf("nonTTY sent: %v %+v", err, *calls)
		}
	}
	srv, calls := appSettingsServer(t)
	_, err := appSettingsRun(t, srv, "update", "--name", "X", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	c := appSettingsMutation(t, *calls)
	if c.Operation != "UpdateApp" || strings.Contains(c.Query, "surfaces:") {
		t.Fatalf("legacy widened: %+v", c)
	}
}
