package cmd

import (
	"encoding/json"
	"errors"
	"fmt"

	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sender1342 = `{"id":"sender1","organizationId":"org1","from":"Robot <robot@example.test>","enabled":true,"hasApiKey":true,"createdAt":"2026-10-31T00:00:00Z","updatedAt":"2026-10-31T00:00:00Z","createdBy":null,"updatedBy":null}`

func TestResend1342Commands(t *testing.T) {
	for _, tc := range []struct {
		name, op, field string
		args            []string
		vars            map[string]any
		response        string
	}{
		{"create", "CreateResendSender", "createResendSender", []string{"create", "--org", "acme.com", "--from", "Robot <robot@example.test>", "--api-key-file", "-", "--enabled=false"}, map[string]any{"orgRef": "acme.com", "from": "Robot <robot@example.test>", "apiKey": "synthetic-key-1342", "enabled": false}, sender1342},
		{"get", "GetResendSender", "resendSender", []string{"get", "sender1"}, map[string]any{"ref": "sender1"}, sender1342},
		{"update", "UpdateResendSender", "updateResendSender", []string{"update", "sender1", "--enabled=false"}, map[string]any{"ref": "sender1", "enabled": false}, sender1342},
		{"rotate", "UpdateResendSender", "updateResendSender", []string{"update", "sender1", "--api-key-file", "-"}, map[string]any{"ref": "sender1", "apiKey": "synthetic-key-1342"}, sender1342},
		{"list", "ListResendSenders", "resendSenders", []string{"list", "--org-id", "org1", "--limit", "3", "--offset", "2"}, map[string]any{"orgId": "org1", "limit": float64(3), "offset": float64(2)}, `{"items":[` + sender1342 + `],"total":1}`},
		{"rm", "DeleteResendSender", "deleteResendSender", []string{"rm", "sender1", "--yes"}, map[string]any{"ref": "sender1"}, `true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, captured := captureGraphQL(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":` + tc.response + `}}`})
			f, out := testFactory(t)
			f.IOStreams.In = strings.NewReader("synthetic-key-1342\n")
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"org", "resend-sender"}, tc.args...), "--json", "--server", srv.URL))
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
			if strings.Contains(out.String(), "synthetic-key-1342") {
				t.Fatal("key leaked")
			}
			if tc.field != "deleteResendSender" && tc.field != "resendSenders" {
				var dto map[string]any
				_ = json.Unmarshal([]byte(out.String()), &dto)
				if dto["id"] != "sender1" || dto["organizationId"] != "org1" || dto["hasApiKey"] != true {
					t.Fatalf("DTO: %v", dto)
				}
				if len(dto) != 9 {
					t.Fatalf("unexpected DTO keys: %v", dto)
				}
			}
			if !json.Valid([]byte(out.String())) {
				t.Fatalf("invalid JSON %s", out)
			}
		})
	}
}

