package cmd

import (
	"strings"
	"testing"
)

func TestC3bServedRecordNullableAndExplicit(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		op, body string
	}{
		{[]string{"team", "worker", "served-prompt", "hrn:worker:acme.com:iris", "--json"}, "WorkerServedPrompt", `{"data":{"worker":{"servedPrompt":null}}}`},
		{[]string{"run", "served-prompt", "run_123", "--json"}, "RunServedPrompt", `{"data":{"appRun":{"servedPrompt":null}}}`},
	} {
		t.Run(tc.op, func(t *testing.T) {
			calls, out, err := runTeamTask(t, map[string]string{tc.op: tc.body}, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 1 || !strings.Contains(out, `"servedPrompt": null`) {
				t.Fatalf("wrong nullable record: %v %s", *calls, out)
			}
			c := c2Mutation(t, calls, tc.op)
			if c.Vars["ref"] != tc.args[len(tc.args)-2] {
				t.Fatalf("wrong ref: %s", c.RawVars)
			}
		})
	}
}
