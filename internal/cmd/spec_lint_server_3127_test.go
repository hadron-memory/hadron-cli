package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#3127: `spec lint` relays the server's two content checks
// (Node.specLint, hadron-server#5109) and carries no copy of them.

type serverLintReport []struct {
	Citation string `json:"citation"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Line     *int   `json:"line"`
	Sentence string `json:"sentence"`
}

func runServerLint3127(t *testing.T, lintResp string, extra ...string) (serverLintReport, string, map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":    resolveSpecJSON,
		"GetNode":       `{"data":{"node":` + badSpecDetail + `}}`,
		"NodeBatch":     specLintRawBodyStub(badSpecDetail),
		"SpecLintBatch": lintResp,
		"Memories":      memListMicromentorJSON,
		"GetMemory":     memGetVectorEnabledJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"spec", "lint", "msg:010:02", "-m", specMem, "--json", "--server", gql.URL}, extra...))
	err := root.Execute()
	var rep serverLintReport
	if strings.TrimSpace(out.String()) == "" && err != nil {
		return nil, "", captured, err
	}
	if jerr := json.Unmarshal([]byte(out.String()), &rep); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out.String())
	}
	return rep, out.String(), captured, err
}

func lintBatch3127(findings string) string {
	return `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[{"id":"sp1","loc":"msg:010:02","specLint":` + findings + `}]}}}`
}

func TestSpecLintRelaysServerWhyErrorAndExitsConflict(t *testing.T) {
	rep, out, captured, err := runServerLint3127(t, lintBatch3127(
		`[{"citation":"msg:010:02","line":12,"message":"a Rule spec needs a Why We Need This Rule section","rule":"RULE_WHY_MISSING","sentence":null,"severity":"ERROR"}]`))
	if exitCodeFor(err) != exitcode.Conflict {
		t.Fatalf("a server ERROR must exit 5, got err=%v code=%d", err, exitCodeFor(err))
	}
	if len(rep) != 1 || rep[0].Rule != "rule-why-missing" || rep[0].Severity != "error" || rep[0].Citation != "msg:010:02" || rep[0].Line == nil || *rep[0].Line != 12 {
		t.Errorf("finding not relayed unchanged: %s", out)
	}
	var vars struct {
		Refs []string `json:"refs"`
	}
	if err := json.Unmarshal(captured["SpecLintBatch"], &vars); err != nil || len(vars.Refs) != 1 || vars.Refs[0] != "sp1" {
		t.Errorf("the server must be asked about the node by id, got %s", captured["SpecLintBatch"])
	}
}

func TestSpecLintServerWarningNeverFailsUnlessStrict(t *testing.T) {
	warn := lintBatch3127(`[{"citation":"msg:010:02","line":4,"message":"a sentence limits something and gives no reason","rule":"LIMIT_WITHOUT_REASON","sentence":"X is excluded from the list.","severity":"WARNING"}]`)
	rep, out, _, err := runServerLint3127(t, warn)
	if err != nil {
		t.Fatalf("a warning must not fail the run, got %v\n%s", err, out)
	}
	if len(rep) != 1 || rep[0].Rule != "limit-without-reason" || rep[0].Severity != "warning" || rep[0].Sentence != "X is excluded from the list." {
		t.Errorf("warning not relayed with its sentence: %s", out)
	}
	_, _, _, err = runServerLint3127(t, warn, "--strict")
	if exitCodeFor(err) != exitcode.Conflict {
		t.Errorf("--strict must promote the warning to a failure, got %v", err)
	}
}

// An older server lacks the field. The report must say the two checks did not
// run — a clean result must never read as "the sections were checked".
func TestSpecLintOlderServerSaysTheChecksDidNotRun(t *testing.T) {
	old := `{"errors":[{"message":"Cannot query field \"specLint\" on type \"Node\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	rep, out, _, err := runServerLint3127(t, old)
	if err != nil {
		t.Fatalf("an older server must not fail the lint: %v\n%s", err, out)
	}
	if len(rep) != 1 || rep[0].Rule != "spec-lint-unavailable" || rep[0].Severity != "info" || !strings.Contains(rep[0].Message, "did NOT run") {
		t.Errorf("expected the not-run notice, got %s", out)
	}
	_, _, _, err = runServerLint3127(t, old, "--strict")
	if err != nil {
		t.Errorf("info must not fail --strict: %v", err)
	}
}

func TestSpecLintNullServerLintIsNotACleanAnswer(t *testing.T) {
	rep, out, _, err := runServerLint3127(t, lintBatch3127(`null`))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(rep) != 1 || rep[0].Rule != "spec-lint-not-read" || !strings.Contains(rep[0].Message, "msg:010:02") {
		t.Errorf("a null specLint must be reported as not read, got %s", out)
	}
}

func TestSpecLintEmptyServerLintIsClean(t *testing.T) {
	rep, out, _, err := runServerLint3127(t, lintBatch3127(`[]`))
	if err != nil || len(rep) != 0 {
		t.Errorf("an empty list is a real, clean answer: err=%v\n%s", err, out)
	}
}

// Any other failure of the read is the command's failure, not a silent skip.
func TestSpecLintServerReadFailureIsNotSwallowed(t *testing.T) {
	_, _, _, err := runServerLint3127(t, `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`)
	if err == nil {
		t.Fatal("a failed specLint read must fail the lint")
	}
}
