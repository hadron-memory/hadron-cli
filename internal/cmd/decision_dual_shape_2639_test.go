package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2639: hadron-server#4513 (Questions S2) changes the decision API's
// shape, and this CLI is released before it merges. Each command that touches a
// decision asks the server once which shape it speaks (does Question have
// background?) and then uses that shape's operations. These tests run every
// verb against both shapes.

// s2DecisionJSON is a question as an S2 server returns it: no parts, urgency,
// context or archival; one question with background, priority, options.
const s2DecisionJSON = `{"id":"q1","urn":"hrn:node:acme.com:kb:questions:QA","code":"QA","n":1,"logId":"l1",
"title":"Which database?","background":"we run two","whyNow":"launch","priority":"HIGH","tier":"ANSWERED","authorizes":false,
"state":"DECIDED","revSeq":5,"portalUrl":"https://example.test/q/QA","viewerIsDecider":true,
"askedAt":"2026-11-01T00:00:00Z","answeredAt":"2026-11-02T00:00:00Z","neededBy":"2026-12-01T00:00:00Z","defaultAction":"keep Postgres",
"askedBy":{"userId":"u1","user":{"id":"u1","name":"Ann","handle":"ann","urn":null},"workerId":null,"worker":null,"appId":null,"app":null},
"deciderIds":["u2"],"deciders":[{"id":"u2","name":"Bo","handle":"bo","urn":null}],"identityMerges":[],
"options":[{"key":"pg","label":"Postgres","whyChoose":"boring","whyNot":null,"risks":"ops load"},{"key":"my","label":"MySQL","whyChoose":null,"whyNot":"slower","risks":null}],
"recommendation":{"key":"pg","reason":"we run it","by":{"userId":"u1","user":null,"workerId":null,"worker":null,"appId":null,"app":null}},
"noRecommendationReason":null,
"answer":{"kind":"ANSWER","key":"pg","text":null,"reason":"agreed","byUserId":"u2","at":"2026-11-02T00:00:00Z","via":"CLI"},
"answers":[{"kind":"ANSWER","key":null,"text":"maybe","reason":null,"byUserId":"u3","at":"2026-11-01T12:00:00Z","via":"PORTAL"},
{"kind":"RETRACTION","key":null,"text":null,"reason":"unsure","byUserId":"u3","at":"2026-11-01T13:00:00Z","via":"PORTAL"},
{"kind":"ANSWER","key":"pg","text":null,"reason":"agreed","byUserId":"u2","at":"2026-11-02T00:00:00Z","via":"CLI"}],
"blocks":[{"ref":"hadron-server#1","node":null,"spec":null}],"sources":[],
"specsTouched":[{"ref":null,"node":null,"spec":"cor:api:240:01"}],"specsMustChange":[],
"withdrawn":null,"evidence":{"approval":{"approvedAt":"2026-11-02T01:00:00Z","revision":5,"integrity":"INTACT","byDecider":true,"byUserId":"u2","by":{"id":"u2","name":"Bo","handle":"bo","urn":null}}}}`

func s2(responses map[string]string) map[string]string {
	responses["DecisionShape"] = shapeS2JSON
	return responses
}

