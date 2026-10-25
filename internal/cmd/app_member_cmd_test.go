package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#956: `app member add|remove|list`. Every write test asserts how many
// mutation calls reached the server — the guards that matter (confirm before
// write, no retry, no pre-read) are all "zero or exactly one call".

const (
	memberApp       = "hrn:app:acme.com:eng-team"
	memberActiveApp = "hrn:app:acme.com:active"
)

const ensureMemberResp = `{"data":{"ensureAppMember":{"role":"member","createdAt":"2026-10-25T00:00:00Z",
	"user":{"id":"usr1","handle":"ada","urn":"hrn:user:ada"}}}}`

const removeMemberResp = `{"data":{"removeAppMember":true}}`

const appMembersResp = `{"data":{"app":{"id":"capp100000000000000000000","urn":"hrn:app:acme.com:eng-team","members":[
	{"role":"owner","createdAt":"2026-10-01T00:00:00Z","user":{"id":"usr1","handle":"ada","urn":"hrn:user:ada"}},
	{"role":"member","createdAt":"2026-10-02T00:00:00Z","user":{"id":"usr2","handle":null,"urn":null}}]}}}`

func memberGQLError(msg, code string) string {
	b, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{
		"message": msg, "extensions": map[string]any{"code": code}}}})
	return string(b)
}

