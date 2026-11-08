package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#1203: `usage events --app-key <id>` is ONE pass-through flag over the
// server's `appKey: ID` argument (hadron-server#2944, merged at main
// 0c46aa6a). The CLI filters, resolves and attributes nothing itself.

// Invariant 1: with the flag absent the request is byte-identical to the one
// sent before the flag existed, for a representative set of the existing flags.
func TestUsageEventsAppKeyAbsentRequestIsUnchanged(t *testing.T) {
	_, _, code, calls := usage65Run(t, usage1205Empty, "usage", "events", "--json",
		"--memory", "acme.com:kb", "--type", "read", "--limit", "5", "--offset", "10",
		"--since", "2026-10-22T00:00:00Z", "--until", "2026-10-23T00:00:00Z")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := `{"memoryId":"hrn:mem:acme.com:kb","eventType":"read","limit":5,"offset":10,"since":"2026-10-22T00:00:00Z","until":"2026-10-23T00:00:00Z"}`
	if got := string(calls["UsageEventsPage"]); got != want {
		t.Errorf("request changed with the flag absent:\n got %s\nwant %s", got, want)
	}
	if strings.Contains(string(calls["UsageEventsPage"]), "appKey") {
		t.Error("appKey must be omitted, never null, when the flag is absent")
	}
}

// The id goes to the server verbatim and composes with every other filter.
func TestUsageEventsAppKeySentVerbatimAndComposes(t *testing.T) {
	_, stderr, code, calls := usage65Run(t, usage1205Empty, "usage", "events", "--json",
		"--app-key", "01abcDEF-key", "--memory", "acme.com:kb", "--type", "read", "--limit", "5",
		"--since", "2026-10-22T00:00:00Z", "--until", "2026-10-23T00:00:00Z")
	if code != 0 {
		t.Fatalf("exit %d %s", code, stderr)
	}
	vars := usage1205Vars(t, calls)
	for k, want := range map[string]any{
		"appKey": "01abcDEF-key", "memoryId": "hrn:mem:acme.com:kb", "eventType": "read",
		"limit": float64(5), "since": "2026-10-22T00:00:00Z", "until": "2026-10-23T00:00:00Z",
	} {
		if vars[k] != want {
			t.Errorf("%s = %v, want %v", k, vars[k], want)
		}
	}
	// Alone, with no resource named: only the key and the default limit.
	_, _, code, calls = usage65Run(t, usage1205Empty, "usage", "events", "--json", "--app-key", "k1")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	vars = usage1205Vars(t, calls)
	if vars["appKey"] != "k1" {
		t.Errorf("appKey = %v", vars["appKey"])
	}
	for _, k := range []string{"memoryId", "loc", "nodeId", "eventType", "offset", "since", "until"} {
		if _, ok := vars[k]; ok {
			t.Errorf("%s must stay omitted, got %v", k, vars[k])
		}
	}
}

// An empty or blank id is a usage error before any request, like the other
// filters; there is no "no filter" spelling except omitting the flag.
func TestUsageEventsAppKeyEmptyRefusedBeforeAnyCall(t *testing.T) {
	for _, v := range []string{"", "  "} {
		out, stderr, code, calls := usage65Run(t, usage1205Empty, "usage", "events", "--app-key", v)
		if code != 2 || len(calls) != 0 || out != "" {
			t.Errorf("--app-key %q: code=%d calls=%d out=%q stderr=%q", v, code, len(calls), out, stderr)
		}
		if !strings.Contains(stderr, "--app-key must not be empty") {
			t.Errorf("the message must say the flag must not be empty: %q", stderr)
		}
	}
}

