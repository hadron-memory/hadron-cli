package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func activity65Run(t *testing.T, response string, args ...string) (string, string, int, map[string]json.RawMessage, int) {
	t.Helper()
	n := 0
	srv, calls := captureGraphQLFunc(t, func(op string) string {
		n++
		if op != "UsageActivity" && op != "UsageEventsPage" {
			t.Errorf("unexpected op %s", op)
		}
		return response
	})
	f, out := testFactory(t)
	stderr := &strings.Builder{}
	f.IOStreams.ErrOut = stderr
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", srv.URL))
	err := root.Execute()
	code := 0
	if err != nil {
		code = renderError(f, err)
	}
	return out.String(), stderr.String(), code, calls, n
}
func TestUsageActivity65OmissionAndScope(t *testing.T) {
	for _, refs := range [][]string{nil, {"m-one"}, {"hrn:mem:acme.com:a", "m-two"}} {
		args := []string{"usage", "activity", "--json"}
		for _, ref := range refs {
			args = append(args, "--memory", ref)
		}
		out, stderr, code, calls, n := activity65Run(t, `{"data":{"usageActivity":[]}}`, args...)
		if code != 0 || strings.TrimSpace(out) != "[]" || n != 1 {
			t.Fatalf("%d %d %s %s", code, n, out, stderr)
		}
		var v map[string]json.RawMessage
		if err := json.Unmarshal(calls["UsageActivity"], &v); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"from", "to", "bucket", "timeZone"} {
			if _, ok := v[key]; ok {
				t.Fatalf("implicit %s: %s", key, v[key])
			}
		}
		if refs == nil {
			if _, ok := v["memoryRefs"]; ok {
				t.Fatalf("implicit scope %s", v["memoryRefs"])
			}
		} else {
			want, _ := json.Marshal(refs)
			if string(v["memoryRefs"]) != string(want) {
				t.Fatal(string(v["memoryRefs"]))
			}
		}
	}
}
func TestUsageActivity65FlagsBucketsAndRefusal(t *testing.T) {
	response := `{"data":{"usageActivity":[{"date":"2026-10-01","reads":3,"writes":0},{"date":"2026-10-03","reads":0,"writes":4}]}}`
	const from, to = "2026-10-01T00:00:00Z", "2026-10-04T00:00:00Z"
	args := []string{"usage", "activity", "--from", from, "--to", to, "--bucket", "WEEK", "--time-zone", "Asia/Tokyo", "--memory", "m1"}
	out, stderr, code, calls, n := activity65Run(t, response, append(args, "--json")...)
	if code != 0 || n != 1 {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	var buckets []map[string]any
	if err := json.Unmarshal([]byte(out), &buckets); err != nil || len(buckets) != 2 || buckets[0]["date"] != "2026-10-01" || buckets[1]["writes"] != float64(4) {
		t.Fatal(out, err)
	}
	var v map[string]any
	_ = json.Unmarshal(calls["UsageActivity"], &v)
	for key, want := range map[string]any{"from": from, "to": to, "bucket": "WEEK", "timeZone": "Asia/Tokyo"} {
		if v[key] != want {
			t.Fatal(key, v)
		}
	}
	text, _, code, _, _ := activity65Run(t, response, args...)
	if code != 0 || !strings.Contains(text, "2026-10-01T00:00:00Z") || !strings.Contains(text, "WEEK") || strings.Contains(text, "2026-10-02") || strings.Contains(text, "TOTAL") || !strings.Contains(text, "unreadable or unknown refs are excluded") {
		t.Fatal(text)
	}
	text, _, code, _, _ = activity65Run(t, response, "usage", "activity")
	if code != 0 || !strings.Contains(text, "server default") {
		t.Fatal(text)
	}
	args = []string{"usage", "activity"}
	for i := 0; i < 21; i++ {
		args = append(args, "--memory", fmt.Sprintf("m%d", i))
	}
	out, stderr, code, calls, n = activity65Run(t, `{"errors":[{"message":"Too many memoryRefs (max 20).","extensions":{"code":"BAD_USER_INPUT"}}]}`, args...)
	if code != 2 || n != 1 || !strings.Contains(stderr, "Too many memoryRefs (max 20).") {
		t.Fatalf("%d %d %s %s", code, n, out, stderr)
	}
	var wire struct {
		MemoryRefs []string `json:"memoryRefs"`
	}
	_ = json.Unmarshal(calls["UsageActivity"], &wire)
	if len(wire.MemoryRefs) != 21 {
		t.Fatal(wire)
	}
}
func TestUsageActivity65NodeAndArgumentParsing(t *testing.T) {
	for _, node := range []string{"0123456789abcdef0123456789abcdef", "hrn:node:acme.com:kb:a:b"} {
		_, stderr, code, calls, n := activity65Run(t, `{"data":{"usageEventsPage":{"total":0,"hasMore":false,"items":[]}}}`, "usage", "events", "--node", node)
		if code != 0 || n != 1 {
			t.Fatal(code, stderr)
		}
		var v map[string]any
		_ = json.Unmarshal(calls["UsageEventsPage"], &v)
		if v["nodeId"] != node {
			t.Fatal(v)
		}
		if _, ok := v["loc"]; ok {
			t.Fatal(v)
		}
	}
	for _, args := range [][]string{{"usage", "events", "--node", "0123456789abcdef0123456789abcdef", "--loc", "a:b"}, {"usage", "events", "--loc", "a:b", "--node", "hrn:node:acme.com:kb:a:b"}, {"usage", "events", "--node="}, {"usage", "activity", "--memory="}, {"usage", "activity", "--from="}, {"usage", "activity", "--to="}, {"usage", "activity", "--bucket="}, {"usage", "activity", "--time-zone="}} {
		_, stderr, code, _, n := activity65Run(t, `{}`, args...)
		if code != 2 || n != 0 {
			t.Fatalf("%v %d %d %s", args, code, n, stderr)
		}
	}
}

func TestUsageActivity65RefusalAndMalformedBuckets(t *testing.T) {
	for _, tc := range []struct {
		server string
		code   int
	}{
		{"UNAUTHENTICATED", 3}, {"FORBIDDEN", 8}, {"MEMORY_NOT_FOUND", 4}, {"BAD_USER_INPUT", 2},
	} {
		response := `{"errors":[{"message":"server declined","extensions":{"code":"` + tc.server + `"}}]}`
		for _, args := range [][]string{{"usage", "activity", "--memory", "unreadable", "--json"}, {"usage", "events", "--node", "0123456789abcdef0123456789abcdef", "--json"}} {
			out, _, code, _, n := activity65Run(t, response, args...)
			if n != 1 || code != tc.code || !strings.Contains(out, "server declined") {
				t.Fatalf("%s %d %d %s", tc.server, n, code, out)
			}
		}
	}
	out, stderr, code, _, n := activity65Run(t, `{"data":{"usageActivity":[{"date":"a","reads":1,"writes":2},null]}}`, "usage", "activity")
	if code != 1 || n != 1 || out != "" || !strings.Contains(stderr, "invalid usage-activity bucket") {
		t.Fatal(code, n, out, stderr)
	}
	for _, node := range []string{"a:b:c", "hrn:mem:acme.com:kb", "plain-loc"} {
		_, _, code, _, n := activity65Run(t, `{}`, "usage", "events", "--node", node)
		if code != 2 || n != 0 {
			t.Fatal(node, code, n)
		}
	}
}

func TestUsageActivity65ScopeText(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "scope: own activity (no --memory sent)"},
		{[]string{"--memory", "unreadable"}, "scope: requested resource union"},
	} {
		args := append([]string{"usage", "activity"}, tc.args...)
		out, _, code, _, _ := activity65Run(t, `{"data":{"usageActivity":[]}}`, args...)
		if code != 0 || !strings.Contains(out, tc.want) || strings.Contains(out, "TOTAL") {
			t.Fatalf("code=%d, output=%q, want=%q and no TOTAL", code, out, tc.want)
		}
		if len(tc.args) > 0 && !strings.Contains(out, "unreadable or unknown refs are excluded") {
			t.Fatalf("missing resource caveat: %q", out)
		}
	}
}