// countingGraphQL answers by operation name and counts calls per operation.
func countingGraphQL(t *testing.T, responses map[string]string) (*httptest.Server, func(op string) int, func(op string) json.RawMessage) {
	t.Helper()
	var mu sync.Mutex
	calls := map[string]int{}
	vars := map[string]json.RawMessage{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls[body.OperationName]++
		vars[body.OperationName] = body.Variables
		mu.Unlock()
		resp, ok := responses[body.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = memberGQLError("unexpected operation", "INTERNAL_SERVER_ERROR")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv,
		func(op string) int { mu.Lock(); defer mu.Unlock(); return calls[op] },
		func(op string) json.RawMessage { mu.Lock(); defer mu.Unlock(); return vars[op] }
}

func setActiveApp(t *testing.T, ref string) {
	t.Helper()
	path, err := config.File()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("app = \""+ref+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func memberVar(t *testing.T, raw json.RawMessage, key string) any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("vars: %v (%s)", err, raw)
	}
	return v[key]
}

// Test 1: every userRef form reaches the server byte-identical.
func TestAppMemberUserRefPassesThroughUnchanged(t *testing.T) {
	for _, ref := range []string{"usr1", "Ada@Acme.COM", "Ada", "hrn:user:ada", " ada "} {
		srv, _, vars := countingGraphQL(t, map[string]string{"EnsureAppMember": ensureMemberResp, "RemoveAppMember": removeMemberResp})
		for _, verb := range []string{"add", "remove"} {
			f, _ := testFactory(t)
			args := []string{"app", "member", verb, "--user", ref, "--app", memberApp, "--yes", "--json", "--server", srv.URL}
			if verb == "add" {
				args = append(args, "--role", "member")
			}
			root := NewRootCmd(f)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("%s %q: %v", verb, ref, err)
			}
			op := map[string]string{"add": "EnsureAppMember", "remove": "RemoveAppMember"}[verb]
			if got := memberVar(t, vars(op), "userRef"); got != ref {
				t.Errorf("%s: userRef %q reached the server as %q", verb, ref, got)
			}
			if got := memberVar(t, vars(op), "appRef"); got != memberApp {
				t.Errorf("%s: appRef = %v", verb, got)
			}
		}
	}
}

// Test 2: explicit --app, the active App, and neither.
func TestAppMemberAppResolution(t *testing.T) {
	cases := []struct {
		name   string
		active string
		flag   []string
		want   string // "" = Usage refusal with zero calls
	}{
		{"explicit", "", []string{"--app", memberApp}, memberApp},
		{"explicit beats active", memberActiveApp, []string{"--app", memberApp}, memberApp},
		{"active default", memberActiveApp, nil, memberActiveApp},
		{"neither", "", nil, ""},
	}
	for _, verb := range []string{"add", "remove", "list"} {
		for _, c := range cases {
			t.Run(verb+"/"+c.name, func(t *testing.T) {
				srv, calls, vars := countingGraphQL(t, map[string]string{
					"EnsureAppMember": ensureMemberResp, "RemoveAppMember": removeMemberResp, "AppMembers": appMembersResp})
				f, _ := testFactory(t)
				if c.active != "" {
					setActiveApp(t, c.active)
				}
				args := append([]string{"app", "member", verb}, c.flag...)
				if verb != "list" {
					args = append(args, "--user", "ada", "--yes")
				}
				if verb == "add" {
					args = append(args, "--role", "member")
				}
				root := NewRootCmd(f)
				root.SetArgs(append(args, "--json", "--server", srv.URL))
				err := root.Execute()
				op := map[string]string{"add": "EnsureAppMember", "remove": "RemoveAppMember", "list": "AppMembers"}[verb]
				if c.want == "" {
					if exitCodeFor(err) != exitcode.Usage || calls(op) != 0 {
						t.Fatalf("no App must be a Usage refusal with 0 calls: err=%v calls=%d", err, calls(op))
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if got := memberVar(t, vars(op), "appRef"); got != c.want {
					t.Errorf("appRef = %v, want %s", got, c.want)
				}
			})
		}
	}
}

// Test 3: an explicit blank --app refuses even when an active App is set. The
// shared resolver would silently use the active App.
func TestAppMemberExplicitBlankAppRefuses(t *testing.T) {
	for _, blank := range []string{"", "  "} {
		for _, verb := range []string{"add", "remove", "list"} {
			srv, calls, _ := countingGraphQL(t, map[string]string{
				"EnsureAppMember": ensureMemberResp, "RemoveAppMember": removeMemberResp, "AppMembers": appMembersResp})
			f, _ := testFactory(t)
			setActiveApp(t, memberActiveApp)
			args := []string{"app", "member", verb, "--app", blank, "--yes"}
			if verb != "list" {
				args = append(args, "--user", "ada")
			}
			if verb == "add" {
				args = append(args, "--role", "member")
			}
			if verb == "list" {
				args = args[:len(args)-1] // list takes no --yes
			}
			root := NewRootCmd(f)
			root.SetArgs(append(args, "--server", srv.URL))
			err := root.Execute()
			total := calls("EnsureAppMember") + calls("RemoveAppMember") + calls("AppMembers")
			if exitCodeFor(err) != exitcode.Usage || total != 0 {
				t.Errorf("%s --app %q: want Usage + 0 calls, got err=%v calls=%d", verb, blank, err, total)
			}
		}
	}
}

// Test 4: add without --role is a Usage error and invents no default.
func TestAppMemberAddRequiresRole(t *testing.T) {
	srv, calls, _ := countingGraphQL(t, map[string]string{"EnsureAppMember": ensureMemberResp})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "member", "add", "--user", "ada", "--app", memberApp, "--yes", "--server", srv.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Usage || calls("EnsureAppMember") != 0 {
		t.Fatalf("want Usage + 0 calls, got err=%v calls=%d", err, calls("EnsureAppMember"))
	}
	f2, _ := testFactory(t)
	root = NewRootCmd(f2)
	root.SetArgs([]string{"app", "member", "add", "--user", "ada", "--role", " ", "--app", memberApp, "--yes", "--server", srv.URL})
	if err := root.Execute(); exitCodeFor(err) != exitcode.Usage || calls("EnsureAppMember") != 0 {
		t.Fatalf("blank role: want Usage + 0 calls, got err=%v", err)
	}
}

// Test 5: confirmation. Nothing is written before it.
func TestAppMemberConfirmation(t *testing.T) {
	for _, verb := range []string{"add", "remove"} {
		op := map[string]string{"add": "EnsureAppMember", "remove": "RemoveAppMember"}[verb]
		base := []string{"app", "member", verb, "--user", "Ada@acme.com", "--app", memberApp}
		if verb == "add" {
			base = append(base, "--role", "owner")
		}
		run := func(t *testing.T, tty bool, answers string, extra ...string) (int, string, error) {
			srv, calls, _ := countingGraphQL(t, map[string]string{"EnsureAppMember": ensureMemberResp, "RemoveAppMember": removeMemberResp})
			var errOut string
			var err error
			args := append(append([]string{}, base...), extra...)
			args = append(args, "--server", srv.URL)
			if tty {
				f, _, eo := testFactoryTTY(t, answers)
				root := NewRootCmd(f)
				root.SetArgs(args)
				err = root.Execute()
				errOut = eo.String()
			} else {
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(args)
				err = root.Execute()
			}
			return calls(op), errOut, err
		}
		t.Run(verb+"/non-TTY without --yes", func(t *testing.T) {
			n, _, err := run(t, false, "")
			if exitCodeFor(err) != exitcode.Usage || n != 0 {
				t.Fatalf("want Usage + 0 calls, got err=%v calls=%d", err, n)
			}
		})
		t.Run(verb+"/TTY no", func(t *testing.T) {
			n, _, err := run(t, true, "n\n")
			if exitCodeFor(err) != exitcode.Cancelled || n != 0 {
				t.Fatalf("want Cancelled + 0 calls, got err=%v calls=%d", err, n)
			}
		})
		t.Run(verb+"/TTY EOF", func(t *testing.T) {
			n, _, err := run(t, true, "")
			if exitCodeFor(err) != exitcode.Cancelled || n != 0 {
				t.Fatalf("want Cancelled + 0 calls, got err=%v calls=%d", err, n)
			}
		})
		t.Run(verb+"/TTY yes names app user role", func(t *testing.T) {
			n, prompt, err := run(t, true, "y\n")
			if err != nil || n != 1 {
				t.Fatalf("want success + 1 call, got err=%v calls=%d", err, n)
			}
			for _, want := range []string{memberApp, `"Ada@acme.com"`} {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt must name %s: %q", want, prompt)
				}
			}
			if strings.Contains(prompt, "cannot be undone") {
				t.Errorf("a reversible membership change must not claim it cannot be undone: %q", prompt)
			}
			if !strings.Contains(prompt, "from --app") {
				t.Errorf("prompt must name where the App came from: %q", prompt)
			}
			if verb == "add" && !strings.Contains(prompt, `"owner"`) {
				t.Errorf("add prompt must name the role: %q", prompt)
			}
		})
		t.Run(verb+"/--yes", func(t *testing.T) {
			n, prompt, err := run(t, true, "", "--yes")
			if err != nil || n != 1 || prompt != "" {
				t.Fatalf("want 1 call and no prompt, got err=%v calls=%d prompt=%q", err, n, prompt)
			}
		})
	}
}

// Test 6: same-role repeat and changed-role add each make exactly one call, and
// nothing reads the list first.
func TestAppMemberAddNeverPreReads(t *testing.T) {
	srv, calls, _ := countingGraphQL(t, map[string]string{"EnsureAppMember": ensureMemberResp})
	for _, role := range []string{"member", "member", "owner"} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "member", "add", "--user", "ada", "--role", role, "--app", memberApp, "--yes", "--json", "--server", srv.URL})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		var dto struct{ Status, Role, UserRef string }
		if err := json.Unmarshal([]byte(out.String()), &dto); err != nil || dto.Status != "member" {
			t.Fatalf("dto: %v %s", err, out.String())
		}
	}
	if calls("EnsureAppMember") != 3 || calls("AppMembers") != 0 {
		t.Errorf("want 3 upserts and no list read, got %d / %d", calls("EnsureAppMember"), calls("AppMembers"))
	}
}

