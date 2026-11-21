package cmd

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	gqlast "github.com/vektah/gqlparser/v2/ast"
	gqlparser "github.com/vektah/gqlparser/v2/parser"

	"github.com/hadron-memory/hadron-cli/internal/cmd/lintwarn"
)

func TestAdvisoryLintRetryHelp2227(t *testing.T) {
	for _, args := range [][]string{{"team", "worker", "get"}, {"team", "worker", "served-prompt"}, {"team", "session", "served-prompts"}, {"chat", "served-prompts"}, {"run", "served-prompt"}} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(args, "--help"))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			help := out.String()
			if !strings.Contains(help, "Run the command again to retry the advisory read.") || strings.Contains(help, "Retry if a page") || strings.Contains(help, "Retry only if") || strings.Contains(help, "only if a page") {
				t.Fatalf("cause-dependent or missing retry advice: %s", help)
			}
			if !strings.Contains(strings.ReplaceAll(help, "\n", " "), "cannot repair") {
				t.Fatal("help must retain recorded-revision limitation")
			}
		})
	}
}

func TestAdvisoryLintStderrDocs2227(t *testing.T) {
	b, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<class> [signature lint, prompt lint]", "first call's labels win", "fixed order", "`, ` separator", "node lint stays bare"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("diagnostic contract missing %q", want)
		}
	}
	b, err = os.ReadFile("../../docs/plans/worker-lint-2007.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "fixed stderr prefix") || !strings.Contains(string(b), "bracketed field-label suffix") {
		t.Fatal("plan must distinguish diagnostic prefix and suffix")
	}
}

func TestWorkerGetUnavailableLabels2227(t *testing.T) {
	for _, tc := range []struct {
		name              string
		signature, prompt bool
		labels            string
	}{{"signature-only", true, false, "signature lint"}, {"prompt-only", false, true, "prompt lint"}, {"both", true, true, "signature lint, prompt lint"}} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, stderr, err := runWorkerGet2007(t, visibleWorker2007, func(op string, v map[string]any) string {
				switch op {
				case "GetWorkerSignatureLint":
					if tc.signature {
						return sigLint2007("null")
					}
					return sigLint2007(lintPage2007(0, 0, "", false))
				case "GetWorkerPromptLint":
					if tc.prompt {
						return promptLint2007("rec9", 5, "null")
					}
					return promptLint2007("rec9", 5, lintPage2007(0, 0, "", true))
				}
				return ""
			}, "--json")
			if err != nil || stderr != lintwarn.UnavailableClass+" ["+tc.labels+"]\n" {
				t.Fatalf("err=%v stderr=%q", err, stderr)
			}
		})
	}
}

func TestWorkerUpdateUnavailableLabels2227(t *testing.T) {
	_, _, stderr, err := runWorkerUpdate2007(t, true, func(op string, v map[string]any) string {
		if op == "GetWorkerSignatureLint" {
			return updateLint2007("null")
		}
		return ""
	}, "--json")
	if err != nil || stderr != lintwarn.UnavailableClass+" [signature lint]\n" {
		t.Fatalf("err=%v stderr=%q", err, stderr)
	}
}

func TestServedPromptUnavailableLabels2227(t *testing.T) {
	_, _, stderr, err := runCLI2007(t, func(op string, v map[string]any) string {
		switch op {
		case "WorkerServedPrompt":
			return c3bWorkerRecord(record2007("rec1"))
		case "WorkerServedPromptLint":
			return single2007("worker", "rec1", "null")
		}
		return ""
	}, "team", "worker", "served-prompt", workerRef2007, "--json")
	if err != nil || stderr != lintwarn.UnavailableClass+" [current lint]\n" {
		t.Fatalf("err=%v stderr=%q", err, stderr)
	}
}

func TestServedHistoryUnavailableLabels2227(t *testing.T) {
	for _, surface := range []string{"session", "chat"} {
		t.Run(surface, func(t *testing.T) {
			args := []string{"team", "session", "served-prompts", "sess-1", "--offset", "3", "--limit", "2"}
			if surface == "chat" {
				args = []string{"chat", "served-prompts", "chat-1", "--offset", "3", "--limit", "2"}
			}
			h := historyHandler2007(surface, func() string {
				return historyLint2007(surface, 9, histItem2007("r1", "null"), histItem2007("r2", "null"))
			}, nil)
			_, _, stderr, err := runCLI2007(t, h, append(args, "--lint-warnings", "--json")...)
			if err != nil || stderr != lintwarn.UnavailableClass+" [current lint]\n" {
				t.Fatalf("err=%v stderr=%q", err, stderr)
			}
		})
	}
}

func TestWorkerLintBlockUnindented2227(t *testing.T) {
	_, out, stderr, err := runWorkerGet2007(t, visibleWorker2007, func(op string, v map[string]any) string {
		switch op {
		case "GetWorkerSignatureLint":
			return sigLint2007(lintPage2007(0, 0, "", false))
		case "GetWorkerPromptLint":
			return promptLint2007("", 0, "null")
		}
		return ""
	})
	if err != nil || stderr != "" {
		t.Fatalf("err=%v stderr=%q", err, stderr)
	}
	want := "\nLint (advisory):\nsignature lint: no warnings for the signature and trailer\nprompt lint: nothing served to this worker's open session\n"
	at := strings.Index(out, "\nLint (advisory):\n")
	if at < 0 || out[at:] != want {
		t.Fatalf("unindented block mismatch: %q", out)
	}
}

func TestWithoutLintAllowlistFreshness2227(t *testing.T) {
	b, err := os.ReadFile("../api/queries/worker_lint.graphql")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := gqlparser.ParseQuery(&gqlast.Source{Input: string(b)})
	if err != nil {
		t.Fatal(err)
	}
	var operations []string
	for _, op := range doc.Operations {
		if strings.HasSuffix(op.Name, "Lint") || strings.HasSuffix(op.Name, "LintAt") {
			operations = append(operations, op.Name)
		}
	}
	// Read the helper's actual switch, rather than repeating its allowlist here.
	f, err := goparser.ParseFile(token.NewFileSet(), "served_revision_c3b_test.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var filtered []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "withoutLint2007" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			c, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, item := range c.List {
				lit, ok := item.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				filtered = append(filtered, s)
			}
			return true
		})
	}
	sort.Strings(operations)
	sort.Strings(filtered)
	if len(operations) == 0 || !reflect.DeepEqual(operations, filtered) {
		t.Fatalf("worker_lint.graphql operations=%v; withoutLint2007=%v", operations, filtered)
	}
}
