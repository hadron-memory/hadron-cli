package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// #510 / cor:api:230:01 — the portal link is SERVER-BUILT and the client never
// composes one. Two independent reasons it can be absent (no usable web origin
// on the deployment; no URN to build from), both rendering as nothing.
//
// The shared irisWorkerJSON carries NO portalUrl, so it is already the absent
// case — which means a test that only used it would watch the feature not
// happen and pass. withPortalURL is the present case, and it has to be built.
func withPortalURL(url string) string {
	out := strings.Replace(irisWorkerJSON, `"slug":"iris"`,
		`"portalUrl":"`+url+`","slug":"iris"`, 1)
	if out == irisWorkerJSON {
		panic("withPortalURL matched nothing — fixture shape changed")
	}
	return out
}

// The line `worker get` prints, and the framing is the point: `URL:` directly
// under `urn:`, exactly as `node get` renders it, so the standing instruction
// in every role-agent briefing — never hand-build a portal link, copy the URL
// line — reads identically on both surfaces.
func TestTeamWorkerGetPrintsPortalURL(t *testing.T) {
	teamGitDir(t)
	const url = "https://hadronmemory.com/app/u/hrn:worker:acme.com:eng-team:iris"
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":       `{"data":{"worker":` + withPortalURL(url) + `}}`,
		"TeamAppIdentity": teamAppIdentityJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "wkr1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "URL: "+url) {
		t.Errorf("worker get must print the server's URL line:\n%s", out.String())
	}
	// Under the urn it opens, not somewhere else in the block: the two are one
	// answer about one worker, and a reader copying "the URL line" should not
	// have to work out which identifier it belongs to.
	urnAt := strings.Index(out.String(), "urn: ")
	urlAt := strings.Index(out.String(), "URL: ")
	if urnAt < 0 || urlAt < urnAt {
		t.Errorf("URL must follow the urn it opens:\n%s", out.String())
	}
}

// The absent case, which is the whole contract: no line, no placeholder, and
// above all NO locally-composed fallback. A link to a guessed origin fails
// silently for whoever clicks it — worse than the caller seeing nothing.
func TestTeamWorkerGetOmitsAnAbsentPortalURL(t *testing.T) {
	teamGitDir(t)
	for _, tc := range []struct{ name, worker string }{
		// Null: the server had no usable origin, or no URN to build from.
		{"null", irisWorkerJSON},
		// Empty string: a distinct wire value that must degrade identically —
		// rendering "URL: " with nothing after it is the shape a bare nil
		// check lets through.
		{"empty", withPortalURL("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{
				"GetWorker":       `{"data":{"worker":` + tc.worker + `}}`,
				"TeamAppIdentity": teamAppIdentityJSON,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "worker", "get", "wkr1", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if strings.Contains(out.String(), "URL:") {
				t.Errorf("an absent link must render nothing at all:\n%s", out.String())
			}
			// Never a guess. hadronmemory.com is the origin a fallback would
			// most plausibly reach for, and the point of the field is that the
			// client does not know it.
			if strings.Contains(out.String(), "http") {
				t.Errorf("the CLI must not compose a link when the server sent none:\n%s", out.String())
			}
			// The identifier SURVIVES the link's absence — they are independent
			// answers, and losing the link must not cost the caller the ref.
			if !strings.Contains(out.String(), "urn: hrn:worker:acme.com:eng-team:iris") {
				t.Errorf("the urn must still print when the link is absent:\n%s", out.String())
			}
		})
	}
}

