package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#1344: account deletion, hiding revoked API keys, impersonation audit
// reads. Each case drives the real command tree against a fake GraphQL server
// and asserts the request the server received, not just the output.

type run1344 struct {
	out  string
	err  error
	vars map[string]json.RawMessage
	op   string
}

func exec1344(t *testing.T, responses map[string]string, op string, args ...string) run1344 {
	t.Helper()
	gql, calls := captureGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", gql.URL))
	err := root.Execute()
	r := run1344{out: out.String(), err: err, op: op, vars: map[string]json.RawMessage{}}
	if raw, ok := calls[op]; ok {
		_ = json.Unmarshal(raw, &r.vars)
	}
	if _, ok := calls[op]; !ok {
		r.vars = nil
	}
	return r
}

func TestUserDelete1344(t *testing.T) {
	ok := map[string]string{"DeleteUser": `{"data":{"deleteUser":true}}`}

	t.Run("own account sends neither ref nor reason", func(t *testing.T) {
		r := exec1344(t, ok, "DeleteUser", "user", "delete", "--yes", "--json")
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.vars == nil {
			t.Fatal("deleteUser was never sent")
		}
		if _, has := r.vars["userRef"]; has {
			t.Errorf("own-account delete sent userRef: %v", r.vars)
		}
		if _, has := r.vars["reason"]; has {
			t.Errorf("no --reason but reason sent: %v", r.vars)
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.out), &got); err != nil {
			t.Fatal(err)
		}
		if string(got["deleted"]) != "true" || string(got["user"]) != "null" {
			t.Errorf("json: %s", r.out)
		}
	})

	t.Run("named user and reason are sent", func(t *testing.T) {
		r := exec1344(t, ok, "DeleteUser", "user", "delete", "--user", "@old", "--reason", "GDPR request 42", "--yes", "--json")
		if r.err != nil {
			t.Fatal(r.err)
		}
		if string(r.vars["userRef"]) != `"@old"` || string(r.vars["reason"]) != `"GDPR request 42"` {
			t.Errorf("variables: %v", r.vars)
		}
		if !strings.Contains(r.out, `"user": "@old"`) {
			t.Errorf("json: %s", r.out)
		}
	})

	t.Run("non-interactive without --yes refuses before any request", func(t *testing.T) {
		r := exec1344(t, ok, "DeleteUser", "user", "delete")
		if r.err == nil || exitCodeFor(r.err) != 2 {
			t.Fatalf("want usage exit 2, got %v", r.err)
		}
		if r.vars != nil {
			t.Fatalf("a deletion was sent without confirmation: %v", r.vars)
		}
	})

	t.Run("empty --user or --reason is a usage error and sends nothing", func(t *testing.T) {
		for _, args := range [][]string{
			{"user", "delete", "--user", "", "--yes"},
			{"user", "delete", "--reason", "", "--yes"},
		} {
			r := exec1344(t, ok, "DeleteUser", args...)
			if r.err == nil || exitCodeFor(r.err) != 2 || r.vars != nil {
				t.Errorf("%v: err=%v sent=%v", args, r.err, r.vars)
			}
		}
	})

	t.Run("server refusal keeps its code and is not rewritten", func(t *testing.T) {
		forbidden := map[string]string{"DeleteUser": `{"data":null,"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}]}`}
		r := exec1344(t, forbidden, "DeleteUser", "user", "delete", "--user", "someone", "--reason", "x", "--yes")
		if r.err == nil || exitCodeFor(r.err) != 8 {
			t.Fatalf("want exit 8, got %v", r.err)
		}
		if strings.Contains(strings.ToLower(r.err.Error()), "not found") || strings.Contains(strings.ToLower(r.err.Error()), "exist") {
			t.Errorf("refusal leaks existence: %v", r.err)
		}
		blocked := map[string]string{"DeleteUser": `{"data":null,"errors":[{"message":"sole owner","extensions":{"code":"SOLE_ORG_OWNER"}}]}`}
		r = exec1344(t, blocked, "DeleteUser", "user", "delete", "--yes", "--json")
		if r.err == nil || !strings.Contains(r.err.Error(), "sole owner") {
			t.Errorf("SOLE_ORG_OWNER message lost: %v", r.err)
		}
	})

	t.Run("a false answer is not success", func(t *testing.T) {
		r := exec1344(t, map[string]string{"DeleteUser": `{"data":{"deleteUser":false}}`}, "DeleteUser", "user", "delete", "--yes")
		if r.err == nil {
			t.Fatal("deleteUser:false reported success")
		}
	})
}