func TestDecisionGetOnS2MapsToTheOneDecisionShape(t *testing.T) {
	c, out, err := runDecision(t, s2(map[string]string{"DecisionS2": `{"data":{"question":` + s2DecisionJSON + `}}`}),
		"get", "hrn:node:acme.com:kb:questions:QA", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if c.probes != 1 {
		t.Fatalf("the shape is asked once, got %d", c.probes)
	}
	v := decisionOnlyOp(t, c, "DecisionS2")
	if v["ref"] != "hrn:node:acme.com:kb:questions:QA" {
		t.Fatalf("ref: %v", v)
	}
	decisionAbsent(t, v, "code", "logRef")
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	for k, want := range map[string]any{
		"background": "we run two", "context": "we run two", "whyNow": "launch",
		"priority": "HIGH", "urgency": "HIGH", "tier": "ANSWERED", "state": "DECIDED", "revision": float64(5),
		"neededBy": "2026-12-01T00:00:00Z", "defaultAction": "keep Postgres", "archival": false,
	} {
		if !reflect.DeepEqual(got[k], want) {
			t.Errorf("%s: got %v want %v", k, got[k], want)
		}
	}
	for _, k := range []string{"followUps", "specsMustChange"} {
		if l, ok := got[k].([]any); !ok || len(l) != 0 {
			t.Errorf("%s must be [] , got %v", k, got[k])
		}
	}
	if l, _ := got["specsTouched"].([]any); len(l) != 1 {
		t.Errorf("specsTouched: %v", got["specsTouched"])
	}
	parts, _ := got["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("an S2 decision is one part, got %v", got["parts"])
	}
	p, _ := parts[0].(map[string]any)
	if p["n"] != float64(1) || p["citation"] != "QA-1" || p["question"] != "Which database?" {
		t.Errorf("part: %v", p)
	}
	opts, _ := p["options"].([]any)
	o, _ := opts[0].(map[string]any)
	if o["whyChoose"] != "boring" || o["risks"] != "ops load" || o["meaning"] != nil || o["cost"] != nil {
		t.Errorf("option: %v", o)
	}
	rec, _ := p["recommendation"].(map[string]any)
	if rec["key"] != "pg" || rec["reason"] != "we run it" || rec["attributedTo"] != nil {
		t.Errorf("recommendation: %v", rec)
	}
	ans, _ := p["answer"].(map[string]any)
	if ans["key"] != "pg" || ans["kind"] != "ANSWER" || ans["freeform"] != false || ans["recordedByUserId"] != nil {
		t.Errorf("answer: %v", ans)
	}
	hist, _ := p["answerHistory"].([]any)
	if len(hist) != 3 {
		t.Fatalf("every answer-list entry is history, got %v", hist)
	}
	if h1, _ := hist[0].(map[string]any); h1["freeform"] != true || h1["text"] != "maybe" {
		t.Errorf("a free-text answer is freeform: %v", h1)
	}
	if h2, _ := hist[1].(map[string]any); h2["kind"] != "RETRACTION" || h2["freeform"] != false {
		t.Errorf("a retraction is kept, and is not an answer: %v", h2)
	}
	if ap, _ := got["approval"].(map[string]any); ap["integrity"] != "INTACT" {
		t.Errorf("approval: %v", got["approval"])
	}
}

func TestDecisionGetTextOnS2(t *testing.T) {
	_, out, err := runDecision(t, s2(map[string]string{"DecisionS2": `{"data":{"question":` + s2DecisionJSON + `}}`}), "get", "QA", "--log", "l1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"priority: HIGH", "background: we run two", "why now: launch", "QA-1  Which database?", "- pg: Postgres  (recommended)", "answer: pg (CLI"} {
		if !strings.Contains(out, want) {
			t.Errorf("text lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "urgency") || strings.Contains(out, "context:") {
		t.Errorf("an S2 decision is not shown with the old names:\n%s", out)
	}
}

func TestDecisionGetByCodeOnS2AndNotFound(t *testing.T) {
	c, _, err := runDecision(t, s2(map[string]string{"DecisionS2": `{"data":{"question":` + s2DecisionJSON + `}}`}), "get", "QA", "--log", "hrn:node:acme.com:kb:questions")
	if err != nil {
		t.Fatal(err)
	}
	v := decisionOnlyOp(t, c, "DecisionS2")
	if v["code"] != "QA" || v["logRef"] != "hrn:node:acme.com:kb:questions" {
		t.Fatalf("vars: %v", v)
	}
	_, _, err = runDecision(t, s2(map[string]string{"DecisionS2": `{"data":{"question":null}}`}), "get", "zz")
	if err == nil || exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("a missing decision is not found, got %v", err)
	}
}

func TestDecisionProbeFailureIsAClearError(t *testing.T) {
	for name, probe := range map[string]string{
		// A validation refusal about something other than background says nothing about the shape.
		"validation failure about another field": `{"errors":[{"message":"Unknown argument \"awaitingMe\" on field \"Query.questions\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
		"an empty answer":                        `{"data":null}`,
	} {
		c, _, err := runDecision(t, map[string]string{"DecisionShape": probe}, "get", "d1")
		if err == nil || !strings.Contains(err.Error(), "cannot tell which decision API this server speaks") {
			t.Fatalf("%s: want the probe's own error, got %v", name, err)
		}
		if len(c.ops) != 0 {
			t.Fatalf("%s: no decision operation may run on a guess, got %v", name, c.ops)
		}
	}
}

// shapeServer is a hand-written server for the probe: it answers by what the
// request says, so it can refuse introspection the way the hosted server does,
// and answer with HTTP statuses the shared fake does not.
type shapeServer struct {
	legacy  bool                        // an older server: it has no Question.background
	probe   func(w http.ResponseWriter) // overrides the probe's answer when set
	queries []string
	got     []string
}

func (s *shapeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"capabilities":[]}`))
		return
	}
	var body struct {
		OperationName string `json:"operationName"`
		Query         string `json:"query"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.queries = append(s.queries, body.Query)
	s.got = append(s.got, body.OperationName)
	w.Header().Set("Content-Type", "application/json")
	// The hosted server switches introspection off: HTTP 400, validation failed.
	if strings.Contains(body.Query, "__type") || strings.Contains(body.Query, "__schema") {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errors":[{"message":"GraphQL introspection is not allowed by Apollo Server, but the query contained __type.","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`))
		return
	}
	switch body.OperationName {
	case "DecisionShape":
		if s.probe != nil {
			s.probe(w)
			return
		}
		if s.legacy {
			w.WriteHeader(http.StatusBadRequest) // Apollo answers a validation failure with 400
			_, _ = w.Write([]byte(shapeLegacyJSON))
			return
		}
		_, _ = w.Write([]byte(shapeS2JSON))
	case "DecisionS2":
		_, _ = w.Write([]byte(`{"data":{"question":` + s2DecisionJSON + `}}`))
	case "Decision":
		_, _ = w.Write([]byte(`{"data":{"question":` + decisionJSON + `}}`))
	default:
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected operation ` + body.OperationName + `"}]}`))
	}
}

