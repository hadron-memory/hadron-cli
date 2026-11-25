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

// --all / --prefix take the corpus path: nodes come from the batch read, and
// the server is asked about all of them by id. One comes back unavailable: it
// must be named, never dropped (blind P2-1/P2-4 on #3150).
func runCorpusLint3127(t *testing.T, flag, scope string, lintResp string, extra ...string) (string, map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec","p1"]`) + `,` + specNodeList("msg:010:03", `["spec","p1"]`) + `]}}`,
		"NodeBatch": `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` +
			specBatchNode("msg:010:02") + `,` + specBatchNode("msg:010:03") + `]}}}`,
		"SpecLintBatch": lintResp,
		"Memories":      memListMicromentorJSON,
		"GetMemory":     memGetVectorEnabledJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := []string{"spec", "lint", flag}
	if scope != "" {
		args = append(args, scope)
	}
	root.SetArgs(append(append(args, "-m", specMem, "--server", gql.URL), extra...))
	err := root.Execute()
	return out.String(), captured, err
}

func corpusLintResp3127(unavailable string) string {
	return `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[` + unavailable + `],"nodes":[` +
		`{"id":"id-msg:010:02","loc":"msg:010:02","specLint":[{"citation":"msg:010:02","line":4,"message":"limits something, no reason","rule":"LIMIT_WITHOUT_REASON","sentence":"X is excluded.","severity":"WARNING"}]}` +
		// Every ref asked about comes back in nodes or unavailable (the nodeBatch
		// contract): the second spec is clean when nothing is named unavailable.
		cleanSecond3127(unavailable) + `]}}}`
}

func TestSpecLintCorpusAsksForEveryNodeAndNamesTheUnavailable(t *testing.T) {
	for _, c := range []struct{ flag, scope string }{{"--all", ""}, {"--prefix", "msg:010"}} {
		out, captured, err := runCorpusLint3127(t, c.flag, c.scope, corpusLintResp3127(`"id-msg:010:03"`), "--json")
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.flag, err, out)
		}
		var vars struct {
			Refs []string `json:"refs"`
		}
		_ = json.Unmarshal(captured["SpecLintBatch"], &vars)
		if len(vars.Refs) != 2 {
			t.Errorf("%s: the server must be asked about both specs by id, got %v", c.flag, vars.Refs)
		}
		if !strings.Contains(out, `"rule": "limit-without-reason"`) {
			t.Errorf("%s: relayed finding missing:\n%s", c.flag, out)
		}
		if !strings.Contains(out, `"rule": "spec-lint-not-read"`) || !strings.Contains(out, "msg:010:03") {
			t.Errorf("%s: a spec the server reports unavailable must be named, not dropped:\n%s", c.flag, out)
		}
		// A CLI-local finding keeps its shape: line and sentence only on server findings.
		var rep []map[string]any
		_ = json.Unmarshal([]byte(out), &rep)
		for _, r := range rep {
			if r["rule"] != "limit-without-reason" {
				if _, has := r["line"]; has {
					t.Errorf("%s: a non-server finding gained a line: %v", c.flag, r)
				}
				if _, has := r["sentence"]; has {
					t.Errorf("%s: a non-server finding gained a sentence: %v", c.flag, r)
				}
			}
		}
	}
}

func TestSpecLintTablePrintsLineAndSentence(t *testing.T) {
	out, _, err := runCorpusLint3127(t, "--all", "", corpusLintResp3127(""))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "limit-without-reason") || !strings.Contains(out, "line 4: limits something, no reason: X is excluded.") {
		t.Errorf("table must carry the line and the sentence:\n%s", out)
	}
}

func cleanSecond3127(unavailable string) string {
	if unavailable != "" {
		return ""
	}
	return `,{"id":"id-msg:010:03","loc":"msg:010:03","specLint":[]}`
}
