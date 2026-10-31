package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// #1421 section 1: kill the surviving terminal-safety mutants from #1415
// (Ralion §10 P3-1). Server text is hostile; no ESC byte may reach a human view.

const hostileEsc = `x\u001b[31mRED\u001b]0;pwn\u0007y`

func hostileReason(kind, summary, who, next string) string {
	return strings.Replace(runWith(`"failureReason":{"kind":"`+kind+`","summary":"`+summary+`","whoCanFix":"`+who+`","nextStep":"`+next+`"}`),
		`"status":"COMPLETED"`, `"status":"FAILED"`, 1)
}

func TestRunGetSanitizesEveryReasonField(t *testing.T) {
	for name, run := range map[string]string{
		"kind":      hostileReason(hostileEsc, "s", "w", "n"),
		"summary":   hostileReason("K", hostileEsc, "w", "n"),
		"whoCanFix": hostileReason("K", "s", hostileEsc, "n"),
		"nextStep":  hostileReason("K", "s", "w", hostileEsc),
	} {
		t.Run(name, func(t *testing.T) {
			h := runGetHuman(t, run)
			if strings.ContainsAny(h, "\x1b\x07") {
				t.Errorf("control byte leaked in run get: %q", h)
			}
			if !strings.Contains(h, "RED") {
				t.Errorf("sanitized text should survive: %q", h)
			}
		})
	}
}

