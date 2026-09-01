package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const uUserJSON = `{"id":"usr1","name":"Alice","email":"alice@acme.com","handle":"alice",
	"githubUsername":null,"roles":["CONTRIBUTOR"]}`

func TestUserSearch(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SearchUsers": `{"data":{"users":{"total":1,"items":[` + uUserJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "search", "alice", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["SearchUsers"], &vars)
	if vars["query"] != "alice" {
		t.Errorf("search query var: %v", vars)
	}
	var users []struct {
		ID     string `json:"id"`
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal([]byte(out.String()), &users); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	if len(users) != 1 || users[0].Handle != "alice" {
		t.Errorf("users: %+v", users)
	}
}

// uIdentityUserJSON is a GitHub-login account with no email — the shape whose
// identity fields decide whether merging it into an email account preserves
// the GitHub login or destroys it (cor:api:010:02).
const uIdentityUserJSON = `{"id":"usr2","name":"Bob","email":null,"handle":"bob",
	"githubUsername":"bobgh","roles":["READER"],"identityProvider":"github","githubId":4242,
	"externalId":"gh|4242","externalAppId":null,"linkedAt":"2026-07-01T00:00:00Z"}`

func TestUserSearchSurfacesIdentityFields(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"SearchUsers": `{"data":{"users":{"total":1,"items":[` + uIdentityUserJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "search", "bob", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var users []struct {
		IdentityProvider *string `json:"identityProvider"`
		GithubID         *int    `json:"githubId"`
		ExternalID       *string `json:"externalId"`
		LinkedAt         *string `json:"linkedAt"`
	}
	if err := json.Unmarshal([]byte(out.String()), &users); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	if len(users) != 1 {
		t.Fatalf("users: %+v", users)
	}
	u := users[0]
	if u.IdentityProvider == nil || *u.IdentityProvider != "github" {
		t.Errorf("identityProvider: %v", u.IdentityProvider)
	}
	if u.GithubID == nil || *u.GithubID != 4242 {
		t.Errorf("githubId: %v", u.GithubID)
	}
	if u.ExternalID == nil || *u.ExternalID != "gh|4242" {
		t.Errorf("externalId: %v", u.ExternalID)
	}
	if u.LinkedAt == nil || *u.LinkedAt != "2026-07-01T00:00:00Z" {
		t.Errorf("linkedAt: %v", u.LinkedAt)
	}
}

// No positional arg must OMIT the query variable entirely — `filter: {}` is
// what the server reads as the platform-admin full-list request. Sending an
// explicit empty string would instead be a blank query (an empty page).
func TestUserSearchOmitsQueryVariableForFullList(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SearchUsers": `{"data":{"users":{"total":1,"items":[` + uUserJSON + `]}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "search", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	if err := json.Unmarshal(captured["SearchUsers"], &vars); err != nil {
		t.Fatalf("decoding captured variables: %v", err)
	}
	// This assertion is a NEGATIVE one, so it would pass vacuously on an
	// empty map — check something was actually captured first.
	if len(vars) == 0 {
		t.Fatal("no SearchUsers variables captured")
	}
	if _, present := vars["query"]; present {
		t.Errorf("query variable must be omitted for the full list, got %v", vars)
	}
}

// A non-admin gets an empty page rather than an error; say why instead of
// rendering a bare empty table.
func TestUserSearchFullListHintsWhenEmpty(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"SearchUsers": `{"data":{"users":{"total":0,"items":[]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "search", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "platform ADMIN/OWNER") {
		t.Errorf("expected a permission hint, got:\n%s", out.String())
	}
}

// searchPagesServer serves a 3-user population two rows at a time, recording
// the offset of every request.
func searchPagesServer(t *testing.T, offsets *[]int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Offset *int `json:"offset"`
			} `json:"variables"`
		}
		// A decode failure would silently look like offset=0 and make the
		// paging assertions pass for the wrong reason. t.Errorf (not Fatalf)
		// — this runs on the server's goroutine.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		off := 0
		if body.Variables.Offset != nil {
			off = *body.Variables.Offset
		}
		*offsets = append(*offsets, off)
		items := `{"id":"u3","handle":"c","roles":[]}`
		if off == 0 {
			items = `{"id":"u1","handle":"a","roles":[]},{"id":"u2","handle":"b","roles":[]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"users":{"total":3,"items":[` + items + `]}}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

// The whole-population contract: a listing that spans pages must drain, or
// duplicate detection quietly misses the tail (the issue-#23 failure mode).
func TestUserSearchPagesToExhaustion(t *testing.T) {
	var offsets []int
	gql := searchPagesServer(t, &offsets)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "search", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var users []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out.String()), &users); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	if len(users) != 3 {
		t.Errorf("want all 3 users, got %d: %+v", len(users), users)
	}
	if len(offsets) != 2 || offsets[0] != 0 || offsets[1] != 2 {
		t.Errorf("want offsets [0 2], got %v", offsets)
	}
}

