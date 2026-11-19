package cmd

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// cli#1053 slice 2 (the remaining app and agent pages): facts the evaluator's
// round 1 found missing or wrong, pinned so a later edit cannot drop them.

func flatHelp1053S2(t *testing.T, path ...string) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{}, path...), "--help"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

// Each page says the thing round 1 said a reader could not find on it.
func TestSlice2PagesSayWhatRound1FoundMissing1053(t *testing.T) {
	for _, tc := range []struct {
		page []string
		want []string
	}{
		// where a <revision-loc> comes from, and that the CLI cannot list them
		{[]string{"agent", "revision", "publish"}, []string{`"hadron agent revision create" printed it`}},
		{[]string{"agent", "revision", "restore"}, []string{`"hadron agent revision create" printed it`, "REVISION_SLOTS_EXHAUSTED"}},
		{[]string{"agent", "revision"}, []string{"Listing an Agent's revisions is not available from the CLI yet"}},
		// facts the shorter text must keep (the shorter uninstall and delete text still says them)
		{[]string{"app", "uninstall"}, []string{"open dark-worker alarms", "Uninstall no longer deletes"}},
		{[]string{"app", "delete"}, []string{"delete by id from the start"}},
		// the default view of an Agent, not only its edge cases
		{[]string{"agent", "get"}, []string{"the Agent's name, URN and ID", "offer readiness", "exits 4"}},
		// roles come from the roles page
		{[]string{"app", "member", "add"}, []string{`Run "hadron app member roles"`}},
		// which App, and how to get an App key
		{[]string{"app", "user", "create"}, []string{`mint one with "hadron app key create"`, "the global --app is not used"}},
		// what a scope is, how to see them, and that nothing is asked first
		{[]string{"app", "key", "scope", "revoke"}, []string{"attention:deliver or host:operate", "--scopes", "no confirmation prompt"}},
		{[]string{"app", "key", "list"}, []string{"a host is a machine that launches a team App's workers", "PREVIEW"}},
		// the Agents an App runs, and the column that is a plan not a promise
		{[]string{"app", "agent", "list"}, []string{"which Agents does this App run?", "BOOT PROMPT (NEXT BIND)", "Approval is never inferred from a pointer"}},
		{[]string{"app", "memory-grant", "rm"}, []string{"list with --include-revoked to check the outcome before retrying", "userless runs"}},
		// what a grant is comes before the page's own details
		{[]string{"app", "memory-grant", "list"}, []string{`"hadron app memory-grant add"`, "REGISTER"}},
		// what properties hold, with a minimal object
		{[]string{"app", "properties", "set"}, []string{`{"team": {"note": "hello"}}`, "taskIntegrations"}},
	} {
		help := flatHelp1053S2(t, tc.page...)
		for _, w := range tc.want {
			if !strings.Contains(help, w) {
				t.Errorf("%v help lacks %q", tc.page, w)
			}
		}
	}
}

// API operation names and release-note words are for the maintainers, not the
// reader of a page.
func TestSlice2HelpHasNoOperationNamesOrReleaseNotes1053(t *testing.T) {
	banned := []string{"joinApp", "leaveApp", "revokeAppKey", "dark-worker episode", "BREAKING:", "AppAgent.", "bootPromptServe"}
	for _, page := range [][]string{
		{"app", "join"}, {"app", "leave"}, {"app", "key", "revoke"}, {"app", "uninstall"}, {"app", "delete"},
		{"app", "reinstall"}, {"app", "agent", "list"}, {"app", "member", "add"}, {"app", "memories"},
	} {
		help := flatHelp1053S2(t, page...)
		for _, b := range banned {
			if strings.Contains(help, b) {
				t.Errorf("%v help carries %q", page, b)
			}
		}
	}
}

