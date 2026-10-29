package cmd

import (
	"strings"
	"testing"
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

func TestWorkerGetOmitsAnAbsentPromptOverride(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":       `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamAppIdentity": `{"data":{"app":{"id":"capp100000000000000000000","urn":"hrn:app:acme.com:eng-team","name":"Eng Team"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "hrn:worker:acme.com:eng-team:iris", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out.String(), "Prompt override") {
		t.Errorf("no override means no line:\n%s", out.String())
	}
}