// An explicit --limit is a request for one page, not a drain.
func TestUserSearchExplicitLimitIsSinglePage(t *testing.T) {
	var offsets []int
	gql := searchPagesServer(t, &offsets)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "search", "--limit", "2", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(offsets) != 1 {
		t.Errorf("want exactly one request with an explicit --limit, got %v", offsets)
	}
}

func TestProfileSet(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateMyProfile": `{"data":{"updateMyProfile":` + uUserJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"profile", "set", "--name", "Alice A", "--handle", "alice", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateMyProfile"], &vars)
	if vars["name"] != "Alice A" || vars["handle"] != "alice" {
		t.Errorf("profile vars: %v", vars)
	}
	// --email was not passed, so it must be omitted (leave the field unchanged).
	if _, present := vars["email"]; present {
		t.Errorf("unset --email must be omitted, got %v", vars["email"])
	}
}

func TestProfileSetNothingIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"profile", "set", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Fatalf("expected nothing-to-update usage error, got %v", err)
	}
}

// #262: a --handle is validated client-side against the shared urn-lib handle
// rule (dot-free lowercase slug, no reserved word) — a bad handle fails fast,
// BEFORE the mutation. The fake server would answer UpdateMyProfile if the
// command reached it, so asserting it's never called proves the rejection is
// client-side (not a later network error).
func TestProfileSetRejectsInvalidHandle(t *testing.T) {
	for _, bad := range []string{"has.dot", "UpperCase", "has space"} {
		gql, captured := captureGraphQL(t, map[string]string{
			"UpdateMyProfile": `{"data":{"updateMyProfile":` + uUserJSON + `}}`,
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"profile", "set", "--handle", bad, "--server", gql.URL})
		if err := root.Execute(); err == nil {
			t.Errorf("handle %q should be rejected", bad)
		}
		if _, called := captured["UpdateMyProfile"]; called {
			t.Errorf("invalid handle %q must be rejected before UpdateMyProfile is called", bad)
		}
	}
}

func TestUserMerge(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MergeUsers": `{"data":{"mergeUsers":` + uUserJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// Refs pass through verbatim: a bare handle source, a URN target.
	root.SetArgs([]string{"user", "merge", "dup", "--into", "hrn:user:alice", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	if err := json.Unmarshal(captured["MergeUsers"], &vars); err != nil {
		t.Fatalf("captured MergeUsers vars not JSON: %v", err)
	}
	if vars["source"] != "dup" || vars["target"] != "hrn:user:alice" {
		t.Errorf("source/target vars = %v/%v, want dup / hrn:user:alice", vars["source"], vars["target"])
	}
	// JSON output is the surviving target user.
	var dto struct {
		ID     string `json:"id"`
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.ID != "usr1" || dto.Handle != "alice" {
		t.Errorf("survivor dto = %+v, want id usr1 / handle alice", dto)
	}
}

func TestUserMergeHumanOutput(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"MergeUsers": `{"data":{"mergeUsers":` + uUserJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "merge", "dup", "--into", "alice", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Human output names both the source ref and the surviving user id.
	if got := out.String(); !strings.Contains(got, "dup") || !strings.Contains(got, "usr1") {
		t.Errorf("human output should name source and survivor: %q", got)
	}
}

// Surrounding whitespace on either ref is trimmed once and the normalized value
// is what reaches the server (so an accidental " alice " doesn't fail resolution).
func TestUserMergeTrimsRefs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MergeUsers": `{"data":{"mergeUsers":` + uUserJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "merge", "  dup  ", "--into", "  alice  ", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	if err := json.Unmarshal(captured["MergeUsers"], &vars); err != nil {
		t.Fatalf("captured MergeUsers vars not JSON: %v", err)
	}
	if vars["source"] != "dup" || vars["target"] != "alice" {
		t.Errorf("refs not trimmed on the wire: source=%v target=%v", vars["source"], vars["target"])
	}
}

func TestUserMergeRequiresInto(t *testing.T) {
	// A missing/empty --into is a usage error before any GraphQL request.
	for _, args := range [][]string{
		{"user", "merge", "dup", "--yes", "--server", "http://127.0.0.1:1"},
		{"user", "merge", "dup", "--into", "  ", "--yes", "--server", "http://127.0.0.1:1"},
		{"user", "merge", "  ", "--into", "alice", "--yes", "--server", "http://127.0.0.1:1"},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(args)
		if err := root.Execute(); err == nil {
			t.Errorf("expected a usage error for args %v, got nil", args)
		}
	}
}

// Without --yes and no interactive terminal (the test IOStreams), the
// confirmation gate refuses — user merge is a destructive global operation —
// and MergeUsers must never be reached (cancellation performs no request).
func TestUserMergeRequiresYesNonInteractive(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MergeUsers": `{"data":{"mergeUsers":` + uUserJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "merge", "dup", "--into", "alice", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a refusal without --yes in non-interactive mode")
	}
	if _, ok := captured["MergeUsers"]; ok {
		t.Error("MergeUsers must not be called when the confirmation gate refuses")
	}
}

