package cmd

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// cli#752 — server-authored authorship and content-validation stamps
// (hadron-server#1326) in `node get` and the revision reads, shown as the
// server reports them and never upgraded to a claim.

const (
	taskAuthorship     = `{"kind":"TASK","authoredAt":"2026-09-28T10:00:00Z","taskRef":"hrn:node:acme.com:kb:tasks:write","taskRevision":3,"human":null}`
	redactedAuthorship = `{"kind":"TASK","authoredAt":"2026-09-28T10:00:00Z","taskRef":null,"taskRevision":null,"human":null}`
	humanAuthorship    = `{"kind":"HUMAN","authoredAt":"2026-09-28T10:00:00Z","taskRef":null,"taskRevision":null,"human":{"handle":"holger","urn":"hrn:user:holger"}}`
)

func validation(state, outcome string, forRevision int) string {
	if outcome == "" {
		return `{"state":"` + state + `","latestReport":null}`
	}
	return `{"state":"` + state + `","latestReport":{"outcome":"` + outcome + `","nodeRevision":` + itoa(forRevision) +
		`,"reportedAt":"2026-09-28T11:00:00Z","validationTaskRef":"hrn:node:acme.com:kb:tasks:check","validationTaskRevision":2}}`
}

func stampedLive(rev int, authorship, cv string) string {
	return `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[{"id":"n1","revision":` + itoa(rev) +
		`,"authorship":` + authorship + `,"contentValidation":` + cv + `}]}}}`
}

// stampedServer answers the stamped probe with `probes` in order (the last
// one repeating) and GetNode with the fixture node.
func stampedServer(t *testing.T, probes ...string) string {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		mu.Lock()
		defer mu.Unlock()
		switch op {
		case "ResolveUrn":
			return `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`
		case "GetNode":
			return nodeGetJSON(testNodeURL)
		case "NodeLiveRevisionsStamped":
			p := probes[min(calls, len(probes)-1)]
			calls++
			return p
		}
		return ""
	})
	return gql.URL
}

func nodeGetOut(t *testing.T, url string, json bool) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := []string{"node", "get", testNodeURN, "--server", url}
	if json {
		args = append(args, "--json")
	}
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("node get: %v", err)
	}
	return out.String()
}

type stampsJSON struct {
	Revision   *int `json:"revision"`
	Authorship *struct {
		Kind    string  `json:"kind"`
		TaskRef *string `json:"taskRef"`
	} `json:"authorship"`
	ContentValidation *struct {
		State        string `json:"state"`
		LatestReport *struct {
			Outcome      string `json:"outcome"`
			NodeRevision int    `json:"nodeRevision"`
		} `json:"latestReport"`
	} `json:"contentValidation"`
}

func TestNodeGetShowsTheStampsOfThisRevision(t *testing.T) {
	url := stampedServer(t, stampedLive(7, taskAuthorship, validation("PASS", "PASS", 7)))
	var d stampsJSON
	raw := nodeGetOut(t, url, true)
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("--json: %v (%q)", err, raw)
	}
	if d.Revision == nil || *d.Revision != 7 || d.Authorship == nil || d.Authorship.Kind != "TASK" ||
		d.ContentValidation == nil || d.ContentValidation.State != "PASS" {
		t.Errorf("unexpected stamps: %s", raw)
	}
	text := nodeGetOut(t, url, false)
	for _, want := range []string{"authorship: task hrn:node:acme.com:kb:tasks:write rev 3", "validation: PASS for this revision, by task hrn:node:acme.com:kb:tasks:check rev 2", "(advisory)"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

// A report for an OLDER revision is not a pass for this one: STALE, naming
// the revision the report was for — the old PASS is never shown as current.
func TestNodeGetStaleValidationClaimsNoPass(t *testing.T) {
	url := stampedServer(t, stampedLive(9, humanAuthorship, validation("STALE", "PASS", 7)))
	text := nodeGetOut(t, url, false)
	if !strings.Contains(text, "validation: STALE — the latest report (PASS) was for revision 7, not this revision") ||
		strings.Contains(text, "validation: PASS") {
		t.Errorf("a stale report must not read as a pass:\n%s", text)
	}
	if !strings.Contains(text, "authorship: human @holger") {
		t.Errorf("human authorship: \n%s", text)
	}
}

func TestNodeGetFailAndErrorStatesAreReported(t *testing.T) {
	for _, state := range []string{"FAIL", "ERROR"} {
		url := stampedServer(t, stampedLive(4, taskAuthorship, validation(state, state, 4)))
		if text := nodeGetOut(t, url, false); !strings.Contains(text, "validation: "+state+" for this revision") {
			t.Errorf("%s not reported:\n%s", state, text)
		}
	}
}

// Legacy content on a stamping server: authorship null is the server's own
// answer, and validation is present (UNVALIDATED) — distinct from an older
// server, below.
func TestNodeGetUnstampedLegacyNode(t *testing.T) {
	url := stampedServer(t, stampedLive(2, "null", validation("UNVALIDATED", "", 0)))
	raw := nodeGetOut(t, url, true)
	if !strings.Contains(raw, `"authorship": null`) || !strings.Contains(raw, `"state": "UNVALIDATED"`) {
		t.Errorf("unexpected legacy stamps: %s", raw)
	}
	text := nodeGetOut(t, url, false)
	if !strings.Contains(text, "authorship: not recorded") || !strings.Contains(text, "validation: UNVALIDATED") {
		t.Errorf("legacy text: \n%s", text)
	}
}

// The server redacts a task it won't let you read: kind stays TASK, the ref
// is null, and the CLI says so rather than calling it a human or unknown.
func TestNodeGetRedactedTaskIsNotDisclosed(t *testing.T) {
	url := stampedServer(t, stampedLive(3, redactedAuthorship, validation("UNVALIDATED", "", 0)))
	raw := nodeGetOut(t, url, true)
	var d stampsJSON
	_ = json.Unmarshal([]byte(raw), &d)
	if d.Authorship == nil || d.Authorship.Kind != "TASK" || d.Authorship.TaskRef != nil {
		t.Errorf("a redacted task stays TASK with a null ref: %s", raw)
	}
	if text := nodeGetOut(t, url, false); !strings.Contains(text, "a task you can't read (redacted by the server)") {
		t.Errorf("redaction text:\n%s", text)
	}
}

// A server that predates the stamps: contentValidation null (the "can't
// say" answer), no fabricated stamp, and the revision read still works.
func TestNodeGetOnAServerWithoutStamps(t *testing.T) {
	gql := revisionOnlyServer(t)
	raw := nodeGetOut(t, gql, true)
	if !strings.Contains(raw, `"contentValidation": null`) || !strings.Contains(raw, `"authorship": null`) || !strings.Contains(raw, `"revision": 5`) {
		t.Errorf("want null stamps and the revision: %s", raw)
	}
	if text := nodeGetOut(t, gql, false); !strings.Contains(text, "stamps: unknown (the server predates") {
		t.Errorf("older-server text:\n%s", text)
	}
}

func revisionOnlyServer(t *testing.T) string {
	t.Helper()
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "ResolveUrn":
			return `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`
		case "GetNode":
			return nodeGetJSON(testNodeURL)
		case "NodeLiveRevisions":
			return liveRevisions(map[string]int{"n1": 5})
		}
		return "" // NodeLiveRevisionsStamped: the no-stamps default
	})
	return gql.URL
}

