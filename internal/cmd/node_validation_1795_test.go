package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// #1795: pins and wording for `node validation record` after the server
// projection (6a90a3b8, #1706): an explicit --task never overrides a refusal of
// the task SET, and the exit-2 / exit-5 split is spelled out.

const ready1795 = `{"state":"READY","tasks":[{"broken":false,"taskId":"t1","taskRef":"hrn:node:example.com:kb:one","revision":3}]}`

// recordServer1795 answers the discovery read with set and the record mutation
// with recordReply; it reports how many writes arrived.
func recordServer1795(t *testing.T, set, recordReply string) (*httptest.Server, *int) {
	t.Helper()
	writes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			OperationName string
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		w.Header().Set("Content-Type", "application/json")
		switch q.OperationName {
		case "ContentValidationNode":
			fmt.Fprintf(w, `{"data":{"node":{"id":"n1","revision":7,"memoryId":"m1","validationTasks":%s}}}`, set)
		case "RecordContentValidation":
			writes++
			fmt.Fprint(w, recordReply)
		default:
			t.Errorf("unexpected operation %s", q.OperationName)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &writes
}

func record1795(t *testing.T, srv *httptest.Server, extra ...string) error {
	t.Helper()
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"node", "validation", "record", "01a1b494d4b471ac80e3b1f63080b211", "--outcome", "PASS", "--server", srv.URL}, extra...))
	return root.Execute()
}

// An explicit --task does not change what the set itself says: an empty
// effective set is still a "not in the set" usage error (exit 2) once --task is
// named, a BROKEN set still refuses (exit 5) and an UNAVAILABLE one (exit 8),
// each before any write.
func TestValidationRecordTaskChoiceNeverOverridesTheSetRefusal1795(t *testing.T) {
	for name, tc := range map[string]struct {
		set  string
		want int
	}{
		"not configured + --task": {`{"state":"NOT_CONFIGURED","tasks":[]}`, 2},
		"broken + --task":         {`{"state":"BROKEN","tasks":[{"broken":true,"taskId":null,"taskRef":null,"revision":null}]}`, 5},
		"unavailable + --task":    {`{"state":"UNAVAILABLE","tasks":[]}`, 8},
	} {
		t.Run(name, func(t *testing.T) {
			srv, writes := recordServer1795(t, tc.set, `{"data":{}}`)
			err := record1795(t, srv, "--task", "t1")
			if exitCodeFor(err) != tc.want || *writes != 0 {
				t.Fatalf("exit=%d want=%d writes=%d err=%v", exitCodeFor(err), tc.want, *writes, err)
			}
		})
	}
}

// The server's refusal codes on the record mutation map to the exit the help
// documents: invalid revision or wrong task is exit 2; a node that changed
// between read and write, a run-id conflict, no or a broken task are exit 5.
func TestValidationRecordRefusalExits1795(t *testing.T) {
	for code, want := range map[string]int{
		"CONTENT_VALIDATION_NODE_REVISION_INVALID": 2,
		"CONTENT_VALIDATION_TASK_REVISION_INVALID": 2,
		"CONTENT_VALIDATION_WRONG_TASK":            2,
		"CONFLICT":                                 5,
		"CONTENT_VALIDATION_RUN_ID_CONFLICT":       5,
	} {
		t.Run(code, func(t *testing.T) {
			srv, writes := recordServer1795(t, ready1795, fmt.Sprintf(`{"errors":[{"message":"refused","extensions":{"code":%q}}]}`, code))
			err := record1795(t, srv)
			if exitCodeFor(err) != want || *writes != 1 {
				t.Fatalf("exit=%d want=%d writes=%d err=%v", exitCodeFor(err), want, *writes, err)
			}
		})
	}
}

// The help and the embedded agent guide say the same exit-2 / exit-5 split.
func TestValidationRecordExitSplitIsDocumented1795(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "validation", "record", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	guide, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	const want = "Exit 2: the input or the choice is invalid (an unknown --outcome, a --task outside the listed set, an invalid revision or a wrong task)"
	const want5 = "Exit 5: no task is configured or the task is broken, the node changed between the read and the write, or the --run-id conflicts"
	for name, text := range map[string]string{"help": flat(out.String()), "agentic-usage": flat(string(guide))} {
		for _, w := range []string{want, want5} {
			if !strings.Contains(text, w) {
				t.Errorf("%s lacks %q", name, w)
			}
		}
	}
}

// AuthorCheck is a pointer to a pointer on purpose; its declaration says why.
func TestAuthorCheckDeclarationExplainsTheThreeStates1795(t *testing.T) {
	b, err := os.ReadFile("node/stamps.go")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\n\t// ", " ")
	for _, w := range []string{"AuthorCheck is a pointer to a pointer", "omitted", "explicit null", "a value"} {
		if !strings.Contains(src, w) {
			t.Errorf("the AuthorCheck declaration comment lacks %q", w)
		}
	}
}
