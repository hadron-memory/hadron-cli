package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #535 §1: a newly created agent is in no App's cast pool, so `worker cast`
// cannot reach it — and "✓ created" reads as done. --install-into does the
// second step in the same run.
func TestAgentCreateInstallInto(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAgent":         `{"data":{"createAgent":` + agentJSON + `}}`,
		"InstallAgentIntoApp": installResp,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Support Bot",
		"--install-into", "hrn:app:acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// The install must target the agent the create just returned, by ID —
	// not the name or anything the caller typed.
	var vars map[string]any
	if err := json.Unmarshal(captured["InstallAgentIntoApp"], &vars); err != nil {
		t.Fatalf("captured vars: %v", err)
	}
	if vars["appRef"] != "hrn:app:acme.com:eng-team" {
		t.Errorf("appRef must pass through verbatim: %v", vars)
	}
	if vars["agentRef"] != "agt1" {
		t.Errorf("agentRef must be the created agent's id, got %v", vars["agentRef"])
	}
	// trainingMode is PER-APP; sending it unasked would re-set the flag for
	// every Agent already installed in that App.
	if _, present := vars["trainingMode"]; present {
		t.Errorf("trainingMode must be OMITTED, got %v", vars["trainingMode"])
	}

	// The embedded agentDTO must inline, so `id` stays top-level for every
	// existing --json consumer, with `install` added alongside it.
	var dto struct {
		ID      string `json:"id"`
		URN     string `json:"urn"`
		Install *struct {
			AppRef string `json:"appRef"`
			AppID  string `json:"appId"`
			AppURN string `json:"appUrn"`
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"install"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("unmarshal output: %v (%s)", err, out.String())
	}
	if dto.ID != "agt1" || dto.URN == "" {
		t.Errorf("agent fields must stay top-level: %s", out.String())
	}
	if dto.Install == nil {
		t.Fatalf("install report missing: %s", out.String())
	}
	if dto.Install.Status != "installed" || dto.Install.AppID != "app1" ||
		dto.Install.AppURN != "hrn:app:acme.com:eng-team" {
		t.Errorf("install report: %+v", dto.Install)
	}
	if dto.Install.AppRef != "hrn:app:acme.com:eng-team" {
		t.Errorf("appRef echoes what the caller passed: %+v", dto.Install)
	}
	if dto.Install.Error != "" {
		t.Errorf("a successful install carries no error: %+v", dto.Install)
	}
}

// PR #543 review (Copilot): --install-into takes an App ID *or* a URN, so
// putting the raw ref in appUrn would let that field carry a non-URN. appId and
// appUrn are the SERVER's resolved values and are omitted when there are none;
// appRef is the echo of what was passed.
func TestAgentCreateInstallIntoFailureOmitsResolvedAppFields(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`,
		"InstallAgentIntoApp": `{"errors":[{"message":"nope",
			"extensions":{"code":"FORBIDDEN"}}]}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// An App ID, deliberately not a URN.
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Support Bot",
		"--install-into", "app1", "--json", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatalf("expected a non-zero exit")
	}
	var raw struct {
		Install map[string]any `json:"install"`
	}
	if err := json.Unmarshal([]byte(out.String()), &raw); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, out.String())
	}
	if _, present := raw.Install["appUrn"]; present {
		t.Errorf("appUrn must be omitted on failure — it would carry the ID %q: %s", "app1", out.String())
	}
	if _, present := raw.Install["appId"]; present {
		t.Errorf("appId must be omitted on failure (nothing resolved it): %s", out.String())
	}
	if raw.Install["appRef"] != "app1" {
		t.Errorf("appRef must echo what was passed: %s", out.String())
	}
}

// Without --install-into the --json shape must be byte-identical to before:
// the contract is extended, never changed.
func TestAgentCreateWithoutInstallIntoOmitsInstallKey(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Support Bot",
		"--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out.String()), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := raw["install"]; present {
		t.Errorf("install must be omitted when --install-into is absent: %s", out.String())
	}
	if raw["id"] != "agt1" {
		t.Errorf("agent fields must stay top-level: %s", out.String())
	}
}

// The two calls are not atomic. If the install fails the agent EXISTS, and the
// worst outcome is a user who reads the failure as "nothing happened" and
// re-runs create — making a second agent. So the error must say created-but-not-
// installed and print the command that finishes it, and the agent must still be
// emitted so its URN is not lost.
func TestAgentCreateInstallIntoFailureKeepsTheAgentAndSaysSo(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`,
		"InstallAgentIntoApp": `{"errors":[{"message":"not allowed",
			"extensions":{"code":"FORBIDDEN"}}]}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Support Bot",
		"--install-into", "hrn:app:acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatalf("a failed install must be a non-zero exit, got success")
	}
	msg := err.Error()
	for _, want := range []string{
		"CREATED but NOT installed",
		"do not re-run",
		"hadron app agent add hrn:app:acme.com:eng-team agt1",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error must contain %q, got: %s", want, msg)
		}
	}
	// The FORBIDDEN guidance shared with `app agent add` must survive the wrap —
	// this is the likeliest failure, since installing needs a stricter role than
	// creating and can refuse right after the create succeeded.
	if !strings.Contains(msg, "CONTRIBUTOR+") {
		t.Errorf("install FORBIDDEN guidance must be preserved, got: %s", msg)
	}
	// The agent line is still printed: losing a just-created agent's URN is the
	// one outcome with no cheap recovery.
	if !strings.Contains(out.String(), "created agent") {
		t.Errorf("the created agent must still be emitted, got: %s", out.String())
	}
}

