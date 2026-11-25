package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2465: the `question` group wraps the #1641 operations at the S1-S4 shape.
// Every verb is exactly one call; refs go to the server as typed; who may do what
// is the server's rule (cor:api:240:01), so the CLI's only refusals are usage ones.

const questionJSON = `{"id":"q1","urn":"hrn:node:acme.com:kb:questions:QA","code":"QA","n":1,"logId":"l1",
"title":"Pick a db","background":null,"whyNow":null,"state":"OPEN","tier":"ANSWERED","priority":"NORMAL","answered":false,
"authorizes":false,"revSeq":3,"portalUrl":"https://example.test/q/QA","viewerIsDecider":true,"viewerMayAnswer":true,
"askedAt":"2026-11-01T00:00:00Z","answeredAt":null,"neededBy":null,"defaultAction":null,"taskId":null,
"askedBy":{"userId":"u1","user":{"id":"u1","name":"Ann","handle":"ann","urn":null},"workerId":null,"worker":null,"appId":null,"app":null},
"assigneeId":null,"assignee":null,"effectiveAssigneeId":"u2","effectiveAssignee":{"id":"u2","name":"Bo","handle":"bo","urn":null},
"deciderIds":["u2"],"deciders":[{"id":"u2","name":"Bo","handle":"bo","urn":null}],"identityMerges":[],
"options":[{"key":"pg","label":"Postgres","whyChoose":"we run it","whyNot":null,"risks":"ops load"}],
"recommendation":{"key":"pg","reason":"boring","by":null},"noRecommendationReason":null,
"answer":null,"answers":[],"blocks":[],"sources":[],"specsTouched":[],"specsMustChange":[],"withdrawn":null,"evidence":{"approval":null}}`

// questionAnsweredJSON is questionJSON after an answer: a standing answer and one
// earlier retraction-free entry.
var questionAnsweredJSON = strings.NewReplacer(
	`"answered":false`, `"answered":true`,
	`"state":"OPEN"`, `"state":"ANSWERED_UNCONFIRMED"`,
	`"answeredAt":null`, `"answeredAt":"2026-11-02T00:00:00Z"`,
	`"answer":null,"answers":[]`, `"answer":{"kind":"ANSWER","at":"2026-11-02T00:00:00Z","byUserId":"u2","by":{"id":"u2","name":"Bo","handle":"bo","urn":null},"key":"pg","text":null,"reason":null,"via":"CLI"},"answers":[{"kind":"ANSWER","at":"2026-11-02T00:00:00Z","byUserId":"u2","by":null,"key":"pg","text":null,"reason":null,"via":"CLI"}]`,
).Replace(questionJSON)

// answeredAs is questionAnsweredJSON with the standing answer's key and text replaced,
// for a reply that shows the answer the test asked for. Only the answer entries change:
// the options and the recommendation keep their own keys.
func answeredAs(key, text string) string {
	var q map[string]any
	if err := json.Unmarshal([]byte(questionAnsweredJSON), &q); err != nil {
		panic(err)
	}
	set := func(e map[string]any) {
		e["key"], e["text"] = nil, nil
		if key != "" {
			e["key"] = key
		}
		if text != "" {
			e["text"] = text
		}
	}
	set(q["answer"].(map[string]any))
	for _, e := range q["answers"].([]any) {
		set(e.(map[string]any))
	}
	b, _ := json.Marshal(q)
	return string(b)
}

// answeredWrite is answeredAs for a reply to a real write: the revision advanced to
// rev and the standing answer carries the reason it was given with.
func answeredWrite(key, text, reason string, rev int) string {
	var q map[string]any
	if err := json.Unmarshal([]byte(answeredAs(key, text)), &q); err != nil {
		panic(err)
	}
	q["revSeq"] = rev
	set := func(e map[string]any) {
		e["reason"] = nil
		if reason != "" {
			e["reason"] = reason
		}
	}
	set(q["answer"].(map[string]any))
	for _, e := range q["answers"].([]any) {
		set(e.(map[string]any))
	}
	b, _ := json.Marshal(q)
	return string(b)
}

// questionRetractedJSON is questionJSON after a retraction: no standing answer, a
// RETRACTION as the last answers entry, and a revision newer than the one read.
var questionRetractedJSON = strings.NewReplacer(
	`"revSeq":3`, `"revSeq":5`,
	`"answers":[]`, `"answers":[{"kind":"ANSWER","at":"2026-11-02T00:00:00Z","byUserId":"u2","by":null,"key":"pg","text":null,"reason":null,"via":"CLI"},{"kind":"RETRACTION","at":"2026-11-03T00:00:00Z","byUserId":"u2","by":null,"key":null,"text":null,"reason":"wrong click","via":"CLI"}]`,
).Replace(questionJSON)

const questionLogJSON = `{"id":"l1","urn":"hrn:node:acme.com:kb:questions","loc":"questions","name":"Questions","prefix":"Q","memoryId":"m1",
"lastCode":1,"revSeq":2,"portalUrl":null,"defaultDeciderIds":["u2"],"defaultDeciders":[{"id":"u2","name":"Bo","handle":"bo","urn":null}],
"eligibleDeciderIds":[],"eligibleDeciders":[],"identityMerges":[],"purges":[]}`

type questionCall struct {
	ops  []string
	vars map[string]map[string]any
	raw  map[string]json.RawMessage // each operation's variables as sent
}

func runQuestion(t *testing.T, responses map[string]string, args ...string) (*questionCall, string, error) {
	t.Helper()
	calls := &questionCall{vars: map[string]map[string]any{}, raw: map[string]json.RawMessage{}}
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		calls.ops = append(calls.ops, op)
		return responses[op]
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"question"}, args...), "--server", gql.URL))
	err := root.Execute()
	for op, raw := range captured {
		calls.raw[op] = raw
		v := map[string]any{}
		_ = json.Unmarshal(raw, &v)
		calls.vars[op] = v
	}
	return calls, out.String(), err
}

func questionOnlyOp(t *testing.T, c *questionCall, op string) map[string]any {
	t.Helper()
	if len(c.ops) != 1 || c.ops[0] != op {
		t.Fatalf("want exactly one %s, got %v", op, c.ops)
	}
	return c.vars[op]
}

func questionAbsent(t *testing.T, v map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := v[k]; ok {
			t.Fatalf("%q must be omitted, got %v", k, v)
		}
	}
}

func writeJSONFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// --- question log ---

func TestQuestionLogCreateSendsArgsVerbatimAndOmitsUnset(t *testing.T) {
	c, _, err := runQuestion(t, map[string]string{
		"CreateQuestionLog": `{"data":{"createQuestionLog":` + questionLogJSON + `}}`,
	}, "log", "create", "hrn:mem:acme.com:kb", "--loc", "questions", "--decider", "u2", "--decider", "u3")
	if err != nil {
		t.Fatal(err)
	}
	v := questionOnlyOp(t, c, "CreateQuestionLog")
	if v["memoryRef"] != "hrn:mem:acme.com:kb" || v["loc"] != "questions" || !reflect.DeepEqual(v["defaultDeciders"], []any{"u2", "u3"}) {
		t.Fatalf("vars: %v", v)
	}
	questionAbsent(t, v, "name", "prefix", "eligibleDeciders")
}

func TestQuestionLogCreateRequiresLocAndDecider(t *testing.T) {
	for _, args := range [][]string{
		{"log", "create", "m1", "--decider", "u2"},
		{"log", "create", "m1", "--loc", "d"},
	} {
		c, _, err := runQuestion(t, nil, args...)
		if exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
			t.Fatalf("%v: want a usage error and no call, got %v ops=%v", args, err, c.ops)
		}
	}
}

// log list: the server's questionLogs, one scope, [] when none, no request on a
// wrong scope.
func TestQuestionLogListScopesAndEmptyIsAnArray(t *testing.T) {
	c, out, err := runQuestion(t, map[string]string{"QuestionLogs": `{"data":{"questionLogs":[]}}`}, "log", "list", "--app", "hrn:app:acme.com:ops", "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("err=%v out=%q", err, out)
	}
	v := questionOnlyOp(t, c, "QuestionLogs")
	if v["appRef"] != "hrn:app:acme.com:ops" {
		t.Fatalf("vars: %v", v)
	}
	questionAbsent(t, v, "memoryRef")
	c, out, err = runQuestion(t, map[string]string{"QuestionLogs": `{"data":{"questionLogs":[` + questionLogJSON + `]}}`}, "log", "list", "--memory", "hrn:mem:acme.com:kb")
	if err != nil || !strings.Contains(out, "Questions") {
		t.Fatalf("err=%v out=%q", err, out)
	}
	v = questionOnlyOp(t, c, "QuestionLogs")
	questionAbsent(t, v, "appRef")
	for _, args := range [][]string{{"log", "list"}, {"log", "list", "--app", "a", "--memory", "m"}} {
		c, _, err = runQuestion(t, nil, args...)
		if exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
			t.Fatalf("%v: want a usage error and no call: %v ops=%v", args, err, c.ops)
		}
	}
}

