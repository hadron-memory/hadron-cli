package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #452 / server #1010 — `promptOverride` was write-once, settable only at
// castWorker time, which fixed a casting's individuality at the one moment
// nobody yet knows what makes it individual.

// updatedWorkerJSON is the worker as updateWorker returns it: the override set,
// and `prompt` RE-RENDERED to the briefing a bind now delivers.
const updatedWorkerJSON = `{"id":"wkr1","urn":"hrn:worker:acme.com:eng-team:iris","slug":"iris",
	"appId":"capp100000000000000000000","agentId":"agt1","name":"Iris","role":"backend-engineer",
	"prompt":"You are Iris.\n\nYou favour small, reviewable PRs.","promptOverride":"You favour small, reviewable PRs.",
	"memoryId":"mw1","retiredAt":null,"retiredBy":null,
	"createdAt":"2026-08-14T00:00:00Z","createdBy":"u-holger"}`

func workerUpdateStubs(resp string) map[string]string {
	return map[string]string{
		"GetWorker":    `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"UpdateWorker": resp,
	}
}

func TestWorkerUpdateSendsTheOverride(t *testing.T) {
	gql, captured := captureGraphQL(t, workerUpdateStubs(`{"data":{"updateWorker":`+updatedWorkerJSON+`}}`))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "update", "hrn:worker:acme.com:eng-team:iris",
		"--prompt-override", "You favour small, reviewable PRs.", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		PromptOverride *string `json:"promptOverride"`
		WorkerRef      string  `json:"workerRef"`
	}
	if err := json.Unmarshal(captured["UpdateWorker"], &vars); err != nil {
		t.Fatalf("vars: %v", err)
	}
	if vars.PromptOverride == nil || *vars.PromptOverride != "You favour small, reviewable PRs." {
		t.Errorf("the override must reach the wire verbatim: %+v", vars)
	}
	// The receipt shows the RE-RENDERED briefing, not just the override: that
	// is what a session driver adopts, and it is what materially changed.
	if !strings.Contains(out.String(), "Boot briefing now reads") {
		t.Errorf("the receipt must show the re-rendered briefing:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "You are Iris.") {
		t.Errorf("the briefing must include the shared template, not only the override:\n%s", out.String())
	}
}

// THE LOAD-BEARING WIRE ASSERTION. Omitting promptOverride means PRESERVE
// server-side, so the clear path has to put an explicit `null` on the wire —
// which is why the operation deliberately carries no omitempty. Asserted
// against the raw request body, because a decode cannot tell an absent key from
// a null one: both unmarshal to a nil *string, so a decode-based check passes
// against exactly the bug it is meant to catch.
func TestWorkerUpdateClearSendsAnExplicitNull(t *testing.T) {
	cleared := strings.Replace(updatedWorkerJSON,
		`"prompt":"You are Iris.\n\nYou favour small, reviewable PRs.","promptOverride":"You favour small, reviewable PRs."`,
		`"prompt":"You are Iris.","promptOverride":null`, 1)
	gql, captured := captureGraphQL(t, workerUpdateStubs(`{"data":{"updateWorker":`+cleared+`}}`))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "update", "hrn:worker:acme.com:eng-team:iris",
		"--clear-prompt-override", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	raw := string(captured["UpdateWorker"])
	if !strings.Contains(raw, `"promptOverride":null`) {
		t.Errorf("the clear must send an explicit null, not omit the field — an omitted\n"+
			"field means PRESERVE, which makes --clear-prompt-override a silent no-op.\ngot: %s", raw)
	}
	if !strings.Contains(out.String(), "cleared the prompt override") {
		t.Errorf("the receipt must say it cleared: %s", out.String())
	}
}

// Three refusals, all BEFORE any write.
func TestWorkerUpdateRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"nothing named", []string{}, "nothing to update"},
		{"empty override", []string{"--prompt-override", ""}, "--clear-prompt-override to remove"},
		{"both flags", []string{"--prompt-override", "x", "--clear-prompt-override"}, "mutually exclusive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, workerUpdateStubs(`{"data":{"updateWorker":`+updatedWorkerJSON+`}}`))
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := append([]string{"team", "worker", "update", "hrn:worker:acme.com:eng-team:iris"}, tc.args...)
			root.SetArgs(append(args, "--server", gql.URL))
			err := root.Execute()
			if err == nil {
				t.Fatal("expected a usage refusal")
			}
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Errorf("exit = %d, want %d", got, exitcode.Usage)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want %q in %q", tc.want, err.Error())
			}
			// Nothing may reach the server on a refusal — an empty
			// --prompt-override is the case that matters, since the SERVER
			// would accept it as a clear.
			if _, called := captured["UpdateWorker"]; called {
				t.Error("a refused update must not write")
			}
		})
	}
}

// A retired worker refuses: an override is briefing text delivered at bind
// time, and a retired worker takes no new bindings, so the edit could never
// reach anyone. Already mapped to Conflict; pinned at the command surface so
// the exit code a caller actually gets is the one documented.
func TestWorkerUpdateRetiredIsConflict(t *testing.T) {
	gql, _ := captureGraphQL(t, workerUpdateStubs(
		`{"errors":[{"message":"worker is retired","extensions":{"code":"WORKER_RETIRED"}}]}`))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "update", "hrn:worker:acme.com:eng-team:iris",
		"--prompt-override", "x", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a retired worker must refuse")
	}
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Errorf("exit = %d, want %d (Conflict)", got, exitcode.Conflict)
	}
}
