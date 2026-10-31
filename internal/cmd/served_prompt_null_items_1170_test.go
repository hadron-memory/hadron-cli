package cmd

import (
	"strings"
	"testing"
)

// #1170 (Basil's residual on #1159): a record whose provenance items are null
// is refused as invalid. The existing --all case put the null on the second
// page, where the no-advance guard also fails the read, so it passed with the
// items guard removed. These pin the guard by its own message, on a first
// page and without --all.
func TestServedPromptRefusesNullProvenanceItemsOnTheFirstPage(t *testing.T) {
	nullItems := strings.Replace(c3bRecord, `[{"nodeId":"source1","revSeq":3,"hash":"source-hash","pruned":false}]`, `null`, 1)
	if nullItems == c3bRecord {
		t.Fatal("fixture replacement did not apply")
	}
	for _, tc := range []struct {
		name, op, body string
		args           []string
	}{
		{"worker, one page", "WorkerServedPrompt", c3bWorkerRecord(nullItems),
			[]string{"team", "worker", "served-prompt", "hrn:worker:acme.com:iris", "--json"}},
		{"run, one page", "RunServedPrompt", `{"data":{"appRun":{"servedPrompt":` + nullItems + `}}}`,
			[]string{"run", "served-prompt", "run_123", "--json"}},
		{"worker, first --all page", "WorkerServedPrompt", c3bWorkerRecord(nullItems),
			[]string{"team", "worker", "served-prompt", "hrn:worker:acme.com:iris", "--limit", "1", "--all", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, out, err := runTeamTask(t, map[string]string{tc.op: tc.body}, tc.args...)
			if exitCodeFor(err) != 1 || !strings.Contains(err.Error(), "invalid served record") {
				t.Fatalf("err %v (exit %d), want the invalid-record refusal", err, exitCodeFor(err))
			}
			if len(*calls) != 1 {
				t.Fatalf("%d requests, want the first page refused before any other", len(*calls))
			}
			if strings.Contains(out, "recorded-hash") {
				t.Fatalf("partial result printed: %s", out)
			}
		})
	}
}
