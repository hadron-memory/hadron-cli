package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func dispositionResponse1556(code string, ref any) string {
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"disposeAgentTaskFinding": map[string]any{
		"id": "task1", "findingDisposition": map[string]any{"findingId": "F1", "disposition": code, "reference": ref},
	}}})
	return string(b)
}

func TestFindingDispositions1556Write(t *testing.T) {
	for _, code := range []string{"fixed", "accepted", "deferred", "rejected"} {
		t.Run(code, func(t *testing.T) {
			r := taskResponses()
			r["AgentTaskDisposeFinding"] = dispositionResponse1556(code, nil)
			calls, out, err := runTeamTask(t, r, "team", "task", "dispose-finding", "AT1.2", "F1", "--disposition", code, "--json")
			if err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 1 || (*calls)[0].Op != "AgentTaskDisposeFinding" {
				t.Fatalf("calls=%v", *calls)
			}
			v := (*calls)[0].Vars
			if v["taskRef"] != "AT1.2" || v["findingId"] != "F1" || v["disposition"] != code {
				t.Fatalf("vars=%v", v)
			}
			if _, ok := v["reference"]; ok {
				t.Fatal("omitted reference sent")
			}
			var compact bytes.Buffer
			if e := json.Compact(&compact, []byte(out)); e != nil {
				t.Fatal(e)
			}
			if compact.String()+"\n" != fmt.Sprintf("{\"taskId\":\"task1\",\"findingId\":\"F1\",\"disposition\":%q,\"reference\":null}\n", code) {
				t.Fatalf("receipt=%q", out)
			}
		})
	}
}

