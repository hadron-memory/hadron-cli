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
		if c.Operation == "GetAppRenameURN" {
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
			{"--urn", "urn", `"renamed"`, false, false},
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
				out, err := appSettingsRun(t, srv, mode, append(flags, "--yes")...)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "update" {
					var dto map[string]json.RawMessage
					if err := json.Unmarshal([]byte(out), &dto); err != nil {
						t.Fatal(err)
					}
					wantKeys := []string{"app", "changed", "id", "name", "sessionTimeoutSeconds", "status", "urn"}
					got := []string{}
					for key := range dto {
						got = append(got, key)
					}
					sort.Strings(got)
					if !reflect.DeepEqual(got, wantKeys) {
						t.Fatalf("JSON keys=%v want%v", got, wantKeys)
					}
					var changed []string
					if err := json.Unmarshal(dto["changed"], &changed); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(changed, []string{tc.key}) {
						t.Fatalf("changed=%v want%v", changed, tc.key)
					}
				}
				if len(*calls) != 1 {
					t.Fatalf("--yes settings must send exactly one mutation: %+v", *calls)
				}
				call := appSettingsMutation(t, *calls)
				expectedOperation := "UpdateAppSettings"
				if mode == "install" {
					expectedOperation = "CreateAppSettings"
					if tc.flag == "--urn" || tc.flag == "--type" {
						expectedOperation = "CreateApp"
					}
				} else if tc.flag == "--session-timeout-seconds" {
					expectedOperation = "UpdateApp"
				}
				if call.Operation != expectedOperation {
					t.Fatalf("operation=%s want %s", call.Operation, expectedOperation)
				}
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
	if string(v["policy"]) != `{"allow":["run.execute"]}` || keys1058(v) != "agentRef,name,orgRef,policy" {
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

func TestAppSettings1628FalseClearIsOmitted(t *testing.T) {
	for _, flag := range []string{"--clear-surfaces=false", "--clear-agent-tools=false", "--clear-expires-at=false"} {
		t.Run(flag, func(t *testing.T) {
			srv, calls := appSettingsServer(t)
			_, err := appSettingsRun(t, srv, "update", flag, "--yes")
			if err == nil || len(*calls) != 0 {
				t.Fatalf("false clear should refuse an empty update before HTTP: err=%v calls=%+v", err, *calls)
			}
			_, err = appSettingsRun(t, srv, "update", flag, "--name", "new", "--yes")
			if err != nil {
				t.Fatal(err)
			}
			c := appSettingsMutation(t, *calls)
			if c.Operation != "UpdateApp" || len(c.Vars) != 2 {
				t.Fatalf("false clear changed wire: %+v", c)
			}
		})
	}
}

func TestAppSettings1628ConfirmationFieldsOnly(t *testing.T) {
	srv, calls := appSettingsServer(t)
	f, _, prompt := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "update", appRef1058, "--surface", "private-sentinel-value", "--training-mode=false", "--server", srv.URL})
	err := root.Execute()
	if exitCodeFor(err) != 6 || len(*calls) != 0 {
		t.Fatalf("declined: %v calls=%+v", err, *calls)
	}
	expected := "Update App " + appRef1058 + ": trainingMode, surfaces?"
	if !strings.Contains(prompt.String(), expected) || strings.Contains(prompt.String(), "private-sentinel-value") {
		t.Fatalf("prompt=%q want %q without values", prompt.String(), expected)
	}
}

