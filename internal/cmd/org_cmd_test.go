package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const orgJSON = `{"id":"org1","urn":"acme.com","name":"Acme","listedOnMarketplace":true,
	"createdAt":"2026-06-19T00:00:00Z","updatedAt":"2026-06-19T00:00:00Z"}`
const orgUserJSON = `{"id":"usr1","name":"Alice","email":"alice@acme.com","handle":"alice",
	"githubUsername":null,"roles":["CONTRIBUTOR"]}`

func TestOrgCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateOrganization": `{"data":{"createOrganization":` + orgJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "create", "--name", "Acme", "--urn", "acme.com", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateOrganization"], &vars)
	if vars["name"] != "Acme" || vars["urn"] != "acme.com" {
		t.Errorf("create vars: %v", vars)
	}
	var dto map[string]any
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto["id"] != "org1" || dto["urn"] != "acme.com" {
		t.Errorf("dto: %v", dto)
	}
}

// An invalid --urn (space) is rejected client-side, before any network call —
// CreateOrganization must never be reached (issue #189).
func TestOrgCreateRejectsInvalidURN(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateOrganization": `{"data":{"createOrganization":` + orgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "create", "--name", "Acme", "--urn", "Flow Lab", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error for a URN with a space")
	}
	if _, ok := captured["CreateOrganization"]; ok {
		t.Error("CreateOrganization should not be called when --urn is invalid")
	}
}

