package cmd

import (
	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"os"
	"reflect"
	"strings"
	"testing"
)

type credentialCase1595 struct {
	op, body string
	args     []string
}

func credentialCases1595() []credentialCase1595 {
	grant := strings.Replace(grant1338, `"revokedAt":null`, `"revokedAt":"2026-11-03T00:00:00Z"`, 1)
	return []credentialCase1595{
		{"AppKeysMetadata", `{"appKeysPage":{"items":[],"total":0,"hasMore":false}}`, []string{"app", "key", "list", "acme.com:team"}},
		{"AppKeysScopes", `{"appKeysPage":{"items":[],"total":0,"hasMore":false}}`, []string{"app", "key", "list", "acme.com:team", "--scopes"}},
		{"AppKeysScopesHostUses", `{"appKeysPage":{"items":[],"total":0,"hasMore":false}}`, []string{"app", "key", "list", "acme.com:team", "--scopes", "--host-uses", "2"}},
		{"RevokeAppKey", `{"revokeAppKey":true}`, []string{"app", "key", "revoke", "key1", "--yes"}},
		{"RevokeAppKeyScope", `{"revokeAppKeyScope":{"id":"key1","appId":"a1","label":null,"revokedAt":null,"scopes":[]}}`, []string{"app", "key", "scope", "revoke", "key1", "host:operate"}},
		{"AppMemoryWriteGrants", `{"appMemoryWriteGrants":[]}`, []string{"app", "memory-grant", "list", "acme.com:team"}},
		{"RevokeAppMemoryWriteGrant", `{"revokeAppMemoryWriteGrant":` + grant + `}`, []string{"app", "memory-grant", "rm", "g1", "--yes"}},
	}
}
func TestCredential1595InstructionWire(t *testing.T) {
	for _, tc := range credentialCases1595() {
		t.Run(tc.op, func(t *testing.T) {
			op := tc.op + "Instructed"
			calls, r := (run1592{bound: true, response: map[string]string{op: `{"data":` + tc.body + `}`}}).exec(t, append(tc.args, "--instruction", "task:"+taskRef1592, "--json")...)
			if r.err != nil {
				t.Fatal(r.err)
			}
			if len(*calls) != 1 {
				t.Fatalf("calls %v", *calls)
			}
			c := (*calls)[0]
			want := map[string]any{"kind": "TASK", "ref": taskRef1592}
			if c.Op != op || c.Session != "s-new" || !reflect.DeepEqual(c.Vars["instruction"], want) {
				t.Fatalf("wire %+v", c)
			}
			if strings.Contains(c.Query, "app { keys") {
				t.Fatal("nested App.keys")
			}
		})
	}
}
func TestCredential1595OrdinaryWire(t *testing.T) {
	for _, tc := range credentialCases1595() {
		t.Run(tc.op, func(t *testing.T) {
			var previous attnCall
			for _, bound := range []bool{false, true} {
				calls, r := (run1592{bound: bound, response: map[string]string{tc.op: `{"data":` + tc.body + `}`}}).exec(t, append(tc.args, "--json")...)
				if r.err != nil || len(*calls) != 1 {
					t.Fatalf("%v %v", r.err, *calls)
				}
				c := (*calls)[0]
				if c.Query != ordinaryDocuments1595[tc.op] {
					t.Fatal("ordinary query differs from pinned document (paged keys in #1547)")
				}
				_, hasInstruction := c.Vars["instruction"]
				if c.Session != "" || hasInstruction {
					t.Fatalf("legacy attributed %+v", c)
				}
				if bound && (c.Query != previous.Query || string(c.RawVars) != string(previous.RawVars)) {
					t.Fatal("legacy wire changed")
				}
				previous = c
			}
		})
	}
}
func TestCredential1595MalformedBeforeRequests(t *testing.T) {
	for _, tc := range credentialCases1595() {
		for _, raw := range []string{"task:", "TASK:ref", "live-chat:  ", "bad"} {
			t.Run(tc.op+raw, func(t *testing.T) {
				calls, r := (run1592{bound: true, response: map[string]string{}}).exec(t, append(tc.args, "--instruction", raw, "--json")...)
				if exitCodeFor(r.err) != 2 || len(*calls) != 0 {
					t.Fatalf("%v %+v", r.err, *calls)
				}
			})
		}
	}
}