func TestAppSettings1628MixedAndLegacyInstall(t *testing.T) {
	for _, tc := range []struct {
		mode, operation, expected string
		flags                     []string
	}{
		{"install", "CreateApp", `{"agentRef":"a1","name":"Support","orgRef":"acme.com"}`, nil},
		{"install", "CreateAppSettings", `{"agentRef":"a1","name":"Support","orgRef":"acme.com","urn":"renamed","appType":"CHATBOT","description":"private-description","role":"READER","sessionTimeoutSeconds":91,"trainingMode":false}`, []string{"--urn", "renamed", "--type", "CHATBOT", "--description", "private-description", "--role", "READER", "--session-timeout-seconds", "91", "--training-mode=false", "--yes"}},
		{"update", "UpdateAppSettings", `{"ref":"hrn:app:acme.com:support","name":"Desk","description":"private-description","sessionTimeoutSeconds":1234,"urn":"renamed","appType":"CLOUD","role":"READER","surfaces":[]}`, []string{"--name", "Desk", "--description", "private-description", "--session-timeout-seconds", "1234", "--urn", "renamed", "--type", "CLOUD", "--role", "READER", "--clear-surfaces", "--yes"}},
		{"update", "UpdateAppSettings", `{"ref":"hrn:app:acme.com:support","description":null,"role":"READER"}`, []string{"--clear-description", "--role", "READER", "--yes"}},
	} {
		t.Run(tc.mode+tc.operation+tc.expected, func(t *testing.T) {
			srv, calls := appSettingsServer(t)
			_, err := appSettingsRun(t, srv, tc.mode, tc.flags...)
			if err != nil {
				t.Fatal(err)
			}
			call := appSettingsMutation(t, *calls)
			if len(*calls) != 1 || call.Operation != tc.operation {
				t.Fatalf("calls=%+v expected %s", *calls, tc.operation)
			}
			var expected map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.expected), &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(call.Vars, expected) {
				t.Fatalf("variables=%s expected=%s", mustJSON1628(t, call.Vars), tc.expected)
			}
			if tc.operation == "CreateApp" && strings.Contains(call.Query, "surfaces:") {
				t.Fatal("legacy install document widened")
			}
		})
	}
}
func mustJSON1628(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func TestAppSettings1628InstallDeclineAndPersonal(t *testing.T) {
	srv, calls := appSettingsServer(t)
	f, _, prompt := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "install", "--org", "acme.com", "--agent", "a1", "--name", "private-name", "--urn", "private-slug", "--type", "CHATBOT", "--description", "private-description", "--surface", "private-surface", "--training-mode=false", "--server", srv.URL})
	err := root.Execute()
	if exitCodeFor(err) != 6 || len(*calls) != 0 {
		t.Fatalf("declined install err=%v calls=%+v", err, *calls)
	}
	expected := "Install App with settings: [agentRef name orgRef urn appType description trainingMode surfaces]?"
	if !strings.Contains(prompt.String(), expected) || strings.Contains(prompt.String(), "private-") {
		t.Fatalf("prompt=%q", prompt.String())
	}
	f, _ = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"app", "install", "--owner-me", "--agent", "a1", "--name", "Personal", "--role", "READER", "--yes", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	call := appSettingsMutation(t, *calls)
	if len(*calls) != 1 || call.Operation != "CreateAppSettings" || keys1058(call.Vars) != "agentRef,name,role" {
		t.Fatalf("personal install wire=%+v", *calls)
	}
}
func TestAppSettings1628AdditionalLocalRefusals(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		flags []string
	}{
		{"install", []string{"--type", "bogus"}},
		{"install", []string{"--type="}},
		{"update", []string{"--urn", "bad:slug"}},
		{"update", []string{"--urn="}},
		{"install", []string{"--policy-file", filepath.Join(t.TempDir(), "missing.json")}},
	} {
		t.Run(tc.mode+strings.Join(tc.flags, " "), func(t *testing.T) {
			srv, calls := appSettingsServer(t)
			_, err := appSettingsRun(t, srv, tc.mode, append(tc.flags, "--yes")...)
			if exitCodeFor(err) != 2 || len(*calls) != 0 {
				t.Fatalf("err=%v calls=%+v", err, *calls)
			}
			if tc.flags[0] == "--policy-file" && !strings.Contains(err.Error(), "cannot read --policy-file") {
				t.Fatalf("read error replaced: %v", err)
			}
		})
	}
}
func TestAppSettings1628SettingsUnavailableIsSentOnce(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var call appSettingsCall
		if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
			t.Error(err)
		}
		if call.Operation != "UpdateAppSettings" {
			t.Errorf("operation=%s", call.Operation)
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("gateway unavailable"))
	}))
	defer srv.Close()
	out, err := appSettingsRun(t, srv, "update", "--role", "READER", "--yes")
	if exitCodeFor(err) != 7 || calls != 1 || !strings.Contains(err.Error(), "outcome unknown") || strings.Contains(out, "updated") {
		t.Fatalf("err=%v calls=%d stdout=%s", err, calls, out)
	}
}
func TestAppSettings1628BadRenameReadNeverMutates(t *testing.T) {
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"data":{"app":null}}`, 4},
		{`{"errors":[{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`, 8},
		{`{"data":{"app":{"urn":"not-a-urn"}}}`, 1},
		{`{"data":{"app":{"urn":"hrn:app:acme.com:bad\u001bname"}}}`, 1},
	} {
		t.Run(tc.body, func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var call appSettingsCall
				if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
					t.Error(err)
				}
				if call.Operation != "GetAppRenameURN" {
					t.Errorf("unexpected mutation: %s", call.Operation)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			f, out, prompt := testFactoryTTY(t, "y\n")
			root := NewRootCmd(f)
			root.SetArgs([]string{"app", "update", appRef1058, "--urn", "renamed", "--server", srv.URL})
			err := root.Execute()
			if exitCodeFor(err) != tc.code || calls != 1 || strings.Contains(out.String(), "Updated") || strings.Contains(prompt.String(), "Update App") {
				t.Fatalf("err=%v calls=%d out=%q prompt=%q", err, calls, out.String(), prompt.String())
			}
		})
	}
}

func TestAppSettings1628BlankExpiryRefuses(t *testing.T) {
	for _, mode := range []string{"install", "update"} {
		for _, value := range []string{"", " ", "\t\n"} {
			t.Run(mode+value, func(t *testing.T) {
				srv, calls := appSettingsServer(t)
				_, err := appSettingsRun(t, srv, mode, "--expires-at", value, "--yes")
				if exitCodeFor(err) != 2 || len(*calls) != 0 {
					t.Fatalf("blank expiry: err=%v calls=%+v", err, *calls)
				}
			})
		}
	}
}

func TestAppSettings1628RenameReadConditions(t *testing.T) {
	for _, tty := range []bool{false, true} {
		for _, yes := range []bool{false, true} {
			t.Run(mustJSON1628(t, []bool{tty, yes}), func(t *testing.T) {
				srv, calls := appSettingsServer(t)
				f, _ := testFactory(t)
				if tty {
					f, _, _ = testFactoryTTY(t, "y\n")
				}
				root := NewRootCmd(f)
				args := []string{"app", "update", appRef1058, "--urn", "renamed", "--server", srv.URL}
				if yes {
					args = append(args, "--yes")
				}
				root.SetArgs(args)
				err := root.Execute()
				if !tty && !yes {
					if exitCodeFor(err) != 2 || len(*calls) != 0 {
						t.Fatalf("nonTTY err=%v calls=%+v", err, *calls)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				expected := 1
				if tty && !yes {
					expected = 2
				}
				if len(*calls) != expected {
					t.Fatalf("calls=%+v expected%d", *calls, expected)
				}
				if yes && (*calls)[0].Operation != "UpdateAppSettings" {
					t.Fatalf("yes pre-read: %+v", *calls)
				}
				if tty && !yes && (*calls)[0].Operation != "GetAppRenameURN" {
					t.Fatalf("missing pre-read: %+v", *calls)
				}
			})
		}
	}
}
func TestAppSettings1628AllListCommas(t *testing.T) {
	for _, tc := range []struct{ mode, flag, key string }{
		{"install", "--surface", "surfaces"}, {"update", "--surface", "surfaces"}, {"install", "--install-optional", "installOptional"},
	} {
		t.Run(tc.mode+tc.flag, func(t *testing.T) {
			srv, calls := appSettingsServer(t)
			_, err := appSettingsRun(t, srv, tc.mode, tc.flag, "a,b", tc.flag, "c", "--yes")
			if err != nil {
				t.Fatal(err)
			}
			c := appSettingsMutation(t, *calls)
			expectedKeys := "ref," + tc.key
			if tc.mode == "install" {
				expectedKeys = "agentRef," + tc.key + ",name,orgRef"
				if tc.key == "surfaces" {
					expectedKeys = "agentRef,name,orgRef,surfaces"
				}
			}
			if string(c.Vars[tc.key]) != `["a,b","c"]` || keys1058(c.Vars) != expectedKeys {
				t.Fatalf("vars=%+v keys=%s", c.Vars, keys1058(c.Vars))
			}
		})
	}
}