func TestPlatformInvite1342(t *testing.T) {
	srv, captured := captureGraphQL(t, map[string]string{"InviteUser": `{"data":{"inviteUser":{"id":"i1","slug":"private-slug","memberRole":"READER","activationCount":0,"createdAt":"2026-10-31T00:00:00Z"}}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "invite", "--email", "alice@example.test", "--github-username", "alice", "--name", "Alice", "--expires-in-days", "7", "--max-activations", "0", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(captured["InviteUser"], &got)
	if got["email"] != "alice@example.test" || got["githubUsername"] != "alice" || got["name"] != "Alice" || got["expiresInDays"] != float64(7) || got["maxActivations"] != float64(0) {
		t.Fatalf("vars %v", got)
	}
	var dto map[string]any
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto["slug"] != "private-slug" {
		t.Fatal(out.String())
	}
}

func TestMemberRefs1342(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		op, body string
	}{
		{[]string{"add", "acme.com", "--user", "alice@example.test", "--role", "READER"}, "AddOrgMember", `{"addOrgMember":{"id":"m1","role":"READER","user":` + orgUserJSON + `}}`},
		{[]string{"set-role", "acme.com", "--user", "alice@example.test", "--role", "READER"}, "UpdateOrgMember", `{"updateOrgMember":{"id":"m1","role":"READER","user":` + orgUserJSON + `}}`},
		{[]string{"rm", "acme.com", "--user", "alice@example.test", "--yes"}, "RemoveOrgMember", `{"removeOrgMember":true}`},
	} {
		t.Run(tc.op, func(t *testing.T) {
			srv, captured := captureGraphQL(t, map[string]string{tc.op: `{"data":` + tc.body + `}`})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"org", "member"}, tc.args...), "--server", srv.URL))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			_ = json.Unmarshal(captured[tc.op], &got)
			if got["orgRef"] != "acme.com" || got["userRef"] != "alice@example.test" {
				t.Fatalf("refs not forwarded: %v", got)
			}
			if _, ok := got["orgId"]; ok {
				t.Fatal("deprecated orgId")
			}
		})
	}
}

func TestResend1342SecretErrors(t *testing.T) {
	for _, code := range []string{"FORBIDDEN", "UNAUTHENTICATED", "BAD_USER_INPUT"} {
		for _, args := range [][]string{{"create", "--org", "acme.com", "--from", "x@example.test", "--api-key-file", "-"}, {"update", "s1", "--api-key-file", "-"}} {
			op := "CreateResendSender"
			if args[0] == "update" {
				op = "UpdateResendSender"
			}
			srv, _ := captureGraphQL(t, map[string]string{op: fmt.Sprintf(`{"errors":[{"message":"synthetic-key-1342 leaked backend diagnostic","extensions":{"code":%q}}]}`, code)})
			f, out := testFactory(t)
			f.IOStreams.In = strings.NewReader("synthetic-key-1342\n")
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"org", "resend-sender"}, args...), "--json", "--server", srv.URL))
			err := root.Execute()
			if err == nil {
				t.Fatal("expected refusal")
			}
			want := 2
			if code == "FORBIDDEN" {
				want = 8
			}
			if code == "UNAUTHENTICATED" {
				want = 3
			}
			var coded interface{ ServerCode() string }
			if exitCodeFor(err) != want || !errors.As(err, &coded) || coded.ServerCode() != code {
				t.Fatalf("classification %v", err)
			}
			if strings.Contains(err.Error()+out.String(), "synthetic-key-1342") {
				t.Fatal("key leaked")
			}
		}
	}
}

func TestResend1342RefusalsBeforeWrite(t *testing.T) {
	for _, args := range [][]string{
		{"rm", "s1"}, {"update", "s1"}, {"update", "s1", "--api-key-file", ""}, {"create", "--org", " ", "--from", "x@example.test"}, {"create", "--org", "acme.com", "--from", " "}, {"create", "--org", "acme.com", "--from", "x@example.test", "--api-key-file", "-"}, {"list", "--limit", "-1"}, {"list", "--offset", "-1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, `{"data":{}}`) }))
			defer srv.Close()
			f, _ := testFactory(t)
			f.IOStreams.In = strings.NewReader("")
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"org", "resend-sender"}, args...), "--server", srv.URL))
			if err := root.Execute(); err == nil {
				t.Fatal("expected local refusal")
			}
			if calls != 0 {
				t.Fatalf("%d network calls", calls)
			}
		})
	}
}

func TestResend1342EmptyAndNull(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		op, body string
		exit     int
		empty    bool
	}{
		{[]string{"list"}, "ListResendSenders", `{"resendSenders":{"items":[],"total":0}}`, 0, true},
		{[]string{"get", "missing"}, "GetResendSender", `{"resendSender":null}`, 4, false},
		{[]string{"rm", "s1", "--yes"}, "DeleteResendSender", `{"deleteResendSender":false}`, 1, false},
		{[]string{"update", "s1", "--enabled=false"}, "UpdateResendSender", `{"updateResendSender":null}`, 1, false},
	} {
		t.Run(tc.op, func(t *testing.T) {
			srv, _ := captureGraphQL(t, map[string]string{tc.op: `{"data":` + tc.body + `}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"org", "resend-sender"}, tc.args...), "--json", "--server", srv.URL))
			err := root.Execute()
			if exitCodeFor(err) != tc.exit {
				t.Fatalf("err %v", err)
			}
			if tc.empty && strings.TrimSpace(out.String()) != "[]" {
				t.Fatalf("empty %s", out.String())
			}
		})
	}
}