func runDecisionAgainst(t *testing.T, srv *shapeServer, args ...string) (string, error) {
	t.Helper()
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"decision"}, args...), "--server", ts.URL))
	err := root.Execute()
	return out.String(), err
}

// TestDecisionShapeProbeSurvivesAServerThatRefusesIntrospection pins the P1 of
// #2647's first head: the hosted server refuses __type and __schema, so a probe
// that used them made every decision command fail on the server this exists
// for. The probe must work on both shapes against a server that refuses them,
// and must never send one.
func TestDecisionShapeProbeSurvivesAServerThatRefusesIntrospection(t *testing.T) {
	for name, tc := range map[string]struct {
		legacy bool
		op     string
		want   string
	}{
		"S2 server":    {legacy: false, op: "DecisionS2", want: "Which database?"},
		"older server": {legacy: true, op: "Decision", want: "Pick a db"},
	} {
		srv := &shapeServer{legacy: tc.legacy}
		out, err := runDecisionAgainst(t, srv, "get", "d1")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(out, tc.want) {
			t.Fatalf("%s: out: %s", name, out)
		}
		if !reflect.DeepEqual(srv.got, []string{"DecisionShape", tc.op}) {
			t.Fatalf("%s: one probe, then that shape's read, got %v", name, srv.got)
		}
		for _, q := range srv.queries {
			if strings.Contains(q, "__type") || strings.Contains(q, "__schema") {
				t.Fatalf("%s: the probe must not use introspection: %s", name, q)
			}
		}
		if !strings.Contains(srv.queries[0], "background") {
			t.Fatalf("%s: the probe asks for Question.background: %s", name, srv.queries[0])
		}
	}
}