// --json is the agent contract, and it must answer the SAME shape on both
// worker surfaces: a key present on `get` and missing on `list` for the same
// entity is something every consumer then has to special-case.
func TestTeamWorkerPortalURLInJSONOnBothSurfaces(t *testing.T) {
	teamGitDir(t)
	const url = "https://hadronmemory.com/app/u/hrn:worker:acme.com:eng-team:iris"
	withURL := withPortalURL(url)

	t.Run("get", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"GetWorker": `{"data":{"worker":` + withURL + `}}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "worker", "get", "wkr1", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if dto["portalUrl"] != url {
			t.Errorf("portalUrl = %v, want %q", dto["portalUrl"], url)
		}
	})

	t.Run("list", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"WorkersRoster": `{"data":{"workers":{"total":1,"items":[` + withURL + `]}}}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "worker", "list", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		var workers []map[string]any
		if err := json.Unmarshal([]byte(out.String()), &workers); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(workers) != 1 {
			t.Fatalf("want 1 worker, got %d: %s", len(workers), out.String())
		}
		if workers[0]["portalUrl"] != url {
			t.Errorf("portalUrl = %v, want %q", workers[0]["portalUrl"], url)
		}
	})

	// The absent value stays a JSON null rather than vanishing: `portalUrl` is
	// not omitempty, so a consumer can tell "this deployment emits no links"
	// from "this CLI is too old to know about the key".
	t.Run("null is present in the shape", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"GetWorker": `{"data":{"worker":` + irisWorkerJSON + `}}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "worker", "get", "wkr1", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(out.String(), `"portalUrl": null`) {
			t.Errorf("an absent link must still carry its key as null:\n%s", out.String())
		}
	})
}

// The table deliberately gains NO column: a full URL per row is wide, and that
// table is the one #487's remaining half still wants HELD and LAST DRIVEN
// columns in. This pins the decision so it is revisited on purpose rather than
// drifted into.
func TestTeamWorkerListTableHasNoURLColumn(t *testing.T) {
	teamGitDir(t)
	const url = "https://hadronmemory.com/app/u/hrn:worker:acme.com:eng-team:iris"
	gql, _ := captureGraphQL(t, map[string]string{
		"WorkersRoster": `{"data":{"workers":{"total":1,"items":[` +
			withPortalURL(url) + `]}}}`,
		"TeamAppIdentity": teamAppIdentityJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "list", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out.String(), url) {
		t.Errorf("the roster table must stay narrow — the URL is a --json-only field:\n%s", out.String())
	}
	// But the URN column, which is what a caller resolves with, is still there.
	if !strings.Contains(out.String(), "hrn:worker:acme.com:eng-team:iris") {
		t.Errorf("the urn column must survive:\n%s", out.String())
	}
}

// The bind receipt. Beyond #510's literal list, and here for two reasons:
// hadron_start_session already returns this line, so a desktop worker got its
// link at bind and a CLI one did not; and the boot briefing printed directly
// below tells this worker to sign what it publishes with a clickable URN.
func TestTeamSessionStartReceiptCarriesThePortalURL(t *testing.T) {
	teamGitDir(t)
	const url = "https://hadronmemory.com/app/u/hrn:worker:acme.com:eng-team:iris"
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + withPortalURL(url) + `}}`,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--tool", "claude-code", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "URL: "+url) {
		t.Errorf("the bind receipt must hand the worker its link:\n%s", out.String())
	}
}

// A WIRE assertion, and it must read the generated operation rather than
// captureGraphQL — which records only request VARIABLES, so searching it for a
// selected field can never fail (review:assert-the-query-not-the-capture).
//
// It matters most for WorkersRoster: that projection exists to TRIM fields, so
// a future trim could drop portalUrl and only --json would notice.
func TestWorkerQueriesSelectPortalURL(t *testing.T) {
	for name, op := range map[string]string{
		"GetWorker":     gen.GetWorker_Operation,
		"WorkersRoster": gen.WorkersRoster_Operation,
		"Workers":       gen.Workers_Operation,
	} {
		var found bool
		for _, line := range strings.Split(op, "\n") {
			// Field-EXACT, not a substring: a sibling field sharing the prefix
			// would satisfy Contains and turn this into a false positive.
			if strings.TrimSpace(line) == "portalUrl" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s must select portalUrl on the wire:\n%s", name, op)
		}
	}
}