// The printed stamps are the AFTER probe's — the one paired with the printed
// revision. A validation report can land without a new revision.
func TestNodeGetPrintsTheAfterProbeStamps(t *testing.T) {
	url := stampedServer(t,
		stampedLive(6, taskAuthorship, validation("UNVALIDATED", "", 0)),
		stampedLive(6, taskAuthorship, validation("FAIL", "FAIL", 6)))
	var d stampsJSON
	raw := nodeGetOut(t, url, true)
	_ = json.Unmarshal([]byte(raw), &d)
	if d.ContentValidation == nil || d.ContentValidation.State != "FAIL" {
		t.Errorf("want the after-probe's FAIL, got %s", raw)
	}
}

// History shows each snapshot's OWN stamps, as the server snapshotted them.
func TestNodeRevisionGetShowsTheSnapshotsStamps(t *testing.T) {
	snap := `{"data":{"nodeRevision":{"id":"r1","nodeId":"n1","loc":"findings:flaky","name":"Flaky","description":null,"tags":[],` +
		`"createdAt":"2026-09-20T00:00:00Z","editedBy":null,"editedByInfo":null,"editedByUser":null,"revLabel":null,"changes":["content"],` +
		`"content":"old body","authorship":` + humanAuthorship + `,"contentValidation":` + validation("PASS", "PASS", 3) + `}}}`
	gql, _ := captureGraphQL(t, map[string]string{"NodeRevisionStamped": snap})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "get", "r1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("revision get: %v", err)
	}
	if !strings.Contains(out.String(), "validation: PASS for this snapshot's revision") || !strings.Contains(out.String(), "authorship: human @holger") {
		t.Errorf("snapshot stamps:\n%s", out.String())
	}
}

func TestNodeRevisionListCarriesStampsAndFallsBack(t *testing.T) {
	row := `{"id":"r1","nodeId":"n1","loc":"findings:flaky","name":"Flaky","description":null,"tags":[],"createdAt":"2026-09-20T00:00:00Z",` +
		`"editedBy":null,"editedByInfo":null,"editedByUser":null,"revLabel":null,"changes":[]`
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn":           `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"NodeRevisionsStamped": `{"data":{"nodeRevisions":[` + row + `,"authorship":null,"contentValidation":` + validation("STALE", "FAIL", 1) + `}]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "list", testNodeURN, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("revision list: %v", err)
	}
	if !strings.Contains(out.String(), `"state": "STALE"`) {
		t.Errorf("stamped list: %s", out.String())
	}

	// An older server: the plain history, with null stamps — never invented.
	gql, _ = captureGraphQL(t, map[string]string{
		"ResolveUrn":    `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"NodeRevisions": `{"data":{"nodeRevisions":[` + row + `}]}}`,
	})
	f, out = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "list", testNodeURN, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("revision list fallback: %v", err)
	}
	if !strings.Contains(out.String(), `"contentValidation": null`) || !strings.Contains(out.String(), `"id": "r1"`) {
		t.Errorf("fallback list: %s", out.String())
	}
}