// Test 7: remove is "absent now", never "removed", and repeats succeed.
func TestAppMemberRemoveReportsAbsentNotRemoved(t *testing.T) {
	srv, calls, _ := countingGraphQL(t, map[string]string{"RemoveAppMember": removeMemberResp})
	for i := 0; i < 2; i++ {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "member", "remove", "--user", "ada", "--app", memberApp, "--yes", "--json", "--server", srv.URL})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		var dto struct{ Status string }
		if err := json.Unmarshal([]byte(out.String()), &dto); err != nil || dto.Status != "absent" {
			t.Fatalf("status must be absent: %v %s", err, out.String())
		}
		f, out = testFactory(t)
		root = NewRootCmd(f)
		root.SetArgs([]string{"app", "member", "remove", "--user", "ada", "--app", memberApp, "--yes", "--server", srv.URL})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if s := out.String(); strings.Contains(s, "removed") || !strings.Contains(s, "not a member") {
			t.Errorf("text must say 'not a member', never 'removed': %q", s)
		}
	}
	if calls("RemoveAppMember") != 4 {
		t.Errorf("calls = %d", calls("RemoveAppMember"))
	}
}

// Test 8: typed refusals keep the server's text and are definite.
func TestAppMemberDefiniteRefusalsKeepServerText(t *testing.T) {
	cases := []struct{ msg, code string }{
		{`role "boss" is not valid; allowed: owner, member`, "InvalidRoleError"},
		{"App already has the maximum number of members", "MaxMembersExceededError"},
		{"this would leave the App with no owner", "OrphanAppError"},
		{"Forbidden: only an App member manager may do this", "INTERNAL_SERVER_ERROR"},
	}
	for _, c := range cases {
		srv, calls, _ := countingGraphQL(t, map[string]string{"EnsureAppMember": memberGQLError(c.msg, c.code)})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "member", "add", "--user", "ada", "--role", "boss", "--app", memberApp, "--yes", "--server", srv.URL})
		err := root.Execute()
		if err == nil || exitCodeFor(err) == 0 {
			t.Fatalf("%s: want a non-zero refusal", c.code)
		}
		if !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: server text must be kept verbatim: %v", c.code, err)
		}
		if strings.Contains(strings.ToLower(err.Error()), "unknown") || calls("EnsureAppMember") != 1 {
			t.Errorf("%s: a definite refusal must not read as outcome unknown / retry: %v calls=%d", c.code, err, calls("EnsureAppMember"))
		}
	}
}