// A misbehaving server returning null for the non-null mergeUsers field must
// yield an error, not a nil-pointer panic.
func TestUserMergeNullResult(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"MergeUsers": `{"data":{"mergeUsers":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "merge", "dup", "--into", "alice", "--yes", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error when the server returns a null user")
	}
}

// Server-side failures (forbidden, same-user merge) propagate through the CLI's
// API error mapping rather than being duplicated locally.
func TestUserMergeServerErrorPropagates(t *testing.T) {
	for _, msg := range []string{
		`{"errors":[{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}]}`,
		`{"errors":[{"message":"cannot merge a user into itself","extensions":{"code":"BAD_USER_INPUT"}}]}`,
	} {
		gql, _ := captureGraphQL(t, map[string]string{"MergeUsers": msg})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"user", "merge", "dup", "--into", "alice", "--yes", "--server", gql.URL})
		if err := root.Execute(); err == nil {
			t.Errorf("expected a server error to propagate for %q", msg)
		}
	}
}

func TestUserSearchRejectsBadArgs(t *testing.T) {
	cases := [][]string{
		{"user", "search", "  ", "--server", "http://127.0.0.1:1"},
		{"user", "search", "alice", "--limit", "-1", "--server", "http://127.0.0.1:1"},
		{"user", "search", "alice", "--offset", "-5", "--server", "http://127.0.0.1:1"},
	}
	for _, args := range cases {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(args)
		if err := root.Execute(); err == nil {
			t.Errorf("expected a usage error for args %v, got nil", args)
		}
	}
}

// --- user set-roles (#75) -------------------------------------------------

// uAdminUserJSON is the post-update projection: roles REPLACED, not merged.
const uAdminUserJSON = `{"id":"usr1","name":"Alice","email":"alice@acme.com","handle":"alice",
	"githubUsername":null,"roles":["ADMIN"]}`

// setRolesFakes wires both round trips the command makes: GetUser reads the
// current roles (for before → after) and UpdateUserRoles performs the replace.
func setRolesFakes() map[string]string {
	return map[string]string{
		"GetUser":         `{"data":{"user":` + uUserJSON + `}}`,
		"UpdateUserRoles": `{"data":{"updateUserRoles":` + uAdminUserJSON + `}}`,
	}
}

func TestUserSetRoles(t *testing.T) {
	gql, captured := captureGraphQL(t, setRolesFakes())
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// An id ref skips search resolution and is passed through as the PK.
	root.SetArgs([]string{"user", "set-roles", "usr1", "--role", "admin", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var vars struct {
		UserID string   `json:"userId"`
		Roles  []string `json:"roles"`
	}
	if err := json.Unmarshal(captured["UpdateUserRoles"], &vars); err != nil {
		t.Fatalf("captured UpdateUserRoles vars not JSON: %v", err)
	}
	if vars.UserID != "usr1" {
		t.Errorf("userId = %q, want usr1", vars.UserID)
	}
	// Lowercase input must reach the wire as the server's enum spelling.
	if len(vars.Roles) != 1 || vars.Roles[0] != "ADMIN" {
		t.Errorf("roles = %v, want [ADMIN]", vars.Roles)
	}

	var dto struct {
		User          struct{ ID string } `json:"user"`
		PreviousRoles []string            `json:"previousRoles"`
		Roles         []string            `json:"roles"`
		Changed       bool                `json:"changed"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.User.ID != "usr1" {
		t.Errorf("user.id = %q, want usr1", dto.User.ID)
	}
	// The before/after pair is the point of the DTO: it shows what the
	// replacement displaced without a second read.
	if len(dto.PreviousRoles) != 1 || dto.PreviousRoles[0] != "CONTRIBUTOR" {
		t.Errorf("previousRoles = %v, want [CONTRIBUTOR]", dto.PreviousRoles)
	}
	if len(dto.Roles) != 1 || dto.Roles[0] != "ADMIN" {
		t.Errorf("roles = %v, want [ADMIN]", dto.Roles)
	}
	if !dto.Changed {
		t.Error("changed should be true when the role set differs")
	}
}

