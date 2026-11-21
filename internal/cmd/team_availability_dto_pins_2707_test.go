package cmd

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

// cli#2707 (Jonas F1, Jeremy F1/F3): every site that builds an availability
// DTO is pinned field by field, with a DISTINCT value in every field, so a
// swapped pair (appId/family, until/setAt, worker/setByParty) or an omitted
// field at any of the six keyed call sites fails here. The same constants build
// the server's answer and the assertions, so the fixture and the check cannot
// drift apart.

const (
	pinAppID    = "capp100000000000000000000"
	pinFamily   = "codex"
	pinState    = "UNAVAILABLE"
	pinReason   = "OUT_OF_TOKENS"
	pinUntil    = "2026-12-01T10:00:00Z"
	pinSetAt    = "2026-11-30T09:00:00Z"
	pinSetBy    = "APP_MANAGER"
	pinWorkerID = "wkr-pin-1"
	pinWorkerNm = "PinWorker"
	pinWorkerUr = "hrn:worker:acme.com:eng:pinworker"
	pinSetterID = "wkr-pin-2"
	pinSetterNm = "PinSetter"
	pinSetterUr = "hrn:worker:acme.com:eng:pinsetter"
)

func pinParty(id, name, urn string) string {
	return `{"id":"` + id + `","kind":"WORKER","name":"` + name + `","urn":"` + urn + `"}`
}

func pinWorkerRecord() string {
	return `{"worker":` + pinParty(pinWorkerID, pinWorkerNm, pinWorkerUr) + `,"state":"` + pinState + `","reason":"` + pinReason +
		`","until":"` + pinUntil + `","setAt":"` + pinSetAt + `","setBy":"` + pinSetBy +
		`","setByParty":` + pinParty(pinSetterID, pinSetterNm, pinSetterUr) + `,"inEffect":true}`
}

func pinFamilyRecord() string {
	return `{"appId":"` + pinAppID + `","family":"` + pinFamily + `","state":"` + pinState + `","reason":"` + pinReason +
		`","until":"` + pinUntil + `","setAt":"` + pinSetAt + `","setBy":` + pinParty(pinSetterID, pinSetterNm, pinSetterUr) + `,"inEffect":true}`
}

// receiptField decodes one JSON object into raw values, so each key is read as
// the CLI printed it (no struct of the test's own to drift from the DTO).
func receiptFields2707(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not a JSON object (%v): %s", err, raw)
	}
	return m
}

func wantString2707(t *testing.T, where string, m map[string]json.RawMessage, key, want string) {
	t.Helper()
	var got string
	if err := json.Unmarshal(m[key], &got); err != nil || got != want {
		t.Errorf("%s: %s = %s, want %q", where, key, m[key], want)
	}
}

