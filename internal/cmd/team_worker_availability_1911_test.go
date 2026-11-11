package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1911 (hadron-server#3865 slice 2, cor:agt:060:02): `worker get` shows the
// stored availability provenance. A null answer is withheld OR no record, which
// the CLI cannot tell apart, so it renders nothing and emits null.

const party1911 = `{"id":"u1","kind":"USER","name":"Holger","urn":"hrn:user:acme.com:holger"}`

func availRec1911(state, reason, until, setBy, setByParty string) string {
	return `{"state":"` + state + `","reason":` + reason + `,"until":` + until + `,"setAt":"2026-11-11T10:00:00Z","setBy":"` + setBy + `","setByParty":` + setByParty + `,"inEffect":` + map[bool]string{true: "true", false: "false"}[state == "UNAVAILABLE"] + `}`
}

func workerGetAvail1911(t *testing.T, availResp string, args ...string) (string, error) {
	t.Helper()
	teamGitDir(t)
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "GetWorker":
			return `{"data":{"worker":` + profileWorkerJSON + `}}`
		case "GetWorkerProfile":
			return profileNames
		case "GetWorkerAvailability":
			return availResp
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"team", "worker", "get", profileHexID}, args...), "--server", gql.URL))
	err := root.Execute()
	return out.String(), err
}

// availLine1911 is the availability line alone: other lines of worker get may
// legitimately say "withheld" or "unknown" about other fields.
func availLine1911(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "  availability:") {
			return l
		}
	}
	return ""
}

func availJSON1911(t *testing.T, out string) json.RawMessage {
	t.Helper()
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	a, ok := got["availability"]
	if !ok {
		t.Fatalf("availability key missing: %s", out)
	}
	return a
}