func TestDecisionShapeProbeOnlyReadsValidationFailureAsOlder(t *testing.T) {
	for name, tc := range map[string]struct {
		probe   func(w http.ResponseWriter)
		wantErr bool
		wantOp  string
	}{
		"401 unauthenticated is not older": {wantErr: true, probe: func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errors":[{"message":"sign in","extensions":{"code":"UNAUTHENTICATED"}}]}`))
		}},
		"a gateway's 502 is not older": {wantErr: true, probe: func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`<html>bad gateway</html>`))
		}},
		"a 400 with no GraphQL error is not older": {wantErr: true, probe: func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{}`))
		}},
		"a validation failure about another field is not older": {wantErr: true, probe: func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"message":"Cannot query field \"items\" on type \"QuestionPage\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`))
		}},
		"a validation failure beside another error is not older": {wantErr: true, probe: func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"message":"Cannot query field \"background\" on type \"Question\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}},{"message":"x","extensions":{"code":"INTERNAL"}}]}`))
		}},
		"an execution error on a 200 validated, so it is S2": {wantOp: "DecisionS2", probe: func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"errors":[{"message":"too many","extensions":{"code":"QUESTION_LOG_TOO_LARGE_FOR_FILTER"}}],"data":null}`))
		}},
	} {
		srv := &shapeServer{probe: tc.probe}
		_, err := runDecisionAgainst(t, srv, "get", "d1")
		if tc.wantErr {
			if err == nil || !strings.Contains(err.Error(), "cannot tell which decision API this server speaks") {
				t.Fatalf("%s: want the probe's own error, got %v", name, err)
			}
			if !reflect.DeepEqual(srv.got, []string{"DecisionShape"}) {
				t.Fatalf("%s: nothing runs after a failed probe, got %v", name, srv.got)
			}
			continue
		}
		if err != nil || len(srv.got) != 2 || srv.got[1] != tc.wantOp {
			t.Fatalf("%s: got %v %v", name, err, srv.got)
		}
	}
	// The exit code of the underlying failure is kept: a 401 stays an auth exit.
	srv := &shapeServer{probe: func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errors":[{"message":"sign in","extensions":{"code":"UNAUTHENTICATED"}}]}`))
	}}
	_, err := runDecisionAgainst(t, srv, "get", "d1")
	if err == nil || exitCodeFor(err) == exitcode.Error {
		t.Fatalf("an auth failure keeps its exit code, got %v (%d)", err, exitCodeFor(err))
	}
}

func TestDecisionListOnS2SendsPriorityAndAwaitingMe(t *testing.T) {
	page := `{"data":{"questions":{"total":1,"items":[` + s2DecisionJSON + `]}}}`
	for name, args := range map[string][]string{
		"--priority":                  {"list", "--log", "l1", "--priority", "HIGH,URGENT", "--state", "OPEN", "--tier", "ANSWERED"},
		"--urgency is the old name":   {"list", "--log", "l1", "--urgency", "URGENT"},
		"awaiting-me, no other flags": {"list", "--awaiting-me"},
	} {
		c, out, err := runDecision(t, s2(map[string]string{"DecisionsS2": page}), args...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		v := decisionOnlyOp(t, c, "DecisionsS2")
		decisionAbsent(t, v, "urgency")
		switch name {
		case "--priority":
			if !reflect.DeepEqual(v["priority"], []any{"HIGH", "URGENT"}) || !reflect.DeepEqual(v["state"], []any{"OPEN"}) || v["tier"] != "ANSWERED" || v["logRef"] != "l1" {
				t.Fatalf("%s: vars %v", name, v)
			}
			decisionAbsent(t, v, "awaitingMe", "limit", "offset", "sort", "authorizes")
		case "--urgency is the old name":
			if !reflect.DeepEqual(v["priority"], []any{"URGENT"}) {
				t.Fatalf("%s: vars %v", name, v)
			}
		default:
			if v["awaitingMe"] != true {
				t.Fatalf("%s: vars %v", name, v)
			}
			decisionAbsent(t, v, "priority", "logRef")
		}
		if !strings.Contains(out, "PRIORITY") || !strings.Contains(out, "HIGH") || strings.Contains(out, "URGENCY") {
			t.Fatalf("%s: the table shows the priority:\n%s", name, out)
		}
	}
}

func TestDecisionListPriorityVocabularyPerShape(t *testing.T) {
	// An older server has urgency NORMAL and URGENT: --priority maps onto it, and LOW or HIGH are refused before any operation.
	page := `{"data":{"questions":{"total":0,"items":[]}}}`
	c, _, err := runDecision(t, map[string]string{"Decisions": page}, "list", "--log", "l1", "--priority", "URGENT")
	if err != nil {
		t.Fatal(err)
	}
	if v := decisionOnlyOp(t, c, "Decisions"); !reflect.DeepEqual(v["urgency"], []any{"URGENT"}) {
		t.Fatalf("--priority on an older server is its urgency: %v", v)
	}
	c, _, err = runDecision(t, map[string]string{"Decisions": page}, "list", "--log", "l1", "--priority", "LOW")
	if err == nil || exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
		t.Fatalf("LOW on an older server is a usage error with no operation, got %v %v", err, c.ops)
	}
	// --urgency never takes LOW or HIGH, on either server, and is refused before the probe.
	c, _, err = runDecision(t, s2(map[string]string{}), "list", "--log", "l1", "--urgency", "HIGH")
	if err == nil || exitCodeFor(err) != exitcode.Usage || c.probes != 0 {
		t.Fatalf("--urgency HIGH is refused before any call, got %v probes %d", err, c.probes)
	}
	c, _, err = runDecision(t, s2(map[string]string{}), "list", "--log", "l1", "--urgency", "URGENT", "--priority", "LOW")
	if err == nil || exitCodeFor(err) != exitcode.Usage || c.probes != 0 {
		t.Fatalf("both spellings are refused before any call, got %v", err)
	}
}