// Test 9: outcome unknown — non-zero, no success DTO, exactly one call, and it
// points at `app member list`.
func TestAppMemberOutcomeUnknown(t *testing.T) {
	partial := `{"data":{"ensureAppMember":null},"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
	cases := map[string]string{
		"post-write internal error": memberGQLError("Internal server error", "INTERNAL_SERVER_ERROR"),
		"data and errors":           partial,
		"no code":                   `{"errors":[{"message":"mystery"}]}`,
		"null payload":              `{"data":{"ensureAppMember":null}}`,
		"mixed with a refusal":      `{"errors":[{"message":"x","extensions":{"code":"InvalidRoleError"}},{"message":"y","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`,
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			srv, calls, _ := countingGraphQL(t, map[string]string{"EnsureAppMember": resp})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"app", "member", "add", "--user", "ada", "--role", "member", "--app", memberApp, "--yes", "--json", "--server", srv.URL})
			err := root.Execute()
			if err == nil {
				t.Fatal("must exit non-zero")
			}
			if out.String() != "" {
				t.Errorf("no success DTO on stdout: %q", out.String())
			}
			if !strings.Contains(err.Error(), "outcome unknown") || !strings.Contains(err.Error(), "app member list") {
				t.Errorf("must say outcome unknown and point at list: %v", err)
			}
			if calls("EnsureAppMember") != 1 {
				t.Errorf("must not retry: %d calls", calls("EnsureAppMember"))
			}
		})
	}
	t.Run("transport failure", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/capabilities" {
				_, _ = w.Write([]byte(`{"capabilities":[]}`))
				return
			}
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		}))
		defer srv.Close()
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "member", "remove", "--user", "ada", "--app", memberApp, "--yes", "--server", srv.URL})
		err := root.Execute()
		if err == nil || out.String() != "" || !strings.Contains(err.Error(), "outcome unknown") {
			t.Fatalf("want outcome unknown, no stdout: err=%v out=%q", err, out.String())
		}
	})
}