func TestUserSetRolesRepeatableAndDeduped(t *testing.T) {
	gql, captured := captureGraphQL(t, setRolesFakes())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "set-roles", "usr1",
		"--role", "contributor", "--role", "READER", "--role", "contributor",
		"--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal(captured["UpdateUserRoles"], &vars); err != nil {
		t.Fatalf("captured vars not JSON: %v", err)
	}
	// Order preserved, case normalized, exact duplicate dropped.
	if len(vars.Roles) != 2 || vars.Roles[0] != "CONTRIBUTOR" || vars.Roles[1] != "READER" {
		t.Errorf("roles = %v, want [CONTRIBUTOR READER]", vars.Roles)
	}
}

func TestUserSetRolesRejectsUnknownRole(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "set-roles", "usr1", "--role", "superadmin", "--yes", "--json"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a usage error for an unknown role")
	}
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", got, exitcode.Usage)
	}
	// The message must name the valid set — that's the whole point of
	// validating client-side instead of letting enum coercion fail on the wire.
	for _, want := range []string{"superadmin", "owner", "admin", "contributor", "reader"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err.Error(), want)
		}
	}
}

func TestUserSetRolesRejectsEmptyRole(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// `--role ""` must not read as "clear every role".
	root.SetArgs([]string{"user", "set-roles", "usr1", "--role", "", "--yes", "--json"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a usage error for an empty role")
	} else if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", got, exitcode.Usage)
	}
}

func TestUserSetRolesRequiresRole(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "set-roles", "usr1", "--yes", "--json"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error when --role is omitted")
	}
}

func TestUserSetRolesRequiresYesNonInteractive(t *testing.T) {
	gql, captured := captureGraphQL(t, setRolesFakes())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// No --yes, and the test factory's stdin is not a terminal: a role
	// replacement must not proceed unattended.
	root.SetArgs([]string{"user", "set-roles", "usr1", "--role", "admin", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a refusal without --yes in non-interactive mode")
	}
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", got, exitcode.Usage)
	}
	// The refusal is up front: a caller that could never answer the prompt
	// should not have spent a round trip finding that out.
	if len(captured) != 0 {
		t.Errorf("no GraphQL call should be made before refusing, got %v", captured)
	}
}

