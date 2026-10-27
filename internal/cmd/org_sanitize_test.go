package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#1104: org ls and org member ls render server-supplied cells; text
// sanitizes them, --json keeps the originals.

func orgSanitizeRow(id, urn, name string) string {
	return `{"id":` + jsonText(id) + `,"urn":` + jsonText(urn) + `,"name":` + jsonText(name) +
		`,"listedOnMarketplace":true,"createdAt":"2026-06-19T00:00:00Z","updatedAt":"2026-06-19T00:00:00Z"}`
}

func TestOrgLsSanitizesCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"Organizations": `{"data":{"organizations":{"total":2,"items":[` +
			orgSanitizeRow("o1", "acme.com", hostileCellLabel) + `,` +
			orgSanitizeRow("o2", longCellText, "Contoso") + `]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "ls", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	assertSafeTableText(t, out.String(), 2, 1)
}

func TestOrgLsJSONKeepsTheOriginals(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"Organizations": `{"data":{"organizations":{"total":1,"items":[` +
			orgSanitizeRow("o1", longCellText, hostileCellLabel) + `]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "ls", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var orgs []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &orgs); err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 1 || orgs[0]["name"] != hostileCellLabel || orgs[0]["urn"] != longCellText {
		t.Errorf("--json must round-trip the original values, got %v", orgs)
	}
}

func TestOrgLsTextKeepsOrdinaryValues(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"Organizations": `{"data":{"organizations":{"total":1,"items":[` + orgJSON + `]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "ls", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"org1", "acme.com", "Acme"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("ordinary value %q must pass unchanged:\n%s", want, out)
		}
	}
}

func orgMemberSanitizeRow(id, name, email string) string {
	return `{"id":` + jsonText(id) + `,"role":"ADMIN","canInvite":false,` +
		`"user":{"id":` + jsonText(id) + `,"name":` + jsonText(name) + `,"email":` + jsonText(email) +
		`,"handle":"alice","githubUsername":null,"roles":["CONTRIBUTOR"]}}`
}

func TestOrgMemberLsSanitizesCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"OrgMembers": `{"data":{"organization":{"id":"org1","members":[` +
			orgMemberSanitizeRow("u1", hostileCellLabel, "a@acme.com") + `,` +
			orgMemberSanitizeRow("u2", "Bob", longCellText) + `]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "member", "ls", "org1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	assertSafeTableText(t, out.String(), 2, 1)
}

func TestOrgMemberLsJSONKeepsTheOriginals(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"OrgMembers": `{"data":{"organization":{"id":"org1","members":[` +
			orgMemberSanitizeRow("u1", hostileCellLabel, longCellText) + `]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "member", "ls", "org1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want one member, got %v", rows)
	}
	user, _ := rows[0]["user"].(map[string]any)
	if user == nil || user["name"] != hostileCellLabel || user["email"] != longCellText {
		t.Errorf("--json must round-trip the original values, got %v", rows)
	}
}

// jsonEscaped is jsonText without the surrounding quotes, for splicing a
// value into an existing JSON string literal in a fixture.
func jsonEscaped(s string) string {
	q := jsonText(s)
	return q[1 : len(q)-1]
}

func jsonText(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
