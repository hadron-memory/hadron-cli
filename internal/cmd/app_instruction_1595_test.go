package cmd

import (
	"encoding/json"
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
		{"AppKeysMetadata", `{"appKeys":[]}`, []string{"app", "key", "list", "acme.com:team"}},
		{"AppKeysScopes", `{"appKeys":[]}`, []string{"app", "key", "list", "acme.com:team", "--scopes"}},
		{"AppKeysScopesHostUses", `{"appKeys":[]}`, []string{"app", "key", "list", "acme.com:team", "--scopes", "--host-uses", "2"}},
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
			if c.Op != op || c.Session == "" || !reflect.DeepEqual(c.Vars["instruction"], want) {
				t.Fatalf("wire %+v", c)
			}
			if strings.Contains(c.Query, "app { keys") {
				t.Fatal("nested App.keys")
			}
		})
	}
}
func TestCredential1595LegacyWire(t *testing.T) {
	for _, tc := range credentialCases1595() {
		t.Run(tc.op, func(t *testing.T) {
			var previous attnCall
			for _, bound := range []bool{false, true} {
				calls, r := (run1592{bound: bound, response: map[string]string{tc.op: `{"data":` + tc.body + `}`}}).exec(t, append(tc.args, "--json")...)
				if r.err != nil || len(*calls) != 1 {
					t.Fatalf("%v %v", r.err, *calls)
				}
				c := (*calls)[0]
				if c.Session != "" || c.Vars["instruction"] != nil {
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
				var e map[string]any
				_ = json.Unmarshal([]byte(r.errOut), &e)
			})
		}
	}
}
