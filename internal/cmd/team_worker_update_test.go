package cmd

import (
	"strings"
	"testing"
)

// #452 / server #1010 added a per-worker `promptOverride`; #1174 retired it
// from the CLI. These fixtures keep a hostile server that still sends the key,
// to prove the CLI never presents it.

// updatedWorkerJSON is a worker as a still-sending server returns it: the
// retired `promptOverride` present beside the served `prompt`.
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