func TestFindingDispositions1556Read(t *testing.T) {
	r := taskResponses()
	r["AgentTaskFindingDispositions"] = `{"data":{"agentTask":{"id":"task1","findingDispositions":{"items":[],"total":12,"hasMore":true}}}}`
	calls, out, err := runTeamTask(t, r, "team", "task", "finding-dispositions", "AT1.2", "--limit", "2", "--offset", "4", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if e := json.Compact(&compact, []byte(out)); e != nil {
		t.Fatal(e)
	}
	if compact.String() != "{\"items\":[],\"total\":12,\"hasMore\":true}" {
		t.Fatalf("page=%q", out)
	}
	if len(*calls) != 1 || (*calls)[0].Vars["limit"] != float64(2) || (*calls)[0].Vars["offset"] != float64(4) {
		t.Fatalf("calls=%v", *calls)
	}
}

func TestFindingDispositions1556Unresolved(t *testing.T) {
	for _, val := range []string{"true", "false"} {
		r := taskResponses()
		r["AgentTaskListUnresolved"] = taskPage2734("", 0, false)
		calls, out, err := runTeamTask(t, r, "team", "task", "list", "--unresolved="+val, "--json")
		if err != nil {
			t.Fatal(err)
		}
		var compact bytes.Buffer
		if e := json.Compact(&compact, []byte(out)); e != nil {
			t.Fatal(e)
		}
		if compact.String() != "{\"tasks\":[],\"total\":0,\"hasMore\":false}" {
			t.Fatalf("out=%q", out)
		}
		found := false
		for _, c := range *calls {
			if c.Op == "AgentTaskListUnresolved" {
				found = true
				if c.Vars["unresolvedFindings"] != (val == "true") {
					t.Fatalf("vars=%v", c.Vars)
				}
			}
		}
		if !found {
			t.Fatalf("calls=%v", *calls)
		}
	}
}

func TestFindingDispositions1556Refusals(t *testing.T) {
	for code, want := range map[string]int{"TASK_NOT_OWNER": 8, "TASK_NOT_FINISHED": 5, "TASK_FINDING_NOT_FOUND": 4} {
		t.Run(code, func(t *testing.T) {
			r := taskResponses()
			r["AgentTaskDisposeFinding"] = fmt.Sprintf(`{"errors":[{"message":"Refused","extensions":{"code":%q}}]}`, code)
			calls, out, err := runTeamTask(t, r, "team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed")
			if exitCodeFor(err) != want || len(*calls) != 1 || out != "" || !strings.Contains(err.Error(), code) {
				t.Fatalf("err=%v out=%q calls=%v", err, out, *calls)
			}
		})
	}
}

func TestFindingDispositions1556ReferenceAndServerReceipt(t *testing.T) {
	label := "danger\x1b]52;c;secret\a\nnext"
	ref := map[string]any{"kind": "URL", "target": "https://example.invalid/ref\x1b]52;c;target\a\tX", "label": label}
	for _, jsonMode := range []bool{true, false} {
		r := taskResponses()
		r["AgentTaskDisposeFinding"] = dispositionResponse1556("accepted", ref)
		args := []string{"team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--ref", "url=https://example.invalid/input|raw\tlabel"}
		if jsonMode {
			args = append(args, "--json")
		}
		calls, out, err := runTeamTask(t, r, args...)
		if err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 1 {
			t.Fatalf("unexpected fetch/retry: %v", *calls)
		}
		v := (*calls)[0].Vars["reference"].(map[string]any)
		if v["kind"] != "URL" || v["target"] != "https://example.invalid/input" || v["label"] != "raw\tlabel" {
			t.Fatalf("ref=%v", v)
		}
		if jsonMode {
			var got map[string]any
			if e := json.Unmarshal([]byte(out), &got); e != nil {
				t.Fatal(e)
			}
			if got["disposition"] != "accepted" || got["reference"].(map[string]any)["label"] != label {
				t.Fatalf("server receipt lost: %s", out)
			}
		} else if strings.ContainsAny(out, "\x1b\a") || !strings.Contains(out, "accepted") || strings.Contains(out, "fixed") {
			t.Fatalf("unsafe/guessed receipt %q", out)
		}
	}
	r := taskResponses()
	r["AgentTaskDisposeFinding"] = dispositionResponse1556("fixed", nil)
	calls, _, err := runTeamTask(t, r, "team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--ref", "issue=https://example.invalid/1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := (*calls)[0].Vars["reference"].(map[string]any)["label"]; ok {
		t.Fatal("unset label not omitted")
	}
}

func TestFindingDispositions1556BadArgsAndMissingReadback(t *testing.T) {
	for _, args := range [][]string{
		{"dispose-finding", "task1", "F1"},
		{"dispose-finding", "task1", "F1", "--disposition", ""},
		{"dispose-finding", "task1", "F1", "--disposition", "unknown"},
		{"dispose-finding", "task1", " ", "--disposition", "fixed"},
		{"dispose-finding", "task1", "F1", "--disposition", "fixed", "--ref", ""},
		{"finding-dispositions", "task1", "--limit", "201"},
		{"finding-dispositions", "task1", "--offset", "-1"},
		{"list", "--team", "--unresolved"},
		{"list", "--team=worker:Iris", "--unresolved=false"},
	} {
		calls, _, err := runTeamTask(t, taskResponses(), append([]string{"team", "task"}, args...)...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Errorf("args=%v err=%v calls=%v", args, err, *calls)
		}
	}
	for _, body := range []string{`{"data":{"disposeAgentTaskFinding":null}}`, `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDispositions":null}}}`, `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDispositions":{"items":[],"total":0,"hasMore":false}}}}`} {
		r := taskResponses()
		r["AgentTaskDisposeFinding"] = body
		calls, out, err := runTeamTask(t, r, "team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--json")
		if exitCodeFor(err) != 1 || out != "" || len(*calls) != 1 {
			t.Fatalf("fabricated success/retry: %v %q %v", err, out, *calls)
		}
	}
}

func TestFindingDispositions1556ReadOrderAndNull(t *testing.T) {
	r := taskResponses()
	r["AgentTaskFindingDispositions"] = `{"data":{"agentTask":{"id":"task1","findingDispositions":{"items":[{"findingId":"F2","disposition":"deferred","reference":null},{"findingId":"F1","disposition":"fixed","reference":null}],"total":7,"hasMore":true}}}}`
	_, out, err := runTeamTask(t, r, "team", "task", "finding-dispositions", "task1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Items []struct {
			FindingID string `json:"findingId"`
		}
		Total   int
		HasMore bool
	}
	if err = json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].FindingID != "F2" || page.Total != 7 || !page.HasMore {
		t.Fatalf("page changed %s", out)
	}
	r["AgentTaskFindingDispositions"] = `{"data":{"agentTask":null}}`
	_, out, err = runTeamTask(t, r, "team", "task", "finding-dispositions", "task1")
	if exitCodeFor(err) != 4 || out != "" {
		t.Fatalf("missing task %v %q", err, out)
	}
	r["AgentTaskFindingDispositions"] = `{"data":{"agentTask":{"id":"task1","findingDispositions":null}}}`
	_, out, err = runTeamTask(t, r, "team", "task", "finding-dispositions", "task1")
	if exitCodeFor(err) != 1 || out != "" {
		t.Fatalf("null page %v %q", err, out)
	}
}

