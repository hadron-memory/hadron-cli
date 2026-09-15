package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// #455 — the USER column showed the raw PK (`019d28f1…`), which is
// unrecognisable and not accepted by anything a reader would type next. It now
// shows the person's URN.
//
// #481 — the provenance query rendered "nobody logged this", "wrong App scope"
// and "no such artifact" as the same empty table, and the inference a reader
// draws from a bare header row is the one that is wrong: that the worklog is
// broken.

const provenanceWorklog = `{"data":{"teamWorkItems":{"total":1,"items":[
	{"nodeId":"w1","sessionId":"s-old","workerId":"wkr1","workerName":"Iris","tool":"github",
	 "kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"opened",
	 "at":"2026-08-13T10:00:00Z","detail":null}]}}}`

const emptyWorklog = `{"data":{"teamWorkItems":{"total":0,"items":[]}}}`

func TestSessionListShowsTheUserURNNotTheID(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, sessionRenderStubs(map[string]string{
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
	}))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "hrn:user:holger") {
		t.Errorf("USER must render the person's URN:\n%s", got)
	}
	// The raw id must be GONE from that cell, not merely joined by the URN —
	// "shows the URN too" would satisfy a weaker assertion while leaving the
	// column as wide and as unreadable as the complaint.
	if strings.Contains(got, "u-holger") {
		t.Errorf("the raw user id must not survive in the USER cell:\n%s", got)
	}
}

// Best-effort, like describeApp: a user the caller cannot read degrades to the
// id — which is exactly what the column showed before #455, so the worst case
// is today's behaviour rather than a blank cell.
func TestSessionListFallsBackToTheUserIDWhenUnreadable(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
		// Denied and missing are both null here (anti-enumeration).
		"GetUser": `{"data":{"user":null}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("an unreadable user must not fail the listing: %v", err)
	}
	if !strings.Contains(out.String(), "u-holger") {
		t.Errorf("degrade to the id rather than blanking the cell:\n%s", out.String())
	}
}

// A user with no handle has nothing to compose a URN from, so the server sends
// null — a different route to the same fallback, and the one a bare assertion
// on "user == nil" would miss.
func TestSessionListFallsBackWhenTheUserHasNoURN(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
		"GetUser": `{"data":{"user":{"id":"u-holger","urn":null,"name":"Holger","email":null,
			"handle":null,"githubUsername":null,"roles":[],"identityProvider":null,
			"githubId":null,"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "u-holger") {
		t.Errorf("a null urn must fall back to the id:\n%s", out.String())
	}
}

// --json must not pay for a render-only decoration. The labeller lives inside
// the render callback for exactly this reason (the PR #504 rule), and the
// cheapest way to pin it is that the read never happens.
func TestSessionListJSONIssuesNoDecoratingReads(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, called := captured["GetUser"]; called {
		t.Error("--json must not issue the user lookup: it renders no USER cell")
	}
	// And the shape is untouched: still a bare array carrying the raw userId,
	// which is the actionable ref an agent wants.
	var rows []struct {
		UserID *string `json:"userId"`
	}
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatalf("--json must stay a bare array: %v\n%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].UserID == nil || *rows[0].UserID != "u-holger" {
		t.Errorf("--json keeps the raw userId: %+v", rows)
	}
}

// #481's core: zero records must SAY so, and say which App and which ref it
// looked in. A bare header row answers three different questions identically.
func TestProvenanceEmptyResultExplainsItself(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, sessionRenderStubs(map[string]string{
		"TeamMemoryApp": `{"data":{"memory":{"id":"m1","appId":"capp100000000000000000000"}}}`,
		"TeamWorkItems": emptyWorklog,
	}))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--pr", "https://github.com/hadron-memory/urn-lib-js/pull/15",
		"-m", "acme.com::eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("an empty provenance result is not an error: %v", err)
	}
	got := out.String()

	// 1. It says there are none, in words.
	if !strings.Contains(got, "no worklog records") {
		t.Errorf("an empty result must say so:\n%s", got)
	}
	// 2. It says the absence is not a malfunction — the reading the issue was
	//    filed about.
	if !strings.Contains(got, "not an error") {
		t.Errorf("must distinguish 'nobody logged it' from 'broken':\n%s", got)
	}
	// 3. It names the App, so "wrong team" is checkable.
	if !strings.Contains(got, "app:") {
		t.Errorf("the resolved App must be named even with zero rows:\n%s", got)
	}
	// 4. It echoes the CANONICAL ref, not what was typed — the user pasted a
	//    URL, and seeing the normalized form is how a wrong repo or number
	//    becomes self-diagnosable.
	if !strings.Contains(got, "hadron-memory/urn-lib-js#15") {
		t.Errorf("the normalized ref must be echoed:\n%s", got)
	}
	if strings.Contains(got, "https://github.com") {
		t.Errorf("echo the canonical ref, not the raw input:\n%s", got)
	}
}