func TestDecisionAskOnS2PassesTheFileAndFlagsMapToNewNames(t *testing.T) {
	in := `{"title":"old","options":[{"key":"pg","label":"Postgres","whyChoose":"boring"}],"recommendation":{"key":"pg","reason":"we run it"},"specsTouched":[{"spec":"cor:api:240:01"}],"neededBy":"2026-12-01T00:00:00Z","defaultAction":"keep"}`
	c, out, err := runDecision(t, s2(map[string]string{"AskDecisionS2": `{"data":{"askQuestion":` + s2DecisionJSON + `}}`}),
		"ask", "l1", "--input-file", writeJSONFile(t, in), "--title", "new", "--context", "bg", "--urgency", "URGENT", "--tier", "APPROVED")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "✓ asked QA") {
		t.Fatalf("out: %s", out)
	}
	input, _ := decisionOnlyOp(t, c, "AskDecisionS2")["input"].(map[string]any)
	if input["title"] != "new" || input["background"] != "bg" || input["priority"] != "URGENT" || input["tier"] != "APPROVED" {
		t.Fatalf("the old flag names map onto the new fields: %v", input)
	}
	if _, ok := input["context"]; ok {
		t.Fatalf("context is not an S2 field: %v", input)
	}
	if _, ok := input["urgency"]; ok {
		t.Fatalf("urgency is not an S2 field: %v", input)
	}
	for _, k := range []string{"options", "recommendation", "specsTouched", "neededBy", "defaultAction"} {
		if _, ok := input[k]; !ok {
			t.Fatalf("%s from the file must reach the server: %v", k, input)
		}
	}
	if v, ok := input["deciders"]; !ok || v != nil {
		t.Fatalf("deciders is null (the log's defaults), got %v (present %t)", v, ok)
	}
	// The new names do the same.
	c, _, err = runDecision(t, s2(map[string]string{"AskDecisionS2": `{"data":{"askQuestion":` + s2DecisionJSON + `}}`}),
		"ask", "l1", "--input-file", writeJSONFile(t, `{"title":"t","yesno":true}`), "--background", "bg", "--priority", "LOW")
	if err != nil {
		t.Fatal(err)
	}
	input, _ = decisionOnlyOp(t, c, "AskDecisionS2")["input"].(map[string]any)
	if input["background"] != "bg" || input["priority"] != "LOW" || input["yesno"] != true {
		t.Fatalf("input: %v", input)
	}
}

