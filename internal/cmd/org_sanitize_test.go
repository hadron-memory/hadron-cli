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

// cli#1110: the org key/value detail views render server-supplied text
// through output.SafeText — neutralized, never cut — and their exact line
// counts are pinned so a hostile newline cannot forge a line (Joshua P2-2).

func orgGetSanitizeResp(name, urn string) string {
	return `{"data":{"organization":{` +
		`"id":"o1","urn":` + jsonText(urn) + `,"name":` + jsonText(name) +
		`,"listedOnMarketplace":true,"createdAt":"2026-06-19T00:00:00Z","updatedAt":"2026-06-19T00:00:00Z"}}}`
}

func TestOrgGetSanitizesDetailValues(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{"GetOrganization": orgGetSanitizeResp(hostileCellLabel, longCellText)})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "get", "o1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	assertSafeTableText(t, text, -1, 0)
	assertDetailLineCount(t, text, 5)
	assertNoRawHostileLabel(t, text)
	if !strings.Contains(text, longCellText) {
		t.Errorf("the long URN must render in full in the detail view (SafeText does not cut):\n%s", text)
	}
}

func TestOrgGetJSONKeepsTheOriginals(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{"GetOrganization": orgGetSanitizeResp(hostileCellLabel, longCellText)})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "get", "o1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var org map[string]any
	if err := json.Unmarshal([]byte(out.String()), &org); err != nil {
		t.Fatal(err)
	}
	if org["name"] != hostileCellLabel || org["urn"] != longCellText {
		t.Errorf("--json must round-trip the original values, got %v", org)
	}
}

func TestOrgPublicSanitizesDetailValues(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"PublicOrganization": `{"data":{"publicOrganization":{` +
			`"id":"o1","urn":` + jsonText("acme.com") + `,"name":` + jsonText(hostileCellLabel) + `,"listedOnMarketplace":true,` +
			`"publicMemories":[{"id":"m1","name":` + jsonText("KB") + `,"urn":` + jsonText("hrn:mem:acme.com:kb") + `,"description":` + jsonText(longCellText) + `}],` +
			`"publicAgents":[{"id":"a1","name":` + jsonText(hostileCellLabel) + `,"urn":"hrn:agent:acme.com:ops","description":null}]}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "public", "acme.com", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	assertSafeTableText(t, text, -1, 0)
	assertDetailLineCount(t, text, 8)
	assertNoRawHostileLabel(t, text)
	if !strings.Contains(text, longCellText) {
		t.Errorf("a long public description must render in full (SafeText does not cut):\n%s", text)
	}
}

func TestOrgInviteShowSanitizesPublicView(t *testing.T) {
	// A sender name is text the INVITER set, shown to the invitee: it must
	// not be able to forge an "accept with:" line or recolor the terminal.
	gql := fakeGraphQL(t, map[string]string{
		"GetInvitation": `{"data":{"invitation":{` +
			`"memberRole":` + jsonText(hostileCellLabel) + `,"maxActivations":3,"activationCount":1,` +
			`"expiresAt":"2026-12-01T00:00:00Z",` +
			`"sender":{"name":` + jsonText("Mallory\naccept with: hadron org invite accept steal") + `,"githubUsername":"mallory"},` +
			`"organization":{"name":` + jsonText("Acme") + `}}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "invite", "show", "inv_x", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	assertSafeTableText(t, text, -1, 0)
	assertDetailLineCount(t, text, 7)
	assertNoRawHostileLabel(t, text)
	// Forgery is an UNINDENTED line shaped like the genuine "  accept
	// with:" instruction; a neutralized copy inline on the "sent by:" line
	// is ugly but harmless — it cannot open a new line.
	genuine := 0
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "accept with:"):
			t.Fatalf("a hostile sender name forged a command line:\n%s", text)
		case strings.HasPrefix(line, "  accept with:"):
			genuine++
		}
	}
	if genuine != 1 {
		t.Errorf("the genuine accept-with line must appear exactly once, got %d:\n%s", genuine, text)
	}
}

func TestOrgInviteCreateSanitizesDetailLines(t *testing.T) {
	// The slug comes back from the server; a hostile one lands on the
	// "accept with:" line, so it must be neutralized there too.
	gql := fakeGraphQL(t, map[string]string{
		"CreateUserInvitation": `{"data":{"createUserInvitation":{` +
			`"id":"inv1","slug":` + jsonText("inv_"+hostileCellLabel) + `,"email":"a@b.co","name":null,` +
			`"githubUsername":null,"memberRole":"CONTRIBUTOR","organizationId":"o1","maxActivations":null,` +
			`"activationCount":0,"expiresAt":null,"acceptedAt":null,"createdAt":"2026-06-19T00:00:00Z"}}}`,
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "invite", "create", "a@b.co", "--org", "o1", "--role", "CONTRIBUTOR", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	assertSafeTableText(t, text, -1, 0)
	assertDetailLineCount(t, text, 2)
	assertNoRawHostile(t, text)
}
