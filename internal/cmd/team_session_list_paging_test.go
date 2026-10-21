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

// sessionsNoTotal: the sessions list returns a bare array and no total.
func sessionsNoTotal(page []string, _ int) string {
	return `{"data":{"sessions":[` + strings.Join(page, ",") + `]}}`
}

func runSessionList(t *testing.T, src *pagedSource, args ...string) (ids []string, calls []map[string]any, err error) {
	t.Helper()
	teamGitDir(t)
	gql, rec := pagedGraphQL(t, map[string]*pagedSource{"TeamSessions": src}, sessionRenderStubs(map[string]string{}))
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
	return ids, rec["TeamSessions"].Calls, err
}

// cli#967: no --limit/--offset lists every session, however the server clamps.
func TestSessionListDrainsPastAClampedPage(t *testing.T) {
	ids, calls, err := runSessionList(t, &pagedSource{rows: sessionRows(45), serve: 20, wrap: sessionsNoTotal})
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
	if len(calls) != 4 {
		t.Errorf("want 4 requests (20+20+5+empty), got %d", len(calls))
	}
}

func TestSessionListActiveDrainsPastAClampedPage(t *testing.T) {
	ids, _, err := runSessionList(t, &pagedSource{rows: sessionRows(45), serve: 20, wrap: sessionsNoTotal}, "--active")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 30 { // 45 rows, indexes divisible by 3 (15 of them) have ended
		t.Fatalf("want 30 active sessions, got %d", len(ids))
	}
}

func TestSessionListExplicitLimitOrOffsetIsOneRequest(t *testing.T) {
	for _, args := range [][]string{{"--limit", "5"}, {"--offset", "3"}} {
		ids, calls, err := runSessionList(t, &pagedSource{rows: sessionRows(45), serve: 20, wrap: sessionsNoTotal}, args...)
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
	ids, _, err := runSessionList(t, &pagedSource{rows: sessionRows(45), serve: 20, wrap: sessionsNoTotal, failAtCall: 2})
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