func TestDecisionAskOnS2RefusesTheOldShapeFile(t *testing.T) {
	// parts and context are the older server's keys. The file is valid for an older server, so it passes the pre-call check; against an S2 server it is refused once the shape is known, before the ask.
	in := `{"title":"t","parts":[{"question":"q","yesno":true,"noRecommendation":{"reason":"none"}}],"context":"c"}`
	c, _, err := runDecision(t, s2(map[string]string{}), "ask", "l1", "--input-file", writeJSONFile(t, in))
	if err == nil || exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
		t.Fatalf("want a usage error and no ask, got %v ops %v", err, c.ops)
	}
	if !strings.Contains(err.Error(), "parts") {
		t.Fatalf("the refusal names the key: %v", err)
	}
	// And the other way: an S2 file against an older server.
	c, _, err = runDecision(t, nil, "ask", "l1", "--input-file", writeJSONFile(t, `{"title":"t","options":[{"key":"a","label":"A"}]}`))
	if err == nil || exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
		t.Fatalf("an S2 file on an older server is a usage error with no ask, got %v ops %v", err, c.ops)
	}
}

func TestDecisionAskBadInputIsRefusedBeforeAnyCallForBothShapes(t *testing.T) {
	for name, in := range map[string]string{
		"unknown key":   `{"titel":"x","title":"t"}`,
		"duplicate key": `{"title":"a","title":"b"}`,
		"mis-cased":     `{"Title":"a"}`,
		"nested typo":   `{"title":"t","options":[{"key":"a","lable":"A"}]}`,
	} {
		c, _, err := runDecision(t, s2(map[string]string{}), "ask", "l1", "--input-file", writeJSONFile(t, in))
		if err == nil || exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 || c.probes != 0 {
			t.Fatalf("%s: want usage error and no call, got %v probes %d ops %v", name, err, c.probes, c.ops)
		}
	}
}

func TestDecisionAskFlagSpellingsConflict(t *testing.T) {
	for name, args := range map[string][]string{
		"background and context": {"--background", "a", "--context", "b"},
		"priority and urgency":   {"--priority", "HIGH", "--urgency", "URGENT"},
		"urgency out of range":   {"--urgency", "LOW"},
	} {
		c, _, err := runDecision(t, s2(map[string]string{}), append([]string{"ask", "l1", "--input-file", writeJSONFile(t, `{"title":"t"}`)}, args...)...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || c.probes != 0 {
			t.Fatalf("%s: want usage error before any call, got %v probes %d", name, err, c.probes)
		}
	}
}

func TestDecisionAskOnLegacyMapsTheNewNamesToTheOldFields(t *testing.T) {
	c, _, err := runDecision(t, map[string]string{"AskDecision": `{"data":{"askQuestion":` + decisionJSON + `}}`},
		"ask", "l1", "--input-file", writeJSONFile(t, `{"title":"t","parts":[{"question":"q","yesno":true,"noRecommendation":{"reason":"n"}}]}`),
		"--background", "bg", "--priority", "URGENT")
	if err != nil {
		t.Fatal(err)
	}
	input, _ := decisionOnlyOp(t, c, "AskDecision")["input"].(map[string]any)
	if input["context"] != "bg" || input["urgency"] != "URGENT" {
		t.Fatalf("--background is context and --priority is urgency on an older server: %v", input)
	}
	if _, ok := input["background"]; ok {
		t.Fatalf("background is not an older field: %v", input)
	}
	_, _, err = runDecision(t, map[string]string{}, "ask", "l1", "--input-file", writeJSONFile(t, `{"title":"t","parts":[{"question":"q","yesno":true,"noRecommendation":{"reason":"n"}}]}`), "--priority", "HIGH")
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("HIGH on an older server is a usage error, got %v", err)
	}
}