// The --instruction explanation is one short block that points at one longer
// explanation, not 25 lines pasted on every page that takes the flag.
func TestInstructionBlockIsShortAndPointsAtTheLongerExplanation1053(t *testing.T) {
	for _, page := range [][]string{
		{"app", "member", "add"}, {"app", "member", "remove"}, {"app", "leave"},
		{"app", "model-group", "set"}, {"app", "properties", "set"},
		{"app", "key", "revoke"}, {"app", "memory-grant", "list"},
	} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{}, page...), "--help"))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		i := strings.Index(text, "--instruction <kind>:<ref> is for an AI worker")
		if i < 0 {
			t.Errorf("%v: no --instruction block", page)
			continue
		}
		block := text[i:]
		if j := strings.Index(block, "\n\n"); j >= 0 {
			// the block is two paragraphs; take through the second
			if k := strings.Index(block[j+2:], "\n\n"); k >= 0 {
				block = block[:j+2+k]
			}
		}
		if n := strings.Count(block, "\n"); n > 20 {
			t.Errorf("%v: the --instruction block is %d lines; keep it short and point at agentic-usage", page, n)
		}
		if !strings.Contains(strings.Join(strings.Fields(block), " "), "hadron agentic-usage") {
			t.Errorf("%v: the --instruction block does not point at hadron agentic-usage", page)
		}
	}
}

// The receipts the slice-2 help quotes are the ones the commands print, and
// the --instruction block's pointers resolve (a quoted line that the binary does
// not print misleads the reader).
func TestSlice2QuotedReceiptsAreWhatTheCommandsPrint1053(t *testing.T) {
	const appID = "capp100000000000000000000"
	for _, tc := range []struct {
		page  []string
		args  []string
		quote string // as the help writes it, with <ref> for the App
		print string // what the command prints, with the real ref
	}{
		{[]string{"app", "delete"}, []string{"app", "delete", appID, "--yes"}, "✓ App <ref> is deleted", "✓ App " + appID + " is deleted"},
		{[]string{"app", "uninstall"}, []string{"app", "uninstall", appID, "--yes"}, "✓ App <ref> is uninstalled", "✓ App " + appID + " is uninstalled"},
		{[]string{"app", "reinstall"}, []string{"app", "reinstall", appID}, "✓ App <ref> is installed", "✓ App " + appID + " is installed"},
	} {
		out, _, _, err := runLifecycleOrdered(t, allLifecycleResponses(), tc.args...)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if !strings.Contains(out, tc.print) {
			t.Fatalf("%v prints %q, not %q", tc.args, out, tc.print)
		}
		if !strings.Contains(flatHelp1053S2(t, tc.page...), `"`+tc.quote) {
			t.Errorf("%v help does not quote %q", tc.page, tc.quote)
		}
	}
	_, r := run1625(nil).exec(t, "app", "leave", joinApp1625, "--yes")
	if r.err != nil || !strings.Contains(r.out, "✓ You are not a member of") || !strings.Contains(r.out, "personal memory") {
		t.Fatalf("app leave prints %q (%v)", r.out, r.err)
	}
	if h := flatHelp1053S2(t, "app", "leave"); !strings.Contains(h, `"✓ You are not a member of <App> now" and a note that your personal memory is kept`) {
		t.Errorf("app leave help does not say what it prints")
	}
	_, r = run1625(nil).exec(t, "app", "member", "add", "--user", "ada", "--role", "member", "--yes", "--app", joinApp1625)
	if r.err != nil || !strings.Contains(r.out, "is a member of "+joinApp1625+" (from --app) with role member") {
		t.Fatalf("app member add prints %q (%v)", r.out, r.err)
	}
	if h := flatHelp1053S2(t, "app", "member", "add"); !strings.Contains(h, `"✓ <user> is a member of <App> (from --app) with role <role>" ("from the active App" when you did not pass --app)`) {
		t.Errorf("app member add help does not quote its receipt as printed")
	}
	// The other receipt variant: no --app, the active App (its own fixture server,
	// so the order of these checks does not matter).
	{
		gql, _ := captureGraphQL(t, map[string]string{"EnsureAppMember": `{"data":{"ensureAppMember":{"role":"member","createdAt":"2026-11-04T00:00:00Z","user":{"id":"usr1","handle":"ada","urn":"hrn:user:ada"}}}}`})
		f, out := testFactory(t)
		setActiveApp(t, joinApp1625)
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "member", "add", "--user", "ada", "--role", "member", "--yes", "--server", gql.URL})
		if err := root.Execute(); err != nil || !strings.Contains(out.String(), "is a member of "+joinApp1625+" (from the active App) with role member") {
			t.Errorf("app member add with the active App prints %q (%v)", out.String(), err)
		}
	}
	_, r = run1625(nil).exec(t, "app", "join", joinApp1625)
	if first, _, _ := strings.Cut(r.out, "\n"); r.err != nil || first != "✓ You are a member of "+joinApp1625+" with role member" {
		t.Fatalf("app join prints %q (%v); its receipt is the one line %q", r.out, r.err, "✓ You are a member of <App> with role <role>")
	}
	if h := flatHelp1053S2(t, "app", "join"); !strings.Contains(h, `"✓ You are a member of <App> with role <role>"`) {
		t.Errorf("app join help does not quote its receipt")
	}
}

