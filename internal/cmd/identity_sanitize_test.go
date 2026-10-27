package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#1104: the identity and access command tables render server-supplied
// cells; text sanitizes them, --json keeps the originals.

func userSanitizeRow(id, name string) string {
	return `{"id":` + jsonText(id) + `,"name":` + jsonText(name) + `,"email":` + jsonText(name+"@acme.com") +
		`,"handle":` + jsonText(name) + `,"githubUsername":null,"roles":["USER"],"identityProvider":"github","githubId":4242,"externalId":"gh|4242"}`
}

func TestUserLsSanitizesCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"SearchUsers": `{"data":{"users":{"total":2,"items":[` +
			userSanitizeRow("u1", hostileCellLabel) + `,` + userSanitizeRow("u2", longCellText) + `]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "search", "x", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	assertSafeTableText(t, out.String(), 2, 3)
}

func TestUserLsJSONKeepsTheOriginals(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"SearchUsers": `{"data":{"users":{"total":1,"items":[` + userSanitizeRow("u1", hostileCellLabel) + `]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "search", "x", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var users []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0]["name"] != hostileCellLabel {
		t.Errorf("--json must round-trip the original values, got %v", users)
	}
}

func grantSanitizeRow(id, handle string) string {
	return `{"id":` + jsonText(id) + `,"principalType":"USER","principalId":"u2","principalHandle":` + jsonText(handle) +
		`,"organizationId":"org1","organizationUrn":"acme.com","actions":["memory.clone"],"expiresAt":null,"createdAt":"2026-07-10T00:00:00Z"}`
}

func TestGrantLsSanitizesCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"PrincipalGrants": `{"data":{"principalGrants":{"total":2,"items":[` +
			grantSanitizeRow("pg1", hostileCellLabel) + `,` + grantSanitizeRow(longCellText, "jane") + `]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"grant", "ls", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	assertSafeTableText(t, out.String(), 2, 1)
}

func TestGrantLsJSONKeepsTheOriginals(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"PrincipalGrants": `{"data":{"principalGrants":{"total":1,"items":[` + grantSanitizeRow("pg1", hostileCellLabel) + `]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"grant", "ls", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var grants []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &grants); err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 || grants[0]["principalHandle"] != hostileCellLabel {
		t.Errorf("--json must round-trip the original values, got %v", grants)
	}
}

func appSanitizeRow(id, name, urn string) string {
	return `{"id":` + jsonText(id) + `,"urn":` + jsonText(urn) + `,"name":` + jsonText(name) +
		`,"appType":"CHATBOT","agentId":"agent1","memberCount":2,"createdAt":"2026-06-11T00:00:00Z"}`
}

func TestAppLsSanitizesCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"Apps": `{"data":{"apps":{"total":2,"items":[` +
			appSanitizeRow("a1", hostileCellLabel, "hrn:app:acme.com:eng") + `,` +
			appSanitizeRow("a2", "Eng", longCellText) + `]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "ls", "--org", "acme.com", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	assertSafeTableText(t, out.String(), 2, 1)
}

func TestAppInstallSanitizesCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"CreateApp": `{"data":{"createApp":` + appSanitizeRow("a1", hostileCellLabel, longCellText) + `}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "install", "--org", "acme.com", "--agent", "agent1",
		"--name", "Eng", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	// One row, no header (output.NewTable with no headers).
	assertSafeTableText(t, out.String(), 0, 1)
	if !strings.Contains(out.String(), "✓ installed") {
		t.Errorf("the static status label must survive:\n%s", out)
	}
}

func TestAppMemberLsSanitizesUnguardedCellsInText(t *testing.T) {
	// Every value cell is SafeCell now, HANDLE and ROLE included (cli#1110,
	// Charles's QA probe): the old memberText/memberCell quoting guard only
	// caught C0/C1 controls — Cf bidi overrides and Zl/Zp separators passed
	// raw, and quoting never bounded length.
	gql := fakeGraphQL(t, map[string]string{
		"AppMembers": `{"data":{"app":{"id":"a1","urn":"hrn:app:acme.com:eng","members":[` +
			`{"role":` + jsonText(hostileCellLabel) + `,"createdAt":` + jsonText(hostileCellLabel) + `,"user":{"id":` + jsonText(longCellText) + `,"handle":` + jsonText(hostileCellLabel) + `,"urn":null}},` +
			`{"role":"owner","createdAt":"2026-10-02T00:00:00Z","user":{"id":"usr2","handle":null,"urn":null}}]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "member", "ls", "--app", "capp100000000000000000000", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	// context line + header + two member rows
	text := out.String()
	assertSafeTableText(t, text, 3, 1)
	if !strings.Contains(text, cutCellText) {
		t.Errorf("the long ID cell must render in the cut form:\n%s", text)
	}
	assertNoRawHostile(t, text)
}

// Charles's cli#1110 QA finding, pinned: a HANDLE or ROLE carrying bidi or
// separator runes must not survive into the table — the quoting guard let
// exactly those classes through, uncut.
func TestAppMemberLsNeutralizesBidiAndSeparatorCells(t *testing.T) {
	bidi := "a\u202eb"
	sep := "c\u2028d"
	gql := fakeGraphQL(t, map[string]string{
		"AppMembers": `{"data":{"app":{"id":"a1","urn":"hrn:app:acme.com:eng","members":[` +
			`{"role":` + jsonText(sep) + `,"createdAt":"2026-10-02T00:00:00Z",` +
			`"user":{"id":"usr1","handle":` + jsonText(bidi) + `,"urn":null}}]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "member", "ls", "--app", "capp100000000000000000000", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	assertSafeTableText(t, text, 2, 0)
	for _, raw := range []string{"\u202e", "\u2028"} {
		if strings.Contains(text, raw) {
			t.Errorf("the hostile cell must not survive into the table (got %q):\n%s", raw, text)
		}
	}
}

func appKeyScopesSanitizeResp(scope, host string) string {
	return `{"data":{"grantAppKeyScope":{"id":"key1","appId":"capp1","label":"orca1 host","revokedAt":null,` +
		`"scopes":[{"scope":` + jsonText(scope) + `,"hostId":` + jsonText(host) + `,"grantedAt":"2026-10-14T00:00:00Z","grantedBy":"u1"}]}}}`
}

func TestAppKeyGrantSanitizesScopeCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GrantAppKeyScope": appKeyScopesSanitizeResp(hostileCellLabel, longCellText),
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "key", "scope", "grant", "key1", "host:operate", "--host-id", "orca1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	// The ✓ line plus a two-line table (header + one scope row).
	text := out.String()
	assertSafeTableText(t, text, -1, 1)
	assertDetailLineCount(t, text, 3)
	if strings.Contains(text, strings.Repeat("日", 257)) {
		t.Errorf("a long value was rendered uncut:\n%s", text)
	}
}

func tokenSanitizeRow(id, label string) string {
	return `{"id":` + jsonText(id) + `,"label":` + jsonText(label) + `,"keyPreview":` + jsonText(label) +
		`,"issuedVia":"cli","createdAt":"2026-06-19T00:00:00Z","lastUsedAt":null,"revokedAt":null}`
}

func TestAuthTokenLsSanitizesCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"MyUserApiKeys": `{"data":{"myUserApiKeys":[` +
			tokenSanitizeRow("uak_1", hostileCellLabel) + `,` + tokenSanitizeRow("uak_2", longCellText) + `]}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "ls", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	assertSafeTableText(t, out.String(), 2, 2)
}

func TestAuthTokenLsJSONKeepsTheOriginals(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"MyUserApiKeys": `{"data":{"myUserApiKeys":[` + tokenSanitizeRow("uak_1", hostileCellLabel) + `]}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "ls", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var tokens []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &tokens); err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0]["label"] != hostileCellLabel {
		t.Errorf("--json must round-trip the original values, got %v", tokens)
	}
}

func TestAuthTokenValidateSanitizesDetailTail(t *testing.T) {
	hostile := strings.ReplaceAll(authCtxUserJSON, "Holger", jsonEscaped(hostileCellLabel))
	hostile = strings.ReplaceAll(hostile, "hdrk_ab1", jsonEscaped(longCellText))
	gql := fakeGraphQL(t, map[string]string{"AuthContext": hostile})
	defer gql.Close()
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader("hdr_user_secret123\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "validate", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	assertSafeTableText(t, out.String(), -1, 1)
	assertDetailLineCount(t, out.String(), 2)
	if !strings.Contains(out.String(), "Token is valid") {
		t.Errorf("the validation verdict must survive:\n%s", out)
	}
}

func TestAccessCheckSanitizesDetailAndGrants(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		// the user reference resolves through a search before the check
		"SearchUsers": `{"data":{"users":{"total":1,"items":[` +
			`{"id":"u1","name":"Alice","email":"alice@acme.com","handle":"alice","githubUsername":null,"roles":["USER"]}]}}}`,
		"EffectiveAccess": `{"data":{"effectiveAccess":{
			"user":{"id":"u` + jsonEscaped(longCellText) + `","name":"` + jsonEscaped(hostileCellLabel) + `","email":"a@acme.com","handle":"alice"},
			"resourceUrn":"hrn:memory:acme.com::kb","resourceKind":"memory",
			"canRead":true,"canWrite":false,"canManage":false,"canDelete":false,
			"role":"` + jsonEscaped(hostileCellLabel) + `","grants":[
				{"source":"` + jsonEscaped(hostileCellLabel) + `","role":"reader","via":null},
				{"source":"` + jsonEscaped(longCellText) + `","role":"writer","via":"team"}]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"access", "check", "alice@acme.com", "hrn:mem:acme.com:kb", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	// 3 detail lines + blank + caps (header/row) + blank + "Grants:" + table
	// (header + 2 rows) = 11 lines, none of them forgeable.
	assertSafeTableText(t, out.String(), 10, 1)
}

// Chip's cli#1110 QA P3, pinned: an explicit-empty ROLE renders blank, not
// an em-dash — memberText rendered it blank at 5face16 and the rendered
// bytes are contract.
func TestAppMemberLsEmptyRoleRendersBlank(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"AppMembers": `{"data":{"app":{"id":"a1","urn":"hrn:app:acme.com:eng","members":[` +
			`{"role":"","createdAt":"2026-10-02T00:00:00Z","user":{"id":"usr1","handle":"ada","urn":null}}]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "member", "ls", "--app", "capp100000000000000000000", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	assertSafeTableText(t, text, 2, 0)
	if strings.Contains(text, "—") {
		t.Errorf("an explicit-empty ROLE must stay blank, not an em-dash:\n%s", text)
	}
}
