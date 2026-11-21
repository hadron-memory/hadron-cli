package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
		"answer", "hrn:node:acme.com:kb:questions:QA", "--expected-revision", "3", "--option", "yes")
	if err != nil {
		t.Fatal(err)
	}
	v := questionOnlyOp(t, c, "AnswerQuestion")
	want := map[string]any{
		"ref": "hrn:node:acme.com:kb:questions:QA", "expectedRevision": float64(3),
		"input": map[string]any{"optionKey": "yes", "via": "CLI"},
	}
	if !reflect.DeepEqual(v, want) {
		t.Fatalf("variables = %v, want exactly %v", v, want)
	}
	if !strings.HasPrefix(out, "✓ answered QA: yes\n") || !strings.Contains(out, "answer: pg") {
		t.Fatalf("out: %q", out)
	}
	c, _, err = runQuestion(t, map[string]string{"AnswerQuestion": `{"data":{"answerQuestion":` + questionAnsweredJSON + `}}`},
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
	c, out, err := runQuestion(t, map[string]string{"AnswerQuestion": `{"data":{"answerQuestion":` + questionAnsweredJSON + `}}`}, "answer", "QA", "--expected-revision", "3", "--text-file", p)
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
	gql, captured := captureGraphQLFunc(t, func(op string) string { return `{"data":{"answerQuestion":` + questionAnsweredJSON + `}}` })
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
	c, out, err := runQuestion(t, map[string]string{"RetractQuestionAnswer": `{"data":{"retractQuestionAnswer":` + questionJSON + `}}`},
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
	c, _, _ = runQuestion(t, map[string]string{"RetractQuestionAnswer": `{"data":{"retractQuestionAnswer":` + questionJSON + `}}`}, "retract", "QA", "--expected-revision", "4")
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
			if exitCodeFor(err) != exitcode.Usage || err == nil || err.Error() != "hadron decision was renamed to hadron question (hadron-server#1641). Nothing was sent." {
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
