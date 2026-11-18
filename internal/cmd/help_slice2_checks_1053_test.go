package cmd

import (
	"encoding/json"
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
		// facts the shorter text must keep (the BLIND seat's F1 and F2 on #2536)
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
// the --instruction block's pointers resolve (the BLIND seat's F5 and F6 on
// #2536: a quoted line that the binary does not print misleads the reader).
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
	_, r = run1625(nil).exec(t, "app", "join", joinApp1625)
	if r.err != nil || !strings.Contains(r.out, "✓ You are a member of") || !strings.Contains(r.out, "with role member") {
		t.Fatalf("app join prints %q (%v)", r.out, r.err)
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

// The BLIND seat's P2-6 on #2536: each receipt and --json key set the slice-2
// help quotes is checked against what the command prints, so the quote cannot
// drift. text must appear in the output; quote in the flattened help; keys are
// the --json object keys (all must be present and the help must name them).
func TestSlice2ReceiptsAndJSONKeysMatchTheHelp1053(t *testing.T) {
	type tc struct {
		name  string
		page  []string
		args  []string
		resp  map[string]string
		text  []string // in the text output
		quote []string // in the flattened help
		keys  []string // top-level --json keys (object) the help names
		array bool     // --json is an array, not an object
	}
	rev := func(field, loc string) map[string]string {
		return map[string]string{map[string]string{"publishRevision": "PublishRevision", "restoreRevision": "RestoreRevision"}[field]: `{"data":{"` + field + `":{"revisionLoc":"` + loc + `","label":"launch","published":true,"createdAt":"2026-11-01T00:00:00Z"}}}`}
	}
	cases := []tc{
		{"revision publish", []string{"agent", "revision", "publish"}, []string{"agent", "revision", "publish", "hrn:agent:acme.com:bot", "revisions:rev-7", "--version-label", "launch"},
			rev("publishRevision", "revisions:rev-7"), []string{"✓ published revision revisions:rev-7 (label launch, published: true)"},
			[]string{`"✓ published revision <loc> (label <label>, published: <true|false>)"`}, []string{"revisionLoc", "label", "published", "createdAt"}, false},
		{"revision restore", []string{"agent", "revision", "restore"}, []string{"agent", "revision", "restore", "hrn:agent:acme.com:bot", "revisions:rev-5"},
			rev("restoreRevision", "revisions:rev-5"), []string{"✓ restored revision revisions:rev-5 (label launch, published: true)"},
			[]string{`"✓ restored revision <loc> (label <label>, published: <true|false>)"`}, []string{"revisionLoc", "label", "published", "createdAt"}, false},
		{"member roles", []string{"app", "member", "roles"}, []string{"app", "member", "roles", "--app", "hrn:app:acme.com:team"},
			map[string]string{"AppMemberRoles": `{"data":{"app":{"availableMemberRoles":["owner","member"]}}}`}, []string{"owner", "member"},
			[]string{"Prints one role per line", "--json is an array of role strings"}, nil, true},
		{"key scope revoke", []string{"app", "key", "scope", "revoke"}, []string{"app", "key", "scope", "revoke", "key1", "host:operate"},
			map[string]string{"RevokeAppKeyScope": `{"data":{"revokeAppKeyScope":{"id":"key1","appId":"a1","label":null,"revokedAt":null,"scopes":[]}}}`},
			[]string{"✓ key key1 no longer holds host:operate", "scopes: none"},
			[]string{`"✓ key <id> no longer holds <scope>"`, `"scopes: none"`}, []string{"id", "appId", "label", "revokedAt", "scopes"}, false},
		{"key list", []string{"app", "key", "list"}, []string{"app", "key", "list", "acme.com:team"},
			map[string]string{"AppKeysMetadata": `{"data":{"appKeysPage":{"items":[{"id":"key1","keyPreview":"hk_ab","label":"ci","createdAt":"2026-11-01T00:00:00Z","lastUsedAt":null,"revokedAt":null}],"total":1,"hasMore":false}}}`},
			[]string{"ID", "PREVIEW", "LABEL", "CREATED", "LAST USED", "REVOKED", "key1", "hk_ab"},
			[]string{"ID (what \"hadron app key revoke\" takes), PREVIEW", "LAST USED and REVOKED"}, nil, true},
		{"user create", []string{"app", "user", "create"}, []string{"app", "user", "create", "--external-id", "crm-1"},
			map[string]string{"CreateAppUser": `{"data":{"createAppUser":{"id":"u1","handle":"ada","name":"Ada","urn":"hrn:user:ada","externalId":"crm-1"}}}`},
			[]string{"u1", "ada", "crm-1"}, []string{"(id, handle, name, URN and external id"}, []string{"id", "handle", "name", "urn", "externalId"}, false},
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
				var arr []any
				if err := json.Unmarshal([]byte(jsonOut), &arr); err != nil {
					t.Fatalf("--json is not an array: %v: %s", err, jsonOut)
				}
				return
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(jsonOut), &obj); err != nil {
				t.Fatalf("--json is not an object: %v: %s", err, jsonOut)
			}
			for _, k := range c.keys {
				if _, ok := obj[k]; !ok {
					t.Errorf("%v --json lacks key %q: %s", c.args, k, jsonOut)
				}
				if !strings.Contains(help, k) {
					t.Errorf("%v help does not name the --json key %q", c.page, k)
				}
			}
		})
	}
}
