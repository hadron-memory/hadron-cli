package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// `worker release` clears the HOLD (hadron-cli#495, cor:agt:020:09) — the only
// thing that frees one. Three branches, and telling them apart is most of the
// command: the server decides who MAY release, the CLI only says which act you
// are performing.
//
// The worker fixtures below carry an explicit hold, since the shared
// irisWorkerJSON is unheld.

// heldBy returns irisWorkerJSON with the hold set to a given user.
func heldBy(userID string) string {
	return strings.Replace(irisWorkerJSON, `"memoryId":"mw1"`,
		`"memoryId":"mw1","heldByUserId":"`+userID+`","heldAt":"2026-08-20T09:00:00Z"`, 1)
}

const authContextHolgerJSON = `{"data":{"authContext":{"principalType":"USER","appId":null,"agentId":null,
	"user":{"id":"u-holger","name":"Holger","email":"h@example.com","handle":"holger",
	        "githubUsername":null,"roles":["ADMIN"]},
	"apiKey":null,"impersonation":null}}}`

func releaseStubs(worker string, extra map[string]string) map[string]string {
	m := map[string]string{
		"Workers":       `{"data":{"workers":{"total":1,"items":[` + worker + `]}}}`,
		"AuthContext":   authContextHolgerJSON,
		"ReleaseWorker": `{"data":{"releaseWorker":` + irisWorkerJSON + `}}`,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// Releasing YOUR OWN name notifies nobody and loses nothing, so it must not
// prompt — a prompt on the ordinary end-of-work step is what teaches people to
// reach for --yes reflexively, which would then also skip the force prompt that
// actually matters.
func TestWorkerReleaseSelfDoesNotPrompt(t *testing.T) {
	gql, captured := captureGraphQL(t, releaseStubs(heldBy("u-holger"), nil))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// No --yes, and no TTY: a prompt here would refuse as non-interactive.
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a self-release must not prompt: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["ReleaseWorker"], &vars)
	if vars["workerRef"] != "wkr1" {
		t.Errorf("release vars: %v", vars)
	}
	got := out.String()
	if !strings.Contains(got, "✓ released Iris") {
		t.Errorf("receipt: %s", got)
	}
	if strings.Contains(got, "force-released") || strings.Contains(got, "team chat") {
		t.Errorf("a self-release announces nothing — do not imply it does: %s", got)
	}
	// The transfer is the least guessable consequence and applies here too.
	if !strings.Contains(got, "go with the name, not with you") {
		t.Errorf("the receipt must say the memory/handoff follows the name: %s", got)
	}
}

// Releasing SOMEONE ELSE'S name is an admin force-release: it posts to the team
// chat naming both parties. Doing that without asking would make a caller
// perform a public act privately, which is the failure cor:agt:020:09 exists to
// prevent — so it refuses non-interactively rather than proceeding.
func TestWorkerReleaseForcedRefusesWithoutYes(t *testing.T) {
	gql, captured := captureGraphQL(t, releaseStubs(heldBy("u-dara"), map[string]string{
		"GetUser": `{"data":{"user":{"id":"u-dara","name":"Dara","email":null,"handle":"dara",
			"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
			"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
	}))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("force-releasing someone else's name must not proceed unprompted")
	}
	if _, called := captured["ReleaseWorker"]; called {
		t.Error("the mutation must not run when the confirmation was refused")
	}
}

// …and with --yes it proceeds, names the prior holder as a PERSON, and says the
// chat post happened rather than leaving the caller to discover it there.
func TestWorkerReleaseForcedNamesTheHolderAndTheChatPost(t *testing.T) {
	gql, _ := captureGraphQL(t, releaseStubs(heldBy("u-dara"), map[string]string{
		"GetUser": `{"data":{"user":{"id":"u-dara","name":"Dara","email":null,"handle":"dara",
			"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
			"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
	}))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris", "--yes",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"force-released Iris", "Dara (@dara)", "announced in the team chat"} {
		if !strings.Contains(got, want) {
			t.Errorf("the force receipt must carry %q: %s", want, got)
		}
	}
}

// A holder whose user record the caller may not read must not fail the release
// — the name is decoration, and gating an operation on a display label is the
// describeApp lesson. It falls back to the id.
func TestWorkerReleaseSurvivesUnreadableHolder(t *testing.T) {
	gql, _ := captureGraphQL(t, releaseStubs(heldBy("u-dara"), map[string]string{
		"GetUser": `{"errors":[{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}]}`,
	}))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris", "--yes",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("an unreadable holder must not fail the release: %v", err)
	}
	if !strings.Contains(out.String(), "u-dara") {
		t.Errorf("it must fall back to the holder's id: %s", out.String())
	}
}

// Idempotent: releasing a name nobody holds succeeds server-side. Printing
// "✓ released" there would be a receipt for something that did not happen.
func TestWorkerReleaseUnheldReportsTheNoOp(t *testing.T) {
	gql, _ := captureGraphQL(t, releaseStubs(irisWorkerJSON, nil)) // no hold
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "was not held — nothing to release") {
		t.Errorf("a no-op must say so: %s", got)
	}
	if strings.Contains(got, "✓ released") {
		t.Errorf("a no-op must not print a success receipt: %s", got)
	}
}

// The --json contract. wasHeld/releasedFromUserId describe the state BEFORE the
// call, because the returned worker is post-release by construction — a
// consumer could not compute "did anything happen" from the payload otherwise.
func TestWorkerReleaseJSONShape(t *testing.T) {
	for _, tc := range []struct {
		name    string
		worker  string
		wasHeld bool
		forced  bool
		status  string
	}{
		{"self", heldBy("u-holger"), true, false, "released"},
		{"forced", heldBy("u-dara"), true, true, "released"},
		{"unheld", irisWorkerJSON, false, false, "not-held"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, releaseStubs(tc.worker, map[string]string{
				"GetUser": `{"data":{"user":{"id":"u-dara","name":"Dara","email":null,"handle":"dara",
					"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
					"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
			}))
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "worker", "release", "Iris", "--yes", "--json",
				"--app", "acme.com:eng-team", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var dto struct {
				ID                 string  `json:"id"`
				Name               string  `json:"name"`
				WasHeld            bool    `json:"wasHeld"`
				ReleasedFromUserID *string `json:"releasedFromUserId"`
				Forced             bool    `json:"forced"`
				Status             string  `json:"status"`
			}
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatalf("--json must parse: %v (%s)", err, out.String())
			}
			if dto.WasHeld != tc.wasHeld || dto.Forced != tc.forced || dto.Status != tc.status {
				t.Errorf("shape: %s", out.String())
			}
			if tc.wasHeld && dto.ReleasedFromUserID == nil {
				t.Errorf("the prior holder is the one fact only the pre-read has: %s", out.String())
			}
			if !tc.wasHeld && dto.ReleasedFromUserID != nil {
				t.Errorf("a no-op released nobody: %s", out.String())
			}
		})
	}
}

// An App-key caller has no user, so it can never be the holder (cor:agt:020:09
// — an App key holds nothing). A null authContext.user must land in the FORCE
// branch rather than erroring or being mistaken for the holder.
func TestWorkerReleaseAppKeyCallerIsNeverTheHolder(t *testing.T) {
	gql, captured := captureGraphQL(t, releaseStubs(heldBy("u-dara"), map[string]string{
		"AuthContext": `{"data":{"authContext":{"principalType":"APP","appId":"app1","agentId":null,
			"user":null,"apiKey":null,"impersonation":null}}}`,
		// Stubbed because reaching the prompt REQUIRES resolving the holder —
		// its absence is itself the signal that the force branch was taken.
		"GetUser": `{"data":{"user":{"id":"u-dara","name":"Dara","email":null,"handle":"dara",
			"githubUsername":null,"roles":[],"identityProvider":null,"githubId":null,
			"externalId":null,"externalAppId":null,"linkedAt":null}}}`,
	}))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "release", "Iris",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("an App-key caller is not the holder — this must take the force branch and prompt")
	}
	if _, called := captured["ReleaseWorker"]; called {
		t.Error("the mutation must not run when the force confirmation was refused")
	}
}
