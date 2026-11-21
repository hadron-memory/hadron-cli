package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

// cli#2707 (Jonas F1, Jeremy F1/F3): every site that builds an availability
// DTO is pinned field by field, with a distinct value in every field (the two
// booleans excepted), so a swapped pair (appId/family, until/setAt,
// worker/setByParty) or an omitted field at any of the six keyed call sites
// fails here. The same constants build the server's answer and the assertions,
// so the fixture and the check cannot drift apart. Two things keep a receipt
// built from the REQUEST from passing: the answer differs from the flags
// (worker name, family, reason), and each subtest asserts which operation was
// sent and whether it carried the instruction claim.

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

func pinParty(kind, id, name, urn string) string {
	return `{"id":"` + id + `","kind":"` + kind + `","name":"` + name + `","urn":"` + urn + `"}`
}

func pinWorkerRecord() string {
	return `{"worker":` + pinParty("WORKER", pinWorkerID, pinWorkerNm, pinWorkerUr) + `,"state":"` + pinState + `","reason":"` + pinReason +
		`","until":"` + pinUntil + `","setAt":"` + pinSetAt + `","setBy":"` + pinSetBy +
		`","setByParty":` + pinParty("USER", pinSetterID, pinSetterNm, pinSetterUr) + `,"inEffect":true}`
}

func pinFamilyRecord() string {
	return `{"appId":"` + pinAppID + `","family":"` + pinFamily + `","state":"` + pinState + `","reason":"` + pinReason +
		`","until":"` + pinUntil + `","setAt":"` + pinSetAt + `","setBy":` + pinParty("USER", pinSetterID, pinSetterNm, pinSetterUr) + `,"inEffect":true}`
}

// receiptFields2707 decodes one JSON object into raw values, so each key is read as
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

func wantParty2707(t *testing.T, where string, raw json.RawMessage, kind, id, name, urn string) {
	t.Helper()
	var p struct {
		ID   string  `json:"id"`
		Kind string  `json:"kind"`
		Name *string `json:"name"`
		Urn  *string `json:"urn"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.ID != id || p.Kind != kind || p.Name == nil || *p.Name != name || p.Urn == nil || *p.Urn != urn {
		t.Errorf("%s: party %s, want %s/%s/%s/%s", where, raw, kind, id, name, urn)
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil || len(keys) != 4 {
		t.Errorf("%s: a party has exactly id, kind, name and urn: %s", where, raw)
	}
}

func checkWorkerRecord2707(t *testing.T, where string, m map[string]json.RawMessage) {
	t.Helper()
	wantParty2707(t, where+" worker", m["worker"], "WORKER", pinWorkerID, pinWorkerNm, pinWorkerUr)
	wantParty2707(t, where+" setByParty", m["setByParty"], "USER", pinSetterID, pinSetterNm, pinSetterUr)
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
	wantParty2707(t, where+" setBy", m["setBy"], "USER", pinSetterID, pinSetterNm, pinSetterUr)
	if string(m["inEffect"]) != "true" {
		t.Errorf("%s: inEffect = %s", where, m["inEffect"])
	}
	if len(m) != 8 {
		t.Errorf("%s: the family record has %d keys, want exactly its 8: %v", where, len(m), m)
	}
}

// runPinned2707 runs one availability command against a fake server that
// answers every operation with the distinct-valued records.
func runPinned2707(t *testing.T, args ...string) ([]byte, []snapshotCall1614) {
	t.Helper()
	teamGitDir(t)
	srv, calls := snapshotServer1614(t, func(c snapshotCall1614) string {
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
	return []byte(out.String()), *calls
}

// writeOps2707 is the availability write operations a command sent, with the
// instruction claim each carried (nil for none).
func writeOps2707(calls []snapshotCall1614) (ops []string, claims []any) {
	for _, c := range calls {
		if strings.Contains(c.OperationName, "AvailabilitySet") {
			ops = append(ops, c.OperationName)
			claims = append(claims, c.Variables["instruction"])
		}
	}
	return ops, claims
}

// The flags the set subtests send. The server's answer differs from them on
// purpose (worker name, family, reason), so a receipt built from the request
// instead of the server's record fails the pins.
const (
	flagWorker2707 = "flag-worker"
	flagFamily2707 = "flag-family"
	flagReason2707 = "OTHER"
	// The instruction claim of the instructed subtests: a live-chat reference
	// whose ref has a space inside and at the end, so a trimmed or reshaped
	// claim is visible.
	claimFlag2707 = "live-chat:client: 7 "
)

func assertWrite2707(t *testing.T, calls []snapshotCall1614, wantOp string, instructed bool) {
	t.Helper()
	ops, claims := writeOps2707(calls)
	if len(ops) != 1 || ops[0] != wantOp {
		t.Fatalf("the command must send exactly %s, sent %v", wantOp, ops)
	}
	claim, hasClaim := claims[0].(map[string]any)
	if instructed {
		if !hasClaim || claim["kind"] != "LIVE_CHAT" || claim["ref"] != "client: 7 " || len(claim) != 2 {
			t.Fatalf("the instructed write must carry the claim kind=LIVE_CHAT ref=%q: %v", "client: 7 ", claims[0])
		}
	} else if claims[0] != nil {
		t.Fatalf("a plain write must carry no instruction claim: %v", claims[0])
	}
	for _, c := range calls {
		if c.OperationName == wantOp {
			if input, ok := c.Variables["input"].(map[string]any); !ok || input["reason"] != flagReason2707 {
				t.Fatalf("the write did not send the flags' reason %q: %v", flagReason2707, c.Variables["input"])
			}
		}
	}
}

func TestAvailabilityDTOFieldsArePinnedAtEveryBuilderCallSite2707(t *testing.T) {
	setWorker := []string{"team", "availability", "set", "--worker", flagWorker2707, "--state", "unavailable", "--reason", flagReason2707}
	setFamily := []string{"team", "availability", "set", "--family", flagFamily2707, "--state", "unavailable", "--reason", flagReason2707}
	claim := []string{"--instruction", claimFlag2707}
	t.Run("get: workers and families (two call sites)", func(t *testing.T) {
		out, _ := runPinned2707(t, "team", "availability", "get")
		got := receiptFields2707(t, out)
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
		out, calls := runPinned2707(t, setWorker...)
		assertWrite2707(t, calls, "WorkerAvailabilitySet", false)
		checkWorkerRecord2707(t, "set worker", receiptFields2707(t, out))
	})
	t.Run("set worker, instructed", func(t *testing.T) {
		out, calls := runPinned2707(t, append(append([]string{}, setWorker...), claim...)...)
		assertWrite2707(t, calls, "WorkerAvailabilitySetInstructed", true)
		checkWorkerRecord2707(t, "set worker instructed", receiptFields2707(t, out))
	})
	t.Run("set family, plain", func(t *testing.T) {
		out, calls := runPinned2707(t, setFamily...)
		assertWrite2707(t, calls, "ModelFamilyAvailabilitySet", false)
		checkFamilyRecord2707(t, "set family", receiptFields2707(t, out))
	})
	t.Run("set family, instructed", func(t *testing.T) {
		out, calls := runPinned2707(t, append(append([]string{}, setFamily...), claim...)...)
		assertWrite2707(t, calls, "ModelFamilyAvailabilitySetInstructed", true)
		checkFamilyRecord2707(t, "set family instructed", receiptFields2707(t, out))
	})
}
