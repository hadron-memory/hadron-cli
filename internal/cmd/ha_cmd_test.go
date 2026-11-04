package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// cli#231 (read-only registry cut): `hadron ha list|get|ops`.

const haInstanceJSON = `{"id":"ha1","organizationId":"o1","slug":"home","name":"Home",
	"url":"https://ha.example.com","enabled":true,"opAllowlist":["get_state"],
	"createdAt":"2026-07-01T00:00:00Z","updatedAt":"2026-07-02T00:00:00Z",
	"createdBy":null,"updatedBy":"u2"}`

// runHA drives the root command and returns stdout, stderr, the exit code and
// the request variables captured per operation.
func runHA(t *testing.T, responses map[string]string, args ...string) (string, string, int, map[string]json.RawMessage) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", gql.URL, "ha"}, args...))
	code := 0
	if err := root.Execute(); err != nil {
		code = renderError(f, err)
	}
	return out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), code, captured
}

func haRows(n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = strings.Replace(haInstanceJSON, `"id":"ha1"`, fmt.Sprintf(`"id":"ha%d"`, i), 1)
	}
	return strings.Join(rows, ",")
}

func TestHaListDefaultsToAnExplicitPageOf20(t *testing.T) {
	out, _, code, vars := runHA(t, map[string]string{
		"HomeAssistantInstances": `{"data":{"homeAssistantInstances":{"items":[` + haInstanceJSON + `],"total":1}}}`,
	}, "list", "--org", "acme.com", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	var sent map[string]any
	_ = json.Unmarshal(vars["HomeAssistantInstances"], &sent)
	// The CLI's own default (20) is sent explicitly — not the server's 50.
	if sent["limit"] != float64(20) || sent["orgId"] != "acme.com" {
		t.Errorf("want limit 20 + orgId forwarded, got %v", sent)
	}
	if _, ok := sent["offset"]; ok {
		t.Errorf("offset 0 must be omitted, got %v", sent)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json invalid: %v\n%s", err, out)
	}
	if len(got) != 1 || got[0]["slug"] != "home" {
		t.Errorf("unexpected list output: %s", out)
	}
}

func TestHaListExplicitPageAndStderrContinuation(t *testing.T) {
	out, errOut, code, vars := runHA(t, map[string]string{
		"HomeAssistantInstances": `{"data":{"homeAssistantInstances":{"items":[` + haRows(2) + `],"total":9}}}`,
	}, "list", "--limit", "2", "--offset", "4", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var sent map[string]any
	_ = json.Unmarshal(vars["HomeAssistantInstances"], &sent)
	if sent["limit"] != float64(2) || sent["offset"] != float64(4) {
		t.Errorf("want limit 2 offset 4, got %v", sent)
	}
	if !strings.Contains(errOut, "showing 2 of 9") || !strings.Contains(errOut, "--offset 6") {
		t.Errorf("stderr must say how to continue, got %q", errOut)
	}
	// stdout stays a bare array.
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 2 {
		t.Errorf("stdout must be a 2-element array: %v\n%s", err, out)
	}
}

func TestHaListLastPageHasNoContinuation(t *testing.T) {
	_, errOut, _, _ := runHA(t, map[string]string{
		"HomeAssistantInstances": `{"data":{"homeAssistantInstances":{"items":[` + haRows(2) + `],"total":6}}}`,
	}, "list", "--limit", "2", "--offset", "4", "--json")
	if strings.Contains(errOut, "next page") {
		t.Errorf("no continuation on the last page, got %q", errOut)
	}
}

func TestHaListEmptyIsAnEmptyArray(t *testing.T) {
	out, _, code, _ := runHA(t, map[string]string{
		"HomeAssistantInstances": `{"data":{"homeAssistantInstances":{"items":[],"total":0}}}`,
	}, "list", "--json")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Errorf("want [] exit 0, got %d %q", code, out)
	}
}

func TestHaListRefusesBadPagingBeforeAnyRequest(t *testing.T) {
	for _, args := range [][]string{
		{"list", "--limit", "0"},
		{"list", "--limit", "201"},
		{"list", "--limit", "-1"},
		{"list", "--offset", "-1"},
		// A blank --org must not collapse to "omitted" (the caller's whole union).
		{"list", "--org="},
		{"list", "--org", "  "},
	} {
		_, _, code, vars := runHA(t, map[string]string{}, args...)
		if code != 2 || len(vars) != 0 {
			t.Errorf("%v: want usage exit 2 and zero requests, got code %d vars %v", args, code, vars)
		}
	}
}

// An empty allowlist means EVERY op, not deny-all: --json keeps the empty
// array (never null) and the table says "all".
func TestHaEmptyAllowlistMeansAll(t *testing.T) {
	empty := strings.Replace(haInstanceJSON, `["get_state"]`, `[]`, 1)
	resp := map[string]string{
		"HomeAssistantInstances": `{"data":{"homeAssistantInstances":{"items":[` + empty + `],"total":1}}}`,
	}
	out, _, _, _ := runHA(t, resp, "list", "--json")
	if !strings.Contains(out, `"opAllowlist": []`) && !strings.Contains(out, `"opAllowlist":[]`) {
		t.Errorf("empty allowlist must render as [], got %s", out)
	}
	table, _, _, _ := runHA(t, resp, "list")
	if !strings.Contains(table, "all") {
		t.Errorf("table must read the empty allowlist as 'all':\n%s", table)
	}
}

func TestHaGetPreservesNullsAndShowsNoToken(t *testing.T) {
	out, _, code, _ := runHA(t, map[string]string{
		"HomeAssistantInstance": `{"data":{"homeAssistantInstance":` + haInstanceJSON + `}}`,
	}, "get", "ha1", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if v, ok := got["createdBy"]; !ok || v != nil {
		t.Errorf("createdBy must be present and null, got %v", got["createdBy"])
	}
	if got["updatedBy"] != "u2" {
		t.Errorf("updatedBy: %v", got["updatedBy"])
	}
	for k := range got {
		if strings.Contains(strings.ToLower(k), "token") {
			t.Errorf("DTO must carry no token field, found %q", k)
		}
	}
}

// The server answers null for BOTH an absent row and a denied caller; the CLI
// reports one not-found answer and does not claim the row exists or not.
func TestHaGetAndOpsNullAreOneNotFoundAnswer(t *testing.T) {
	for op, args := range map[string][]string{
		"HomeAssistantInstance":    {"get", "x"},
		"HomeAssistantInstanceOps": {"ops", "x"},
	} {
		field := strings.ToLower(op[:1]) + op[1:]
		_, errOut, code, _ := runHA(t, map[string]string{op: `{"data":{"` + field + `":null}}`}, args...)
		if code != 4 {
			t.Errorf("%s: want exit 4, got %d", op, code)
		}
		if !strings.Contains(errOut, "not found or not visible") {
			t.Errorf("%s: message must not claim existence either way, got %q", op, errOut)
		}
	}
}

// runToolName and inputSchema are the provider's strings, passed through
// untouched — the CLI never composes ha__ names.
func TestHaOpsPassesProviderStringsThrough(t *testing.T) {
	out, _, code, _ := runHA(t, map[string]string{
		"HomeAssistantInstanceOps": `{"data":{"homeAssistantInstanceOps":[
			{"op":"call_service","runToolName":"ha__provider-says__call_service","description":"Call","inputSchema":{"type":"object","x":1}},
			{"op":"get_state","runToolName":"ha__other__get_state","description":"Get","inputSchema":null}]}}`,
	}, "ops", "ha1", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 2 {
		t.Fatalf("want 2 ops: %v\n%s", err, out)
	}
	if got[0]["runToolName"] != "ha__provider-says__call_service" {
		t.Errorf("runToolName must be the provider's, got %v", got[0]["runToolName"])
	}
	if schema, _ := got[0]["inputSchema"].(map[string]any); schema["x"] != float64(1) {
		t.Errorf("inputSchema must pass through, got %v", got[0]["inputSchema"])
	}
	if v, ok := got[1]["inputSchema"]; !ok || v != nil {
		t.Errorf("null inputSchema must stay present and null, got %v", got[1])
	}
}

func TestHaOpsEmptyCatalogIsNotNotFound(t *testing.T) {
	out, _, code, _ := runHA(t, map[string]string{
		"HomeAssistantInstanceOps": `{"data":{"homeAssistantInstanceOps":[]}}`,
	}, "ops", "ha1", "--json")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Errorf("an empty op set is a real answer: got %d %q", code, out)
	}
}

// A disabled instance's HA_INSTANCE_DISABLED refusal keeps the server's own
// message and takes the existing mapping (no new exit code is invented).
func TestHaOpsDisabledKeepsServerMessage(t *testing.T) {
	msg := "Home Assistant instance 'home' is disabled."
	_, errOut, code, _ := runHA(t, map[string]string{
		"HomeAssistantInstanceOps": `{"errors":[{"message":"` + msg + `","extensions":{"code":"HA_INSTANCE_DISABLED"}}],"data":{"homeAssistantInstanceOps":null}}`,
	}, "ops", "ha1")
	if code == 0 {
		t.Fatal("a disabled refusal must be a non-zero exit")
	}
	if !strings.Contains(errOut, msg) {
		t.Errorf("the server's full message must survive, got %q", errOut)
	}
}
