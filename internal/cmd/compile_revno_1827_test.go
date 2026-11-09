package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1827: a compile success whose revNo is below 1 is a malformed response,
// never a built revision (sibling of hadron-portal#1857).

const badRevision1827 = "server returned an invalid revision number %d for the compiled boot prompt; check with node get before compiling again"

func TestCompileRevNoBelowOneIsUnexpectedResponse1827(t *testing.T) {
	for _, c := range c1Commands {
		for _, rev := range []int{0, -1} {
			t.Run(fmt.Sprintf("%s revNo %d", c.name, rev), func(t *testing.T) {
				for _, jsonFlag := range []bool{true, false} {
					r := c1Responses()
					r[c.op] = fmt.Sprintf(`{"data":{"%s":{"node":{"id":"n1","urn":"%s"},"revNo":%d,"contentHash":"abc123","pointerSet":true,"approvalIssued":false}}}`, c.field, c2NodeURN, rev)
					args := append([]string{}, c.args...)
					if jsonFlag {
						args = append(args, "--json")
					}
					calls, out, err := runTeamTask(t, r, args...)
					want := fmt.Sprintf(badRevision1827, rev)
					if err == nil || err.Error() != want || exitCodeFor(err) != exitcode.Error {
						t.Fatalf("json=%v: err = %v (exit %d), want %q exit 1", jsonFlag, err, exitCodeFor(err), want)
					}
					c2Mutation(t, calls, c.op)
					if out != "" {
						t.Fatalf("json=%v: a malformed revision printed output: %q", jsonFlag, out)
					}
				}
			})
		}
	}
}

func TestCompileRevNoOnePrintsAsBefore1827(t *testing.T) {
	r := c1Responses()
	r["CompileAgentBootPrompt"] = strings.Replace(r["CompileAgentBootPrompt"], `"revNo":7`, `"revNo":1`, 1)
	_, out, err := runTeamTask(t, r, "agent", "compile", "hrn:agent:acme.com:support-bot")
	if err != nil || !strings.Contains(out, "revision 1;") {
		t.Fatalf("revNo 1 is the lowest valid revision: %v %q", err, out)
	}
}

func TestBatchCompileRevNoBelowOneIsUnknownWithNoApproveCommand1827(t *testing.T) {
	for _, rev := range []int{0, -1} {
		t.Run(fmt.Sprint(rev), func(t *testing.T) {
			team := []batchWorker{
				{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, lint: "recompile", compile: fmt.Sprintf("rev:%d", rev)},
				{id: "w-bob", name: "Bob", pointer: cptr("w-bob"), revision: 5, lint: "recompile", compile: "ok"},
			}
			fake, out, _, err := runBatch(t, team, "--apply", "--json")
			if got := exitCodeFor(err); got != exitcode.Error {
				t.Fatalf("exit = %d (%v), want %d", got, err, exitcode.Error)
			}
			if n := len(fake.ops("CompileWorkerBootPrompt")); n != 2 {
				t.Fatalf("the batch must continue past the bad row, compiled %d", n)
			}
			d := decodeBatch(t, out)
			if batchNames(d.Compiled) != "Bob" || batchNames(d.Unknown) != "Ann" {
				t.Fatalf("buckets wrong: %s", out)
			}
			if strings.Contains(out, "hadron node approve "+cptr("w-ann")) || strings.Contains(out, fmt.Sprintf(`"revision":%d`, rev)) {
				t.Fatalf("the malformed row leaked a revision or approve command: %s", out)
			}
			if !strings.Contains(fmt.Sprint(d.Unknown[0]["reason"]), fmt.Sprintf("invalid revision number %d", rev)) {
				t.Fatalf("reason does not name the invalid revision: %v", d.Unknown[0])
			}
		})
	}
}