var ordinaryDocuments1595 = map[string]string{
	"AppKeysMetadata":           "\nquery AppKeysMetadata ($appRef: ID!, $limit: Int!, $offset: Int!) {\n\tappKeysPage(appRef: $appRef, limit: $limit, offset: $offset) {\n\t\thasMore\n\t\ttotal\n\t\titems {\n\t\t\tid\n\t\t\tkeyPreview\n\t\t\tlabel\n\t\t\tcreatedAt\n\t\t\tlastUsedAt\n\t\t\trevokedAt\n\t\t}\n\t}\n}\n",
	"AppKeysScopes":             "\nquery AppKeysScopes ($appRef: ID!, $limit: Int!, $offset: Int!) {\n\tappKeysPage(appRef: $appRef, limit: $limit, offset: $offset) {\n\t\thasMore\n\t\ttotal\n\t\titems {\n\t\t\tid\n\t\t\tkeyPreview\n\t\t\tlabel\n\t\t\tcreatedAt\n\t\t\tlastUsedAt\n\t\t\trevokedAt\n\t\t\tscopes {\n\t\t\t\tscope\n\t\t\t\thostId\n\t\t\t\tgrantedAt\n\t\t\t\tgrantedBy\n\t\t\t}\n\t\t}\n\t}\n}\n",
	"AppKeysScopesHostUses":     "\nquery AppKeysScopesHostUses ($appRef: ID!, $first: Int!, $limit: Int!, $offset: Int!) {\n\tappKeysPage(appRef: $appRef, limit: $limit, offset: $offset) {\n\t\thasMore\n\t\ttotal\n\t\titems {\n\t\t\tid\n\t\t\tkeyPreview\n\t\t\tlabel\n\t\t\tcreatedAt\n\t\t\tlastUsedAt\n\t\t\trevokedAt\n\t\t\tscopes {\n\t\t\t\tscope\n\t\t\t\thostId\n\t\t\t\tgrantedAt\n\t\t\t\tgrantedBy\n\t\t\t}\n\t\t\thostUses(first: $first) {\n\t\t\t\thostId\n\t\t\t\taction\n\t\t\t\tcount\n\t\t\t\toperationId\n\t\t\t\trefusedCode\n\t\t\t\tusedAt\n\t\t\t}\n\t\t}\n\t}\n}\n",
	"RevokeAppKey":              "\nmutation RevokeAppKey ($id: ID!) {\n\trevokeAppKey(id: $id)\n}\n",
	"RevokeAppKeyScope":         "\nmutation RevokeAppKeyScope ($id: ID!, $scope: String!) {\n\trevokeAppKeyScope(id: $id, scope: $scope) {\n\t\t... AppKeyScopesFields\n\t}\n}\nfragment AppKeyScopesFields on AppKey {\n\tid\n\tappId\n\tlabel\n\trevokedAt\n\tscopes {\n\t\tscope\n\t\thostId\n\t\tgrantedAt\n\t\tgrantedBy\n\t}\n}\n",
	"AppMemoryWriteGrants":      "\nquery AppMemoryWriteGrants ($appRef: String!, $includeRevoked: Boolean) {\n\tappMemoryWriteGrants(appRef: $appRef, includeRevoked: $includeRevoked) {\n\t\t... AppMemoryGrantFields\n\t}\n}\nfragment AppMemoryGrantFields on AppMemoryWriteGrant {\n\tid\n\tappId\n\tmemoryUrn\n\tprefix\n\tregisterNodeUrn\n\tgrantedBy\n\tcreatedAt\n\trevokedAt\n\trevokedBy\n\tnotEffective\n\tnotEffectiveReason\n}\n",
	"RevokeAppMemoryWriteGrant": "\nmutation RevokeAppMemoryWriteGrant ($id: ID!) {\n\trevokeAppMemoryWriteGrant(id: $id) {\n\t\t... AppMemoryGrantFields\n\t}\n}\nfragment AppMemoryGrantFields on AppMemoryWriteGrant {\n\tid\n\tappId\n\tmemoryUrn\n\tprefix\n\tregisterNodeUrn\n\tgrantedBy\n\tcreatedAt\n\trevokedAt\n\trevokedBy\n\tnotEffective\n\tnotEffectiveReason\n}\n",
}