func wantParty2707(t *testing.T, where string, raw json.RawMessage, id, name, urn string) {
	t.Helper()
	var p struct {
		ID   string  `json:"id"`
		Kind string  `json:"kind"`
		Name *string `json:"name"`
		Urn  *string `json:"urn"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.ID != id || p.Kind != "WORKER" || p.Name == nil || *p.Name != name || p.Urn == nil || *p.Urn != urn {
		t.Errorf("%s: party %s, want %s/%s/%s", where, raw, id, name, urn)
	}
}

func checkWorkerRecord2707(t *testing.T, where string, m map[string]json.RawMessage) {
	t.Helper()
	wantParty2707(t, where+" worker", m["worker"], pinWorkerID, pinWorkerNm, pinWorkerUr)
	wantParty2707(t, where+" setByParty", m["setByParty"], pinSetterID, pinSetterNm, pinSetterUr)
	wantString2707(t, where, m, "state", pinState)
	wantString2707(t, where, m, "reason", pinReason)
	wantString2707(t, where, m, "until", pinUntil)
	wantString2707(t, where, m, "setAt", pinSetAt)
	wantString2707(t, where, m, "setBy", pinSetBy)
	if string(m["inEffect"]) != "true" {
		t.Errorf("%s: inEffect = %s", where, m["inEffect"])
	}
	if len(m) != 8 {
		t.Errorf("%s: the worker record has %d keys, want exactly its 8: %v", where, len(m), m)
	}
}

func checkFamilyRecord2707(t *testing.T, where string, m map[string]json.RawMessage) {
	t.Helper()
	wantString2707(t, where, m, "appId", pinAppID)
	wantString2707(t, where, m, "family", pinFamily)
	wantString2707(t, where, m, "state", pinState)
	wantString2707(t, where, m, "reason", pinReason)
	wantString2707(t, where, m, "until", pinUntil)
	wantString2707(t, where, m, "setAt", pinSetAt)
	wantParty2707(t, where+" setBy", m["setBy"], pinSetterID, pinSetterNm, pinSetterUr)
	if string(m["inEffect"]) != "true" {
		t.Errorf("%s: inEffect = %s", where, m["inEffect"])
	}
	if len(m) != 8 {
		t.Errorf("%s: the family record has %d keys, want exactly its 8: %v", where, len(m), m)
	}
}

// runPinned2707 runs one availability command against a fake server that
// answers every operation with the distinct-valued records.
func runPinned2707(t *testing.T, args ...string) []byte {
	t.Helper()
	teamGitDir(t)
	srv, _ := snapshotServer1614(t, func(c snapshotCall1614) string {
		switch c.OperationName {
		case "GetBoundSessionEnd":
			return `{"data":{"session":{"id":"s-new","endedAt":null,"endReason":null}}}`
		case "TeamAvailability":
			return `{"data":{"teamAvailability":{"workers":[` + pinWorkerRecord() + `],"families":[` + pinFamilyRecord() + `]}}}`
		case "WorkerAvailabilitySet", "WorkerAvailabilitySetInstructed":
			return `{"data":{"setWorkerAvailability":` + pinWorkerRecord() + `}}`
		case "ModelFamilyAvailabilitySet", "ModelFamilyAvailabilitySetInstructed":
			return `{"data":{"setModelFamilyAvailability":` + pinFamilyRecord() + `}}`
		}
		t.Errorf("unexpected op %s", c.OperationName)
		return `{}`
	})
	writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", srv.URL, "--json"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return []byte(out.String())
}

func TestAvailabilityDTOFieldsArePinnedAtEveryBuilderCallSite2707(t *testing.T) {
	setWorker := []string{"team", "availability", "set", "--worker", "PinWorker", "--state", "unavailable", "--reason", "OUT_OF_TOKENS"}
	setFamily := []string{"team", "availability", "set", "--family", pinFamily, "--state", "unavailable", "--reason", "OUT_OF_TOKENS"}
	claim := []string{"--instruction", "live-chat:client: 7 "}
	t.Run("get: workers and families (two call sites)", func(t *testing.T) {
		got := receiptFields2707(t, runPinned2707(t, "team", "availability", "get"))
		var workers, families []json.RawMessage
		if err := json.Unmarshal(got["workers"], &workers); err != nil || len(workers) != 1 {
			t.Fatalf("workers %s", got["workers"])
		}
		if err := json.Unmarshal(got["families"], &families); err != nil || len(families) != 1 {
			t.Fatalf("families %s", got["families"])
		}
		checkWorkerRecord2707(t, "get worker", receiptFields2707(t, workers[0]))
		checkFamilyRecord2707(t, "get family", receiptFields2707(t, families[0]))
	})
	t.Run("set worker, plain", func(t *testing.T) {
		checkWorkerRecord2707(t, "set worker", receiptFields2707(t, runPinned2707(t, setWorker...)))
	})
	t.Run("set worker, instructed", func(t *testing.T) {
		checkWorkerRecord2707(t, "set worker instructed", receiptFields2707(t, runPinned2707(t, append(append([]string{}, setWorker...), claim...)...)))
	})
	t.Run("set family, plain", func(t *testing.T) {
		checkFamilyRecord2707(t, "set family", receiptFields2707(t, runPinned2707(t, setFamily...)))
	})
	t.Run("set family, instructed", func(t *testing.T) {
		checkFamilyRecord2707(t, "set family instructed", receiptFields2707(t, runPinned2707(t, append(append([]string{}, setFamily...), claim...)...)))
	})
}