const keyRow1344 = `{"id":"k1","label":"ci","keyPreview":"hdr_ab1","issuedVia":null,"createdAt":"2026-01-01T00:00:00Z","lastUsedAt":null,"revokedAt":"2026-02-01T00:00:00Z","hiddenAt":%s}`

func keyJSON1344(hidden string) string { return strings.Replace(keyRow1344, "%s", hidden, 1) }

func TestAuthTokenHide1344(t *testing.T) {
	for _, tc := range []struct {
		verb   string
		hidden bool
		stamp  string
		word   string
	}{
		{"hide", true, `"2026-03-01T00:00:00Z"`, "Hid"},
		{"unhide", false, `null`, "Unhid"},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			resp := map[string]string{"SetUserApiKeyHidden": `{"data":{"setUserApiKeyHidden":` + keyJSON1344(tc.stamp) + `}}`}
			r := exec1344(t, resp, "SetUserApiKeyHidden", "auth", "token", tc.verb, "k1")
			if r.err != nil {
				t.Fatal(r.err)
			}
			want := "false"
			if tc.hidden {
				want = "true"
			}
			if string(r.vars["id"]) != `"k1"` || string(r.vars["hidden"]) != want {
				t.Errorf("variables: %v", r.vars)
			}
			if !strings.Contains(r.out, tc.word+" API token k1") {
				t.Errorf("text: %s", r.out)
			}
			j := exec1344(t, resp, "SetUserApiKeyHidden", "auth", "token", tc.verb, "k1", "--json")
			var got map[string]json.RawMessage
			if err := json.Unmarshal([]byte(j.out), &got); err != nil {
				t.Fatal(err)
			}
			if string(got["hiddenAt"]) != tc.stamp {
				t.Errorf("hiddenAt %s, want %s", got["hiddenAt"], tc.stamp)
			}
			if _, leaked := got["rawKey"]; leaked {
				t.Error("hide must never carry key material")
			}
		})
	}

	t.Run("hiding an active key keeps the server's refusal", func(t *testing.T) {
		resp := map[string]string{"SetUserApiKeyHidden": `{"data":null,"errors":[{"message":"key is active","extensions":{"code":"BAD_USER_INPUT","reason":"KEY_ACTIVE"}}]}`}
		r := exec1344(t, resp, "SetUserApiKeyHidden", "auth", "token", "hide", "k1")
		if r.err == nil || exitCodeFor(r.err) != 2 || !strings.Contains(r.err.Error(), "key is active") {
			t.Fatalf("want usage exit with the server's message, got %v", r.err)
		}
	})

	t.Run("a foreign or unknown id is exit 4", func(t *testing.T) {
		resp := map[string]string{"SetUserApiKeyHidden": `{"data":null,"errors":[{"message":"not found","extensions":{"code":"NOT_FOUND"}}]}`}
		r := exec1344(t, resp, "SetUserApiKeyHidden", "auth", "token", "hide", "k1")
		if r.err == nil || exitCodeFor(r.err) != 4 {
			t.Fatalf("want exit 4, got %v", r.err)
		}
	})
}

func TestAuthTokenListIncludeHidden1344(t *testing.T) {
	resp := map[string]string{"MyUserApiKeys": `{"data":{"myUserApiKeys":[` + keyJSON1344(`"2026-03-01T00:00:00Z"`) + `]}}`}

	t.Run("default omits includeHidden", func(t *testing.T) {
		r := exec1344(t, resp, "MyUserApiKeys", "auth", "token", "list", "--json")
		if r.err != nil {
			t.Fatal(r.err)
		}
		if _, has := r.vars["includeHidden"]; has {
			t.Errorf("default list sent includeHidden: %v", r.vars)
		}
	})
	t.Run("flag sends includeHidden true and shows hiddenAt", func(t *testing.T) {
		r := exec1344(t, resp, "MyUserApiKeys", "auth", "token", "list", "--include-hidden", "--json")
		if r.err != nil {
			t.Fatal(r.err)
		}
		if string(r.vars["includeHidden"]) != "true" {
			t.Errorf("variables: %v", r.vars)
		}
		if !strings.Contains(r.out, `"hiddenAt": "2026-03-01T00:00:00Z"`) {
			t.Errorf("hiddenAt missing: %s", r.out)
		}
		txt := exec1344(t, resp, "MyUserApiKeys", "auth", "token", "list", "--include-hidden")
		if !strings.Contains(txt.out, "revoked (hidden)") {
			t.Errorf("status: %s", txt.out)
		}
	})
}