// Each page's --instruction block names a section of hadron agentic-usage; the
// section must exist, or the pointer sends the reader nowhere.
func TestInstructionBlockPointersResolveInAgenticUsage1053(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	usage := out.String()
	for page, section := range map[string]string{
		"app key revoke":     "## Instructed credential acts",
		"app member add":     "## Instructed staffing and member acts",
		"app properties set": "## App-manager writes with an instruction",
	} {
		if !strings.Contains(usage, section) {
			t.Errorf("agentic-usage has no %q", section)
		}
		name := strings.TrimPrefix(section, "## ")
		if i := strings.Index(name, " ("); i >= 0 {
			name = name[:i]
		}
		if h := flatHelp1053S2(t, strings.Fields(page)...); !strings.Contains(h, `"`+name+`"`) {
			t.Errorf("%s help does not name the section %q", page, name)
		}
	}
}

// The record paragraph is its own paragraph after the --instruction block, and on
// memory-grant pages what a grant is comes before the page's own details.
func TestInstructionBlockParagraphsAreSeparated1053(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "key", "revoke", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\n\nA changing instructed revoke records one app.credential-act event") {
		t.Errorf("the record paragraph is not set off from the --instruction block")
	}
	for _, verb := range []string{"list", "rm"} {
		h := flatHelp1053S2(t, "app", "memory-grant", verb)
		i, j := strings.Index(h, "Manage the server's memory write grants"), strings.Index(h, "--instruction <kind>:<ref>")
		if i < 0 {
			t.Errorf("app memory-grant %s: the 'what a grant is' paragraph is missing", verb)
		}
		if j < 0 {
			t.Errorf("app memory-grant %s: the --instruction block is missing", verb)
		}
		if i >= 0 && j >= 0 && i > j {
			t.Errorf("app memory-grant %s: what a grant is must come before the --instruction block (%d, %d)", verb, i, j)
		}
	}
}

