package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// sessionRows builds n sessions, newest first; every third one has ended.
func sessionRows(n int) []string {
	rows := make([]string, n)
	for i := range rows {
		ended := "null"
		if i%3 == 0 {
			ended = `"2026-08-11T10:00:00Z"`
		}
		rows[i] = fmt.Sprintf(`{"id":"s%03d","agentId":"agt1","workerId":null,"worker":null,"userId":"u-holger","type":"DEVELOPER","repo":"hadron-memory/hadron-cli","branch":null,"prNumber":null,"startedAt":"2026-08-11T09:00:00Z","endedAt":%s,"host":"mac1","tool":"claude-code","transcriptPath":null,"llmModel":null}`, i, ended)
	}
	return rows
}

// sessionsPageAt renders sessionsPage: the real total and hasMore for the
// window (cli#1339).
func sessionsPageAt(page []string, total, offset int) string {
	return fmt.Sprintf(`{"data":{"sessionsPage":{"total":%d,"hasMore":%v,"items":[%s]}}}`, total, offset+len(page) < total, strings.Join(page, ","))
}

func runSessionList(t *testing.T, src *pagedSource, args ...string) (ids []string, calls []map[string]any, err error) {
	t.Helper()
	teamGitDir(t)
	gql, rec := pagedGraphQL(t, map[string]*pagedSource{"TeamSessionsPage": src}, sessionRenderStubs(map[string]string{}))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "session", "list", "--json", "--server", gql.URL}, args...))
	err = root.Execute()
	if err == nil {
		var rows []struct {
			ID string `json:"id"`
		}
		if jerr := json.Unmarshal([]byte(out.String()), &rows); jerr != nil {
			t.Fatalf("not a JSON array: %v\n%s", jerr, out.String())
		}
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
	}
	return ids, rec["TeamSessionsPage"].Calls, err
}

// cli#967/#1339: no --limit/--offset lists every session, however the server
// clamps, and the page's total proves the end (no trailing empty request).
func TestSessionListDrainsPastAClampedPage(t *testing.T) {
	ids, calls, err := runSessionList(t, &pagedSource{rows: sessionRows(45), serve: 20, wrapAt: sessionsPageAt})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 45 {
		t.Fatalf("want 45 sessions, got %d (silent truncation)", len(ids))
	}
	for i, id := range ids {
		if id != fmt.Sprintf("s%03d", i) {
			t.Fatalf("row %d is %s: order lost or duplicated", i, id)
		}
	}
	if len(calls) != 3 {
		t.Errorf("want 3 requests (20+20+5; the total proves the end), got %d", len(calls))
	}
}

func TestSessionListActiveDrainsPastAClampedPage(t *testing.T) {
	ids, _, err := runSessionList(t, &pagedSource{rows: sessionRows(45), serve: 20, wrapAt: sessionsPageAt}, "--active")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 30 { // 45 rows, indexes divisible by 3 (15 of them) have ended
		t.Fatalf("want 30 active sessions, got %d", len(ids))
	}
}

func TestSessionListExplicitLimitOrOffsetIsOneRequest(t *testing.T) {
	for _, args := range [][]string{{"--limit", "5"}, {"--offset", "3"}} {
		ids, calls, err := runSessionList(t, &pagedSource{rows: sessionRows(45), serve: 20, wrapAt: sessionsPageAt}, args...)
		if err != nil {
			t.Fatal(err)
		}
		if len(calls) != 1 {
			t.Errorf("%v: want one request, got %d", args, len(calls))
		}
		if len(ids) == 0 || len(ids) > 20 {
			t.Errorf("%v: unexpected page of %d", args, len(ids))
		}
	}
}

func TestSessionListLaterPageErrorFailsTheListing(t *testing.T) {
	ids, _, err := runSessionList(t, &pagedSource{rows: sessionRows(45), serve: 20, wrapAt: sessionsPageAt, failAtCall: 2})
	if err == nil {
		t.Fatalf("a failed page 2 must not read as a complete list (got %d sessions)", len(ids))
	}
}

// The provenance (--commit) read pages the worklog by total; a clamped page
// must not end it early (cli#967).
func TestSessionListProvenanceDrainsPastAClampedPage(t *testing.T) {
	teamGitDir(t)
	rows := make([]string, 45)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"nodeId":"w%d","sessionId":"s%03d","workerId":"wkr1","workerName":"Iris","tool":"github","kind":"commit","ref":"hadron-memory/hadron-cli@93200b2","action":"pushed","at":"2026-08-13T10:00:00Z","detail":null}`, i, i)
	}
	gql, rec := pagedGraphQL(t, map[string]*pagedSource{
		"TeamWorkItems": {rows: rows, serve: 20, wrap: itemsEnvelope("teamWorkItems")},
	}, map[string]string{
		"TeamMemoryApp":  `{"data":{"memory":{"id":"m1","appId":"capp100000000000000000000"}}}`,
		"GetTeamSession": `{"data":{"session":` + sessionRows(1)[0] + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--commit", "hadron-memory/hadron-cli@93200b2",
		"-m", "acme.com::eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var got []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if n := len(rec["TeamWorkItems"].Calls); n != 3 {
		t.Errorf("want 3 worklog pages (20+20+5), got %d", n)
	}
	if len(got) != 45 {
		t.Errorf("want 45 provenance sessions, got %d", len(got))
	}
}

// cli#1339: a bounded page reports the real total and the next offset on stderr,
// while --json stays the bare array.
func TestSessionListPageShowsRealTotalAndNextOffset(t *testing.T) {
	teamGitDir(t)
	gql, _ := pagedGraphQL(t, map[string]*pagedSource{"TeamSessionsPage": {rows: sessionRows(45), serve: 20, wrapAt: sessionsPageAt}}, sessionRenderStubs(map[string]string{}))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--json", "--limit", "20", "--offset", "20", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	errOut := f.IOStreams.ErrOut.(*strings.Builder).String()
	for _, want := range []string{"showing 21-40 of 45", "more: --offset 40"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q: %q", want, errOut)
		}
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil || len(rows) != 20 {
		t.Fatalf("stdout must stay a bare 20-element array: %v", err)
	}
}

// A server that ignores the offset repeats the first page forever; the drain
// fails as a whole instead of returning a partial list.
func TestSessionListNonAdvancingServerFails(t *testing.T) {
	teamGitDir(t)
	gql, _ := pagedGraphQL(t, map[string]*pagedSource{"TeamSessionsPage": {rows: sessionRows(45), serve: 20, wrapAt: func(_ []string, total, _ int) string {
		return sessionsPageAt(sessionRows(45)[:20], total, 0)
	}}}, sessionRenderStubs(map[string]string{}))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != 7 || out.String() != "" {
		t.Fatalf("want exit 7 and no partial list, got %v out %q", err, out.String())
	}
}

// BLIND P2 (#1460): an empty terminal page with the total still unmet is a
// partial list even when hasMore is false.
func TestSessionListUnmetTotalOnTerminalPageFails(t *testing.T) {
	teamGitDir(t)
	gql, _ := pagedGraphQL(t, map[string]*pagedSource{"TeamSessionsPage": {rows: sessionRows(45), serve: 20, wrapAt: func(page []string, _, offset int) string {
		return sessionsPageAt(page, 45, 0) // total 45, hasMore from offset 0 until the page is empty
	}}}, sessionRenderStubs(map[string]string{}))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != 7 || out.String() != "" {
		t.Fatalf("want exit 7 and no partial list, got %v out %.80q", err, out.String())
	}
}