const session1344 = `{"id":"s1","createdAt":"2026-03-01T00:00:00Z","expiresAt":"2026-03-01T01:00:00Z","endedAt":null,"endedBy":null,"isActive":true,"reason":"support",
 "adminUser":{"id":"a1","handle":"ada","name":"Ada"},"targetUser":{"id":"u1","handle":null,"name":"Uma"},"organization":{"id":"o1","urn":"acme.com","name":"Acme"}}`

func TestAdminImpersonationList1344(t *testing.T) {
	resp := map[string]string{"ImpersonationSessions": `{"data":{"impersonationSessions":{"total":1,"items":[` + session1344 + `]}}}`}

	t.Run("org and active are sent", func(t *testing.T) {
		r := exec1344(t, resp, "ImpersonationSessions", "admin", "impersonation", "list", "--org", "acme.com", "--active", "--json")
		if r.err != nil {
			t.Fatal(r.err)
		}
		if string(r.vars["orgId"]) != `"acme.com"` || !strings.Contains(string(r.vars["filter"]), `"activeOnly":true`) {
			t.Errorf("variables: %v", r.vars)
		}
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.out), &rows); err != nil || len(rows) != 1 {
			t.Fatalf("rows: %v %s", err, r.out)
		}
		for _, k := range []string{"id", "adminUser", "targetUser", "organization", "reason", "createdAt", "expiresAt", "endedAt", "endedBy", "isActive"} {
			if _, ok := rows[0][k]; !ok {
				t.Errorf("missing key %s", k)
			}
		}
		if string(rows[0]["endedAt"]) != "null" {
			t.Errorf("endedAt must be explicit null: %s", rows[0]["endedAt"])
		}
	})

	t.Run("unset flags are absent, not null", func(t *testing.T) {
		r := exec1344(t, resp, "ImpersonationSessions", "admin", "impersonation", "list", "--json")
		if r.err != nil {
			t.Fatal(r.err)
		}
		for _, k := range []string{"orgId", "filter"} {
			if _, has := r.vars[k]; has {
				t.Errorf("%s sent without its flag: %v", k, r.vars)
			}
		}
	})

	t.Run("empty is [] and a refusal is an error", func(t *testing.T) {
		empty := map[string]string{"ImpersonationSessions": `{"data":{"impersonationSessions":{"total":0,"items":[]}}}`}
		r := exec1344(t, empty, "ImpersonationSessions", "admin", "impersonation", "list", "--json")
		if r.err != nil || strings.TrimSpace(r.out) != "[]" {
			t.Fatalf("empty: %v %q", r.err, r.out)
		}
		refused := map[string]string{"ImpersonationSessions": `{"data":null,"errors":[{"message":"name an org","extensions":{"code":"FORBIDDEN"}}]}`}
		r = exec1344(t, refused, "ImpersonationSessions", "admin", "impersonation", "list")
		if r.err == nil || exitCodeFor(r.err) != 8 || r.out != "" {
			t.Fatalf("refusal: %v %q", r.err, r.out)
		}
	})

	t.Run("explicit paging sends exactly that page", func(t *testing.T) {
		r := exec1344(t, resp, "ImpersonationSessions", "admin", "impersonation", "list", "--limit", "5", "--offset", "10", "--json")
		if r.err != nil {
			t.Fatal(r.err)
		}
		if string(r.vars["limit"]) != "5" || string(r.vars["offset"]) != "10" {
			t.Errorf("variables: %v", r.vars)
		}
	})
}

func TestAdminImpersonationGet1344(t *testing.T) {
	r := exec1344(t, map[string]string{"ImpersonationSession": `{"data":{"impersonationSession":` + session1344 + `}}`}, "ImpersonationSession", "admin", "impersonation", "get", "s1", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if string(r.vars["ref"]) != `"s1"` {
		t.Errorf("variables: %v", r.vars)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || string(got["id"]) != `"s1"` {
		t.Fatalf("%v %s", err, r.out)
	}
	// Denied and missing are the same null: one NotFound, saying nothing more.
	n := exec1344(t, map[string]string{"ImpersonationSession": `{"data":{"impersonationSession":null}}`}, "ImpersonationSession", "admin", "impersonation", "get", "s9")
	if n.err == nil || exitCodeFor(n.err) != 4 {
		t.Fatalf("null session: %v", n.err)
	}
}