func TestFindingDispositions1556OmittedFilterKeepsLegacy(t *testing.T) {
	for _, args := range [][]string{{"list", "--json"}, {"list", "--match-failed", "--json"}} {
		r := taskResponses()
		r["AgentTaskListFiltered"] = taskPage2734("", 0, false)
		calls, _, err := runTeamTask(t, r, append([]string{"team", "task"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range *calls {
			if strings.Contains(c.Query, "unresolvedFindings") {
				t.Fatal("omitted flag raised old document floor")
			}
			if _, ok := c.Vars["unresolvedFindings"]; ok {
				t.Fatal("omitted filter sent")
			}
		}
	}
}

func TestFindingDispositions1556RealErrorSink(t *testing.T) {
	for code, want := range map[string]int{"TASK_NOT_OWNER": 8, "TASK_NOT_FINISHED": 5, "TASK_FINDING_NOT_FOUND": 4} {
		for _, jsonMode := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/%t", code, jsonMode), func(t *testing.T) {
				writeTeamBinding(t)
				r := taskResponses()
				r["AgentTaskDisposeFinding"] = fmt.Sprintf(`{"errors":[{"message":"refused hostile\u001b[31m","extensions":{"code":%q}}]}`, code)
				srv, calls := attnServer(t, r)
				setTeamBindingServer(t, srv.URL)
				f, out := testFactory(t)
				stderr := captureErrOut(f)
				args := []string{"team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--server", srv.URL}
				if jsonMode {
					args = append(args, "--json")
				}
				root := NewRootCmd(f)
				root.SetArgs(args)
				err := root.Execute()
				renderFailure(f, args, err)
				if exitCodeFor(err) != want || len(*calls) != 1 {
					t.Fatalf("err=%v calls=%v", err, *calls)
				}
				if jsonMode {
					var env struct {
						Error struct {
							Code       int    `json:"code"`
							ServerCode string `json:"serverCode"`
						}
					}
					if e := json.Unmarshal([]byte(out.String()), &env); e != nil {
						t.Fatal(e)
					}
					if env.Error.Code != want || env.Error.ServerCode != code {
						t.Fatalf("envelope=%s", out.String())
					}
				} else if out.Len() != 0 || !strings.Contains(stderr(), code) || strings.Contains(stderr(), "\x1b") {
					t.Fatalf("human=%q stdout=%q", stderr(), out.String())
				}
			})
		}
	}
}

func TestFindingDispositions1556CountOnlyAndCompoundFilter(t *testing.T) {
	r := taskResponses()
	r["AgentTaskFindingDispositions"] = `{"data":{"agentTask":{"id":"task1","findingDispositions":{"items":[],"total":100,"hasMore":true}}}}`
	calls, _, err := runTeamTask(t, r, "team", "task", "finding-dispositions", "task1", "--limit", "0", "--offset", "3")
	if err != nil || len(*calls) != 1 || (*calls)[0].Vars["limit"] != float64(0) {
		t.Fatalf("err=%v calls=%v", err, *calls)
	}
	r["AgentTaskListUnresolved"] = taskPage2734("", 0, false)
	calls, _, err = runTeamTask(t, r, "team", "task", "list", "--unresolved=false", "--status", "FINISHED", "--owner", "user:alex", "--text", "raw text", "--limit", "2", "--offset", "4", "--json")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range *calls {
		if c.Op == "AgentTaskListUnresolved" {
			found = true
			if c.Vars["unresolvedFindings"] != false || c.Vars["text"] != "raw text" || c.Vars["offset"] != float64(4) || c.Vars["owner"].(map[string]any)["user"] != "alex" {
				t.Fatalf("vars=%v", c.Vars)
			}
		}
	}
	if !found {
		t.Fatal("no unresolved read")
	}
}