// Each receipt and --json key set the slice-2 help quotes is checked against
// what the command prints, so the quote cannot drift: the text output is
// compared whole where it is short, the --json key set is compared for equality
// (not "contains", which is vacuous for short keys like id or name), and the help
// must carry the sentence that names the keys.
func TestSlice2ReceiptsAndJSONKeysMatchTheHelp1053(t *testing.T) {
	type tc struct {
		name  string
		page  []string
		args  []string
		resp  map[string]string
		text  []string // each must appear in the text output
		whole string   // when set, the text output is exactly this
		quote []string // in the flattened help
		keys  []string // the exact top-level --json key set (object)
		sent  string   // the help sentence that names those keys
		array bool     // --json is an array of strings, not an object
	}
	rev := func(field, loc string) map[string]string {
		return map[string]string{map[string]string{"publishRevision": "PublishRevision", "restoreRevision": "RestoreRevision"}[field]: `{"data":{"` + field + `":{"revisionLoc":"` + loc + `","label":"launch","published":true,"createdAt":"2026-11-01T00:00:00Z"}}}`}
	}
	revKeys := []string{"revisionLoc", "label", "published", "createdAt"}
	const revSent = `--json is {"revisionLoc", "label", "published", "createdAt"}`
	cases := []tc{
		{name: "revision publish", page: []string{"agent", "revision", "publish"}, args: []string{"agent", "revision", "publish", "hrn:agent:acme.com:bot", "revisions:rev-7", "--version-label", "launch"},
			resp: rev("publishRevision", "revisions:rev-7"), whole: "✓ published revision revisions:rev-7 (label launch, published: true)\n",
			quote: []string{`"✓ published revision <loc> (label <label>, published: <true|false>)"`}, keys: revKeys, sent: revSent},
		{name: "revision restore", page: []string{"agent", "revision", "restore"}, args: []string{"agent", "revision", "restore", "hrn:agent:acme.com:bot", "revisions:rev-5"},
			resp: rev("restoreRevision", "revisions:rev-5"), whole: "✓ restored revision revisions:rev-5 (label launch, published: true)\n",
			quote: []string{`"✓ restored revision <loc> (label <label>, published: <true|false>)"`}, keys: revKeys, sent: revSent},
		{name: "member roles", page: []string{"app", "member", "roles"}, args: []string{"app", "member", "roles", "--app", "hrn:app:acme.com:team"},
			resp:  map[string]string{"AppMemberRoles": `{"data":{"app":{"availableMemberRoles":["owner","member"]}}}`},
			whole: "owner\nmember\n", quote: []string{"Prints one role per line", "--json is an array of role strings"}, array: true},
		{name: "key scope revoke", page: []string{"app", "key", "scope", "revoke"}, args: []string{"app", "key", "scope", "revoke", "key1", "host:operate"},
			resp:  map[string]string{"RevokeAppKeyScope": `{"data":{"revokeAppKeyScope":{"id":"key1","appId":"a1","label":null,"revokedAt":null,"scopes":[]}}}`},
			text:  []string{"✓ key key1 no longer holds host:operate", "scopes: none"},
			quote: []string{`"✓ key <id> no longer holds <scope>"`, `"scopes: none"`},
			keys:  []string{"id", "appId", "label", "revokedAt", "scopes"}, sent: `--json is {"id", "appId", "label", "revokedAt", "scopes"}`},
		{name: "user create", page: []string{"app", "user", "create"}, args: []string{"app", "user", "create", "--external-id", "crm-1"},
			resp:  map[string]string{"CreateAppUser": `{"data":{"createAppUser":{"id":"u1","handle":"ada","name":"Ada","urn":"hrn:user:ada","externalId":"crm-1"}}}`},
			whole: "user u1  handle ada  name Ada  urn hrn:user:ada  external id crm-1\n",
			quote: []string{"(id, handle, name, URN and external id"},
			keys:  []string{"id", "handle", "name", "urn", "externalId"}, sent: "--json has the keys id, handle, name, urn and externalId"},
		{name: "member add", page: []string{"app", "member", "add"}, args: []string{"app", "member", "add", "--user", "ada", "--role", "member", "--yes", "--app", joinApp1625},
			resp:  map[string]string{"EnsureAppMember": `{"data":{"ensureAppMember":{"role":"member","createdAt":"2026-11-04T00:00:00Z","user":{"id":"usr1","handle":"ada","urn":"hrn:user:ada"}}}}`},
			whole: "✓ ada is a member of " + joinApp1625 + " (from --app) with role member\n",
			quote: []string{`"✓ <user> is a member of <App> (from --app) with role <role>"`},
			keys:  []string{"app", "userRef", "user", "role", "createdAt", "status"}, sent: `--json is the membership (app, userRef, user, role, createdAt, status "member")`},
	}
	run := func(t *testing.T, resp map[string]string, args ...string) (string, error) {
		gql, _ := captureGraphQL(t, resp)
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{}, args...), "--server", gql.URL))
		err := root.Execute()
		return out.String(), err
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := run(t, c.resp, c.args...)
			if err != nil {
				t.Fatalf("%v: %v", c.args, err)
			}
			if c.whole != "" && out != c.whole {
				t.Errorf("%v prints %q, want exactly %q", c.args, out, c.whole)
			}
			for _, w := range c.text {
				if !strings.Contains(out, w) {
					t.Errorf("%v prints %q, lacking %q", c.args, out, w)
				}
			}
			help := flatHelp1053S2(t, c.page...)
			for _, q := range c.quote {
				if !strings.Contains(help, q) {
					t.Errorf("%v help lacks %q", c.page, q)
				}
			}
			jsonOut, err := run(t, c.resp, append(append([]string{}, c.args...), "--json")...)
			if err != nil {
				t.Fatalf("%v --json: %v", c.args, err)
			}
			if c.array {
				var roles []string
				if err := json.Unmarshal([]byte(jsonOut), &roles); err != nil {
					t.Fatalf("--json is not an array of strings: %v: %s", err, jsonOut)
				}
				if !reflect.DeepEqual(roles, []string{"owner", "member"}) {
					t.Errorf("--json roles = %v, want [owner member]", roles)
				}
				return
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(jsonOut), &obj); err != nil {
				t.Fatalf("--json is not an object: %v: %s", err, jsonOut)
			}
			got := make([]string, 0, len(obj))
			for k := range obj {
				got = append(got, k)
			}
			sort.Strings(got)
			want := append([]string{}, c.keys...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%v --json keys = %v, want exactly %v", c.args, got, want)
			}
			if !strings.Contains(help, c.sent) {
				t.Errorf("%v help lacks the key sentence %q", c.page, c.sent)
			}
		})
	}
}