func TestQuestionLogGetAndSetDeciders(t *testing.T) {
	_, out, err := runQuestion(t, map[string]string{"QuestionLog": `{"data":{"questionLog":` + questionLogJSON + `}}`}, "log", "get", "l1", "--json")
	var dto map[string]any
	if err != nil || json.Unmarshal([]byte(out), &dto) != nil || dto["memoryId"] != "m1" || dto["revision"] != float64(2) {
		t.Fatalf("err=%v out=%q", err, out)
	}
	c, _, err := runQuestion(t, map[string]string{"SetQuestionLogDeciders": `{"data":{"setQuestionLogDeciders":` + questionLogJSON + `}}`},
		"log", "set-deciders", "l1", "--decider", "u2", "--expected-revision", "2", "--reason", "r")
	if err != nil {
		t.Fatal(err)
	}
	v := questionOnlyOp(t, c, "SetQuestionLogDeciders")
	if v["expectedRevision"] != float64(2) || v["eligibleDeciders"] != nil {
		t.Fatalf("an omitted --eligible must keep the set (null): %v", v)
	}
}

// --- ask ---

// Acceptance 1: the file is passed through unchanged, including the new fields; an
// unknown key is refused before any request.
func TestQuestionAskPassesInputFileThrough(t *testing.T) {
	file := writeJSONFile(t, `{"title":"Pick a db","options":[{"key":"pg","label":"Postgres","whyChoose":"w","whyNot":"n","risks":"r"}],"recommendation":{"key":"pg","reason":"boring"},"assignee":"u2","neededBy":"2026-12-01T00:00:00Z","defaultAction":"use pg","specsTouched":[{"spec":"cor:dmo:140:03"}]}`)
	c, out, err := runQuestion(t, map[string]string{"AskQuestion": `{"data":{"askQuestion":` + questionJSON + `}}`}, "ask", "hrn:node:acme.com:kb:questions", "--input-file", file)
	if err != nil {
		t.Fatal(err)
	}
	v := questionOnlyOp(t, c, "AskQuestion")
	in := v["input"].(map[string]any)
	if v["logRef"] != "hrn:node:acme.com:kb:questions" || in["title"] != "Pick a db" || in["assignee"] != "u2" || in["neededBy"] != "2026-12-01T00:00:00Z" || in["defaultAction"] != "use pg" {
		t.Fatalf("vars: %v", v)
	}
	opt := in["options"].([]any)[0].(map[string]any)
	if opt["whyChoose"] != "w" || opt["whyNot"] != "n" || opt["risks"] != "r" {
		t.Fatalf("the option's texts must pass through: %v", opt)
	}
	// deciders is deliberately not omitempty: null means the log's defaults.
	if d, ok := in["deciders"]; !ok || d != nil {
		t.Fatalf("deciders must be an explicit null when not given: %v", in)
	}
	questionAbsent(t, in, "priority", "tier", "background", "whyNow", "authorizes")
	if !strings.HasPrefix(out, "✓ asked QA") {
		t.Fatalf("out: %q", out)
	}
}

func TestQuestionAskRefusesBadInputBeforeAnyCall(t *testing.T) {
	for name, tc := range map[string]struct {
		file string
		args []string
	}{
		"unknown key":   {`{"title":"t","parts":[]}`, nil},
		"old key":       {`{"title":"t","urgency":"URGENT"}`, nil},
		"mis-cased":     {`{"Title":"t"}`, nil},
		"duplicate key": {`{"title":"a","title":"b"}`, nil},
		"not an object": {`["title"]`, nil},
		"trailing data": {`{"title":"t"} {}`, nil},
	} {
		t.Run(name, func(t *testing.T) {
			c, _, err := runQuestion(t, nil, append([]string{"ask", "l1", "--input-file", writeJSONFile(t, tc.file)}, tc.args...)...)
			if exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
				t.Fatalf("want a usage error and no call: %v ops=%v", err, c.ops)
			}
		})
	}
	c, _, err := runQuestion(t, nil, "ask", "l1")
	if exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
		t.Fatalf("--input-file is required: %v ops=%v", err, c.ops)
	}
}

func TestQuestionAskFlagsOverrideTopLevelKeys(t *testing.T) {
	file := writeJSONFile(t, `{"title":"from file","priority":"LOW","tier":"ANSWERED","background":"file bg"}`)
	c, _, err := runQuestion(t, map[string]string{"AskQuestion": `{"data":{"askQuestion":` + questionJSON + `}}`},
		"ask", "l1", "--input-file", file, "--title", "from flag", "--priority", "URGENT", "--tier", "APPROVED", "--background", "flag bg", "--assignee", "u2")
	if err != nil {
		t.Fatal(err)
	}
	in := questionOnlyOp(t, c, "AskQuestion")["input"].(map[string]any)
	if in["title"] != "from flag" || in["priority"] != "URGENT" || in["tier"] != "APPROVED" || in["background"] != "flag bg" || in["assignee"] != "u2" {
		t.Fatalf("input: %v", in)
	}
}

// --- list ---

// Acceptance 2: exactly the variables for the flags given, and no others.
func TestQuestionListSendsExactlyTheFiltersGiven(t *testing.T) {
	c, _, err := runQuestion(t, map[string]string{"Questions": `{"data":{"questions":{"total":0,"items":[]}}}`},
		"list", "--app", "hrn:app:acme.com:ops", "--answered=false", "--priority", "HIGH,URGENT", "--assignee", "u1", "--asked-by-me", "--answered-since", "2026-11-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	v := questionOnlyOp(t, c, "Questions")
	want := map[string]any{
		"appRef": "hrn:app:acme.com:ops", "answered": false, "priority": []any{"HIGH", "URGENT"},
		"assigneeId": "u1", "askedByMe": true, "answeredSince": "2026-11-01T00:00:00Z",
	}
	if !reflect.DeepEqual(v, want) {
		t.Fatalf("variables = %v, want exactly %v", v, want)
	}
	c, _, _ = runQuestion(t, map[string]string{"Questions": `{"data":{"questions":{"total":0,"items":[]}}}`}, "list", "--log", "l1", "--answered")
	if v := questionOnlyOp(t, c, "Questions"); !reflect.DeepEqual(v, map[string]any{"logRef": "l1", "answered": true}) {
		t.Fatalf("--answered alone sends true: %v", v)
	}
	c, _, _ = runQuestion(t, map[string]string{"Questions": `{"data":{"questions":{"total":0,"items":[]}}}`},
		"list", "--awaiting-me", "--unassigned", "--asked-by-worker", "w1", "--asked-by-app", "a1", "--authorizes=false", "--tier", "SIGNED", "--sort", "PRIORITY_DESC", "--limit", "5", "--offset", "10", "--state", "OPEN,STALE")
	want = map[string]any{
		"awaitingMe": true, "unassigned": true, "askedByWorkerId": "w1", "askedByAppId": "a1", "authorizes": false,
		"tier": "SIGNED", "sort": "PRIORITY_DESC", "limit": float64(5), "offset": float64(10), "state": []any{"OPEN", "STALE"},
	}
	if v := questionOnlyOp(t, c, "Questions"); !reflect.DeepEqual(v, want) {
		t.Fatalf("variables = %v, want exactly %v", v, want)
	}
}

func TestQuestionListRefusesWrongScopeAndBadTimeBeforeAnyCall(t *testing.T) {
	for name, args := range map[string][]string{
		"no scope":         {"list"},
		"filters only":     {"list", "--answered"},
		"app with log":     {"list", "--app", "a", "--log", "l"},
		"blank app":        {"list", "--app", " "},
		"blank assignee":   {"list", "--log", "l", "--assignee", " "},
		"bad time":         {"list", "--log", "l", "--answered-since", "yesterday"},
		"time not rfc3339": {"list", "--log", "l", "--answered-since", "2026-11-01"},
	} {
		t.Run(name, func(t *testing.T) {
			c, _, err := runQuestion(t, nil, args...)
			if exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
				t.Fatalf("want a usage error and no call: %v ops=%v", err, c.ops)
			}
		})
	}
}