// Condition 1: null prints no availability line, never "available" or "no
// record", and --json carries an explicit null.
func TestWorkerGetAvailabilityNullRendersNothing1911(t *testing.T) {
	resp := `{"data":{"worker":{"availability":null}}}`
	out, err := workerGetAvail1911(t, resp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(out), "availab") {
		t.Fatalf("a null must print no availability text:\n%s", out)
	}
	jout, err := workerGetAvail1911(t, resp, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if a := availJSON1911(t, jout); string(a) != "null" {
		t.Fatalf("availability = %s, want null", a)
	}
}

func TestWorkerGetAvailabilityUnavailableRecord1911(t *testing.T) {
	rec := availRec1911("UNAVAILABLE", `"OUT_OF_TOKENS"`, `"2026-11-12T00:00:00Z"`, "HOLDER", party1911)
	resp := `{"data":{"worker":{"availability":` + rec + `}}}`
	out, err := workerGetAvail1911(t, resp)
	if err != nil {
		t.Fatal(err)
	}
	want := "  availability: UNAVAILABLE — OUT_OF_TOKENS until 2026-11-12T00:00:00Z; set by Holger (HOLDER) at 2026-11-11T10:00:00Z\n"
	if !strings.Contains(out, want) {
		t.Fatalf("want %q in:\n%s", want, out)
	}
	jout, _ := workerGetAvail1911(t, resp, "--json")
	var a map[string]any
	if err := json.Unmarshal(availJSON1911(t, jout), &a); err != nil {
		t.Fatal(err)
	}
	if a["state"] != "UNAVAILABLE" || a["reason"] != "OUT_OF_TOKENS" || a["setBy"] != "HOLDER" || a["setAt"] != "2026-11-11T10:00:00Z" || a["inEffect"] != true {
		t.Fatalf("availability json = %v", a)
	}
	if p, _ := a["setByParty"].(map[string]any); p["name"] != "Holger" {
		t.Fatalf("setByParty = %v", a["setByParty"])
	}
}

// Condition 2: a reason only when unavailable, even if a stale one arrives.
func TestWorkerGetAvailabilityNoReasonWhenAvailable1911(t *testing.T) {
	rec := availRec1911("AVAILABLE", `"HOST_DOWN"`, `null`, "APP_MANAGER", party1911)
	resp := `{"data":{"worker":{"availability":` + rec + `}}}`
	out, err := workerGetAvail1911(t, resp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "HOST_DOWN") {
		t.Fatalf("a reason printed for an available record:\n%s", out)
	}
	if !strings.Contains(out, "  availability: AVAILABLE; set by Holger (APP_MANAGER) at ") {
		t.Fatalf("available line missing:\n%s", out)
	}
	jout, _ := workerGetAvail1911(t, resp, "--json")
	var a map[string]any
	_ = json.Unmarshal(availJSON1911(t, jout), &a)
	if a["reason"] != nil {
		t.Fatalf("reason = %v, want null for an available record", a["reason"])
	}
}

// Condition 3: lapse is a setter of its own, rendered as a lapse.
func TestWorkerGetAvailabilityLapseIsItsOwnSetter1911(t *testing.T) {
	rec := availRec1911("AVAILABLE", `null`, `null`, "LAPSE", `null`)
	out, err := workerGetAvail1911(t, `{"data":{"worker":{"availability":`+rec+`}}}`)
	if err != nil {
		t.Fatal(err)
	}
	want := "  availability: AVAILABLE (lapsed at 2026-11-11T10:00:00Z); set by lapse\n"
	if !strings.Contains(out, want) {
		t.Fatalf("want %q in:\n%s", want, out)
	}
	if l := availLine1911(out); strings.Contains(l, "withheld") || strings.Contains(l, "unknown") {
		t.Fatalf("a lapse must not read as a withheld or unknown setter: %s", l)
	}
}

// Condition 3, second half: a person setter whose party the server withheld is
// said to be withheld, never guessed.
func TestWorkerGetAvailabilityWithheldSetterIsSaidSo1911(t *testing.T) {
	rec := availRec1911("UNAVAILABLE", `"OTHER"`, `null`, "HOLDER", `null`)
	out, err := workerGetAvail1911(t, `{"data":{"worker":{"availability":`+rec+`}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "set by HOLDER (identity withheld)") {
		t.Fatalf("withheld setter not said:\n%s", out)
	}
	stall := availRec1911("UNAVAILABLE", `"STALLED"`, `null`, "STALL_RULE", `null`)
	out, err = workerGetAvail1911(t, `{"data":{"worker":{"availability":`+stall+`}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if l := availLine1911(out); !strings.Contains(l, "set by stall rule") || strings.Contains(l, "withheld") {
		t.Fatalf("the stall rule is a platform setter, not a withheld person:\n%s", out)
	}
}

// A server that predates the field still serves worker get: no availability
// line, a null key. Any other failure is an error, never an old-server null.
func TestWorkerGetAvailabilityOldServerAndErrors1911(t *testing.T) {
	old := `{"errors":[{"message":"Cannot query field \"availability\" on type \"Worker\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	out, err := workerGetAvail1911(t, old, "--json")
	if err != nil {
		t.Fatalf("old server must still serve worker get: %v", err)
	}
	if a := availJSON1911(t, out); string(a) != "null" {
		t.Fatalf("availability = %s, want null", a)
	}
	_, err = workerGetAvail1911(t, `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`)
	if err == nil {
		t.Fatal("a non-capability error must fail the command")
	}
	if got := exitCodeFor(err); got == exitcode.OK {
		t.Fatalf("exit = %d", got)
	}
}

// The availability read is its OWN operation, so a server that predates
// Worker.availability still validates every other worker document
// (review:old-server-graphql-document-compatibility), and its selection carries
// what the line and the DTO render (review:assert-the-query-not-the-capture).
func TestWorkerAvailabilityIsItsOwnOperation1911(t *testing.T) {
	for _, shared := range []string{gen.GetWorker_Operation, gen.Workers_Operation, gen.GetWorkerProfile_Operation} {
		if strings.Contains(shared, "availability") {
			t.Fatalf("a shared worker operation selects availability:\n%s", shared)
		}
	}
	for _, field := range []string{"availability", "state", "reason", "until", "setAt", "setBy", "setByParty", "inEffect"} {
		if !strings.Contains(gen.GetWorkerAvailability_Operation, field) {
			t.Errorf("GetWorkerAvailability does not select %q:\n%s", field, gen.GetWorkerAvailability_Operation)
		}
	}
}
