package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/lintwarn"
)

// cli#2007: served-record lint (ServedPrompt.currentLint). Worker served-prompt
// is always-on; run, session and chat history are opt-in (--lint-warnings).

func runCLI2007(t *testing.T, h handler2007, argv ...string) (*lintFake2007, string, string, error) {
	t.Helper()
	teamGitDir(t)
	srv, fake := newLintFake2007(t, h)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(argv, "--server", srv.URL))
	err := root.Execute()
	return fake, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

const rec2007 = `"id":"%s","kind":"BIND","ordinal":1,"pointerLevel":"WORKER","compiledNodeId":"compiled1","compiledContentHash":"h","servedRevSeq":7,"provenanceRevSeq":5,"servedAt":"2026-10-28T09:00:00Z","variables":{},"pruned":false,"provenance":{"items":[],"total":0}`

func record2007(id string) string { return "{" + fmt.Sprintf(rec2007, id) + "}" }

func single2007(root, id string, page string) string {
	return fmt.Sprintf(`{"data":{%q:{"servedPrompt":{"id":%q,"provenanceRevSeq":5,"currentLint":%s}}}}`, root, id, page)
}

func currentLintOf2007(t *testing.T, out string) *lintwarn.Page {
	t.Helper()
	var env struct {
		ServedPrompt struct {
			CurrentLint *lintwarn.Page `json:"currentLint"`
		} `json:"servedPrompt"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return env.ServedPrompt.CurrentLint
}

func TestWorkerServedPromptLint2007AlwaysOnPagedAndSubjectNamed(t *testing.T) {
	cursors := map[string]string{"": "c1", "c1": "c2", "c2": ""}
	sizes := map[string]int{"": 20, "c1": 20, "c2": 5}
	h := func(op string, v map[string]any) string {
		switch op {
		case "WorkerServedPrompt":
			return c3bWorkerRecord(record2007("rec1"))
		case "WorkerServedPromptLint":
			a := after2007(v)
			return single2007("worker", "rec1", lintPage2007(45, sizes[a], cursors[a], true))
		}
		return ""
	}
	fake, out, errOut, err := runCLI2007(t, h, "team", "worker", "served-prompt", "hrn:worker:acme.com:iris", "--json")
	if err != nil || errOut != "" {
		t.Fatalf("err=%v stderr=%q", err, errOut)
	}
	if p := currentLintOf2007(t, out); p == nil || len(p.Items) != 45 || p.Total != 45 {
		t.Fatalf("every page to the end: %s", out)
	}
	cs := fake.ops("WorkerServedPromptLint")
	if len(cs) != 3 {
		t.Fatalf("3 lint pages, got %d", len(cs))
	}
	for _, c := range cs {
		if !strings.Contains(c.Query, "first: 20") || strings.Contains(c.Query, "first: 50") || strings.Contains(c.Query, "first: 100") {
			t.Errorf("first: 20 explicitly: %s", c.Query)
		}
	}
	if n := len(fake.ops("WorkerServedPrompt")); n != 1 {
		t.Errorf("the main read is unchanged and sent once, got %d", n)
	}
	_, text, _, _ := runCLI2007(t, h, "team", "worker", "served-prompt", "hrn:worker:acme.com:iris")
	for _, want := range []string{"current lint: 45 (the compiled revision recorded for this served prompt, provenance r5)", "BROKEN_INCLUDE occurrence #0: include 0 is broken"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "up to date") || strings.Contains(text, "no warnings") {
		t.Errorf("no claim beyond the record's revision:\n%s", text)
	}
}

func TestWorkerServedPromptLint2007NullAndMismatchAreUnavailable(t *testing.T) {
	for name, lintResp := range map[string]string{
		"null page":       single2007("worker", "rec1", "null"),
		"another record":  single2007("worker", "rec-OTHER", lintPage2007(1, 1, "", true)),
		"older server":    "",
		"record vanished": `{"data":{"worker":{"servedPrompt":null}}}`,
		"short drain":     single2007("worker", "rec1", lintPage2007(4, 1, "", true)),
		"total drift":     "",
	} {
		t.Run(name, func(t *testing.T) {
			_, out, errOut, err := runCLI2007(t, func(op string, v map[string]any) string {
				switch op {
				case "WorkerServedPrompt":
					return c3bWorkerRecord(record2007("rec1"))
				case "WorkerServedPromptLint":
					return lintResp
				}
				return ""
			}, "team", "worker", "served-prompt", "hrn:worker:acme.com:iris", "--json")
			if err != nil {
				t.Fatalf("lint never fails the read: %v", err)
			}
			if !strings.Contains(out, `"currentLint": null`) || strings.Contains(out, "include 0") {
				t.Errorf("null, never another record's lint: %s", out)
			}
			if strings.Count(errOut, lintwarn.UnavailableClass) != 1 {
				t.Errorf("one fixed class: %q", errOut)
			}
		})
	}
	// A page-to-page id change inside one drain.
	_, out, _, _ := runCLI2007(t, func(op string, v map[string]any) string {
		switch op {
		case "WorkerServedPrompt":
			return c3bWorkerRecord(record2007("rec1"))
		case "WorkerServedPromptLint":
			if after2007(v) == "" {
				return single2007("worker", "rec1", lintPage2007(2, 1, "c1", true))
			}
			return single2007("worker", "rec2", lintPage2007(2, 1, "", true))
		}
		return ""
	}, "team", "worker", "served-prompt", "hrn:worker:acme.com:iris", "--json")
	if !strings.Contains(out, `"currentLint": null`) {
		t.Errorf("a record that changes between lint pages is null: %s", out)
	}
	// No record: nothing to lint, no lint read.
	fake, out, _, _ := runCLI2007(t, func(op string, v map[string]any) string {
		if op == "WorkerServedPrompt" {
			return `{"data":{"worker":{"servedPrompt":null}}}`
		}
		return ""
	}, "team", "worker", "served-prompt", "hrn:worker:acme.com:iris", "--json")
	if len(fake.lintOps()) != 0 || !strings.Contains(out, `"servedPrompt": null`) {
		t.Errorf("no record, no lint read: %s %v", out, fake.lintOps())
	}
}

func TestRunServedPromptLint2007OptIn(t *testing.T) {
	h := func(op string, v map[string]any) string {
		switch op {
		case "RunServedPrompt":
			return `{"data":{"appRun":{"servedPrompt":` + record2007("rec1") + `}}}`
		case "RunServedPromptLint":
			return single2007("appRun", "rec1", lintPage2007(1, 1, "", true))
		}
		return ""
	}
	fake, out, _, err := runCLI2007(t, h, "run", "served-prompt", "run-1", "--json")
	if err != nil || len(fake.lintOps()) != 0 || strings.Contains(out, "currentLint") {
		t.Fatalf("without the flag no lint op and no key: err=%v ops=%v\n%s", err, fake.lintOps(), out)
	}
	fake, out, _, err = runCLI2007(t, h, "run", "served-prompt", "run-1", "--lint-warnings", "--json")
	if err != nil || len(fake.ops("RunServedPromptLint")) != 1 {
		t.Fatalf("err=%v ops=%v", err, fake.lintOps())
	}
	if p := currentLintOf2007(t, out); p == nil || p.Total != 1 {
		t.Errorf("currentLint present with the flag: %s", out)
	}
	_, _, _, err = runCLI2007(t, h, "team", "worker", "served-prompt", "hrn:worker:acme.com:iris", "--lint-warnings")
	if err == nil {
		t.Error("worker served-prompt is always-on: it has no --lint-warnings flag")
	}
}

// --- history ------------------------------------------------------------------

func historyLint2007(root string, total int, items ...string) string {
	body := fmt.Sprintf(`{"total":%d,"items":[%s]}`, total, strings.Join(items, ","))
	if root == "session" {
		return `{"data":{"session":{"servedPrompts":` + body + `}}}`
	}
	return `{"data":{"chatServedPrompts":` + body + `}}`
}

func histItem2007(id, page string) string {
	return fmt.Sprintf(`{"id":%q,"currentLint":%s}`, id, page)
}

type histJSON2007 struct {
	ServedPrompts struct {
		Items []struct {
			ID          string         `json:"id"`
			CurrentLint *lintwarn.Page `json:"currentLint"`
		} `json:"items"`
	} `json:"servedPrompts"`
}

func historyHandler2007(root string, lintFirst func() string, at func(v map[string]any) string) handler2007 {
	main, firstOp, atOp := "SessionServedPrompts", "SessionServedPromptsLint", "SessionServedPromptLintAt"
	if root == "chat" {
		main, firstOp, atOp = "ChatServedPrompts", "ChatServedPromptsLint", "ChatServedPromptLintAt"
	}
	return func(op string, v map[string]any) string {
		switch op {
		case main:
			items := "[" + record2007("r1") + "," + record2007("r2") + "]"
			if root == "chat" {
				return `{"data":{"chatServedPrompts":{"items":` + items + `,"total":9}}}`
			}
			return session1280Page(items, 9)
		case firstOp:
			return lintFirst()
		case atOp:
			return at(v)
		}
		return ""
	}
}

func TestHistoryLint2007WholeOrNullPerRecord(t *testing.T) {
	for _, root := range []string{"session", "chat"} {
		cmdArgs := []string{"team", "session", "served-prompts", "sess-1", "--offset", "3", "--limit", "2"}
		keyRoot := "session"
		if root == "chat" {
			cmdArgs = []string{"chat", "served-prompts", "chat-1", "--offset", "3", "--limit", "2"}
			keyRoot = "chat"
		}
		atOp := map[string]string{"session": "SessionServedPromptLintAt", "chat": "ChatServedPromptLintAt"}[root]
		t.Run(root+": inline first page, a second record drained by offset", func(t *testing.T) {
			h := historyHandler2007(keyRoot,
				func() string {
					return historyLint2007(keyRoot, 9, histItem2007("r1", lintPage2007(1, 1, "", true)), histItem2007("r2", lintPage2007(3, 2, "c1", true)))
				},
				func(v map[string]any) string {
					return historyLint2007(keyRoot, 9, histItem2007("r2", lintPage2007(3, 1, "", true)))
				})
			fake, out, errOut, err := runCLI2007(t, h, append(cmdArgs, "--lint-warnings", "--json")...)
			if err != nil || errOut != "" {
				t.Fatalf("err=%v stderr=%q", err, errOut)
			}
			var got histJSON2007
			_ = json.Unmarshal([]byte(out), &got)
			it := got.ServedPrompts.Items
			if len(it) != 2 || it[0].CurrentLint == nil || it[0].CurrentLint.Total != 1 || it[1].CurrentLint == nil || len(it[1].CurrentLint.Items) != 3 {
				t.Fatalf("each record whole: %s", out)
			}
			at := fake.ops(atOp)
			if len(at) != 1 || at[0].Vars["offset"] != float64(3+1) || after2007(at[0].Vars) != "c1" {
				t.Errorf("the second record is re-read alone at shownOffset+1 after its cursor: %+v", at)
			}
			if !strings.Contains(at[0].Query, "first: 20") || !strings.Contains(at[0].Query, "limit: 1") {
				t.Errorf("one record, 20 warnings: %s", at[0].Query)
			}
			if first := fake.ops(map[string]string{"session": "SessionServedPromptsLint", "chat": "ChatServedPromptsLint"}[root]); len(first) != 1 || first[0].Vars["limit"] != float64(2) || first[0].Vars["offset"] != float64(3) {
				t.Errorf("the lint read uses the shown page's own limit and offset: %+v", first)
			}
		})
		t.Run(root+": a record at another position is null, its neighbour is unaffected", func(t *testing.T) {
			h := historyHandler2007(keyRoot, func() string {
				return historyLint2007(keyRoot, 9, histItem2007("r1", lintPage2007(1, 1, "", true)), histItem2007("OTHER", lintPage2007(1, 1, "", true)))
			}, nil)
			_, out, errOut, err := runCLI2007(t, h, append(cmdArgs, "--lint-warnings", "--json")...)
			var got histJSON2007
			_ = json.Unmarshal([]byte(out), &got)
			it := got.ServedPrompts.Items
			if err != nil || len(it) != 2 || it[0].CurrentLint == nil || it[1].CurrentLint != nil || !strings.Contains(out, `"currentLint": null`) || strings.Count(errOut, lintwarn.UnavailableClass) != 1 {
				t.Fatalf("only the mismatched record is null: err=%v stderr=%q\n%s", err, errOut, out)
			}
		})
		t.Run(root+": a history total that shifted makes every record null", func(t *testing.T) {
			h := historyHandler2007(keyRoot, func() string {
				return historyLint2007(keyRoot, 10, histItem2007("r1", lintPage2007(1, 1, "", true)), histItem2007("r2", lintPage2007(1, 1, "", true)))
			}, nil)
			_, out, _, _ := runCLI2007(t, h, append(cmdArgs, "--lint-warnings", "--json")...)
			var got histJSON2007
			_ = json.Unmarshal([]byte(out), &got)
			for _, r := range got.ServedPrompts.Items {
				if r.CurrentLint != nil {
					t.Errorf("%s must be null after a total shift: %s", r.ID, out)
				}
			}
		})
		t.Run(root+": the re-read returns another record or a shifted total", func(t *testing.T) {
			for name, at := range map[string]func(map[string]any) string{
				"other id": func(map[string]any) string {
					return historyLint2007(keyRoot, 9, histItem2007("rX", lintPage2007(3, 1, "", true)))
				},
				"history total": func(map[string]any) string {
					return historyLint2007(keyRoot, 10, histItem2007("r2", lintPage2007(3, 1, "", true)))
				},
			} {
				h := historyHandler2007(keyRoot, func() string {
					return historyLint2007(keyRoot, 9, histItem2007("r1", lintPage2007(1, 1, "", true)), histItem2007("r2", lintPage2007(3, 2, "c1", true)))
				}, at)
				_, out, _, _ := runCLI2007(t, h, append(cmdArgs, "--lint-warnings", "--json")...)
				var got histJSON2007
				_ = json.Unmarshal([]byte(out), &got)
				it := got.ServedPrompts.Items
				if len(it) != 2 || it[0].CurrentLint == nil || it[1].CurrentLint != nil {
					t.Errorf("%s: r1 keeps its page, r2 is null, never partial: %s", name, out)
				}
			}
		})
		t.Run(root+": without the flag no lint op and no key", func(t *testing.T) {
			h := historyHandler2007(keyRoot, func() string { return "" }, nil)
			fake, out, _, err := runCLI2007(t, h, append(cmdArgs, "--json")...)
			if err != nil || len(fake.lintOps()) != 0 || strings.Contains(out, "currentLint") {
				t.Fatalf("err=%v ops=%v\n%s", err, fake.lintOps(), out)
			}
		})
	}
}