func TestUsageActivity65LegacyNodeAndServerDate(t *testing.T) {
	_, _, code, calls, _ := activity65Run(t, `{"data":{"usageEventsPage":{"total":0,"hasMore":false,"items":[]}}}`, "usage", "events", "--node", "acme.com::kb::a:b")
	var vars map[string]any
	if err := json.Unmarshal(calls["UsageEventsPage"], &vars); err != nil {
		t.Fatal(err)
	}
	if code != 0 || vars["nodeId"] != "hrn:node:acme.com::kb::a:b" {
		t.Fatalf("code=%d, nodeId=%v", code, vars["nodeId"])
	}
	response := `{"data":{"usageActivity":[{"date":"a\u001b[31m\u202eb","reads":1,"writes":2}]}}`
	out, _, code, _, _ := activity65Run(t, response, "usage", "activity")
	if code != 0 || strings.ContainsAny(out, "\x1b\u202e") {
		t.Fatalf("unsafe text code=%d out=%q", code, out)
	}
	out, _, code, _, _ = activity65Run(t, response, "usage", "activity", "--json")
	if code != 0 || !strings.Contains(out, `\u001b[31m`) {
		t.Fatalf("lost JSON original code=%d out=%q", code, out)
	}
}

// #1410: the server's bucket order and every count are passed through
// untouched. A sparse, deliberately out-of-order response with distinct reads
// and writes in each bucket must come back as the complete array, in the same
// order, with no zero-fill and no total row. A chronological fixture or a
// field-by-field spot check would miss an accidental sort or a dropped count.
func TestUsageActivity1410PreservesOrderAndEveryCount(t *testing.T) {
	response := `{"data":{"usageActivity":[` +
		`{"date":"2026-10-09","reads":7,"writes":2},` +
		`{"date":"2026-10-02","reads":11,"writes":5},` +
		`{"date":"2026-10-30","reads":1,"writes":13},` +
		`{"date":"2026-10-16","reads":17,"writes":3}]}}`
	out, stderr, code, _, n := activity65Run(t, response, "usage", "activity", "--json")
	if code != 0 || n != 1 {
		t.Fatalf("%d %d %s %s", code, n, out, stderr)
	}
	var got []struct {
		Date   string `json:"date"`
		Reads  int    `json:"reads"`
		Writes int    `json:"writes"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(out, err)
	}
	type bucket = struct {
		Date   string `json:"date"`
		Reads  int    `json:"reads"`
		Writes int    `json:"writes"`
	}
	want := []bucket{
		{"2026-10-09", 7, 2},
		{"2026-10-02", 11, 5},
		{"2026-10-30", 1, 13},
		{"2026-10-16", 17, 3},
	}
	if len(got) != len(want) {
		t.Fatalf("want %d buckets (no zero-fill, no total), got %d: %s", len(want), len(got), out)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bucket %d: want %+v, got %+v (full output %s)", i, want[i], got[i], out)
		}
	}
	// The human table keeps the same order and counts, and adds no total row.
	text, _, code, _, _ := activity65Run(t, response, "usage", "activity")
	if code != 0 || strings.Contains(text, "TOTAL") {
		t.Fatal(code, text)
	}
	last := -1
	for _, w := range want {
		row := fmt.Sprintf("%s  %d  %d", w.Date, w.Reads, w.Writes)
		fields := strings.Join(strings.Fields(row), " ")
		idx := -1
		for _, line := range strings.Split(text, "\n") {
			if strings.Join(strings.Fields(line), " ") == fields {
				idx = strings.Index(text, line)
				break
			}
		}
		if idx < 0 || idx <= last {
			t.Fatalf("table row %q missing or out of order (last offset %d, idx %d):\n%s", fields, last, idx, text)
		}
		last = idx
	}
}