// The scope line names WHICH branch answered, not merely that one did. Two
// resolutions, two phrases — a reader cannot tell "-m you just typed" from
// "a binding you forgot" otherwise.
func TestProvenanceNamesTheScopeSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"explicit -m", []string{"-m", "acme.com::eng-team"}, "from -m"},
		{"explicit --app", []string{"--app", "acme.com:eng-team"}, "from --app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			teamGitDir(t)
			gql, _ := captureGraphQL(t, sessionRenderStubs(map[string]string{
				"TeamMemoryApp":  `{"data":{"memory":{"id":"m1","appId":"capp100000000000000000000"}}}`,
				"TeamWorkItems":  provenanceWorklog,
				"GetTeamSession": `{"data":{"session":` + activeSessionJSON + `}}`,
			}))
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := append([]string{"team", "session", "list", "--pr", "hadron-memory/hadron-cli#371"}, tc.args...)
			root.SetArgs(append(args, "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("want the source phrase %q:\n%s", tc.want, out.String())
			}
		})
	}
}

// The preamble prints BEFORE the payload. Asserted by position rather than
// presence: a scope line under the table is read after the reader has already
// drawn a conclusion from it, which is the whole point of the check.
func TestProvenanceScopeLinePrecedesTheTable(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, sessionRenderStubs(map[string]string{
		"TeamMemoryApp":  `{"data":{"memory":{"id":"m1","appId":"capp100000000000000000000"}}}`,
		"TeamWorkItems":  provenanceWorklog,
		"GetTeamSession": `{"data":{"session":` + activeSessionJSON + `}}`,
	}))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--pr", "hadron-memory/hadron-cli#371",
		"-m", "acme.com::eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	app, header := strings.Index(got, "app:"), strings.Index(got, "WORKER")
	if app < 0 || header < 0 {
		t.Fatalf("expected both a scope line and a table:\n%s", got)
	}
	if app > header {
		t.Errorf("the scope line must come BEFORE the table:\n%s", got)
	}
}

// --json is render-only-free here too: no scope line, no ref echo, still an
// array. Adding scope to a top-level array would mean array → object, a break.
func TestProvenanceJSONShapeUnchanged(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamMemoryApp":  `{"data":{"memory":{"id":"m1","appId":"capp100000000000000000000"}}}`,
		"TeamWorkItems":  provenanceWorklog,
		"GetTeamSession": `{"data":{"session":` + activeSessionJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--pr", "hadron-memory/hadron-cli#371",
		"-m", "acme.com::eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatalf("--json must stay a bare array: %v\n%s", err, out.String())
	}
	if len(rows) != 1 {
		t.Errorf("expected one session, got %d", len(rows))
	}
	if strings.Contains(out.String(), "app:") || strings.Contains(out.String(), "no worklog records") {
		t.Errorf("the human preamble must not leak into --json:\n%s", out.String())
	}
}

// An empty result under --json is an empty ARRAY, not the prose. A script
// branching on length must keep working.
func TestProvenanceEmptyJSONIsAnEmptyArray(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamMemoryApp": `{"data":{"memory":{"id":"m1","appId":"capp100000000000000000000"}}}`,
		"TeamWorkItems": emptyWorklog,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--pr", "hadron-memory/hadron-cli#371",
		"-m", "acme.com::eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "[]" {
		t.Errorf("empty --json must be [], got %q", got)
	}
}