func TestDecisionAmendOnS2(t *testing.T) {
	ok := s2(map[string]string{"AmendDecisionS2": `{"data":{"amendQuestion":` + s2DecisionJSON + `}}`})
	// A flag alone: only that field is sent.
	c, out, err := runDecision(t, ok, "amend", "QA", "--expected-revision", "5", "--reason", "r", "--priority", "HIGH")
	if err != nil {
		t.Fatal(err)
	}
	v := decisionOnlyOp(t, c, "AmendDecisionS2")
	if v["ref"] != "QA" || v["expectedRevision"] != float64(5) || v["reason"] != "r" {
		t.Fatalf("vars: %v", v)
	}
	if !reflect.DeepEqual(v["input"], map[string]any{"priority": "HIGH"}) {
		t.Fatalf("only the given field is sent: %v", v["input"])
	}
	if !strings.HasPrefix(out, "✓ amended QA") {
		t.Fatalf("out: %s", out)
	}
	// Tri-state: null clears, [] clears a list, absent is unchanged.
	c, _, err = runDecision(t, ok, "amend", "QA", "--expected-revision", "5", "--reason", "r",
		"--input-file", writeJSONFile(t, `{"background":null,"whyNow":"now","blocks":[],"specsTouched":[{"spec":"x"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"background": nil, "whyNow": "now", "blocks": []any{}, "specsTouched": []any{map[string]any{"spec": "x"}}}
	if input := decisionOnlyOp(t, c, "AmendDecisionS2")["input"]; !reflect.DeepEqual(input, any(want)) {
		t.Fatalf("clears must reach the server:\n got %v\nwant %v", input, want)
	}
	// --context is the old name of --background and overrides the file's null.
	c, _, err = runDecision(t, ok, "amend", "QA", "--expected-revision", "5", "--reason", "r",
		"--input-file", writeJSONFile(t, `{"background":null}`), "--context", "bg")
	if err != nil {
		t.Fatal(err)
	}
	if input, _ := decisionOnlyOp(t, c, "AmendDecisionS2")["input"].(map[string]any); input["background"] != "bg" {
		t.Fatalf("input: %v", input)
	}
}

func TestDecisionAmendNothingToChangeOnS2(t *testing.T) {
	// {} and no flags: refused before any call. An input empty only to the S2 shape (an empty options list) is refused once the shape is known, and sends no amend.
	c, _, err := runDecision(t, s2(map[string]string{}), "amend", "QA", "--expected-revision", "5", "--reason", "r", "--input-file", writeJSONFile(t, `{}`))
	if err == nil || exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 || c.probes != 0 {
		t.Fatalf("want a usage error before any call, got %v probes %d", err, c.probes)
	}
	c, _, err = runDecision(t, s2(map[string]string{}), "amend", "QA", "--expected-revision", "5", "--reason", "r", "--input-file", writeJSONFile(t, `{"options":[]}`))
	if err == nil || exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
		t.Fatalf("want a usage error and no amend, got %v ops %v", err, c.ops)
	}
}

func TestDecisionWithdrawOnS2(t *testing.T) {
	c, out, err := runDecision(t, s2(map[string]string{"WithdrawDecisionS2": `{"data":{"withdrawQuestion":` + s2DecisionJSON + `}}`}),
		"withdraw", "QA", "--expected-revision", "5", "--reason", "moot", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	v := decisionOnlyOp(t, c, "WithdrawDecisionS2")
	if v["ref"] != "QA" || v["reason"] != "moot" || v["expectedRevision"] != float64(5) {
		t.Fatalf("vars: %v", v)
	}
	if !strings.HasPrefix(out, "✓ withdrew QA") {
		t.Fatalf("out: %s", out)
	}
}

func TestDecisionCommandsThatTouchNoDecisionDoNotProbe(t *testing.T) {
	c, _, err := runDecision(t, map[string]string{"VerifyDecision": `{"data":{"verifyQuestion":{"state":"DECIDED","revision":1,"approvedRevision":1,"approvalIntegrity":"INTACT","approvalByDecider":true,"codec":null,"renderer":null,"findings":[]}}}`}, "verify", "d1")
	if err != nil {
		t.Fatal(err)
	}
	if c.probes != 0 {
		t.Fatalf("verify reads the same fields on both shapes and must not probe, probes %d", c.probes)
	}
	c, _, err = runDecision(t, map[string]string{"DecisionLog": `{"data":{"questionLog":` + decisionLogJSON + `}}`}, "log", "get", "l1")
	if err != nil {
		t.Fatal(err)
	}
	if c.probes != 0 {
		t.Fatalf("log get must not probe, probes %d", c.probes)
	}
}