// #262: an org --urn is validated against the shared urn-lib org-slug rule — it
// must be a bare, dotted, lowercase domain (no scheme prefix / colon), matching
// what the server enforces. Rejected BEFORE the mutation: the fake server would
// answer create/update if reached, so asserting neither op is called proves the
// rejection is client-side (not a later network error).
func TestOrgURNValidatedAsOrgSlug(t *testing.T) {
	// non-dotted, scheme-prefixed, colon, and uppercase all fail.
	for _, bad := range []string{"acme", "hrn:org:acme.com", "acme:com", "Acme.com"} {
		for _, argv := range [][]string{
			{"org", "create", "--name", "X", "--urn", bad},
			{"org", "update", "org1", "--urn", bad},
		} {
			gql, captured := captureGraphQL(t, map[string]string{
				"CreateOrganization": `{"data":{"createOrganization":` + orgJSON + `}}`,
				"UpdateOrganization": `{"data":{"updateOrganization":` + orgJSON + `}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(argv, "--server", gql.URL))
			if err := root.Execute(); err == nil {
				t.Errorf("org --urn %q should be rejected (%v)", bad, argv[:2])
			}
			for _, op := range []string{"CreateOrganization", "UpdateOrganization"} {
				if _, called := captured[op]; called {
					t.Errorf("invalid org --urn %q must be rejected before %s (%v)", bad, op, argv[:2])
				}
			}
		}
	}
}

func TestOrgGetNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetOrganization": `{"data":{"organization":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "get", "org_x", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found, got %v", err)
	}
}

func TestOrgUpdatePreservesUnset(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateOrganization": `{"data":{"updateOrganization":` + orgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "update", "org1", "--name", "Acme Inc", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateOrganization"], &vars)
	if vars["id"] != "org1" || vars["name"] != "Acme Inc" {
		t.Errorf("update vars: %v", vars)
	}
	for _, k := range []string{"urn", "listedOnMarketplace"} {
		if _, present := vars[k]; present {
			t.Errorf("unset %q must be omitted, got %v", k, vars[k])
		}
	}
}

// The renamed --marketplace flag maps to the updateOrganization
// listedOnMarketplace argument (the server dropped isVisible in its
// org-visibility rework, #264).
func TestOrgUpdateMarketplaceFlag(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateOrganization": `{"data":{"updateOrganization":` + orgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "update", "org1", "--marketplace=false", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateOrganization"], &vars)
	if v, present := vars["listedOnMarketplace"]; !present || v != false {
		t.Errorf("--marketplace=false must send listedOnMarketplace:false, got %v (present=%v)", v, present)
	}
}

// #285 review (Codex P1): --json keeps the deprecated isVisible key (mirroring
// listedOnMarketplace) so existing decoders/selectors don't break when the
// server-side field was removed.
func TestOrgGetJSONKeepsIsVisibleAlias(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetOrganization": `{"data":{"organization":` + orgJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "get", "org1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), `"isVisible"`) {
		t.Errorf("deprecated isVisible key must remain in --json, got %s", out.String())
	}
	var dto struct {
		ListedOnMarketplace bool  `json:"listedOnMarketplace"`
		IsVisible           *bool `json:"isVisible"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if !dto.ListedOnMarketplace || dto.IsVisible == nil || *dto.IsVisible != dto.ListedOnMarketplace {
		t.Errorf("isVisible must mirror listedOnMarketplace(=true), got isVisible=%v listed=%v", dto.IsVisible, dto.ListedOnMarketplace)
	}
}

const publicOrgJSON = `{"id":"org1","urn":"acme.com","name":"Acme","listedOnMarketplace":true,
	"publicMemories":[{"id":"m1","name":"KB","urn":"acme.com::kb","description":"the handbook"}],
	"publicAgents":[{"id":"a1","name":"Helper","urn":"acme.com::helper","description":null}]}`

// #270: `org public <ref>` fetches the sanitized discoverable view — org fields
// plus public memories/agents — and forwards the ref verbatim.
func TestOrgPublic(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"PublicOrganization": `{"data":{"publicOrganization":` + publicOrgJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "public", "acme.com", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["PublicOrganization"], &vars)
	if vars["ref"] != "acme.com" {
		t.Errorf("ref must forward verbatim, got %v", vars["ref"])
	}
	var dto struct {
		Name                string `json:"name"`
		ListedOnMarketplace bool   `json:"listedOnMarketplace"`
		PublicMemories      []struct {
			URN         string  `json:"urn"`
			Description *string `json:"description"`
		} `json:"publicMemories"`
		PublicAgents []struct {
			URN         string  `json:"urn"`
			Description *string `json:"description"`
		} `json:"publicAgents"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if dto.Name != "Acme" || !dto.ListedOnMarketplace {
		t.Errorf("org fields wrong: %+v", dto)
	}
	if len(dto.PublicMemories) != 1 || dto.PublicMemories[0].URN != "acme.com::kb" ||
		dto.PublicMemories[0].Description == nil || *dto.PublicMemories[0].Description != "the handbook" {
		t.Errorf("public memories: %+v", dto.PublicMemories)
	}
	if len(dto.PublicAgents) != 1 || dto.PublicAgents[0].URN != "acme.com::helper" || dto.PublicAgents[0].Description != nil {
		t.Errorf("public agents: %+v", dto.PublicAgents)
	}
}

// A null response (not discoverable OR not found — the server collapses both) is
// a not-found error, not a nil-deref.
func TestOrgPublicNotDiscoverable(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"PublicOrganization": `{"data":{"publicOrganization":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "public", "ghost.com", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not discoverable") {
		t.Fatalf("expected a not-discoverable not-found error, got %v", err)
	}
}

// Empty footprint renders [] (not null) — the DTO slices are always initialized.
func TestOrgPublicEmptyListsRenderArray(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"PublicOrganization": `{"data":{"publicOrganization":{"id":"org1","urn":"acme.com","name":"Acme","listedOnMarketplace":false,"publicMemories":[],"publicAgents":[]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "public", "acme.com", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, key := range []string{`"publicMemories": []`, `"publicAgents": []`} {
		if !strings.Contains(out.String(), key) {
			t.Errorf("empty lists must render as [] (%s), got %s", key, out.String())
		}
	}
}

func TestOrgUpdateNothingIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "update", "org1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Fatalf("expected nothing-to-update, got %v", err)
	}
}

func TestOrgRmRequiresYes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "rm", "org1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}

func TestOrgMemberLs(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"OrgMembers": `{"data":{"organization":{"id":"org1","members":[
			{"id":"m1","role":"OWNER","canInvite":true,"user":` + orgUserJSON + `}
		]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "member", "ls", "org1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var members []struct {
		Role string `json:"role"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(out.String()), &members); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	if len(members) != 1 || members[0].Role != "OWNER" || members[0].User.ID != "usr1" {
		t.Errorf("members: %+v", members)
	}
}

// A member roster is where duplicate accounts are most visible, so the
// identity fields have to reach org's own DTO — adding them to the shared
// UserFields fragment alone would fetch them and then drop them.
func TestOrgMemberLsSurfacesIdentityFields(t *testing.T) {
	member := `{"id":"usr9","name":"Dup","email":null,"handle":"dup","githubUsername":"dupgh",
		"roles":["READER"],"identityProvider":"GITHUB","githubId":991,"externalId":null,
		"externalAppId":null,"linkedAt":null}`
	gql, _ := captureGraphQL(t, map[string]string{
		"OrgMembers": `{"data":{"organization":{"id":"org1","members":[
			{"id":"m1","role":"ADMIN","canInvite":false,"user":` + member + `}
		]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "member", "ls", "org1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var members []struct {
		User struct {
			IdentityProvider *string `json:"identityProvider"`
			GithubID         *int    `json:"githubId"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(out.String()), &members); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	if len(members) != 1 {
		t.Fatalf("members: %+v", members)
	}
	u := members[0].User
	if u.IdentityProvider == nil || *u.IdentityProvider != "GITHUB" {
		t.Errorf("identityProvider: %v", u.IdentityProvider)
	}
	if u.GithubID == nil || *u.GithubID != 991 {
		t.Errorf("githubId: %v", u.GithubID)
	}
}

func TestOrgMemberAdd(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"AddOrgMember": `{"data":{"addOrgMember":{"id":"m1","role":"CONTRIBUTOR","user":` + orgUserJSON + `}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// lower-case role must be accepted and normalized to the enum.
	root.SetArgs([]string{"org", "member", "add", "org1", "--user", "usr1", "--role", "contributor", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["AddOrgMember"], &vars)
	if vars["orgId"] != "org1" || vars["userId"] != "usr1" || vars["role"] != "CONTRIBUTOR" {
		t.Errorf("add vars: %v", vars)
	}
}

func TestOrgMemberAddRejectsBadRole(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "member", "add", "org1", "--user", "u", "--role", "SUPERADMIN", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid --role") {
		t.Fatalf("expected invalid-role error, got %v", err)
	}
}

func TestOrgMemberSetRole(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateOrgMember": `{"data":{"updateOrgMember":{"id":"m1","role":"ADMIN","user":` + orgUserJSON + `}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "member", "set-role", "org1", "--user", "usr1", "--role", "admin", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateOrgMember"], &vars)
	if vars["role"] != "ADMIN" || vars["userId"] != "usr1" {
		t.Errorf("set-role vars: %v", vars)
	}
}

func TestOrgMemberRmWithYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RemoveOrgMember": `{"data":{"removeOrgMember":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "member", "rm", "org1", "--user", "usr1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["RemoveOrgMember"], &vars)
	if vars["orgId"] != "org1" || vars["userId"] != "usr1" {
		t.Errorf("rm vars: %v", vars)
	}
}

func TestOrgMemberRmRequiresYes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "member", "rm", "org1", "--user", "usr1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}

const orgInviteJSON = `{"id":"inv1","slug":"inv_abc","email":"alice@partner.com","name":null,
	"githubUsername":null,"memberRole":"CONTRIBUTOR","organizationId":"org1","maxActivations":null,
	"activationCount":0,"expiresAt":null,"acceptedAt":null,"createdAt":"2026-06-19T00:00:00Z"}`

func TestOrgLs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Organizations": `{"data":{"organizations":{"total":1,"items":[` + orgJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "ls", "--mine", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var orgs []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &orgs); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	if len(orgs) != 1 || orgs[0]["urn"] != "acme.com" {
		t.Errorf("orgs: %v", orgs)
	}
	var vars struct {
		Filter *struct {
			MemberOnly *bool `json:"memberOnly"`
		} `json:"filter"`
	}
	_ = json.Unmarshal(captured["Organizations"], &vars)
	if vars.Filter == nil || vars.Filter.MemberOnly == nil || !*vars.Filter.MemberOnly {
		t.Errorf("--mine must send filter.memberOnly=true, got %s", captured["Organizations"])
	}
}

func TestOrgInviteCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateUserInvitation": `{"data":{"createUserInvitation":` + orgInviteJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// mixed-case role normalizes to the UPPER-case Role enum.
	root.SetArgs([]string{"org", "invite", "create", "alice@partner.com", "--org", "org1", "--role", "Contributor", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateUserInvitation"], &vars)
	if vars["orgId"] != "org1" || vars["memberRole"] != "CONTRIBUTOR" || vars["email"] != "alice@partner.com" {
		t.Errorf("invite vars: %v", vars)
	}
	// unset optionals must be omitted, not sent as null/0.
	for _, k := range []string{"name", "githubUsername", "expiresInDays", "maxActivations"} {
		if _, present := vars[k]; present {
			t.Errorf("unset %q must be omitted, got %v", k, vars[k])
		}
	}
	var dto struct {
		Slug string `json:"slug"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.Slug != "inv_abc" {
		t.Errorf("expected slug in output, got %s", out.String())
	}
}

func TestOrgInviteCreateRejectsBadRole(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "invite", "create", "a@b.com", "--org", "org1", "--role", "boss", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid --role") {
		t.Fatalf("expected invalid-role error, got %v", err)
	}
}

func TestOrgInviteAccept(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"AcceptInvitation": `{"data":{"acceptInvitation":true}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "invite", "accept", "inv_abc", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["AcceptInvitation"], &vars)
	if vars["slug"] != "inv_abc" {
		t.Errorf("accept vars: %v", vars)
	}
	if !strings.Contains(out.String(), "accepted") {
		t.Errorf("expected accepted confirmation, got %s", out.String())
	}
}

func TestOrgInviteShow(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetInvitation": `{"data":{"invitation":` + orgInviteJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "invite", "show", "inv_abc", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Slug       string `json:"slug"`
		MemberRole string `json:"memberRole"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.Slug != "inv_abc" || dto.MemberRole != "CONTRIBUTOR" {
		t.Errorf("show dto: %+v", dto)
	}
}

// A false acceptInvitation return is a failed accept → non-zero exit, so
// automation can't read it as success.
func TestOrgInviteAcceptFalseExitsNonZero(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"AcceptInvitation": `{"data":{"acceptInvitation":false}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"org", "invite", "accept", "inv_stale", "--server", gql.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not accepted") {
		t.Fatalf("a false accept must exit non-zero, got %v", err)
	}
}