func TestQuestionListTableAndJSON(t *testing.T) {
	page := `{"data":{"questions":{"total":4,"items":[` + questionJSON + `]}}}`
	_, out, err := runQuestion(t, map[string]string{"Questions": page}, "list", "--log", "l1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CODE", "STATE", "PRIORITY", "TIER", "ASSIGNEE", "REV", "TITLE", "QA", "@bo", "Pick a db"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
	_, out, err = runQuestion(t, map[string]string{"Questions": `{"data":{"questions":{"total":0,"items":[]}}}`}, "list", "--log", "l1", "--json")
	var dto struct {
		Total int   `json:"total"`
		Items []any `json:"items"`
	}
	if err != nil || json.Unmarshal([]byte(out), &dto) != nil || dto.Items == nil || len(dto.Items) != 0 {
		t.Fatalf("an empty page is items [] (never null): err=%v out=%q", err, out)
	}
}

// --- answer / retract ---

// Acceptance 3.
func TestQuestionAnswerSendsViaCLIAndOnlyTheGivenFields(t *testing.T) {
	c, out, err := runQuestion(t, map[string]string{"AnswerQuestion": `{"data":{"answerQuestion":` + questionAnsweredJSON + `}}`},
		"answer", "hrn:node:acme.com:kb:questions:QA", "--expected-revision", "3", "--option", "pg")
	if err != nil {
		t.Fatal(err)
	}
	v := questionOnlyOp(t, c, "AnswerQuestion")
	want := map[string]any{
		"ref": "hrn:node:acme.com:kb:questions:QA", "expectedRevision": float64(3),
		"input": map[string]any{"optionKey": "pg", "via": "CLI"},
	}
	if !reflect.DeepEqual(v, want) {
		t.Fatalf("variables = %v, want exactly %v", v, want)
	}
	if !strings.HasPrefix(out, "✓ answered QA: pg\n") || !strings.Contains(out, "answer: pg") {
		t.Fatalf("out: %q", out)
	}
	c, _, err = runQuestion(t, map[string]string{"AnswerQuestion": `{"data":{"answerQuestion":` + answeredWrite("my", "a note", "changed my mind", 5) + `}}`},
		"answer", "QA", "--expected-revision", "4", "--option", "my", "--text", "a note", "--reason", "changed my mind")
	if err != nil {
		t.Fatal(err)
	}
	in := questionOnlyOp(t, c, "AnswerQuestion")["input"]
	if !reflect.DeepEqual(in, map[string]any{"optionKey": "my", "text": "a note", "reason": "changed my mind", "via": "CLI"}) {
		t.Fatalf("input: %v", in)
	}
}

func TestQuestionAnswerTextFromFileAndStdin(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(p, []byte("free text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, out, err := runQuestion(t, map[string]string{"AnswerQuestion": `{"data":{"answerQuestion":` + answeredAs("", "free text\n") + `}}`}, "answer", "QA", "--expected-revision", "3", "--text-file", p)
	if err != nil {
		t.Fatal(err)
	}
	if in := questionOnlyOp(t, c, "AnswerQuestion")["input"]; !reflect.DeepEqual(in, map[string]any{"text": "free text\n", "via": "CLI"}) {
		t.Fatalf("input: %v", in)
	}
	if !strings.HasPrefix(out, "✓ answered QA: (text)\n") {
		t.Fatalf("a text-only answer reads (text): %q", out)
	}
	// stdin: testFactory's input is not a terminal.
	gql, captured := captureGraphQLFunc(t, func(op string) string { return `{"data":{"answerQuestion":` + answeredAs("", "from stdin") + `}}` })
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader("from stdin")
	root := NewRootCmd(f)
	root.SetArgs([]string{"question", "answer", "QA", "--expected-revision", "3", "--text-file", "-", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var sent struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["AnswerQuestion"], &sent)
	if sent.Input["text"] != "from stdin" {
		t.Fatalf("--text-file - must read stdin: %v", sent.Input)
	}
}

func TestQuestionAnswerRefusesBeforeAnyCall(t *testing.T) {
	for name, args := range map[string][]string{
		"neither option nor text": {"answer", "QA", "--expected-revision", "3"},
		"reason alone":            {"answer", "QA", "--expected-revision", "3", "--reason", "r"},
		"both text flags":         {"answer", "QA", "--expected-revision", "3", "--text", "a", "--text-file", "-"},
		"no expected revision":    {"answer", "QA", "--option", "yes"},
		"blank option":            {"answer", "QA", "--expected-revision", "3", "--option", " "},
		"blank question":          {"answer", " ", "--expected-revision", "3", "--option", "yes"},
		"missing text file":       {"answer", "QA", "--expected-revision", "3", "--text-file", "/no/such/file"},
	} {
		t.Run(name, func(t *testing.T) {
			c, out, err := runQuestion(t, nil, args...)
			if exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 || out != "" {
				t.Fatalf("want a usage error, no call, no output: %v ops=%v out=%q", err, c.ops, out)
			}
		})
	}
}

// Acceptance 4.
func TestQuestionRetractSendsViaCLI(t *testing.T) {
	c, out, err := runQuestion(t, map[string]string{"RetractQuestionAnswer": `{"data":{"retractQuestionAnswer":` + questionRetractedJSON + `}}`},
		"retract", "hrn:node:acme.com:kb:questions:QA", "--expected-revision", "4", "--reason", "wrong click")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"ref": "hrn:node:acme.com:kb:questions:QA", "expectedRevision": float64(4), "reason": "wrong click", "via": "CLI"}
	if v := questionOnlyOp(t, c, "RetractQuestionAnswer"); !reflect.DeepEqual(v, want) {
		t.Fatalf("variables = %v, want exactly %v", v, want)
	}
	if !strings.HasPrefix(out, "✓ answer taken back; QA is pending again\n") {
		t.Fatalf("out: %q", out)
	}
	c, _, _ = runQuestion(t, map[string]string{"RetractQuestionAnswer": `{"data":{"retractQuestionAnswer":` + questionRetractedJSON + `}}`}, "retract", "QA", "--expected-revision", "4")
	questionAbsent(t, questionOnlyOp(t, c, "RetractQuestionAnswer"), "reason")
	c, _, err = runQuestion(t, nil, "retract", "QA")
	if exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
		t.Fatalf("--expected-revision is required: %v ops=%v", err, c.ops)
	}
}

// --- amend ---

// Acceptance 7: a literal null in the file clears; an absent key leaves the field out.
func TestQuestionAmendTriStateFields(t *testing.T) {
	c, _, err := runQuestion(t, map[string]string{"AmendQuestion": `{"data":{"amendQuestion":` + questionJSON + `}}`},
		"amend", "QA", "--expected-revision", "3", "--reason", "reroute", "--input-file", writeJSONFile(t, `{"assignee":null,"background":"new","defaultAction":null}`))
	if err != nil {
		t.Fatal(err)
	}
	in := questionOnlyOp(t, c, "AmendQuestion")["input"].(map[string]any)
	if v, ok := in["assignee"]; !ok || v != nil {
		t.Fatalf("a null in the file must be sent as an explicit null: %v", in)
	}
	if in["background"] != "new" {
		t.Fatalf("background: %v", in)
	}
	if v, ok := in["defaultAction"]; !ok || v != nil {
		t.Fatalf("defaultAction null must clear: %v", in)
	}
	questionAbsent(t, in, "whyNow", "neededBy", "title", "priority")
	c, _, err = runQuestion(t, map[string]string{"AmendQuestion": `{"data":{"amendQuestion":` + questionJSON + `}}`},
		"amend", "QA", "--expected-revision", "3", "--reason", "r", "--title", "new title")
	if err != nil {
		t.Fatal(err)
	}
	in = questionOnlyOp(t, c, "AmendQuestion")["input"].(map[string]any)
	questionAbsent(t, in, "assignee", "background", "whyNow", "neededBy", "defaultAction")
}

func TestQuestionAmendListsClearOnEmptyAndRequireAChange(t *testing.T) {
	c, _, err := runQuestion(t, map[string]string{"AmendQuestion": `{"data":{"amendQuestion":` + questionJSON + `}}`},
		"amend", "QA", "--expected-revision", "3", "--reason", "r", "--input-file", writeJSONFile(t, `{"specsTouched":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	in := questionOnlyOp(t, c, "AmendQuestion")["input"].(map[string]any)
	if got, ok := in["specsTouched"].([]any); !ok || len(got) != 0 {
		t.Fatalf("an empty list clears: %v", in)
	}
	for name, args := range map[string][]string{
		"nothing given":    {"amend", "QA", "--expected-revision", "3", "--reason", "r"},
		"empty object":     {"amend", "QA", "--expected-revision", "3", "--reason", "r", "--input-file", writeJSONFile(t, `{}`)},
		"no reason":        {"amend", "QA", "--expected-revision", "3", "--title", "t"},
		"no revision":      {"amend", "QA", "--reason", "r", "--title", "t"},
		"old key in file":  {"amend", "QA", "--expected-revision", "3", "--reason", "r", "--input-file", writeJSONFile(t, `{"context":"x"}`)},
		"addParts is gone": {"amend", "QA", "--expected-revision", "3", "--reason", "r", "--input-file", writeJSONFile(t, `{"addParts":[]}`)},
	} {
		t.Run(name, func(t *testing.T) {
			c, _, err := runQuestion(t, nil, args...)
			if exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
				t.Fatalf("want a usage error and no call: %v ops=%v", err, c.ops)
			}
		})
	}
}

// --- get / verify / withdraw / purge ---

func TestQuestionGetByRefOrByCode(t *testing.T) {
	r := map[string]string{"Question": `{"data":{"question":` + questionJSON + `}}`}
	c, _, err := runQuestion(t, r, "get", "hrn:node:acme.com:kb:questions:QA")
	if err != nil {
		t.Fatal(err)
	}
	if v := questionOnlyOp(t, c, "Question"); !reflect.DeepEqual(v, map[string]any{"ref": "hrn:node:acme.com:kb:questions:QA"}) {
		t.Fatalf("vars: %v", v)
	}
	c, _, _ = runQuestion(t, r, "get", "QA", "--log", "l1")
	if v := questionOnlyOp(t, c, "Question"); !reflect.DeepEqual(v, map[string]any{"code": "QA", "logRef": "l1"}) {
		t.Fatalf("vars: %v", v)
	}
	_, _, err = runQuestion(t, map[string]string{"Question": `{"data":{"question":null}}`}, "get", "QZ")
	if exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("an unknown question is exit 4: %v", err)
	}
}

// Acceptance 5: the JSON shape; lists are [], the answer is null when unanswered.
func TestQuestionJSONShapes(t *testing.T) {
	_, out, err := runQuestion(t, map[string]string{"Question": `{"data":{"question":` + questionJSON + `}}`}, "get", "QA", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var q map[string]any
	if err := json.Unmarshal([]byte(out), &q); err != nil {
		t.Fatalf("stdout must be only JSON: %v\n%s", err, out)
	}
	for _, k := range []string{"answers", "blocks", "sources", "specsTouched", "specsMustChange", "identityMerges"} {
		if a, ok := q[k].([]any); !ok || len(a) != 0 {
			t.Errorf("%s must be []: %#v", k, q[k])
		}
	}
	if v, ok := q["answer"]; !ok || v != nil {
		t.Errorf("answer must be null when unanswered: %#v", v)
	}
	for _, gone := range []string{"parts", "urgency", "archival", "context", "followUps"} {
		if _, ok := q[gone]; ok {
			t.Errorf("%s was dropped with the old shape and must not appear", gone)
		}
	}
	for _, want := range []string{"background", "whyNow", "priority", "answered", "options", "recommendation", "noRecommendationReason", "answeredAt", "neededBy",
		"defaultAction", "specsTouched", "specsMustChange", "assigneeId", "assignee", "effectiveAssigneeId", "effectiveAssignee", "viewerMayAnswer", "taskId", "revision"} {
		if _, ok := q[want]; !ok {
			t.Errorf("missing %q", want)
		}
	}
	if opts := q["options"].([]any); len(opts) != 1 || opts[0].(map[string]any)["whyChoose"] != "we run it" {
		t.Errorf("options: %v", q["options"])
	}
	_, out, err = runQuestion(t, map[string]string{"AnswerQuestion": `{"data":{"answerQuestion":` + questionAnsweredJSON + `}}`}, "answer", "QA", "--expected-revision", "3", "--option", "pg", "--json")
	if err != nil {
		t.Fatal(err)
	}
	q = nil
	if err := json.Unmarshal([]byte(out), &q); err != nil {
		t.Fatalf("answer --json must hold only JSON (no checkmark line): %v\n%s", err, out)
	}
	if a := q["answer"].(map[string]any); a["key"] != "pg" || a["via"] != "CLI" || a["kind"] != "ANSWER" {
		t.Errorf("answer: %v", a)
	}
	if q["answered"] != true || len(q["answers"].([]any)) != 1 {
		t.Errorf("answered/answers: %v %v", q["answered"], q["answers"])
	}
}

func TestQuestionTextShowsOptionsRecommendationAndAnswer(t *testing.T) {
	_, out, err := runQuestion(t, map[string]string{"Question": `{"data":{"question":` + questionAnsweredJSON + `}}`}, "get", "QA")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"QA  Pick a db", "priority: NORMAL", "assignee: @bo", "- pg: Postgres  (recommended)", "why choose: we run it", "risks: ops load", "recommendation reason: boring", "answer: pg (CLI,"} {
		if !strings.Contains(out, want) {
			t.Errorf("text lacks %q:\n%s", want, out)
		}
	}
}

func TestQuestionVerifyWithdrawPurge(t *testing.T) {
	_, out, err := runQuestion(t, map[string]string{"VerifyQuestion": `{"data":{"verifyQuestion":{"state":"DECIDED","revision":3,"approvedRevision":3,"approvalIntegrity":"INTACT","approvalByDecider":true,"codec":"c","renderer":"r","findings":[]}}}`}, "verify", "QA", "--json")
	if err != nil || !strings.Contains(out, `"findings": []`) && !strings.Contains(out, `"findings":[]`) {
		t.Fatalf("err=%v out=%q", err, out)
	}
	c, _, err := runQuestion(t, nil, "withdraw", "QA", "--expected-revision", "3", "--reason", "r")
	if err == nil || !strings.Contains(err.Error(), "--yes") || len(c.ops) != 0 {
		t.Fatalf("a non-TTY withdraw without --yes refuses before any call: %v ops=%v", err, c.ops)
	}
	c, _, err = runQuestion(t, map[string]string{"WithdrawQuestion": `{"data":{"withdrawQuestion":` + questionJSON + `}}`}, "withdraw", "QA", "--expected-revision", "3", "--reason", "r", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if v := questionOnlyOp(t, c, "WithdrawQuestion"); v["expectedRevision"] != float64(3) || v["reason"] != "r" {
		t.Fatalf("vars: %v", v)
	}
	c, _, err = runQuestion(t, map[string]string{"PurgeUnansweredQuestion": `{"data":{"purgeUnansweredQuestion":true}}`}, "purge", "QA", "--reason", "r", "--yes")
	if err != nil || questionOnlyOp(t, c, "PurgeUnansweredQuestion")["reason"] != "r" {
		t.Fatalf("err=%v", err)
	}
	_, _, err = runQuestion(t, map[string]string{"PurgeUnansweredQuestion": `{"data":{"purgeUnansweredQuestion":false}}`}, "purge", "QA", "--reason", "r", "--yes")
	if err == nil {
		t.Fatalf("a purge the server did not confirm is an error")
	}
}

// --- refusals ---

// Acceptance 6: one request, no stdout, the documented exit.
func TestQuestionRefusalsMapAndDoNothingElse(t *testing.T) {
	for _, tc := range []struct {
		name string
		op   string
		args []string
		code string
		want int
	}{
		{"answer not a decider", "AnswerQuestion", []string{"answer", "QA", "--expected-revision", "3", "--option", "x"}, "QUESTION_NOT_DECIDER", exitcode.Forbidden},
		{"answer unknown option", "AnswerQuestion", []string{"answer", "QA", "--expected-revision", "3", "--option", "x"}, "QUESTION_UNKNOWN_OPTION", exitcode.Usage},
		{"retract not answered", "RetractQuestionAnswer", []string{"retract", "QA", "--expected-revision", "3"}, "QUESTION_NOT_ANSWERED", exitcode.Conflict},
		{"answer write conflict", "AnswerQuestion", []string{"answer", "QA", "--expected-revision", "3", "--option", "x"}, "NODE_WRITE_CONFLICT", exitcode.Conflict},
		{"answer withdrawn", "AnswerQuestion", []string{"answer", "QA", "--expected-revision", "3", "--option", "x"}, "QUESTION_WITHDRAWN", exitcode.Conflict},
		{"answer not found", "AnswerQuestion", []string{"answer", "QA", "--expected-revision", "3", "--option", "x"}, "QUESTION_NOT_FOUND", exitcode.NotFound},
		{"ask assignee not a decider", "AskQuestion", []string{"ask", "l1", "--input-file", writeJSONFile(t, `{"title":"t"}`)}, "QUESTION_ASSIGNEE_NOT_DECIDER", exitcode.Usage},
		{"purge not admin", "PurgeUnansweredQuestion", []string{"purge", "QA", "--reason", "r", "--yes"}, "FORBIDDEN", exitcode.Forbidden},
		{"log missing", "QuestionLog", []string{"log", "get", "l9"}, "QUESTION_LOG_NOT_FOUND", exitcode.NotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, out, err := runQuestion(t, map[string]string{
				tc.op: `{"errors":[{"message":"refused","extensions":{"code":"` + tc.code + `"}}]}`,
			}, tc.args...)
			if err == nil || exitCodeFor(err) != tc.want {
				t.Fatalf("want exit %d, got %v (%d)", tc.want, err, exitCodeFor(err))
			}
			if len(c.ops) != 1 || out != "" {
				t.Fatalf("one call, no output: ops=%v out=%q", c.ops, out)
			}
		})
	}
}

// --- the retired decision group ---

// Acceptance 8: any spelling of hadron decision prints the retirement sentence,
// exits 2 and sends nothing.
func TestDecisionIsRetiredAndSendsNothing(t *testing.T) {
	for _, args := range [][]string{
		{"decision"},
		{"decision", "list", "--log", "x"},
		{"decision", "ask", "l1", "--input-file", "f.json", "--urgency", "URGENT", "--context", "c"},
		{"decision", "log", "get", "l1", "--json"},
		{"decision", "--no-such-old-flag", "x"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			c, out, err := runQuestionRoot(t, args...)
			if exitCodeFor(err) != exitcode.Usage || err == nil || err.Error() != "hadron decision was renamed to hadron question. Nothing was sent." {
				t.Fatalf("want the retirement sentence at exit 2: %v (%d)", err, exitCodeFor(err))
			}
			if len(c.ops) != 0 || out != "" {
				t.Fatalf("nothing may be sent or printed: ops=%v out=%q", c.ops, out)
			}
		})
	}
}

// runQuestionRoot runs args as given (no `question` prefix) against a fake that
// records every operation, so a stub that sent anything would show.
// runQuestionErr is runQuestion that returns what the command wrote to stderr, and the error.
func runQuestionErr(t *testing.T, responses map[string]string, args ...string) (stderr string, err error) {
	t.Helper()
	gql, _ := captureGraphQLFunc(t, func(op string) string { return responses[op] })
	f, _ := testFactory(t)
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"question"}, args...), "--server", gql.URL))
	err = root.Execute()
	return errOut.String(), err
}

func runQuestionRoot(t *testing.T, args ...string) (*questionCall, string, error) {
	t.Helper()
	calls := &questionCall{vars: map[string]map[string]any{}, raw: map[string]json.RawMessage{}}
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		calls.ops = append(calls.ops, op)
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", gql.URL))
	err := root.Execute()
	return calls, out.String(), err
}

// Joshua's BLIND P2: "options": [] makes the question free text, so it must reach
// the server as an empty list, not be omitted (omitempty would drop it and the
// amend would report success having changed nothing).
func TestQuestionAmendEmptyOptionsClearsAndAbsentLeavesThemAlone(t *testing.T) {
	c, _, err := runQuestion(t, map[string]string{"AmendQuestion": `{"data":{"amendQuestion":` + questionJSON + `}}`},
		"amend", "QA", "--expected-revision", "3", "--reason", "free text", "--input-file", writeJSONFile(t, `{"options":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	in := questionOnlyOp(t, c, "AmendQuestion")["input"].(map[string]any)
	if got, ok := in["options"].([]any); !ok || len(got) != 0 {
		t.Fatalf(`"options": [] must be sent as an empty list: %v`, in)
	}
	c, _, err = runQuestion(t, map[string]string{"AmendQuestion": `{"data":{"amendQuestion":` + questionJSON + `}}`},
		"amend", "QA", "--expected-revision", "3", "--reason", "r", "--title", "t")
	if err != nil {
		t.Fatal(err)
	}
	in = questionOnlyOp(t, c, "AmendQuestion")["input"].(map[string]any)
	// An absent list is sent as null ("unchanged" on the server), never as [].
	if v, ok := in["options"]; ok && v != nil {
		t.Fatalf("an absent options must not clear: %v", in)
	}
}

// Joshua's BLIND P2: a null on a key that clears nothing is refused, naming the
// key, before any call (a key the file gives is sent or refused, never dropped);
// the five clearable keys pass.
func TestQuestionAmendRefusesANullThatClearsNothing(t *testing.T) {
	for _, k := range []string{"title", "priority", "tier", "options", "blocks", "sources", "specsTouched", "specsMustChange", "recommendation", "noRecommendation", "yesno"} {
		t.Run(k, func(t *testing.T) {
			c, _, err := runQuestion(t, nil, "amend", "QA", "--expected-revision", "3", "--reason", "r", "--input-file", writeJSONFile(t, `{"`+k+`":null,"title":"t2"}`))
			if k == "title" {
				// title: null with a title given twice is a duplicate-key refusal: use a clean file.
				c, _, err = runQuestion(t, nil, "amend", "QA", "--expected-revision", "3", "--reason", "r", "--input-file", writeJSONFile(t, `{"title":null,"tier":"APPROVED"}`))
			}
			if exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 || err == nil || !strings.Contains(err.Error(), `"`+k+`" is null`) {
				t.Fatalf("want a usage error naming %q and no call: %v ops=%v", k, err, c.ops)
			}
		})
	}
	for _, k := range []string{"background", "whyNow", "assignee", "neededBy", "defaultAction"} {
		c, _, err := runQuestion(t, map[string]string{"AmendQuestion": `{"data":{"amendQuestion":` + questionJSON + `}}`},
			"amend", "QA", "--expected-revision", "3", "--reason", "r", "--input-file", writeJSONFile(t, `{"`+k+`":null}`))
		if err != nil || len(c.ops) != 1 {
			t.Fatalf("%s clears with null: %v ops=%v", k, err, c.ops)
		}
	}
}

// Joshua's BLIND P3: a blank id flag is refused before any call, like --assignee.
func TestQuestionBlankIDFlagsAreRefusedBeforeAnyCall(t *testing.T) {
	for name, args := range map[string][]string{
		"list asked-by-worker": {"list", "--log", "l", "--asked-by-worker", " "},
		"list asked-by-app":    {"list", "--log", "l", "--asked-by-app", ""},
		"ask assignee":         {"ask", "l1", "--input-file", writeJSONFile(t, `{"title":"t"}`), "--assignee", " "},
		"amend assignee":       {"amend", "QA", "--expected-revision", "3", "--reason", "r", "--assignee", " "},
	} {
		t.Run(name, func(t *testing.T) {
			c, _, err := runQuestion(t, nil, args...)
			if exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
				t.Fatalf("want a usage error and no call: %v ops=%v", err, c.ops)
			}
		})
	}
}

// --awaiting-me=false names no scope, and a purge confirmation states the legacy exception.
func TestQuestionAwaitingMeFalseIsNoScopeAndPurgeSaysItsException(t *testing.T) {
	c, _, err := runQuestion(t, nil, "list", "--awaiting-me=false")
	if exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
		t.Fatalf("--awaiting-me=false alone is no scope: %v ops=%v", err, c.ops)
	}
	c, _, err = runQuestion(t, nil, "purge", "QA", "--reason", "r")
	if err == nil || !strings.Contains(err.Error(), "--yes") || len(c.ops) != 0 {
		t.Fatalf("a non-TTY purge without --yes refuses before any call: %v", err)
	}
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	purge, _, ferr := root.Find([]string{"question", "purge"})
	flat := ""
	if ferr == nil {
		flat = strings.Join(strings.Fields(purge.Long), " ")
	}
	// The pre-#4426 format is purgeable only while its payload holds no answer or
	// history (the pinned resolver refuses one that does): the help must not promise more.
	if ferr != nil || !strings.Contains(flat, "only while its payload holds no answer or answer history") || strings.Contains(flat, "answered or not") {
		t.Fatalf("the purge help must state the legacy-format limit, not an answered-or-not exception: %v %q", ferr, flat)
	}
}

// Beckett's P2: success is claimed only when the reply confirms the outcome.
// A reply with no standing answer after answer, or one still showing a standing
// answer after retract, is an unconfirmed outcome (exit 1, nothing printed, in text
// and JSON), pointing at question get.
func TestQuestionAnswerAndRetractRefuseAnInconsistentReply(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		answerArgs := []string{"answer", "QA", "--expected-revision", "3", "--option", "pg"}
		retractArgs := []string{"retract", "QA", "--expected-revision", "4"}
		if asJSON {
			answerArgs = append(answerArgs, "--json")
			retractArgs = append(retractArgs, "--json")
		}
		c, out, err := runQuestion(t, map[string]string{"AnswerQuestion": `{"data":{"answerQuestion":` + questionJSON + `}}`}, answerArgs...)
		if exitCodeFor(err) != exitcode.Error || out != "" || len(c.ops) != 1 || err == nil || !strings.Contains(err.Error(), "unconfirmed") || !strings.Contains(err.Error(), "question get") {
			t.Fatalf("answer with no standing answer returned (json=%v): exit=%d out=%q err=%v", asJSON, exitCodeFor(err), out, err)
		}
		c, out, err = runQuestion(t, map[string]string{"RetractQuestionAnswer": `{"data":{"retractQuestionAnswer":` + questionAnsweredJSON + `}}`}, retractArgs...)
		if exitCodeFor(err) != exitcode.Error || out != "" || len(c.ops) != 1 || err == nil || !strings.Contains(err.Error(), "unconfirmed") || !strings.Contains(err.Error(), "question get") {
			t.Fatalf("retract with a standing answer returned (json=%v): exit=%d out=%q err=%v", asJSON, exitCodeFor(err), out, err)
		}
	}
}

// Beckett's P3: `hadron decision --help` is the one thing the stub shows besides the
// sentence; the fake server address is NOT configured for decision (flag parsing is off)
// and the harness fails the test on any attempt to reach a real host, so the retirement
// test above proves no request is sent.
func TestDecisionHelpShowsTheRetirementPage(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"decision", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--help must show the page: %v", err)
	}
	if !strings.Contains(strings.Join(strings.Fields(out.String()), " "), "was renamed to hadron question") {
		t.Fatalf("the page must name the rename: %q", out.String())
	}
}

// Dara's P2s: an answer is confirmed only by the answer that was asked for, and a
// retraction only by a retraction entry on a newer revision. A different standing
// answer, an unchanged question, an empty object and a stale revision are all an
// unconfirmed outcome (exit 1, nothing on stdout), in text and JSON.
func TestQuestionAnswerAndRetractAreConfirmedByTheirOwnEvidence(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		suffix := []string{}
		if asJSON {
			suffix = []string{"--json"}
		}
		for name, tc := range map[string]struct {
			op, field, body string
			args            []string
		}{
			"answer: a different standing answer": {"AnswerQuestion", "answerQuestion", questionAnsweredJSON, []string{"answer", "QA", "--expected-revision", "3", "--option", "other"}},
			"answer: an empty object":             {"AnswerQuestion", "answerQuestion", `{}`, []string{"answer", "QA", "--expected-revision", "3", "--option", "pg"}},
			"retract: an unchanged open question": {"RetractQuestionAnswer", "retractQuestionAnswer", questionJSON, []string{"retract", "QA", "--expected-revision", "4"}},
			"retract: a stale revision":           {"RetractQuestionAnswer", "retractQuestionAnswer", questionRetractedJSON, []string{"retract", "QA", "--expected-revision", "5"}},
			"retract: an empty object":            {"RetractQuestionAnswer", "retractQuestionAnswer", `{}`, []string{"retract", "QA", "--expected-revision", "4"}},
		} {
			c, out, err := runQuestion(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":` + tc.body + `}}`}, append(append([]string{}, tc.args...), suffix...)...)
			if err == nil || exitCodeFor(err) != exitcode.Error || len(c.ops) != 1 || !strings.Contains(err.Error(), "unconfirmed") || (!asJSON && out != "") {
				t.Errorf("%s (json=%v): want exit 1, unconfirmed, no stdout in text: exit=%d out=%q err=%v", name, asJSON, exitCodeFor(err), out, err)
			}
		}
	}
	// The idempotent same answer still succeeds.
	_, out, err := runQuestion(t, map[string]string{"AnswerQuestion": `{"data":{"answerQuestion":` + questionAnsweredJSON + `}}`}, "answer", "QA", "--expected-revision", "3", "--option", "pg")
	if err != nil || !strings.HasPrefix(out, "✓ answered QA: pg") {
		t.Fatalf("the confirmed answer succeeds: %v %q", err, out)
	}
}

// Jonas's P2: success never depends on a client copy of the server's text normalisation.
// A text or reason that differs from what was given (CRLF stored as LF, a trailing newline
// trimmed, a blank note stored as none, an earlier note still standing) succeeds when the
// reply shows an answer with the option key, and says so in a note on stderr; only a
// text the server did NOT change is silent.
func TestQuestionAnswerNeverGatesOnTextNormalisation(t *testing.T) {
	for name, tc := range map[string]struct {
		args     []string
		reply    string
		wantNote bool
	}{
		"CRLF text, LF returned":           {[]string{"--text", "line-one\r\nline-two"}, answeredAs("", "line-one\nline-two"), false},
		"bare CR text, LF returned":        {[]string{"--text", "a\rb"}, answeredAs("", "a\nb"), false},
		"trailing newline trimmed":         {[]string{"--text", "needs a different approach\n"}, answeredAs("", "needs a different approach"), true},
		"blank note beside an option":      {[]string{"--option", "pg", "--text", "  \n "}, answeredAs("pg", ""), false},
		"surrounding space kept as given":  {[]string{"--text", " keep "}, answeredAs("", " keep "), false},
		"no text asked, an old note shown": {[]string{"--option", "pg"}, answeredAs("pg", "an old note"), true},
		"text asked, none shown":           {[]string{"--option", "pg", "--text", "a note"}, answeredAs("pg", ""), true},
		"a different text shown":           {[]string{"--option", "pg", "--text", "a note"}, answeredAs("pg", "another"), true},
	} {
		args := append([]string{"answer", "QA", "--expected-revision", "3"}, tc.args...)
		stderr, err := runQuestionErr(t, map[string]string{"AnswerQuestion": `{"data":{"answerQuestion":` + tc.reply + `}}`}, args...)
		if err != nil {
			t.Errorf("%s: want success, got %v", name, err)
			continue
		}
		if has := strings.Contains(stderr, "note: the stored text"); has != tc.wantNote {
			t.Errorf("%s: note on stderr = %v, want %v (%q)", name, has, tc.wantNote, stderr)
		}
	}
}

// Andi's P2s: a reason is confirmed only by a write (a newer revision whose standing
// answer carries the reason), while the same answer without a reason may come back
// unchanged (the documented no-op); and a blank text without an option never reaches
// the server.
func TestQuestionAnswerReasonNeedsAWriteAndBlankTextIsRefusedLocally(t *testing.T) {
	args := []string{"answer", "QA", "--expected-revision", "3", "--option", "pg"}
	for name, tc := range map[string]struct {
		extra    []string
		reply    string
		success  bool
		wantNote bool
	}{
		"reason given, reply unchanged and without it": {[]string{"--reason", "why"}, answeredAs("pg", ""), false, false},
		"reason given, revision up, reason absent":     {[]string{"--reason", "why"}, answeredWrite("pg", "", "", 4), true, true},
		"reason given, a different reason shown":       {[]string{"--reason", "why"}, answeredWrite("pg", "", "other", 4), true, true},
		"reason given, written with it":                {[]string{"--reason", "why"}, answeredWrite("pg", "", "why", 4), true, false},
		"no reason, the documented no-op unchanged":    {nil, answeredAs("pg", ""), true, false},
		"no reason, a write":                           {nil, answeredWrite("pg", "", "", 4), true, false},
		"reason CRLF, written as LF":                   {[]string{"--reason", "line-one\r\nline-two"}, answeredWrite("pg", "", "line-one\nline-two", 4), true, false},
		"reason spaces changed in the reply":           {[]string{"--reason", " why "}, answeredWrite("pg", "", "why", 4), true, true},
		"blank reason, written without one":            {[]string{"--reason", "  "}, answeredWrite("pg", "", "", 4), true, false},
		"reply older than the revision read":           {nil, strings.ReplaceAll(answeredAs("pg", ""), `"revSeq":3`, `"revSeq":2`), false, false},
	} {
		stderr, err := runQuestionErr(t, map[string]string{"AnswerQuestion": `{"data":{"answerQuestion":` + tc.reply + `}}`}, append(append([]string{}, args...), tc.extra...)...)
		if tc.success && err != nil {
			t.Errorf("%s: want success, got %v", name, err)
		}
		if !tc.success && (err == nil || !strings.Contains(err.Error(), "unconfirmed")) {
			t.Errorf("%s: want unconfirmed, got %v", name, err)
		}
		if tc.success && strings.Contains(stderr, "note: the stored reason") != tc.wantNote {
			t.Errorf("%s: reason note = %v, want %v (%q)", name, !tc.wantNote, tc.wantNote, stderr)
		}
	}
	empty := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(empty, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, a := range map[string][]string{
		"blank --text, no option":      {"answer", "QA", "--expected-revision", "3", "--text", "   "},
		"empty text file, no option":   {"answer", "QA", "--expected-revision", "3", "--text-file", empty},
		"blank --text beside --option": nil,
	} {
		if a == nil {
			// a blank note beside an option is allowed and reaches the server
			c, _, err := runQuestion(t, map[string]string{"AnswerQuestion": `{"data":{"answerQuestion":` + answeredAs("pg", "") + `}}`}, "answer", "QA", "--expected-revision", "3", "--option", "pg", "--text", "  ")
			if err != nil || len(c.ops) != 1 {
				t.Errorf("%s: want one call and success: %v ops=%v", name, err, c.ops)
			}
			continue
		}
		c, _, err := runQuestion(t, nil, a...)
		if exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
			t.Errorf("%s: want a usage refusal before any call: %v ops=%v", name, err, c.ops)
		}
	}
}

// Jonas's P2 (the pin dropped in the port): every field of the populated question reaches
// the --json contract under its own name, with nothing crossed or lost: assignee versus
// effectiveAssignee, the identity merges, the withdrawal, the approval, the actors of the
// answers, the four reference lists. Each value carries a marker of its own so a swap shows.
const questionFullGQL = `{"id":"q9","urn":"hrn:node:acme.com:kb:questions:DB","code":"DB","n":2,"logId":"l1",
"title":"T","background":"bg","whyNow":"wn","state":"DECIDED","tier":"APPROVED","priority":"URGENT","answered":true,
"authorizes":true,"revSeq":7,"portalUrl":"https://example.test/q/DB","viewerIsDecider":false,"viewerMayAnswer":false,
"askedAt":"2026-11-01T00:00:00Z","answeredAt":"2026-11-02T00:00:00Z","neededBy":"2026-12-01T00:00:00Z","defaultAction":"ship pg","taskId":"t9",
"askedBy":{"userId":null,"user":null,"workerId":"w1","worker":{"id":"w1","name":"Ada","handle":null,"urn":"hrn:worker:acme.com:t:ada"},"appId":"a1","app":{"id":"a1","name":"Team","handle":null,"urn":"hrn:app:acme.com:t"}},
"assigneeId":"u-asg","assignee":{"id":"u-asg","name":"Asg","handle":"asg","urn":null},
"effectiveAssigneeId":"u-eff","effectiveAssignee":{"id":"u-eff","name":"Eff","handle":"eff","urn":null},
"deciderIds":["u2"],"deciders":[{"id":"u2","name":"Bo","handle":"bo","urn":null}],
"identityMerges":[{"fromUserId":"u7","from":{"id":"u7","name":"Old","handle":"old","urn":null},"toUserId":"u2","to":{"id":"u2","name":"Bo","handle":"bo","urn":null},"mergedAt":"2026-10-30T00:00:00Z"}],
"options":[{"key":"pg","label":"Postgres","whyChoose":"wc","whyNot":"wn2","risks":"rk"},{"key":"my","label":"MySQL","whyChoose":null,"whyNot":null,"risks":null}],
"recommendation":{"key":"pg","reason":"boring","by":{"userId":"u1","user":{"id":"u1","name":"Ann","handle":"ann","urn":null},"workerId":null,"worker":null,"appId":null,"app":null}},
"noRecommendationReason":"nr",
"answer":{"kind":"ANSWER","at":"2026-11-02T00:00:00Z","byUserId":"u2","by":{"id":"u2","name":"Bo","handle":"bo","urn":null},"key":"pg","text":"t1","reason":"r1","via":"PORTAL"},
"answers":[{"kind":"ANSWER","at":"2026-11-01T12:00:00Z","byUserId":"u3","by":{"id":"u3","name":"Cy","handle":"cy","urn":null},"key":null,"text":"maybe","reason":null,"via":"CLI"},{"kind":"ANSWER","at":"2026-11-02T00:00:00Z","byUserId":"u2","by":null,"key":"pg","text":"t1","reason":"r1","via":"PORTAL"}],
"blocks":[{"ref":"hadron-server#1","node":null,"spec":null}],
"sources":[{"ref":null,"node":null,"spec":"cor:api:240:01"}],
"specsTouched":[{"ref":null,"node":"hrn:node:acme.com:kb:x","spec":null}],
"specsMustChange":[{"ref":"hadron-cli#2","node":null,"spec":null}],
"withdrawn":{"at":"2026-11-03T00:00:00Z","reason":"moot","by":{"userId":"u4","user":{"id":"u4","name":"Dee","handle":"dee","urn":null},"workerId":null,"worker":null,"appId":null,"app":null}},
"evidence":{"approval":{"approvedAt":"2026-11-02T01:00:00Z","revision":6,"integrity":"INTACT","byDecider":true,"byUserId":"u2","by":{"id":"u2","name":"Bo","handle":"bo","urn":null}}}}`

func TestQuestionGetJSONMapsPopulatedValues(t *testing.T) {
	_, out, err := runQuestion(t, map[string]string{"Question": `{"data":{"question":` + questionFullGQL + `}}`}, "get", "q9", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	const want = `{"id":"q9","urn":"hrn:node:acme.com:kb:questions:DB","code":"DB","n":2,"logId":"l1",
"title":"T","background":"bg","whyNow":"wn","state":"DECIDED","tier":"APPROVED","priority":"URGENT","answered":true,
"authorizes":true,"revision":7,"portalUrl":"https://example.test/q/DB","viewerIsDecider":false,"viewerMayAnswer":false,
"askedAt":"2026-11-01T00:00:00Z","answeredAt":"2026-11-02T00:00:00Z","neededBy":"2026-12-01T00:00:00Z","defaultAction":"ship pg","taskId":"t9",
"askedBy":{"userId":null,"user":null,"workerId":"w1","worker":{"id":"w1","name":"Ada","handle":null,"urn":"hrn:worker:acme.com:t:ada"},"appId":"a1","app":{"id":"a1","name":"Team","handle":null,"urn":"hrn:app:acme.com:t"}},
"assigneeId":"u-asg","assignee":{"id":"u-asg","name":"Asg","handle":"asg","urn":null},
"effectiveAssigneeId":"u-eff","effectiveAssignee":{"id":"u-eff","name":"Eff","handle":"eff","urn":null},
"deciderIds":["u2"],"deciders":[{"id":"u2","name":"Bo","handle":"bo","urn":null}],
"identityMerges":[{"fromUserId":"u7","from":{"id":"u7","name":"Old","handle":"old","urn":null},"toUserId":"u2","to":{"id":"u2","name":"Bo","handle":"bo","urn":null},"mergedAt":"2026-10-30T00:00:00Z"}],
"options":[{"key":"pg","label":"Postgres","whyChoose":"wc","whyNot":"wn2","risks":"rk"},{"key":"my","label":"MySQL","whyChoose":null,"whyNot":null,"risks":null}],
"recommendation":{"key":"pg","reason":"boring","by":{"userId":"u1","user":{"id":"u1","name":"Ann","handle":"ann","urn":null},"workerId":null,"worker":null,"appId":null,"app":null}},
"noRecommendationReason":"nr",
"answer":{"kind":"ANSWER","key":"pg","text":"t1","reason":"r1","byUserId":"u2","by":{"id":"u2","name":"Bo","handle":"bo","urn":null},"at":"2026-11-02T00:00:00Z","via":"PORTAL"},
"answers":[{"kind":"ANSWER","key":null,"text":"maybe","reason":null,"byUserId":"u3","by":{"id":"u3","name":"Cy","handle":"cy","urn":null},"at":"2026-11-01T12:00:00Z","via":"CLI"},{"kind":"ANSWER","key":"pg","text":"t1","reason":"r1","byUserId":"u2","by":null,"at":"2026-11-02T00:00:00Z","via":"PORTAL"}],
"blocks":[{"ref":"hadron-server#1","node":null,"spec":null}],
"sources":[{"ref":null,"node":null,"spec":"cor:api:240:01"}],
"specsTouched":[{"ref":null,"node":"hrn:node:acme.com:kb:x","spec":null}],
"specsMustChange":[{"ref":"hadron-cli#2","node":null,"spec":null}],
"withdrawn":{"at":"2026-11-03T00:00:00Z","reason":"moot","by":{"userId":"u4","user":{"id":"u4","name":"Dee","handle":"dee","urn":null},"workerId":null,"worker":null,"appId":null,"app":null}},
"approval":{"approvedAt":"2026-11-02T01:00:00Z","revision":6,"integrity":"INTACT","byDecider":true,"byUserId":"u2","by":{"id":"u2","name":"Bo","handle":"bo","urn":null}}}`
	var wantV map[string]any
	if err := json.Unmarshal([]byte(want), &wantV); err != nil {
		t.Fatal(err)
	}
	for k, w := range wantV {
		if !reflect.DeepEqual(got[k], w) {
			t.Errorf("%s:\n got %v\nwant %v", k, got[k], w)
		}
	}
	for k := range got {
		if _, ok := wantV[k]; !ok {
			t.Errorf("unexpected key %s", k)
		}
	}
}

// The ones the port also dropped: set-deciders needs its three flags (exit 2, nothing
// sent), --eligible "" clears the set with [] (the server reads null as keep), and a number out of float64
// range inside the opaque grant reaches the server verbatim (#1558).
func TestQuestionLogSetDecidersRequiredFlagsAndEmptyEligibleClears(t *testing.T) {
	for _, args := range [][]string{
		{"log", "set-deciders", "l1", "--decider", "u2", "--reason", "r"},
		{"log", "set-deciders", "l1", "--decider", "u2", "--expected-revision", "2"},
		{"log", "set-deciders", "l1", "--expected-revision", "2", "--reason", "r"},
	} {
		c, _, err := runQuestion(t, nil, args...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
			t.Fatalf("%v: want a usage error and no call: err=%v ops=%v", args, err, c.ops)
		}
	}
	c, _, err := runQuestion(t, map[string]string{"SetQuestionLogDeciders": `{"data":{"setQuestionLogDeciders":` + questionLogJSON + `}}`},
		"log", "set-deciders", "l1", "--decider", "u2", "--eligible", "", "--expected-revision", "2", "--reason", "clear")
	if err != nil {
		t.Fatal(err)
	}
	v := questionOnlyOp(t, c, "SetQuestionLogDeciders")
	if e, ok := v["eligibleDeciders"].([]any); !ok || len(e) != 0 {
		t.Fatalf(`--eligible "" must send eligibleDeciders [] (clear), got %v`, v["eligibleDeciders"])
	}
}

func TestQuestionAskPassesAHugeNumberInGrantVerbatim(t *testing.T) {
	c, _, err := runQuestion(t, map[string]string{"AskQuestion": `{"data":{"askQuestion":` + questionJSON + `}}`},
		"ask", "l1", "--input-file", writeJSONFile(t, `{"grant":{"n":1e999},"title":"a"}`))
	if err != nil {
		t.Fatalf("a huge number in grant must not be refused: %v", err)
	}
	questionOnlyOp(t, c, "AskQuestion")
	var vars struct {
		Input struct {
			Title string          `json:"title"`
			Grant json.RawMessage `json:"grant"`
		} `json:"input"`
	}
	if err := json.Unmarshal(c.raw["AskQuestion"], &vars); err != nil {
		t.Fatalf("variables: %v", err)
	}
	if vars.Input.Title != "a" || string(vars.Input.Grant) != `{"n":1e999}` {
		t.Fatalf("grant must reach the server verbatim, got title=%q grant=%s", vars.Input.Title, vars.Input.Grant)
	}
}

// Jessie's P2: a coded error BELOW the mutation field (beside committed data) is not a
// refusal: it comes from resolving the returned question after the write committed, so
// answer and retract report an unknown outcome (exit 7, not resent, the read-back
// sentence) instead of a typed refusal (exit 8/4). A coded refusal AT the field, or with
// no path, still keeps its typed exit; a null result is an unknown outcome too.
func TestQuestionAnswerAndRetractErrorsBelowTheMutationAreUnknown(t *testing.T) {
	type tc struct {
		op, field string
		args      []string
	}
	for _, c := range []tc{
		{"AnswerQuestion", "answerQuestion", []string{"answer", "QA", "--expected-revision", "3", "--option", "pg"}},
		{"RetractQuestionAnswer", "retractQuestionAnswer", []string{"retract", "QA", "--expected-revision", "4"}},
	} {
		at := func(code, path string) string {
			return `{"data":{"` + c.field + `":null},"errors":[{"message":"x","path":` + path + `,"extensions":{"code":"` + code + `"}}]}`
		}
		for name, r := range map[string]struct {
			resp string
			exit int
		}{
			"FORBIDDEN below the field, beside data":   {`{"data":{"` + c.field + `":` + questionAnsweredJSON + `},"errors":[{"message":"x","path":["` + c.field + `","answers",0,"actor"],"extensions":{"code":"FORBIDDEN"}}]}`, exitcode.Unavailable},
			"NOT_FOUND below the field":                {at("NOT_FOUND", `["`+c.field+`","answer"]`), exitcode.Unavailable},
			"a pathless FORBIDDEN beside the question": {`{"data":{"` + c.field + `":` + questionAnsweredJSON + `},"errors":[{"message":"x","extensions":{"code":"FORBIDDEN"}}]}`, exitcode.Unavailable},
			"mixed at and below the field":             {`{"errors":[{"message":"x","path":["` + c.field + `"],"extensions":{"code":"FORBIDDEN"}},{"message":"y","path":["` + c.field + `","answers"],"extensions":{"code":"FORBIDDEN"}}]}`, exitcode.Unavailable},
			"an internal error at the field":           {at("INTERNAL_SERVER_ERROR", `["`+c.field+`"]`), exitcode.Unavailable},
			"a null result with no error":              {`{"data":{"` + c.field + `":null}}`, exitcode.Unavailable},
			"FORBIDDEN at the field keeps its exit":    {at("FORBIDDEN", `["`+c.field+`"]`), exitcode.Forbidden},
			"NOT_FOUND with no path keeps its exit":    {`{"errors":[{"message":"x","extensions":{"code":"NOT_FOUND"}}]}`, exitcode.NotFound},
			"a conflict at the field keeps its exit":   {at("NODE_WRITE_CONFLICT", `["`+c.field+`"]`), exitcode.Conflict},
		} {
			calls, out, err := runQuestion(t, map[string]string{c.op: r.resp}, c.args...)
			if exitCodeFor(err) != r.exit || len(calls.ops) != 1 {
				t.Errorf("%s %s: exit=%d want=%d sends=%d err=%v", c.args[0], name, exitCodeFor(err), r.exit, len(calls.ops), err)
				continue
			}
			if r.exit != exitcode.Unavailable && strings.Contains(err.Error(), "not confirmed") {
				t.Errorf("%s %s: a refusal must not claim an unknown outcome: %v", c.args[0], name, err)
			}
			if r.exit == exitcode.Unavailable {
				m := err.Error()
				if !strings.Contains(m, "not confirmed") || !strings.Contains(m, "not resent") || !strings.Contains(m, "hadron question get QA") || out != "" {
					t.Errorf("%s %s: an unknown outcome says so, is not resent and names question get: %v out=%q", c.args[0], name, err, out)
				}
			}
		}
	}
}

// Joran's P2 (rounds 1 and 2): what the HTTP layer refused BEFORE any resolver ran is not an
// unknown outcome, and nothing else is a refusal. A plain HTTP 400/401/403/404/405/413/429 (no
// data, no error below the mutation field) keeps the ordinary mapping and never says "not
// confirmed"; 408, 409, a 5xx, a dropped connection, and any status whose body shows data or an
// error below the mutation field are the unknown ones (exit 7, "not confirmed", not resent). A
// connection that never opened is "could not reach the server" (7) with no claim about the write.
// Both verbs; the exit code and the --json envelope are pinned, one request at most.
func TestQuestionWriteTransportShapesAreRefusalOrUnknownAccordingly(t *testing.T) {
	// cli#3112: only requests carrying this leg's own bearer token are the CLI's. Another process dialing
	// a recycled loopback port is not, and must not make a one-request leg read as two.
	var legs int32
	serve := func(t *testing.T, h func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *int32) {
		srv, own, _ := ownRequestServer(t, fmt.Sprintf("hdr_user_leg-%d", atomic.AddInt32(&legs, 1)), h)
		return srv, own
	}
	status := func(code int, body string) func(w http.ResponseWriter, r *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_, _ = w.Write([]byte(body))
		}
	}
	hangup := func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		}
	}
	coded := func(code string) string {
		return `{"errors":[{"message":"x","extensions":{"code":"` + code + `"}}]}`
	}
	for _, verb := range [][]string{
		{"answer", "QA", "--expected-revision", "3", "--option", "pg"},
		{"retract", "QA", "--expected-revision", "4"},
	} {
		field := map[string]string{"answer": "answerQuestion", "retract": "retractQuestionAnswer"}[verb[0]]
		for name, tc := range map[string]struct {
			h    func(w http.ResponseWriter, r *http.Request)
			exit int
			// unknown: the outcome is unknown (exit 7 "not confirmed")
			unknown bool
		}{
			"401 a refused credential":                 {status(401, `no`), exitcode.AuthRequired, false},
			"401 coded UNAUTHENTICATED":                {status(401, coded("UNAUTHENTICATED")), exitcode.AuthRequired, false},
			"403 plain":                                {status(403, `forbidden`), exitcode.Error, false},
			"403 coded FORBIDDEN":                      {status(403, coded("FORBIDDEN")), exitcode.Forbidden, false},
			"429 a rate limit":                         {status(429, `slow down`), exitcode.Error, false},
			"400 an invalid request":                   {status(400, `bad`), exitcode.Error, false},
			"404 plain":                                {status(404, `gone`), exitcode.NotFound, false},
			"408 a request timeout":                    {status(408, `slow`), exitcode.Unavailable, true},
			"409 a conflict page":                      {status(409, `again`), exitcode.Unavailable, true},
			"502 a gateway page":                       {status(502, `<html>bad gateway</html>`), exitcode.Unavailable, true},
			"500 a plain page":                         {status(500, `oops`), exitcode.Unavailable, true},
			"403 whose body shows data":                {status(403, `{"data":{"`+field+`":`+questionAnsweredJSON+`},"errors":[{"message":"x","extensions":{"code":"FORBIDDEN"}}]}`), exitcode.Unavailable, true},
			"403 with an error below the mutation":     {status(403, `{"data":null,"errors":[{"message":"x","path":["`+field+`","answers",0,"actor"],"extensions":{"code":"FORBIDDEN"}}]}`), exitcode.Unavailable, true},
			"the connection dropped after the request": {hangup, exitcode.Unavailable, true},
		} {
			for _, asJSON := range []bool{false, true} {
				f, out := testFactory(t)
				srv, own := serve(t, tc.h) // after testFactory: it sets this leg's HADRON_TOKEN over the default
				args := append([]string{"question"}, verb...)
				if asJSON {
					args = append(args, "--json")
				}
				var errOut strings.Builder
				f.IOStreams.ErrOut = &errOut
				root := NewRootCmd(f)
				root.SetArgs(append(args, "--server", srv.URL))
				err := root.Execute()
				if err == nil || atomic.LoadInt32(own) != 1 {
					t.Errorf("%s %s json=%v: err=%v requests=%d, want an error after exactly one request", verb[0], name, asJSON, err, atomic.LoadInt32(own))
					continue
				}
				if code := renderError(f, err); code != tc.exit {
					t.Errorf("%s %s json=%v: exit=%d want %d: %v", verb[0], name, asJSON, code, tc.exit, err)
				}
				has := strings.Contains(err.Error(), "not confirmed")
				if has != tc.unknown {
					t.Errorf("%s %s json=%v: 'not confirmed'=%v want %v: %v", verb[0], name, asJSON, has, tc.unknown, err)
				}
				if asJSON {
					var env map[string]any
					if jerr := json.Unmarshal([]byte(out.String()), &env); jerr != nil {
						t.Errorf("%s %s: --json stdout is not one JSON document: %v %q", verb[0], name, jerr, out.String())
					} else {
						inner, _ := env["error"].(map[string]any)
						if got, _ := inner["code"].(float64); int(got) != tc.exit || strings.Contains(out.String(), "not confirmed") != tc.unknown {
							t.Errorf("%s %s: the envelope must carry code %d and 'not confirmed' only when unknown: %q", verb[0], name, tc.exit, out.String())
						}
					}
				}
			}
		}
		// A connection that never opened: nothing was sent, and nothing is claimed about the write.
		url := neverOpenedServerURL
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{"question"}, verb...), "--server", url))
		err := root.Execute()
		if err == nil || strings.Contains(err.Error(), "not confirmed") || exitCodeFor(err) != exitcode.Unavailable || !strings.Contains(err.Error(), "could not reach") {
			t.Errorf("%s a connection that never opened: want exit 7 'could not reach the server' without 'not confirmed', got exit=%d %v", verb[0], exitCodeFor(err), err)
		}
	}
}