func TestResend1342Pagination(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.Variables["offset"] != float64(calls) {
			t.Errorf("offset %v", req.Variables)
		}
		calls++
		fmt.Fprintf(w, `{"data":{"resendSenders":{"items":[%s],"total":2}}}`, strings.ReplaceAll(sender1342, "sender1", fmt.Sprintf("sender%d", calls)))
	}))
	defer srv.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "resend-sender", "list", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	_ = json.Unmarshal([]byte(out.String()), &items)
	if calls != 2 || len(items) != 2 {
		t.Fatalf("calls %d items %v", calls, items)
	}
}

func TestPlatformInvite1342OmittedDefaults(t *testing.T) {
	srv, captured := captureGraphQL(t, map[string]string{"InviteUser": `{"data":{"inviteUser":{"id":"i1","slug":"s","memberRole":"READER","activationCount":0,"createdAt":"now"}}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "invite", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["InviteUser"], &vars)
	if len(vars) != 0 {
		t.Fatalf("defaults forced: %v", vars)
	}
}

func TestResend1342KeyFileAndReflection(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "resend.key")
	if err := os.WriteFile(keyFile, []byte("synthetic-key-1342\n"), 0600); err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(sender1342, "robot@example.test", "synthetic-key-1342")
	srv, captured := captureGraphQL(t, map[string]string{"CreateResendSender": `{"data":{"createResendSender":` + body + `}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "resend-sender", "create", "--org", "acme.com", "--from", "Robot <robot@example.test>", "--api-key-file", keyFile, "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateResendSender"], &vars)
	if vars["apiKey"] != "synthetic-key-1342" {
		t.Fatal("file not read")
	}
	if _, exists := vars["enabled"]; exists {
		t.Fatal("enabled default must be omitted")
	}
	if strings.Contains(out.String(), "synthetic-key-1342") || !strings.Contains(out.String(), "[REDACTED]") {
		t.Fatal("reflected key not redacted")
	}
}

func TestResend1342ExplicitEmptyKeySource(t *testing.T) {
	srv, captured := captureGraphQL(t, map[string]string{"UpdateResendSender": `{"data":{"updateResendSender":` + sender1342 + `}}`})
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader("synthetic-key-1342\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "resend-sender", "update", "s1", "--api-key-file=", "--server", srv.URL})
	if err := root.Execute(); exitCodeFor(err) != 2 {
		t.Fatalf("want usage: %v", err)
	}
	if len(captured) != 0 {
		t.Fatal("blank file source fell back to stdin")
	}
}

func TestPlatformInvite1342InvalidBounds(t *testing.T) {
	for _, flag := range []string{"--expires-in-days", "--max-activations"} {
		srv, captured := captureGraphQL(t, map[string]string{})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"user", "invite", flag, "-1", "--server", srv.URL})
		if err := root.Execute(); exitCodeFor(err) != 2 {
			t.Fatalf("negative bound: %v", err)
		}
		if len(captured) != 0 {
			t.Fatal("negative bound made a request")
		}
	}
}

func TestResend1342ReflectedErrorCode(t *testing.T) {
	srv, _ := captureGraphQL(t, map[string]string{"UpdateResendSender": `{"errors":[{"message":"failed","extensions":{"code":"synthetic-key-1342"}}]}`})
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader("synthetic-key-1342\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "resend-sender", "update", "s1", "--api-key-file", "-", "--json", "--server", srv.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("want failure")
	}
	if code := renderError(f, err); code != 1 {
		t.Fatalf("code %d", code)
	}
	if strings.Contains(out.String(), "synthetic-key-1342") {
		t.Fatal("key leaked through error code")
	}
}