// Test 10: a foreign / unknown remove target gets the server's Forbidden text
// as-is, never "not found" (that would reopen the user-existence oracle).
func TestAppMemberRemoveForbiddenIsNotTranslated(t *testing.T) {
	msg := "Forbidden: you must manage this App or be the user themselves"
	srv, _, _ := countingGraphQL(t, map[string]string{"RemoveAppMember": memberGQLError(msg, "INTERNAL_SERVER_ERROR")})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "member", "remove", "--user", "nobody@example.com", "--app", memberApp, "--yes", "--server", srv.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), msg) {
		t.Fatalf("want the Forbidden text verbatim: %v", err)
	}
	if exitCodeFor(err) == exitcode.NotFound || strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Errorf("must not be translated to not-found: %v", err)
	}
}

// Test 11: list.
func TestAppMemberList(t *testing.T) {
	t.Run("rows", func(t *testing.T) {
		srv, _, vars := countingGraphQL(t, map[string]string{"AppMembers": appMembersResp})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "member", "list", "--app", memberApp, "--json", "--server", srv.URL})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		var dto struct {
			AppURN  string `json:"appUrn"`
			Members []struct {
				Role string `json:"role"`
				User struct {
					ID     string  `json:"id"`
					Handle *string `json:"handle"`
				} `json:"user"`
			} `json:"members"`
		}
		if err := json.Unmarshal([]byte(out.String()), &dto); err != nil || len(dto.Members) != 2 || dto.Members[0].Role != "owner" ||
			dto.Members[1].User.Handle != nil || dto.AppURN != memberApp {
			t.Fatalf("dto: %v %s", err, out.String())
		}
		_ = vars
	})
	t.Run("empty", func(t *testing.T) {
		srv, _, _ := countingGraphQL(t, map[string]string{"AppMembers": `{"data":{"app":{"id":"a","urn":"hrn:app:acme.com:eng-team","members":[]}}}`})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "member", "list", "--app", memberApp, "--server", srv.URL})
		if err := root.Execute(); err != nil || !strings.HasSuffix(strings.TrimSpace(out.String()), "no members visible to you") ||
			!strings.Contains(out.String(), "(from --app)") {
			t.Fatalf("err=%v out=%q", err, out.String())
		}
		f, out = testFactory(t)
		root = NewRootCmd(f)
		root.SetArgs([]string{"app", "member", "list", "--app", memberApp, "--json", "--server", srv.URL})
		if err := root.Execute(); err != nil || !strings.Contains(out.String(), `"members": []`) && !strings.Contains(out.String(), `"members":[]`) {
			t.Fatalf("json must carry [] not null: %v %s", err, out.String())
		}
	})
	t.Run("null app is not found", func(t *testing.T) {
		srv, _, _ := countingGraphQL(t, map[string]string{"AppMembers": `{"data":{"app":null}}`})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "member", "list", "--app", memberApp, "--json", "--server", srv.URL})
		err := root.Execute()
		if exitCodeFor(err) != exitcode.NotFound || strings.Contains(out.String(), "members") {
			t.Fatalf("want NotFound and no list: err=%v out=%q", err, out.String())
		}
	})
	t.Run("malformed row is an error", func(t *testing.T) {
		srv, _, _ := countingGraphQL(t, map[string]string{"AppMembers": `{"data":{"app":{"id":"a","urn":"u","members":[null]}}}`})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "member", "list", "--app", memberApp, "--server", srv.URL})
		if err := root.Execute(); err == nil {
			t.Fatal("a malformed row must not become a successful list")
		}
	})
	t.Run("errors are errors", func(t *testing.T) {
		srv, _, _ := countingGraphQL(t, map[string]string{"AppMembers": memberGQLError("nope", "FORBIDDEN")})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "member", "list", "--app", memberApp, "--server", srv.URL})
		if err := root.Execute(); exitCodeFor(err) != exitcode.Forbidden {
			t.Fatalf("want Forbidden, got %v", err)
		}
	})
}

// The selection must never ask for the fields the server withholds.
func TestAppMemberQueriesSelectNoWithheldFields(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct{ Query string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		query = body.Query
		_, _ = w.Write([]byte(appMembersResp))
	}))
	defer srv.Close()
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "member", "list", "--app", memberApp, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"email", "name"} {
		if strings.Contains(query, field) {
			t.Errorf("query selects %q: %s", field, query)
		}
	}
}