// An id the caller can't read back (user(ref:) returns null for denied and
// missing alike, and the search finds nothing) is passed through verbatim as
// a literal id. The update must still run: reading the current roles feeds
// the confirmation message, it is not a gate.
func TestUserSetRolesSurvivesUnreadableCurrentUser(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetUser":         `{"data":{"user":null}}`,
		"SearchUsers":     `{"data":{"users":{"total":0,"items":[]}}}`,
		"UpdateUserRoles": `{"data":{"updateUserRoles":` + uAdminUserJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "set-roles", "usr1", "--role", "admin", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		PreviousRoles []string `json:"previousRoles"`
		Roles         []string `json:"roles"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	// Renders as [] rather than null, per the --json contract.
	if dto.PreviousRoles == nil {
		t.Error("previousRoles must be [] when the prior roles are unknown, not null")
	}
	if len(dto.Roles) != 1 || dto.Roles[0] != "ADMIN" {
		t.Errorf("roles = %v, want [ADMIN]", dto.Roles)
	}
}

func TestUserSetRolesHumanOutputShowsBeforeAndAfter(t *testing.T) {
	gql, _ := captureGraphQL(t, setRolesFakes())
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "set-roles", "usr1", "--role", "admin", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "ADMIN") || !strings.Contains(got, "CONTRIBUTOR") {
		t.Errorf("human output should show both the new and the displaced roles: %q", got)
	}
}

func TestUserSetRolesServerErrorPropagates(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetUser":         `{"data":{"user":` + uUserJSON + `}}`,
		"UpdateUserRoles": `{"errors":[{"message":"Forbidden"}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "set-roles", "usr1", "--role", "admin", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected the server's Forbidden to propagate (platform ADMIN only)")
	}
}

// A destructive global write must not be retargeted by a typo. ResolveUser
// accepts a sole SUBSTRING hit (fine for additive commands like memory
// share); here "alic" matching only "alice" would silently replace alice's
// platform roles, so set-roles resolves exactly (#300 review).
func TestUserSetRolesRejectsFuzzyMatch(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetUser":         `{"data":{"user":null}}`,
		"SearchUsers":     `{"data":{"users":{"total":1,"items":[` + uUserJSON + `]}}}`,
		"UpdateUserRoles": `{"data":{"updateUserRoles":` + uAdminUserJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "set-roles", "alic", "--role", "admin", "--yes", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a usage error for a partial match")
	}
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", got, exitcode.Usage)
	}
	// The error must name what it DID match, so the operator can retype it.
	for _, want := range []string{"alic", "usr1", "alice"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err.Error(), want)
		}
	}
	if _, called := captured["UpdateUserRoles"]; called {
		t.Error("a partial match must be rejected BEFORE the roles are replaced")
	}
}

// An exact handle still resolves through the search fallback.
func TestUserSetRolesAcceptsExactHandleViaSearch(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetUser":         `{"data":{"user":null}}`,
		"SearchUsers":     `{"data":{"users":{"total":1,"items":[` + uUserJSON + `]}}}`,
		"UpdateUserRoles": `{"data":{"updateUserRoles":` + uAdminUserJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "set-roles", "alice", "--role", "admin", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("an exact handle must resolve: %v", err)
	}
	if _, called := captured["UpdateUserRoles"]; !called {
		t.Error("UpdateUserRoles should have been called for an exact match")
	}
}

// `changed` reflects set MEMBERSHIP, not the order the flags were typed.
func TestUserSetRolesChangedIgnoresRoleOrder(t *testing.T) {
	const twoRoleUser = `{"id":"usr1","name":"Alice","email":"alice@acme.com","handle":"alice",
		"githubUsername":null,"roles":["ADMIN","CONTRIBUTOR"]}`
	gql, _ := captureGraphQL(t, map[string]string{
		"GetUser":         `{"data":{"user":` + twoRoleUser + `}}`,
		"UpdateUserRoles": `{"data":{"updateUserRoles":` + twoRoleUser + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// Same set, reversed order.
	root.SetArgs([]string{"user", "set-roles", "usr1",
		"--role", "contributor", "--role", "admin", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Changed *bool `json:"changed"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.Changed == nil || *dto.Changed {
		t.Errorf("changed should be false for the same set in a different order, got %v", dto.Changed)
	}
}

// When the prior roles can't be read, `changed` must be null — claiming
// false would assert nothing changed when the truth is we cannot tell.
func TestUserSetRolesChangedIsNullWhenPriorRolesUnknown(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetUser":         `{"data":{"user":null}}`,
		"SearchUsers":     `{"data":{"users":{"total":0,"items":[]}}}`,
		"UpdateUserRoles": `{"data":{"updateUserRoles":` + uAdminUserJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "set-roles", "usr1", "--role", "admin", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Changed *bool `json:"changed"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.Changed != nil {
		t.Errorf("changed should be null when the prior roles are unknown, got %v", *dto.Changed)
	}
}

// The human output must not report unknown prior roles as "none".
func TestUserSetRolesHumanOutputSaysUnknownNotNone(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetUser":         `{"data":{"user":null}}`,
		"SearchUsers":     `{"data":{"users":{"total":0,"items":[]}}}`,
		"UpdateUserRoles": `{"data":{"updateUserRoles":` + uAdminUserJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "set-roles", "usr1", "--role", "admin", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "unknown") {
		t.Errorf("output should say the prior roles are unknown: %q", got)
	}
	if strings.Contains(got, "was: none") {
		t.Errorf("unknown prior roles must not render as 'none': %q", got)
	}
}