// Invariant 3: a key the caller may not filter by answers exactly as an
// unknown key. The CLI forwards the id, makes NO other call (no appKeys lookup
// that would reveal which keys exist), and exits as any not-found does, with
// the same message either way.
func TestUsageEventsAppKeyUnknownAndUnfilterableAreIdentical(t *testing.T) {
	const refusal = `{"errors":[{"message":"App key not found","extensions":{"code":"APP_KEY_NOT_FOUND"}}]}`
	var outs, errs []string
	var codes []int
	for _, key := range []string{"key-that-does-not-exist", "key-of-another-tenant"} {
		out, stderr, code, calls := usage65Run(t, refusal, "usage", "events", "--app-key", key)
		if len(calls) != 1 || calls["UsageEventsPage"] == nil {
			t.Errorf("only UsageEventsPage may be called, got %v", appKey1203Ops(calls))
		}
		var vars map[string]any
		_ = json.Unmarshal(calls["UsageEventsPage"], &vars)
		if vars["appKey"] != key {
			t.Errorf("the ref must go straight to the server: %v", vars)
		}
		outs, errs, codes = append(outs, out), append(errs, strings.ReplaceAll(stderr, key, "<KEY>")), append(codes, code)
	}
	if codes[0] == 0 || codes[0] != codes[1] || outs[0] != outs[1] || errs[0] != errs[1] {
		t.Errorf("unknown and unfilterable keys must answer identically: %v %q %q", codes, outs, errs)
	}
	if codes[0] != 4 {
		t.Errorf("APP_KEY_NOT_FOUND exits as a not-found (4), got %d", codes[0])
	}
	if outs[0] != "" {
		t.Errorf("a refusal prints no rows, got %q", outs[0])
	}
}

// Invariant 4: a server refusal (e.g. the role gate for a key reading itself
// before the server allows it) goes through api.MapError. It is never rendered
// as an empty result.
func TestUsageEventsAppKeyServerRefusalIsNeverEmpty(t *testing.T) {
	for _, code := range []string{"FORBIDDEN", "UNAUTHENTICATED", "BAD_USER_INPUT"} {
		resp := `{"errors":[{"message":"declined","extensions":{"code":"` + code + `"}}]}`
		out, stderr, exit, _ := usage65Run(t, resp, "usage", "events", "--app-key", "k1")
		if exit == 0 {
			t.Errorf("%s: exit 0 would read as an empty result", code)
		}
		if strings.TrimSpace(out) == "[]" || out != "" {
			t.Errorf("%s: stdout must carry nothing, got %q", code, out)
		}
		if !strings.Contains(stderr, "declined") {
			t.Errorf("%s: the server message must reach stderr: %q", code, stderr)
		}
	}
}

// Invariant 2 and the ruling-0244 label: the row carries no key field, so the
// CLI shows none and invents no "unattributed" cell; a filtered result is the
// server's rows, untouched.
func TestUsageEventsAppKeyAddsNoKeyColumnOrField(t *testing.T) {
	resp := `{"data":{"usageEventsPage":{"total":1,"hasMore":false,"items":[` + usage65Event + `]}}}`
	out, _, code, _ := usage65Run(t, resp, "usage", "events", "--json", "--app-key", "k-secret-id")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("%v %s", err, out)
	}
	for k := range rows[0] {
		if strings.Contains(strings.ToLower(k), "appkey") || strings.Contains(strings.ToLower(k), "key") && k != "keyOwnerType" {
			t.Errorf("unexpected key-bearing field %q", k)
		}
	}
	text, _, _, _ := usage65Run(t, resp, "usage", "events", "--app-key", "k-secret-id")
	if strings.Contains(text, "k-secret-id") || strings.Contains(strings.ToLower(text), "unattributed") {
		t.Errorf("the id is not echoed and no unattributed label exists without a server field:\n%s", text)
	}
}

// Help (gate 6707007215 + addendum 6707982936, ruling 0254, cor:acl:090:01
// rev 12): no resource means your own activity; naming a memory needs READ and
// shows every caller's events as volumes; the key flag shows that key's events
// in memories readable now plus its no-memory events, and works only for the
// key's App manager or the key itself.
func TestUsageEventsHelpStatesScopesAndAppKey(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"usage", "events", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	for _, want := range []string{
		"--app-key",
		"own activity",
		"every caller",
		"READ",
		"manages",
		"no memory",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help must mention %q", want)
		}
	}
	if strings.Contains(help, "You only see events in memories you can read") {
		t.Error("help still states the pre-#2817 rule: a read naming no memory is now your own activity")
	}
}

func appKey1203Ops(m map[string]json.RawMessage) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}
