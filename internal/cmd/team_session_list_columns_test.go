package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// #486 — both `session list` tables led with an opaque UUID and dropped the
// one field that says what a worker IS. The role was already nested on
// TeamSessionFields and discarded between the wire and the render, so this
// costs no query change and no round trip.

// A session with no nested worker: predates worker binding, or the worker is
// unreadable. The role has NO fallback — unlike the name, nothing else on the
// wire carries it — so it must render as a dash rather than be inferred.
const workerlessSessionJSON = `{"id":"s-bare","agentId":"agt1","workerId":null,
	"worker":null,"userId":"u-holger","type":"DEVELOPER",
	"repo":"hadron-memory/hadron-cli","branch":null,"prNumber":null,
	"startedAt":"2026-08-11T09:00:00Z","endedAt":null,"host":"mac1","tool":"claude-code",
	"transcriptPath":null,"llmModel":null}`

func TestSessionListLeadsWithTheWorkerAndItsRole(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	header := strings.SplitN(out.String(), "\n", 2)[0]

	// ROLE is the addition, immediately after WORKER: what a worker IS, beside
	// what it is called.
	if !strings.Contains(header, "ROLE") {
		t.Errorf("the role column must be present:\n%s", header)
	}
	// POSITIONS, not relative order (PR #521 review, @copilot): "SESSION comes
	// after ROLE" is satisfied by SESSION sitting third, which is most of the
	// defect still present.
	cols := strings.Fields(header)
	if len(cols) < 3 {
		t.Fatalf("unexpected header: %q", header)
	}
	if cols[0] != "WORKER" {
		t.Errorf("WORKER must anchor the row, not an id: %v", cols)
	}
	if cols[1] != "ROLE" {
		t.Errorf("ROLE must sit immediately after WORKER: %v", cols)
	}
	// SESSION last: it cannot be dropped — `session end --session <id>` and the
	// worklog join need it — but it does not belong in the position that
	// anchors the reader's eye, which is the whole complaint.
	if cols[len(cols)-1] != "SESSION" {
		t.Errorf("SESSION must be the LAST column: %v", cols)
	}
	if !strings.Contains(out.String(), "backend-engineer") {
		t.Errorf("the role must render:\n%s", out.String())
	}
}

// The id stays FULL. A shortened one would look tidier and silently break
// copy-paste into `--session`; truncation is only safe once that flag learns
// prefix resolution, which is a deliberate change rather than an assumption.
//
// Asserted on the LISTING table only. The provenance table needs an App scope
// and a worklog fixture, and is covered by its own test below — an arm that
// skips itself on a setup error would assert nothing while looking covered,
// which is the defect this repo has spent the week removing.
func TestSessionListKeepsTheSessionIDCopyPasteable(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The WHOLE id, not a prefix of it.
	if !strings.Contains(out.String(), "s-old") {
		t.Errorf("the full session id must survive:\n%s", out.String())
	}
	if strings.Contains(out.String(), "s-ol ") || strings.Contains(out.String(), "s-…") {
		t.Errorf("the id must not be truncated — that breaks --session:\n%s", out.String())
	}
}

// The provenance table gets the same treatment, and needs its own stubs: it
// resolves an App and reads the worklog before it renders.
func TestSessionListProvenanceTableAlsoLeadsWithTheWorker(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamMemoryApp": `{"data":{"memory":{"id":"m1","appId":"app1"}}}`,
		"TeamWorkItems": `{"data":{"teamWorkItems":{"items":[{"nodeId":"w1","sessionId":"s-old",
			"workerId":"wkr1","workerName":"Iris","tool":"github","kind":"pr",
			"ref":"hadron-memory/hadron-cli#371","action":"opened",
			"at":"2026-08-13T10:00:00Z","detail":null}],"total":1}}}`,
		"GetTeamSession": `{"data":{"session":` + activeSessionJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--pr", "hadron-memory/hadron-cli#371",
		"-m", "acme.com::eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	cols := strings.Fields(strings.SplitN(out.String(), "\n", 2)[0])
	if len(cols) < 3 {
		t.Fatalf("unexpected header: %v", cols)
	}
	if cols[0] != "WORKER" || cols[1] != "ROLE" {
		t.Errorf("the provenance table must lead WORKER ROLE too: %v", cols)
	}
	if cols[len(cols)-1] != "SESSION" {
		t.Errorf("SESSION must be LAST here too: %v", cols)
	}
	if !strings.Contains(out.String(), "s-old") {
		t.Errorf("the full session id must survive here too:\n%s", out.String())
	}
}

// A session with no worker renders dashes rather than inventing a role. The
// name has fallbacks (worker id, then agent id); the role has none, because
// nothing else on the wire carries it.
func TestSessionListDashesAnAbsentRole(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamSessions": `{"data":{"sessions":[` + workerlessSessionJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The ROLE CELL of that row, not "a dash appears somewhere in the output"
	// (PR #521 review, @copilot): PR and ENDED are dashes on this fixture too,
	// so the loose assertion passed whatever the role column did.
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	cols := strings.Fields(lines[0])
	roleAt := -1
	for i, c := range cols {
		if c == "ROLE" {
			roleAt = i
		}
	}
	if roleAt < 0 {
		t.Fatalf("no ROLE column: %v", cols)
	}
	var row []string
	for _, l := range lines[1:] {
		if strings.Contains(l, "s-bare") {
			row = strings.Fields(l)
		}
	}
	if row == nil {
		// A session without a worker is surfaced, not dropped — the
		// visibility-gap rule.
		t.Fatalf("a worker-less session must still be listed:\n%s", out.String())
	}
	if roleAt >= len(row) || row[roleAt] != "—" {
		t.Errorf("the ROLE cell must be a dash, got %v (row %v)", row[min(roleAt, len(row)-1)], row)
	}
}

// --json gains the key and moves none. The name is `workerRole`, matching its
// siblings `workerName`/`workerId` rather than the bare `role` the issue
// sketched — the DTO already namespaces the worker's fields, and a bare `role`
// beside `workerName` would read as the SESSION's role.
func TestSessionListJSONGainsWorkerRole(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `,` + workerlessSessionJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	if len(got) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(got))
	}
	if got[0]["workerRole"] != "backend-engineer" {
		t.Errorf("workerRole = %v", got[0]["workerRole"])
	}
	// Present as null rather than omitted: the shape stays uniform across rows,
	// so a consumer can tell "no role" from "this CLI does not know the key".
	role, present := got[1]["workerRole"]
	if !present || role != nil {
		t.Errorf("an absent role must be present as null, got %v (present=%v)", role, present)
	}
	// Nothing that was already there moved.
	for _, key := range []string{"id", "workerName", "workerId", "userId", "startedAt", "active"} {
		if _, ok := got[0][key]; !ok {
			t.Errorf("existing --json key %q disappeared: %s", key, out.String())
		}
	}
}