// A failed install must still report itself in --json, so a scripted caller can
// branch on status without parsing stderr.
func TestAgentCreateInstallIntoFailureIsReportedInJSON(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`,
		"InstallAgentIntoApp": `{"errors":[{"message":"nope",
			"extensions":{"code":"FORBIDDEN"}}]}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Support Bot",
		"--install-into", "hrn:app:acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatalf("expected a non-zero exit")
	}
	var dto struct {
		ID      string `json:"id"`
		Install *struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"install"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, out.String())
	}
	if dto.ID != "agt1" {
		t.Errorf("the created agent must still be in the JSON: %s", out.String())
	}
	if dto.Install == nil || dto.Install.Status != "failed" || dto.Install.Error == "" {
		t.Errorf("install must report failed with a reason: %s", out.String())
	}
}

// #535 §1: without the flag, say the agent is inert — on stderr, so --json
// stdout stays clean.
func TestAgentCreateNotesAgentIsNotInstalled(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Support Bot",
		"--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	note := f.IOStreams.ErrOut.(*strings.Builder).String()
	if !strings.Contains(note, "not yet installed in any App") {
		t.Errorf("expected the not-installed note on stderr, got: %q", note)
	}
	if !strings.Contains(note, "--install-into") {
		t.Errorf("the note must point at the flag that avoids it, got: %q", note)
	}
	if strings.Contains(out.String(), "not yet installed") {
		t.Errorf("the note must not pollute --json stdout: %s", out.String())
	}
}

// With --install-into there is nothing to warn about.
func TestAgentCreateInstallIntoSuppressesTheNote(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateAgent":         `{"data":{"createAgent":` + agentJSON + `}}`,
		"InstallAgentIntoApp": installResp,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Support Bot",
		"--install-into", "hrn:app:acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if note := f.IOStreams.ErrOut.(*strings.Builder).String(); strings.Contains(note, "not yet installed") {
		t.Errorf("the note is wrong when the install happened: %q", note)
	}
}

// PR #543 review (Codex, P1): a lost answer is NOT a refusal. If the install
// commits and the response never arrives, exitcode.Unavailable (#394) says the
// outcome is unknown — so claiming "NOT installed" and sending the caller to
// `app agent add` would invite a Conflict (exit 5, "duplicate install") on an
// install that actually succeeded. Report unknown and say to look first.
func TestAgentCreateInstallIntoLostAnswerIsUnknownNotFailed(t *testing.T) {
	// The connection is dropped without an answer — the strictest lost-answer
	// case (api/transport.go: headers never arrive, so the request may well
	// have reached the server and the mutation may already have committed).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.OperationName == "InstallAgentIntoApp" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			_ = conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"createAgent":` + agentJSON + `}}`))
	}))
	t.Cleanup(srv.Close)

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Support Bot",
		"--install-into", "hrn:app:acme.com:eng-team", "--json", "--server", srv.URL})
	err := root.Execute()
	if err == nil {
		t.Fatalf("expected a non-zero exit")
	}
	if got := exitcode.FromError(err); got != exitcode.Unavailable {
		t.Errorf("a lost answer must stay exit %d (Unavailable), got %d", exitcode.Unavailable, got)
	}
	msg := err.Error()
	if !strings.Contains(msg, "UNKNOWN") {
		t.Errorf("the message must say the outcome is unknown, got: %s", msg)
	}
	if !strings.Contains(msg, "hadron app agent list") {
		t.Errorf("the remedy must be to LOOK first, got: %s", msg)
	}
	if strings.Contains(msg, "NOT installed") {
		t.Errorf("must not assert the install did not happen, got: %s", msg)
	}
	var raw struct {
		Install map[string]any `json:"install"`
	}
	if err := json.Unmarshal([]byte(out.String()), &raw); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, out.String())
	}
	if raw.Install["status"] != "unknown" {
		t.Errorf("status must be \"unknown\", got %v: %s", raw.Install["status"], out.String())
	}
}

// A failed install must not exit 0, and must not collapse to a generic code.
func TestAgentCreateInstallIntoFailurePreservesExitCode(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`,
		"InstallAgentIntoApp": `{"errors":[{"message":"nope",
			"extensions":{"code":"FORBIDDEN"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Support Bot",
		"--install-into", "hrn:app:acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatalf("expected an error")
	}
	if got := exitcode.FromError(err); got == exitcode.OK {
		t.Errorf("a failed install must not exit OK, got %v", got)
	}
}