func TestCredential1595BindingRefusals(t *testing.T) {
	for _, tc := range credentialCases1595() {
		for _, mode := range []string{"unbound", "incomplete", "foreign", "corrupt"} {
			t.Run(tc.op+mode, func(t *testing.T) {
				o := run1592{bound: mode != "unbound", corrupt: mode == "corrupt", response: map[string]string{}}
				if mode == "incomplete" {
					o.mutate = func(b map[string]any) { delete(b, "workerId") }
				}
				if mode == "foreign" {
					o.mutate = func(b map[string]any) { b["server"] = "https://other.invalid" }
				}
				calls, r := o.exec(t, append(tc.args, "--instruction", "live-chat:client:message:7", "--json")...)
				if exitCodeFor(r.err) != 2 || len(*calls) != 0 {
					t.Fatalf("binding failed open: %v %+v", r.err, *calls)
				}
			})
		}
	}
}
func TestCredential1595RefusalNoDowngrade(t *testing.T) {
	for _, tc := range credentialCases1595() {
		t.Run(tc.op, func(t *testing.T) {
			op := tc.op + "Instructed"
			calls, r := (run1592{bound: true, response: map[string]string{op: `{"errors":[{"message":"owner required","extensions":{"code":"FORBIDDEN"}}]}`}}).exec(t, append(tc.args, "--instruction", "live-chat:client:message:7", "--json")...)
			if exitCodeFor(r.err) != 8 || len(*calls) != 1 || !strings.Contains(r.out, `"serverCode": "FORBIDDEN"`) {
				t.Fatalf("%v calls%+v envelope%s", r.err, *calls, r.out)
			}
			if (*calls)[0].Vars["instruction"].(map[string]any)["ref"] != "client:message:7" {
				t.Fatal("ref rewritten")
			}
		})
	}
}
func TestCredential1595PersonOnlyCommandsHaveNoFlag(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	for _, path := range [][]string{{"app", "key", "create"}, {"app", "key", "rotate"}, {"app", "key", "scope", "grant"}, {"app", "memory-grant", "add"}} {
		c, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		if c.Flags().Lookup("instruction") != nil {
			t.Fatalf("person-only %v got flag", path)
		}
	}
}

// Opening a credential store may block while another CLI rebinds the worktree.
// The explicit instruction must not adopt that successor or send the old session.
func TestCredential1595RebindDuringClientSetup(t *testing.T) {
	for _, tc := range credentialCases1595() {
		t.Run(tc.op, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{tc.op + "Instructed": `{"data":` + tc.body + `}`})
			teamGitDir(t)
			writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
			f, _ := testFactory(t)
			t.Setenv("HADRON_TOKEN", "")
			f.TokenStoreFn = func() store.Store {
				rewriteBinding1592(t, func(b map[string]any) { b["sessionId"] = "successor" })
				return memStore{auth.Host(srv.URL): "hdr_user_test"}
			}
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"--server", srv.URL}, tc.args...), "--instruction", "live-chat:client:7", "--json"))
			err := root.Execute()
			if exitCodeFor(err) != 2 || len(*calls) != 0 {
				t.Fatalf("rebind failed open: %v %+v", err, *calls)
			}
		})
	}
}