func runLsHuman(t *testing.T, items ...string) string {
	t.Helper()
	gql, _ := captureGraphQL(t, map[string]string{"AppRuns": `{"data":{"appRuns":{"total":` + string(rune('0'+len(items))) + `,"items":[` + strings.Join(items, ",") + `]}}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "ls", "--org", "acme.com", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return out.String()
}

func TestRunLsSanitizesReasonKindColumn(t *testing.T) {
	h := runLsHuman(t, hostileReason(hostileEsc, "s", "w", "n"))
	if strings.ContainsAny(h, "\x1b\x07") {
		t.Errorf("control byte leaked in run list REASON: %q", h)
	}
}

func TestRunNonDigitCostIsSanitizedGetAndList(t *testing.T) {
	for name, cost := range map[string]string{
		"escape after digit": `1\u001b[31m`,
		"all hostile":        hostileEsc,
		"minus sign":         `-5`,
		"decimal point":      `1.5`,
	} {
		t.Run(name, func(t *testing.T) {
			run := runWith(`"costMicroCents":"` + cost + `"`)
			if h := runGetHuman(t, run); strings.ContainsAny(h, "\x1b\x07") || strings.Contains(h, "micro-cents") {
				t.Errorf("non-digit cost must be sanitized, not formatted: %q", h)
			}
			if h := runLsHuman(t, run); strings.ContainsAny(h, "\x1b\x07") {
				t.Errorf("non-digit cost leaked in run list: %q", h)
			}
		})
	}
}

// #1421 section 2: exact human cost at the boundaries, in get and list.
func TestRunCostBoundariesGetAndList(t *testing.T) {
	cases := []struct{ mc, dollars string }{
		{"0", "$0.00"},
		{"1", "$0.00000001"},
		{"100", "$0.000001"},
		{"100000000", "$1.00"},
		{"150000000", "$1.50"},
		{"1050000", "$0.0105"},
		{"9007199254740993123", "$90071992547.40993123"},
	}
	for _, c := range cases {
		t.Run(c.mc, func(t *testing.T) {
			run := runWith(`"costMicroCents":"` + c.mc + `"`)
			want := c.dollars + " (" + c.mc + " micro-cents)"
			if h := runGetHuman(t, run); !strings.Contains(h, "cost "+want) {
				t.Errorf("get: want %q in %q", "cost "+want, h)
			}
			l := runLsHuman(t, run)
			if !strings.Contains(l, c.dollars) || strings.Contains(l, "micro-cents") {
				t.Errorf("list: want compact %q, no micro-cents: %q", c.dollars, l)
			}
		})
	}
}

// The fake server never checked the outgoing selection: assert the query text.
func TestRunQueriesSelectTheNewFields(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		b, _ := io.ReadAll(r.Body)
		var body struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(b, &body)
		queries = append(queries, body.Query)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(body.Query, "appRuns") {
			_, _ = w.Write([]byte(`{"data":{"appRuns":{"total":0,"items":[]}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"appRun":` + appRunJSON + `}}`))
	}))
	t.Cleanup(srv.Close)
	for _, args := range [][]string{{"run", "get", "run1"}, {"run", "ls", "--org", "acme.com"}} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(args, "--server", srv.URL))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if len(queries) != 2 {
		t.Fatalf("queries = %d", len(queries))
	}
	for _, q := range queries {
		for _, field := range []string{"failureReason", "costMicroCents", "budgetActionsInitial", "budgetTokensInitial", "whoCanFix", "nextStep", "summary", "kind"} {
			if !strings.Contains(q, field) {
				t.Errorf("query does not select %q:\n%s", field, q)
			}
		}
	}
}

// All 23 pre-existing keys keep their value on get, list, trigger and cancel,
// with a populated fixture and a null fixture.
func TestRunExistingJSONKeysUnchangedOnEveryRunCommand(t *testing.T) {
	populated := strings.NewReplacer(
		`"triggerId":null`, `"triggerId":"trg1"`,
		`"curNodeUrn":null`, `"curNodeUrn":"hrn:node:acme.com::ops::tasks:at"`,
		`"parentRunId":null`, `"parentRunId":"run0"`,
		`"policy":null`, `"policy":{"a":1}`,
		`"failure":null`, `"failure":{"code":"X"}`,
	).Replace(appRunJSON)
	for fname, fixture := range map[string]string{"null-heavy": appRunJSON, "populated": populated} {
		var want map[string]json.RawMessage
		if err := json.Unmarshal([]byte(fixture), &want); err != nil {
			t.Fatal(err)
		}
		if len(want) != 23 {
			t.Fatalf("fixture has %d keys, want 23", len(want))
		}
		cmds := map[string]struct {
			op, field string
			args      []string
			list      bool
		}{
			"get":     {"AppRun", "appRun", []string{"run", "get", "run1"}, false},
			"list":    {"AppRuns", "", []string{"run", "ls", "--org", "acme.com"}, true},
			"trigger": {"TriggerAppRun", "triggerAppRun", []string{"run", "trigger", "--app", "capp100000000000000000000", "--entry", "acme.com::ops::tasks:digest"}, false},
			"cancel":  {"CancelAppRun", "cancelAppRun", []string{"run", "cancel", "run1", "--yes"}, false},
		}
		for cname, c := range cmds {
			t.Run(fname+"/"+cname, func(t *testing.T) {
				resp := `{"data":{"` + c.field + `":` + fixture + `}}`
				if c.list {
					resp = `{"data":{"appRuns":{"total":1,"items":[` + fixture + `]}}}`
				}
				gql, _ := captureGraphQL(t, map[string]string{c.op: resp})
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(append(append([]string{}, c.args...), "--json", "--server", gql.URL))
				if err := root.Execute(); err != nil {
					t.Fatalf("execute: %v", err)
				}
				raw := []byte(out.String())
				var got map[string]json.RawMessage
				if c.list {
					var arr []map[string]json.RawMessage
					if err := json.Unmarshal(raw, &arr); err != nil || len(arr) != 1 {
						t.Fatalf("list json: %v\n%s", err, raw)
					}
					got = arr[0]
				} else if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatalf("json: %v\n%s", err, raw)
				}
				for k, v := range want {
					var a, b bytes.Buffer
					_ = json.Compact(&a, v)
					_ = json.Compact(&b, got[k])
					if a.String() != b.String() {
						t.Errorf("%s: %s = %s, want %s", cname, k, b.String(), a.String())
					}
				}
			})
		}
	}
}

// #1421 section 3: wording.

func TestRunListFailedWithoutReasonShowsUnknownNotDash(t *testing.T) {
	failedNoReason := strings.Replace(strings.Replace(runWith(`"failureReason":null`), `"status":"COMPLETED"`, `"status":"FAILED"`, 1), `"id":"run1"`, `"id":"runfail"`, 1)
	okRun := strings.Replace(runWith(`"failureReason":null`), `"id":"run1"`, `"id":"runok"`, 1)
	out := runLsHuman(t, failedNoReason, okRun)
	var sawFail, sawOK bool
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "runfail"):
			sawFail = true
			if !strings.HasSuffix(strings.TrimSpace(l), "unknown") {
				t.Errorf("FAILED run with no reason must say unknown: %q", l)
			}
		case strings.HasPrefix(l, "runok"):
			sawOK = true
			if !strings.HasSuffix(strings.TrimSpace(l), "—") {
				t.Errorf("a run that did not fail keeps the dash: %q", l)
			}
		}
	}
	// A renderer that drops a row must fail here, not pass vacuously.
	if !sawFail {
		t.Errorf("runfail row missing from list output:\n%s", out)
	}
	if !sawOK {
		t.Errorf("runok row missing from list output:\n%s", out)
	}
}

// #1435 item 2.1 (Uma): every non-success terminal status with no reason is
// "unknown" (should have one); a run that is fine or still in flight keeps the
// dash (n/a).
func TestRunListNonSuccessTerminalWithoutReasonShowsUnknown(t *testing.T) {
	cases := map[string]string{
		"FAILED": "unknown", "TIMED_OUT": "unknown", "CANCELLED": "unknown",
		"COMPLETED": "—", "RUNNING": "—", "PENDING": "—",
	}
	for status, want := range cases {
		t.Run(status, func(t *testing.T) {
			r := strings.Replace(strings.Replace(runWith(`"failureReason":null`), `"status":"COMPLETED"`, `"status":"`+status+`"`, 1), `"id":"run1"`, `"id":"rowx"`, 1)
			out := runLsHuman(t, r)
			var saw bool
			for _, l := range strings.Split(out, "\n") {
				if strings.HasPrefix(l, "rowx") {
					saw = true
					if !strings.HasSuffix(strings.TrimSpace(l), want) {
						t.Errorf("%s with no reason: want REASON %q: %q", status, want, l)
					}
				}
			}
			if !saw {
				t.Errorf("row missing:\n%s", out)
			}
		})
	}
}

func TestRunGetUnknownMarkerIsOneWord(t *testing.T) {
	h := runGetHuman(t, runWith(`"budgetActionsInitial":80,"budgetTokensInitial":null,"costMicroCents":null`))
	if !strings.Contains(h, "initial budget 80 actions / unknown tokens") {
		t.Errorf("initial budget must use 'unknown' like cost:\n%s", h)
	}
	if !strings.Contains(h, "cost unknown") {
		t.Errorf("cost marker lost:\n%s", h)
	}
}

func TestRunGetReasonLinesUseLabelValueAndSitUnderStatus(t *testing.T) {
	h := runGetHuman(t, hostileReason("OVER_BUDGET", "ran out", "the app owner", "raise the budget"))
	if strings.Contains(h, "who can fix:") || strings.Contains(h, "next step:") {
		t.Errorf("labels must not use colons:\n%s", h)
	}
	for _, want := range []string{"who can fix the app owner", "next step raise the budget", "reason OVER_BUDGET ran out"} {
		if !strings.Contains(h, want) {
			t.Errorf("missing %q:\n%s", want, h)
		}
	}
	lines := strings.Split(h, "\n")
	if !strings.HasPrefix(lines[0], "run ") || !strings.Contains(lines[1], "reason OVER_BUDGET") {
		t.Errorf("the reason must sit directly under the status line:\n%s", h)
	}
}

func TestAgenticUsageCostNullWording(t *testing.T) {
	b, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "never 0)") || strings.Contains(s, "null = UNKNOWN, never 0") {
		t.Error("ambiguous 'null = UNKNOWN, never 0' wording remains")
	}
	if !strings.Contains(s, "null means unknown; it must not be read as zero") {
		t.Error("agentic-usage must say 'null means unknown; it must not be read as zero'")
	}
}

// #1444: keep the prose paragraph readable at the existing 80-column wrap.
func TestRunListHelpParagraphWrap1444(t *testing.T) {
	f, _ := testFactory(t)
	leaf, _, err := NewRootCmd(f).Find([]string{"run", "list"})
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(leaf.Long, "\n") {
		if width := len([]rune(line)); width > 80 {
			t.Errorf("run list Long line %d has %d columns (max 80): %s", i+1, width, line)
		}
	}
}
