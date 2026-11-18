package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// runWith splices extra AppRun fields into the shared fixture (#1180).
func runWith(extra string) string {
	return strings.TrimSuffix(appRunJSON, "}") + "," + extra + "}"
}

func failedRun(kind, summary, who, next string) string {
	return strings.Replace(runWith(`"failureReason":{"kind":"`+kind+`","summary":"`+summary+`","whoCanFix":"`+who+`","nextStep":"`+next+`"}`),
		`"status":"COMPLETED"`, `"status":"FAILED"`, 1)
}

func runGetHuman(t *testing.T, run string) string {
	t.Helper()
	gql, _ := captureGraphQL(t, map[string]string{"AppRun": `{"data":{"appRun":` + run + `}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "get", "run1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return out.String()
}

func runGetJSON(t *testing.T, run string) map[string]json.RawMessage {
	t.Helper()
	gql, _ := captureGraphQL(t, map[string]string{"AppRun": `{"data":{"appRun":` + run + `}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "get", "run1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out.String()), &m); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	return m
}

func TestRunGetFailureReasonEachKind(t *testing.T) {
	for _, kind := range []string{"NOT_ADOPTED", "MEMORY_UNREADABLE", "OVER_BUDGET", "OTHER"} {
		t.Run(kind, func(t *testing.T) {
			run := failedRun(kind, "sum-"+kind, "who-"+kind, "next-"+kind)
			h := runGetHuman(t, run)
			for _, want := range []string{kind, "sum-" + kind, "who-" + kind, "next-" + kind} {
				if !strings.Contains(h, want) {
					t.Errorf("human output missing %q:\n%s", want, h)
				}
			}
			m := runGetJSON(t, run)
			var fr map[string]string
			if err := json.Unmarshal(m["failureReason"], &fr); err != nil {
				t.Fatalf("failureReason: %v (%s)", err, m["failureReason"])
			}
			if fr["kind"] != kind || fr["summary"] != "sum-"+kind || fr["whoCanFix"] != "who-"+kind || fr["nextStep"] != "next-"+kind {
				t.Errorf("failureReason = %v", fr)
			}
		})
	}
}

func TestRunGetReasonTerminalSafe(t *testing.T) {
	h := runGetHuman(t, failedRun("OTHER", `bad\u001b[31mred`, "x", "y"))
	if strings.Contains(h, "\x1b") {
		t.Errorf("escape byte leaked into human output: %q", h)
	}
}

func TestRunGetCost(t *testing.T) {
	h := runGetHuman(t, runWith(`"costMicroCents":"1050000"`))
	if !strings.Contains(h, "0.0105") || !strings.Contains(h, "1050000") {
		t.Errorf("cost missing:\n%s", h)
	}
	m := runGetJSON(t, runWith(`"costMicroCents":"1050000"`))
	if string(m["costMicroCents"]) != `"1050000"` {
		t.Errorf("costMicroCents = %s, want exact string", m["costMicroCents"])
	}
	// A value past float precision stays an exact string.
	m = runGetJSON(t, runWith(`"costMicroCents":"9007199254740993123"`))
	if string(m["costMicroCents"]) != `"9007199254740993123"` {
		t.Errorf("big cost mangled: %s", m["costMicroCents"])
	}
}

func TestRunGetCostUnknownIsNotZero(t *testing.T) {
	run := runWith(`"costMicroCents":null,"failureReason":null,"budgetActionsInitial":null,"budgetTokensInitial":null`)
	h := runGetHuman(t, run)
	if !strings.Contains(h, "cost unknown") {
		t.Errorf("null cost needs an explicit unknown marker:\n%s", h)
	}
	if strings.Contains(h, "$0") {
		t.Errorf("null cost rendered as zero:\n%s", h)
	}
	m := runGetJSON(t, run)
	for _, k := range []string{"costMicroCents", "failureReason", "budgetActionsInitial", "budgetTokensInitial"} {
		if v, ok := m[k]; !ok || string(v) != "null" {
			t.Errorf("%s = %s (present=%v), want null", k, v, ok)
		}
	}
}

func TestRunGetInitialBudgets(t *testing.T) {
	run := runWith(`"budgetActionsInitial":80,"budgetTokensInitial":20000`)
	h := runGetHuman(t, run)
	if !strings.Contains(h, "80") || !strings.Contains(h, "20000") || !strings.Contains(h, "initial") {
		t.Errorf("initial budgets missing:\n%s", h)
	}
	m := runGetJSON(t, run)
	if string(m["budgetActionsInitial"]) != "80" || string(m["budgetTokensInitial"]) != "20000" {
		t.Errorf("initial budgets = %s / %s", m["budgetActionsInitial"], m["budgetTokensInitial"])
	}
}

// cli#1428 C2: renamed from TestRunGetNoNewFieldsHumanUnchanged. The attempts
// line now says `cost unknown` for a run without a cost, so "unchanged" no longer
// described it; what it pins is that no reason or initial-budget line appears.
func TestRunGetWithoutNewFieldsAddsNoReasonOrInitialLines(t *testing.T) {
	// A fixture with none of the new fields must not gain reason or initial lines.
	h := runGetHuman(t, appRunJSON)
	for _, bad := range []string{"initial", "next step", "who can fix"} {
		if strings.Contains(h, bad) {
			t.Errorf("unexpected %q in output without the new fields:\n%s", bad, h)
		}
	}
}

func TestRunGetJSONExistingKeysUnchanged(t *testing.T) {
	m := runGetJSON(t, appRunJSON)
	want := map[string]string{
		"id": `"run1"`, "status": `"COMPLETED"`, "attempts": `1`, "budgetActions": `50`,
		"budgetTokens": `10000`, "failure": `null`, "finishedAt": `"2026-07-05T00:00:09Z"`,
	}
	for k, v := range want {
		if string(m[k]) != v {
			t.Errorf("%s = %s, want %s", k, m[k], v)
		}
	}
	if len(m) != 23+4 {
		t.Errorf("expected exactly the 23 existing keys plus 4 additive, got %d", len(m))
	}
}

func TestRunLsCostAndReasonColumns(t *testing.T) {
	items := strings.Join([]string{
		strings.Replace(runWith(`"costMicroCents":"1050000"`), `"id":"run1"`, `"id":"runok"`, 1),
		strings.Replace(failedRun("OVER_BUDGET", "s", "w", "n"), `"id":"run1"`, `"id":"runbad"`, 1),
		strings.Replace(runWith(`"costMicroCents":null`), `"id":"run1"`, `"id":"rununk"`, 1),
	}, ",")
	gql, _ := captureGraphQL(t, map[string]string{"AppRuns": `{"data":{"appRuns":{"total":3,"items":[` + items + `]}}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"run", "ls", "--org", "acme.com", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 || !strings.Contains(lines[0], "COST") || !strings.Contains(lines[0], "REASON") {
		t.Fatalf("header needs COST and REASON:\n%s", out.String())
	}
	// cli#1428 C5: a row the renderer drops must fail here, not pass vacuously.
	var sawOK, sawBad, sawUnk bool
	for _, l := range lines[1:] {
		switch {
		case strings.HasPrefix(l, "runok"):
			sawOK = true
			if !strings.Contains(l, "0.0105") {
				t.Errorf("cost missing: %s", l)
			}
		case strings.HasPrefix(l, "runbad"):
			sawBad = true
			if !strings.Contains(l, "OVER_BUDGET") {
				t.Errorf("reason kind missing: %s", l)
			}
		case strings.HasPrefix(l, "rununk"):
			sawUnk = true
			if !strings.Contains(l, "unknown") || strings.Contains(l, "$0") {
				t.Errorf("unknown cost marker: %s", l)
			}
		}
	}
	if !sawOK || !sawBad || !sawUnk {
		t.Errorf("every row must be seen (runok=%v runbad=%v rununk=%v):\n%s", sawOK, sawBad, sawUnk, out.String())
	}
}

// cli#1428 C3: a cost that is not a digit string is shown sanitized, and says
// it is not a micro-cent count, so it cannot read as dollars. Digit strings and
// null keep their wording.
func TestRunCostNonDigitCarriesAMarker1428(t *testing.T) {
	const marker = "(unrecognized)"
	for _, tc := range []struct{ name, cost string }{
		{"letters", `"12abc"`},
		{"negative", `"-5"`},
		{"empty", `""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := runGetHuman(t, runWith(`"costMicroCents":`+tc.cost))
			var attempts string
			for _, l := range strings.Split(h, "\n") {
				if strings.Contains(l, "attempts") {
					attempts = l
				}
			}
			if !strings.Contains(attempts, "cost ") || !strings.Contains(attempts, marker) || strings.Contains(attempts, "$") {
				t.Errorf("get: non-digit cost needs the marker and no dollar sign: %q", attempts)
			}
			gql, _ := captureGraphQL(t, map[string]string{"AppRuns": `{"data":{"appRuns":{"total":1,"items":[` + runWith(`"costMicroCents":`+tc.cost) + `]}}}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"run", "ls", "--org", "acme.com", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if !strings.Contains(out.String(), marker) {
				t.Errorf("list: non-digit cost needs the marker:\n%s", out.String())
			}
		})
	}
	// The digit and null wording is unchanged and carries no marker.
	for _, cost := range []string{`"1050000"`, `null`} {
		if h := runGetHuman(t, runWith(`"costMicroCents":`+cost)); strings.Contains(h, marker) {
			t.Errorf("cost %s must not carry the marker:\n%s", cost, h)
		}
	}
}