// `app key list`: the header is asserted whole, and an empty page prints the
// sentence the help quotes in text and [] in --json.
func TestSlice2KeyListHeaderAndEmptyPage1053(t *testing.T) {
	page := func(items string) map[string]string {
		return map[string]string{"AppKeysMetadata": `{"data":{"appKeysPage":{"items":[` + items + `],"total":0,"hasMore":false}}}`}
	}
	exec := func(t *testing.T, resp map[string]string, extra ...string) string {
		gql, _ := captureGraphQL(t, resp)
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"app", "key", "list", "acme.com:team", "--server", gql.URL}, extra...))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	one := page(`{"id":"key1","keyPreview":"hk_ab","label":"ci","createdAt":"2026-11-01T00:00:00Z","lastUsedAt":null,"revokedAt":null}`)
	header, _, _ := strings.Cut(exec(t, one), "\n")
	if got := strings.Join(strings.Fields(header), " "); got != "ID PREVIEW LABEL CREATED LAST USED REVOKED" {
		t.Errorf("app key list header = %q", got)
	}
	if got := exec(t, page("")); got != "no active App keys returned\n" {
		t.Errorf("empty page prints %q", got)
	}
	if got := strings.TrimSpace(exec(t, page(""), "--json")); got != "[]" {
		t.Errorf("empty page --json = %q, want []", got)
	}
	if h := flatHelp1053S2(t, "app", "key", "list"); !strings.Contains(h, `an empty list is [] (text: "no active App keys returned")`) {
		t.Errorf("app key list help does not quote the empty-page sentence")
	}
	// The key rows: an array of objects with the documented keys.
	var rows []map[string]any
	if err := json.Unmarshal([]byte(exec(t, one, "--json")), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("--json rows: %v", err)
	}
	got := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		got = append(got, k)
	}
	sort.Strings(got)
	if want := []string{"createdAt", "id", "keyPreview", "label", "lastUsedAt", "revokedAt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("key row keys = %v, want %v", got, want)
	}
}

// `app user create` when the server returns no handle, name or URN: text shows
// "-", --json shows null, as the help says.
func TestSlice2UserCreateMissingFields1053(t *testing.T) {
	resp := map[string]string{"CreateAppUser": `{"data":{"createAppUser":{"id":"u1","handle":null,"name":null,"urn":null,"externalId":"crm-1"}}}`}
	exec := func(extra ...string) string {
		gql, _ := captureGraphQL(t, resp)
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"app", "user", "create", "--external-id", "crm-1", "--server", gql.URL}, extra...))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if got, want := exec(), "user u1  handle -  name -  urn -  external id crm-1\n"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(exec("--json")), &obj); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": "u1", "handle": nil, "name": nil, "urn": nil, "externalId": "crm-1"}
	if !reflect.DeepEqual(obj, want) {
		t.Errorf("--json = %#v, want %#v", obj, want)
	}
}