func TestFindingDispositions1556WriterFailureDoesNotRetry(t *testing.T) {
	for _, jsonMode := range []bool{true, false} {
		writeTeamBinding(t)
		r := taskResponses()
		r["AgentTaskDisposeFinding"] = dispositionResponse1556("fixed", nil)
		srv, calls := attnServer(t, r)
		setTeamBindingServer(t, srv.URL)
		f, _ := testFactory(t)
		f.IOStreams.Out = failingWriter{}
		root := NewRootCmd(f)
		args := []string{"team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--server", srv.URL}
		if jsonMode {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		if err := root.Execute(); err == nil || len(*calls) != 1 {
			t.Fatalf("err=%v calls=%v", err, *calls)
		}
	}
}

func TestFindingDispositions1556OperationDocuments(t *testing.T) {
	for _, doc := range []string{gen.AgentTaskList_Operation, gen.AgentTaskListFiltered_Operation} {
		if strings.Contains(doc, "unresolvedFindings") {
			t.Fatal("legacy document gained new argument")
		}
	}
	if !strings.Contains(gen.AgentTaskListUnresolved_Operation, "unresolvedFindings: $unresolvedFindings") {
		t.Fatal("filter variable is not used in document")
	}
	if strings.Contains(gen.AgentTaskDisposeFinding_Operation, "result {") || strings.Contains(gen.AgentTaskFindingDispositions_Operation, "result {") {
		t.Fatal("must not fetch immutable result text")
	}
	for name, doc := range map[string]string{"plain": gen.AgentTaskDisposeFinding_Operation, "instructed": gen.AgentTaskDisposeFindingInstructed_Operation} {
		// cli#1709: parse the document: the mutation's result selects id and
		// exactly the one targeted field, bound to the write's own $findingId
		// (and never the paged field).
		q, perr := parser.ParseQuery(&ast.Source{Input: doc})
		if perr != nil || len(q.Operations) != 1 {
			t.Fatalf("%s: unparsable document: %v", name, perr)
		}
		var write *ast.Field
		for _, sel := range q.Operations[0].SelectionSet {
			if f, ok := sel.(*ast.Field); ok && f.Name == "disposeAgentTaskFinding" {
				write = f
			}
		}
		if write == nil {
			t.Fatalf("%s: no disposeAgentTaskFinding selection", name)
		}
		var sharesVar bool
		for _, arg := range write.Arguments {
			if arg.Name == "findingId" && arg.Value.Kind == ast.Variable && arg.Value.Raw == "findingId" {
				sharesVar = true
			}
		}
		if !sharesVar {
			t.Fatalf("%s: the write's findingId argument must be the $findingId variable the readback shares", name)
		}
		var names []string
		var targeted *ast.Field
		for _, sel := range write.SelectionSet {
			if f, ok := sel.(*ast.Field); ok {
				names = append(names, f.Name)
				if f.Name == "findingDisposition" {
					targeted = f
				}
			}
		}
		if !reflect.DeepEqual(names, []string{"id", "findingDisposition"}) || targeted == nil {
			t.Fatalf("%s: the write's result must select exactly id and findingDisposition, got %v", name, names)
		}
		if len(targeted.Arguments) != 1 || targeted.Arguments[0].Name != "findingId" || targeted.Arguments[0].Value.Kind != ast.Variable || targeted.Arguments[0].Value.Raw != "findingId" {
			t.Fatalf("%s: findingDisposition must be bound to $findingId", name)
		}
		if strings.Contains(doc, "result {") {
			t.Fatalf("%s: must not fetch immutable result text", name)
		}
		// The readback's projection is the shared fragment: the targeted field
		// spreads it, and it selects the finding id, code and reference.
		var spread bool
		for _, sel := range targeted.SelectionSet {
			if fs, ok := sel.(*ast.FragmentSpread); ok && fs.Name == "TaskFindingDispositionFields" {
				spread = true
			}
		}
		frag := q.Fragments.ForName("TaskFindingDispositionFields")
		if !spread || frag == nil {
			t.Fatalf("%s: the targeted field must spread TaskFindingDispositionFields (defined in the document)", name)
		}
		var fnames []string
		for _, sel := range frag.SelectionSet {
			if f, ok := sel.(*ast.Field); ok {
				fnames = append(fnames, f.Name)
			}
		}
		if !reflect.DeepEqual(fnames, []string{"findingId", "disposition", "reference"}) {
			t.Fatalf("%s: fragment fields %v", name, fnames)
		}
	}
}

// cli#1709: the readback is one targeted field. A response that carries only
// the targeted disposition of F100 (a page scan over 100 findings would never
// have found it in a 200-item window it was not sent) is enough; exactly one
// request goes out and the operation never asks for the paged list.
func TestFindingDispositions1709TargetedReadback(t *testing.T) {
	r := taskResponses()
	r["AgentTaskDisposeFinding"] = `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":{"findingId":"F100","disposition":"deferred","reference":{"kind":"ISSUE","target":"https://example.invalid/9","label":null}}}}}`
	calls, out, err := runTeamTask(t, r, "team", "task", "dispose-finding", "task1", "F100", "--disposition", "fixed", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].Op != "AgentTaskDisposeFinding" {
		t.Fatalf("exactly one mutation, no follow-up read: %v", *calls)
	}
	var got map[string]any
	if json.Unmarshal([]byte(out), &got) != nil || len(got) != 4 || got["findingId"] != "F100" || got["taskId"] != "task1" {
		t.Fatalf("receipt: %s", out)
	}
	// The server's value, not the submitted one (a later replacement is shown), reference kept.
	gotRef, isMap := got["reference"].(map[string]any)
	if got["disposition"] != "deferred" || !isMap || gotRef["target"] != "https://example.invalid/9" {
		t.Fatalf("must print the server's value: %s", out)
	}
	q := (*calls)[0].Query
	if !strings.Contains(q, "findingDisposition(findingId: $findingId)") || strings.Contains(q, "findingDispositions") {
		t.Fatalf("outgoing document is not the targeted readback: %s", q)
	}
}

// Every unusable readback is the same unconfirmed-write failure: exit 1, no
// success output, one request (no retry), and guidance that does not claim the
// write failed.
func TestFindingDispositions1709UnusableReadback(t *testing.T) {
	ok := `{"findingId":"F1","disposition":"fixed","reference":null}`
	for name, body := range map[string]string{
		"null mutation":        `{"data":{"disposeAgentTaskFinding":null}}`,
		"omitted target":       `{"data":{"disposeAgentTaskFinding":{"id":"task1"}}}`,
		"null target":          `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":null}}}`,
		"empty task id":        `{"data":{"disposeAgentTaskFinding":{"id":"","findingDisposition":` + ok + `}}}`,
		"wrong finding id":     `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":{"findingId":"F2","disposition":"fixed","reference":null}}}}`,
		"case-folded id":       `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":{"findingId":"f1","disposition":"fixed","reference":null}}}}`,
		"empty disposition":    `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":{"findingId":"F1","disposition":"","reference":null}}}}`,
		"unknown disposition":  `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":{"findingId":"F1","disposition":"wontfix","reference":null}}}}`,
		"reference no target":  `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":{"findingId":"F1","disposition":"fixed","reference":{"kind":"ISSUE","target":"","label":null}}}}}`,
		"wrong field type":     `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":{"findingId":7,"disposition":"fixed","reference":null}}}}`,
		"disposition not text": `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":{"findingId":"F1","disposition":["fixed"],"reference":null}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := taskResponses()
			r["AgentTaskDisposeFinding"] = body
			calls, out, err := runTeamTask(t, r, "team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--json")
			if err == nil || out != "" || len(*calls) != 1 {
				t.Fatalf("must fail once with no success output: err=%v out=%q calls=%d", err, out, len(*calls))
			}
			if exitCodeFor(err) != 1 {
				t.Fatalf("exit %d, want 1: %v", exitCodeFor(err), err)
			}
			if !strings.Contains(err.Error(), "may have been recorded") || !strings.Contains(err.Error(), "read finding-dispositions before retrying") {
				t.Fatalf("recovery guidance missing or claims failure: %v", err)
			}
		})
	}
}

// A provider without the field refuses the document (a typed GraphQL
// validation error): that is not an unconfirmed write, the selection is not
// dropped, and nothing is sent twice.
func TestFindingDispositions1709ProviderWithoutField(t *testing.T) {
	r := taskResponses()
	r["AgentTaskDisposeFinding"] = `{"errors":[{"message":"Cannot query field \"findingDisposition\" on type \"AgentTask\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	calls, out, err := runTeamTask(t, r, "team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed")
	if err == nil || out != "" || len(*calls) != 1 {
		t.Fatalf("err=%v out=%q calls=%d", err, out, len(*calls))
	}
	if strings.Contains(err.Error(), "may have been recorded") {
		t.Fatalf("a refused document is not an unconfirmed write: %v", err)
	}
}

// The submitted and returned values vary independently, for all four codes and
// with/without a reference: the receipt is always the returned one.
func TestFindingDispositions1709ReturnedNotSubmitted(t *testing.T) {
	for _, ret := range []string{"fixed", "accepted", "deferred", "rejected"} {
		for _, withRef := range []bool{false, true} {
			var ref any
			if withRef {
				ref = map[string]any{"kind": "NODE", "target": "hrn:node:x", "label": nil}
			}
			r := taskResponses()
			r["AgentTaskDisposeFinding"] = dispositionResponse1556(ret, ref)
			_, out, err := runTeamTask(t, r, "team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--ref", "issue=https://example.invalid/1", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if json.Unmarshal([]byte(out), &got) != nil || got["disposition"] != ret {
				t.Fatalf("returned %s must win: %s", ret, out)
			}
			if _, has := got["reference"]; !has || (got["reference"] == nil) == withRef {
				t.Fatalf("reference must follow the server (none stays an explicit null): %s", out)
			}
		}
	}
}

// cli#1709 (blind round 1): the readback field can fail after the write
// committed. A GraphQL error whose path runs through findingDisposition is an
// unconfirmed write (recovery guidance, one request); one on the write field
// itself, or a pre-execution validation refusal, keeps its typed mapping and
// is never described as "may have been recorded".
func TestFindingDispositions1709ReadbackFieldError(t *testing.T) {
	partial := `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":null}},"errors":[{"message":"boom","path":["disposeAgentTaskFinding","findingDisposition"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
	nested := `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":null}},"errors":[{"message":"boom","path":["disposeAgentTaskFinding","findingDisposition","reference","target"]}]}`
	for name, tc := range map[string]struct {
		body        string
		unconfirmed bool
		wantExit    int
	}{
		"readback field error":                 {partial, true, 1},
		"readback nested error":                {nested, true, 1},
		"write field refusal":                  {`{"data":{"disposeAgentTaskFinding":null},"errors":[{"message":"Refused","path":["disposeAgentTaskFinding"],"extensions":{"code":"TASK_NOT_OWNER"}}]}`, false, 8},
		"pathless refusal":                     {`{"errors":[{"message":"Refused","extensions":{"code":"TASK_NOT_FINISHED"}}]}`, false, 5},
		"id field error":                       {`{"data":{"disposeAgentTaskFinding":null},"errors":[{"message":"boom","path":["disposeAgentTaskFinding","id"]}]}`, true, 1},
		"root generic plus readback":           {`{"data":null,"errors":[{"message":"boom","path":["disposeAgentTaskFinding"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}},{"message":"a","path":["disposeAgentTaskFinding","findingDisposition"]}]}`, true, 1},
		"readback plus root generic":           {`{"data":null,"errors":[{"message":"a","path":["disposeAgentTaskFinding","findingDisposition"]},{"message":"boom","path":["disposeAgentTaskFinding"]}]}`, true, 1},
		"root generic alone":                   {`{"data":null,"errors":[{"message":"boom","path":["disposeAgentTaskFinding"]}]}`, true, 1},
		"root typed plus readback":             {`{"data":null,"errors":[{"message":"Refused","path":["disposeAgentTaskFinding"],"extensions":{"code":"TASK_NOT_OWNER"}},{"message":"a","path":["disposeAgentTaskFinding","findingDisposition"]}]}`, false, 8},
		"foreign root path":                    {`{"data":{"disposeAgentTaskFinding":null},"errors":[{"message":"boom","path":["somethingElse","x"],"extensions":{"code":"TASK_NOT_FINISHED"}}]}`, false, 5},
		"readback plus pathless":               {`{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":null}},"errors":[{"message":"a","path":["disposeAgentTaskFinding","findingDisposition"]},{"message":"other"}]}`, true, 1},
		"readback plus foreign":                {`{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":null}},"errors":[{"message":"x","path":["somethingElse"]},{"message":"a","path":["disposeAgentTaskFinding","id"]}]}`, true, 1},
		"readback plus write":                  {`{"data":null,"errors":[{"message":"a","path":["disposeAgentTaskFinding","findingDisposition"]},{"message":"Refused","path":["disposeAgentTaskFinding"],"extensions":{"code":"TASK_NOT_OWNER"}}]}`, false, 8},
		"write plus readback (reverse order)":  {`{"data":null,"errors":[{"message":"Refused","path":["disposeAgentTaskFinding"],"extensions":{"code":"TASK_NOT_OWNER"}},{"message":"a","path":["disposeAgentTaskFinding","findingDisposition"]}]}`, false, 8},
		"pathless typed refusal plus readback": {`{"data":null,"errors":[{"message":"a","path":["disposeAgentTaskFinding","id"]},{"message":"Refused","extensions":{"code":"TASK_NOT_FINISHED"}}]}`, true, 1},
		"pathless typed refusal alone":         {`{"errors":[{"message":"Refused","extensions":{"code":"TASK_FINDING_NOT_FOUND"}}]}`, false, 4},
	} {
		t.Run(name, func(t *testing.T) {
			r := taskResponses()
			r["AgentTaskDisposeFinding"] = tc.body
			calls, out, err := runTeamTask(t, r, "team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--json")
			if err == nil || out != "" || len(*calls) != 1 || exitCodeFor(err) != tc.wantExit {
				t.Fatalf("err=%v out=%q calls=%d exit=%d want %d", err, out, len(*calls), exitCodeFor(err), tc.wantExit)
			}
			if got := strings.Contains(err.Error(), "may have been recorded"); got != tc.unconfirmed {
				t.Fatalf("unconfirmed guidance = %v, want %v: %v", got, tc.unconfirmed, err)
			}
		})
	}
}

// The instructed command (--instruction) handles an unusable readback, a
// malformed body and a readback-field error the same way: exit 1, empty
// output, one request, guidance that does not claim the write failed.
func TestFindingDispositions1709InstructedUnusableReadback(t *testing.T) {
	for name, body := range map[string]string{
		"wrong finding id": `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":{"findingId":"F2","disposition":"fixed","reference":null}}}}`,
		"null target":      `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":null}}}`,
		"truncated body":   `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":{"findingId":`,
		"blank body":       ` `,
		"wrong type":       `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":{"findingId":1,"disposition":"fixed"}}}}`,
		"readback error":   `{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":null}},"errors":[{"message":"boom","path":["disposeAgentTaskFinding","findingDisposition"]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := taskResponses()
			r["AgentTaskDisposeFindingInstructed"] = body
			calls, out, err := runTeamTask(t, r, "team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--json", "--instruction", "team-chat-message:"+taskInstructionRef2422)
			if err == nil || out != "" || len(*calls) != 1 || exitCodeFor(err) != 1 {
				t.Fatalf("err=%v out=%q calls=%d exit=%d", err, out, len(*calls), exitCodeFor(err))
			}
			if !strings.Contains(err.Error(), "may have been recorded") || !strings.Contains(err.Error(), "read finding-dispositions before retrying") {
				t.Fatalf("recovery guidance missing: %v", err)
			}
		})
	}
}

// cli#1709 (local round 1): a 200 whose body is blank or truncated JSON reaches
// the client as a decoder EOF. It is an unusable success body, so the plain
// command gives exit 1 and the recovery guidance (not the generic connection
// advice), with one request and no output.
func TestFindingDispositions1709DecoderEOF(t *testing.T) {
	for name, body := range map[string]string{
		"blank":     ` `,
		"truncated": `{"data":{"disposeAgentTaskFinding":`,
		"empty":     ``,
	} {
		t.Run(name, func(t *testing.T) {
			r := taskResponses()
			r["AgentTaskDisposeFinding"] = body
			calls, out, err := runTeamTask(t, r, "team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--json")
			if err == nil || out != "" || len(*calls) != 1 || exitCodeFor(err) != 1 || !strings.Contains(err.Error(), "read finding-dispositions before retrying") {
				t.Fatalf("err=%v out=%q calls=%d exit=%d", err, out, len(*calls), exitCodeFor(err))
			}
		})
	}
}

// The same classes arrive with HTTP 500 when the server answers an execution
// error with a non-200 status carrying the GraphQL errors (cli#1709, local
// round 2): errors beneath the write's result are an unconfirmed write, an
// error on the write field itself stays a typed refusal.
func TestFindingDispositions1709HTTP500(t *testing.T) {
	for name, tc := range map[string]struct {
		body        string
		unconfirmed bool
		wantExit    int
	}{
		"readback field":              {`{"data":{"disposeAgentTaskFinding":{"id":"task1","findingDisposition":null}},"errors":[{"message":"boom","path":["disposeAgentTaskFinding","findingDisposition"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, true, 1},
		"id field":                    {`{"data":{"disposeAgentTaskFinding":null},"errors":[{"message":"boom","path":["disposeAgentTaskFinding","id"]}]}`, true, 1},
		"write refusal":               {`{"data":null,"errors":[{"message":"Refused","path":["disposeAgentTaskFinding"],"extensions":{"code":"TASK_NOT_OWNER"}}]}`, false, 8},
		"root generic alone":          {`{"data":null,"errors":[{"message":"boom","path":["disposeAgentTaskFinding"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, true, 1},
		"write refusal plus readback": {`{"data":null,"errors":[{"message":"a","path":["disposeAgentTaskFinding","findingDisposition"]},{"message":"Refused","path":["disposeAgentTaskFinding"],"extensions":{"code":"TASK_NOT_OWNER"}}]}`, false, 8},
		// No GraphQL error to classify: a gateway-style 500 keeps the transport
		// layer's own mapping (exit 7), not the readback recovery class.
		"empty errors": {`{"errors":[]}`, false, 7},
		"no body":      {``, false, 7},
	} {
		t.Run(name, func(t *testing.T) {
			writeTeamBinding(t)
			base, calls := attnServer(t, taskResponses())
			var sent atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				if strings.Contains(string(raw), `"operationName":"AgentTaskDisposeFinding"`) {
					sent.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(tc.body))
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(raw))
				base.Config.Handler.ServeHTTP(w, r)
			}))
			t.Cleanup(srv.Close)
			_ = calls
			setTeamBindingServer(t, srv.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"--server", srv.URL, "team", "task", "dispose-finding", "task1", "F1", "--disposition", "fixed", "--json"})
			err := root.Execute()
			if err == nil || out.String() != "" || sent.Load() != 1 || exitCodeFor(err) != tc.wantExit {
				t.Fatalf("err=%v out=%q sent=%d exit=%d want %d", err, out.String(), sent.Load(), exitCodeFor(err), tc.wantExit)
			}
			if got := strings.Contains(err.Error(), "may have been recorded"); got != tc.unconfirmed {
				t.Fatalf("unconfirmed guidance = %v, want %v: %v", got, tc.unconfirmed, err)
			}
		})
	}
}
