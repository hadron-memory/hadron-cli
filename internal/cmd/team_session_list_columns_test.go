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
	workerAt := strings.Index(header, "WORKER")
	roleAt := strings.Index(header, "ROLE")
	sessionAt := strings.Index(header, "SESSION")
	if workerAt != 0 {
		t.Errorf("WORKER must anchor the row, not an id:\n%s", header)
	}
	if roleAt < workerAt {
		t.Errorf("ROLE belongs beside WORKER:\n%s", header)
	}
	// SESSION last: it cannot be dropped — `session end --session <id>` and the
	// worklog join need it — but it does not belong in the position that
	// anchors the reader's eye, which is the whole complaint.
	if sessionAt < roleAt {
		t.Errorf("SESSION must move to the end:\n%s", header)
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
	header := strings.SplitN(out.String(), "\n", 2)[0]
	if !strings.Contains(header, "ROLE") {
		t.Errorf("the provenance table must carry ROLE too:\n%s", header)
	}
	if strings.Index(header, "WORKER") != 0 {
		t.Errorf("WORKER must anchor the provenance row as well:\n%s", header)
	}
	if strings.Index(header, "SESSION") < strings.Index(header, "ROLE") {
		t.Errorf("SESSION must move to the end here too:\n%s", header)
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
	if !strings.Contains(out.String(), "—") {
		t.Errorf("an absent role must render as a dash:\n%s", out.String())
	}
	// And the row still appears — a session without a worker is surfaced, not
	// dropped (the visibility-gap rule).
	if !strings.Contains(out.String(), "s-bare") {
		t.Errorf("a worker-less session must still be listed:\n%s", out.String())
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
