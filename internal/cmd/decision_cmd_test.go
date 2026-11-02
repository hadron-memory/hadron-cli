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

// cli#1335: the `decision` group wraps the ten #1641 operations. Every verb is
// exactly one call; refs go to the server as typed; who may do what is the
// server's rule (cor:api:240:01), so the CLI's only refusals are usage ones.

const decisionJSON = `{"id":"d1","urn":"hrn:node:acme.com:kb:decisions:DA","code":"DA","n":1,"logId":"l1",
"title":"Pick a db","context":null,"state":"OPEN","tier":"APPROVED","urgency":"NORMAL","authorizes":false,
"archival":false,"revSeq":3,"portalUrl":"https://example.test/d/DA","viewerIsDecider":true,
"askedAt":"2026-11-01T00:00:00Z","answeredAt":null,
"askedBy":{"userId":"u1","user":{"id":"u1","name":"Ann","handle":"ann","urn":null},"workerId":null,"worker":null,"appId":null,"app":null},
"deciderIds":["u2"],"deciders":[{"id":"u2","name":"Bo","handle":"bo","urn":null}],"identityMerges":[],
"parts":[{"n":1,"citation":"DA-1","question":"Which?","options":[{"key":"pg","label":"Postgres","meaning":null,"cost":null}],
"recommendation":{"key":"pg","reason":"boring","attributedTo":null,"by":{"userId":"u1","user":null,"workerId":null,"worker":null,"appId":null,"app":null}},
"noRecommendationReason":null,"answer":null,"answerHistory":[]}],
"blocks":[],"followUps":[],"sources":[],"withdrawn":null,"evidence":{"approval":null}}`

const decisionLogJSON = `{"id":"l1","urn":"hrn:node:acme.com:kb:decisions","loc":"decisions","name":"Decisions","prefix":"D",
"lastCode":1,"revSeq":2,"portalUrl":null,"defaultDeciderIds":["u2"],"defaultDeciders":[{"id":"u2","name":"Bo","handle":"bo","urn":null}],
"eligibleDeciderIds":[],"eligibleDeciders":[],"identityMerges":[],"purges":[]}`

type decisionCall struct {
	ops  []string
	vars map[string]map[string]any
}

func runDecision(t *testing.T, responses map[string]string, args ...string) (*decisionCall, string, error) {
	t.Helper()
	calls := &decisionCall{vars: map[string]map[string]any{}}
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		calls.ops = append(calls.ops, op)
		return responses[op]
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"decision"}, args...), "--server", gql.URL))
	err := root.Execute()
	for op, raw := range captured {
		v := map[string]any{}
		_ = json.Unmarshal(raw, &v)
		calls.vars[op] = v
	}
	return calls, out.String(), err
}

func decisionOnlyOp(t *testing.T, c *decisionCall, op string) map[string]any {
	t.Helper()
	if len(c.ops) != 1 || c.ops[0] != op {
		t.Fatalf("want exactly one %s, got %v", op, c.ops)
	}
	return c.vars[op]
}

func decisionAbsent(t *testing.T, v map[string]any, keys ...string) {
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

// --- decision log ---

func TestDecisionLogCreateSendsArgsVerbatimAndOmitsUnset(t *testing.T) {
	c, _, err := runDecision(t, map[string]string{
		"CreateDecisionLog": `{"data":{"createDecisionLog":` + decisionLogJSON + `}}`,
	}, "log", "create", "hrn:mem:acme.com:kb", "--loc", "decisions", "--decider", "u2", "--decider", "hrn:worker:acme.com:t:bo")
	if err != nil {
		t.Fatal(err)
	}
	v := decisionOnlyOp(t, c, "CreateDecisionLog")
	if v["memoryRef"] != "hrn:mem:acme.com:kb" || v["loc"] != "decisions" {
		t.Fatalf("vars: %v", v)
	}
	if !reflect.DeepEqual(v["defaultDeciders"], []any{"u2", "hrn:worker:acme.com:t:bo"}) {
		t.Fatalf("defaultDeciders: %v", v["defaultDeciders"])
	}
	decisionAbsent(t, v, "name", "prefix", "eligibleDeciders")
}

func TestDecisionLogCreateSendsOptionalsWhenGiven(t *testing.T) {
	c, _, err := runDecision(t, map[string]string{
		"CreateDecisionLog": `{"data":{"createDecisionLog":` + decisionLogJSON + `}}`,
	}, "log", "create", "m1", "--loc", "d", "--decider", "u2", "--name", "Rulings", "--prefix", "R", "--eligible", "u3")
	if err != nil {
		t.Fatal(err)
	}
	v := decisionOnlyOp(t, c, "CreateDecisionLog")
	if v["name"] != "Rulings" || v["prefix"] != "R" || !reflect.DeepEqual(v["eligibleDeciders"], []any{"u3"}) {
		t.Fatalf("vars: %v", v)
	}
}

func TestDecisionLogCreateRequiresLocAndDecider(t *testing.T) {
	for _, args := range [][]string{
		{"log", "create", "m1", "--decider", "u2"},
		{"log", "create", "m1", "--loc", "d"},
	} {
		c, _, err := runDecision(t, nil, args...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
			t.Fatalf("%v: want usage error, no call; err=%v ops=%v", args, err, c.ops)
		}
	}
}

func TestDecisionLogGetJSONAndNotFound(t *testing.T) {
	c, out, err := runDecision(t, map[string]string{
		"DecisionLog": `{"data":{"decisionLog":` + decisionLogJSON + `}}`,
	}, "log", "get", "hrn:node:acme.com:kb:decisions", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if v := decisionOnlyOp(t, c, "DecisionLog"); v["ref"] != "hrn:node:acme.com:kb:decisions" {
		t.Fatalf("vars: %v", v)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if got["prefix"] != "D" || got["revision"] != float64(2) {
		t.Fatalf("dto: %v", got)
	}
	for _, k := range []string{"eligibleDeciders", "eligibleDeciderIds", "purges", "identityMerges"} {
		if l, ok := got[k].([]any); !ok || len(l) != 0 {
			t.Fatalf("%s must render [], got %v", k, got[k])
		}
	}

	_, _, err = runDecision(t, map[string]string{"DecisionLog": `{"data":{"decisionLog":null}}`}, "log", "get", "x")
	if err == nil || exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("null log must be not-found, got %v", err)
	}
}

func TestDecisionLogSetDecidersRequiresRevisionReasonDecider(t *testing.T) {
	for _, args := range [][]string{
		{"log", "set-deciders", "l1", "--decider", "u2", "--reason", "r"},
		{"log", "set-deciders", "l1", "--decider", "u2", "--expected-revision", "2"},
		{"log", "set-deciders", "l1", "--expected-revision", "2", "--reason", "r"},
	} {
		c, _, err := runDecision(t, nil, args...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
			t.Fatalf("%v: want usage error, no call; err=%v ops=%v", args, err, c.ops)
		}
	}
	c, _, err := runDecision(t, map[string]string{
		"SetDecisionLogDeciders": `{"data":{"setDecisionLogDeciders":` + decisionLogJSON + `}}`,
	}, "log", "set-deciders", "l1", "--decider", "u2", "--expected-revision", "2", "--reason", "rotate")
	if err != nil {
		t.Fatal(err)
	}
	v := decisionOnlyOp(t, c, "SetDecisionLogDeciders")
	if v["logRef"] != "l1" || v["expectedRevision"] != float64(2) || v["reason"] != "rotate" ||
		!reflect.DeepEqual(v["defaultDeciders"], []any{"u2"}) {
		t.Fatalf("vars: %v", v)
	}
	decisionAbsent(t, v, "eligibleDeciders")
}

// --- ask / amend: the input object is passed through unchanged ---

func TestDecisionAskPassesInputFileThrough(t *testing.T) {
	in := `{"title":"Pick a db","parts":[{"question":"Which?","options":[{"key":"pg","label":"Postgres"}],"recommendation":{"key":"pg"}}],"blocks":[{"ref":"hadron-server#1"}]}`
	c, _, err := runDecision(t, map[string]string{
		"AskDecision": `{"data":{"askDecision":` + decisionJSON + `}}`,
	}, "ask", "hrn:node:acme.com:kb:decisions", "--input-file", writeJSONFile(t, in))
	if err != nil {
		t.Fatal(err)
	}
	v := decisionOnlyOp(t, c, "AskDecision")
	if v["logRef"] != "hrn:node:acme.com:kb:decisions" {
		t.Fatalf("logRef: %v", v["logRef"])
	}
	var want any
	_ = json.Unmarshal([]byte(in), &want)
	if !reflect.DeepEqual(v["input"], want) {
		t.Fatalf("input must reach the server unchanged:\n got %v\nwant %v", v["input"], want)
	}
}

func TestDecisionAskFlagsOverrideTopLevelKeys(t *testing.T) {
	in := `{"title":"old","parts":[{"question":"Q","yesno":true,"noRecommendation":{"reason":"none"}}]}`
	c, _, err := runDecision(t, map[string]string{
		"AskDecision": `{"data":{"askDecision":` + decisionJSON + `}}`,
	}, "ask", "l1", "--input-file", writeJSONFile(t, in), "--title", "new", "--context", "ctx", "--tier", "SIGNED", "--urgency", "URGENT")
	if err != nil {
		t.Fatal(err)
	}
	input, _ := decisionOnlyOp(t, c, "AskDecision")["input"].(map[string]any)
	if input["title"] != "new" || input["context"] != "ctx" || input["tier"] != "SIGNED" || input["urgency"] != "URGENT" {
		t.Fatalf("input: %v", input)
	}
	if _, ok := input["parts"]; !ok {
		t.Fatalf("parts from the file must survive: %v", input)
	}
}

func TestDecisionAskRefusesBadInputBeforeAnyCall(t *testing.T) {
	for name, args := range map[string][]string{
		"no input":           {"ask", "l1"},
		"array":              {"ask", "l1", "--input-file", writeJSONFile(t, `[1]`)},
		"trailing":           {"ask", "l1", "--input-file", writeJSONFile(t, `{"title":"a"} {}`)},
		"not json":           {"ask", "l1", "--input-file", writeJSONFile(t, `{title`)},
		"null":               {"ask", "l1", "--input-file", writeJSONFile(t, `null`)},
		"empty file":         {"ask", "l1", "--input-file", writeJSONFile(t, ``)},
		"unknown key":        {"ask", "l1", "--input-file", writeJSONFile(t, `{"titel":"typo","title":"t","parts":[]}`)},
		"nested unknown key": {"ask", "l1", "--input-file", writeJSONFile(t, `{"title":"t","parts":[{"question":"q","yes_no":true}]}`)},
	} {
		c, _, err := runDecision(t, nil, args...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
			t.Fatalf("%s: want usage error, no call; err=%v ops=%v", name, err, c.ops)
		}
	}
}

func TestDecisionAmendRequiresRevisionReasonAndAChange(t *testing.T) {
	for name, args := range map[string][]string{
		"no revision": {"amend", "d1", "--reason", "r", "--title", "t"},
		"no reason":   {"amend", "d1", "--expected-revision", "3", "--title", "t"},
		"no change":   {"amend", "d1", "--expected-revision", "3", "--reason", "r"},
		"empty file":  {"amend", "d1", "--expected-revision", "3", "--reason", "r", "--input-file", writeJSONFile(t, `{}`)},
	} {
		c, _, err := runDecision(t, nil, args...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
			t.Fatalf("%s: want usage error, no call; err=%v ops=%v", name, err, c.ops)
		}
	}
}

func TestDecisionAmendSendsOnlyGivenFields(t *testing.T) {
	c, _, err := runDecision(t, map[string]string{
		"AmendDecision": `{"data":{"amendDecision":` + decisionJSON + `}}`,
	}, "amend", "hrn:node:acme.com:kb:decisions:DA", "--expected-revision", "3", "--reason", "typo", "--urgency", "URGENT")
	if err != nil {
		t.Fatal(err)
	}
	v := decisionOnlyOp(t, c, "AmendDecision")
	if v["ref"] != "hrn:node:acme.com:kb:decisions:DA" || v["expectedRevision"] != float64(3) || v["reason"] != "typo" {
		t.Fatalf("vars: %v", v)
	}
	if !reflect.DeepEqual(v["input"], map[string]any{"urgency": "URGENT"}) {
		t.Fatalf("amend must send only the given field (omitted = unchanged), got %v", v["input"])
	}
}

func TestDecisionAmendMergesFileAndFlags(t *testing.T) {
	in := `{"addParts":[{"question":"Also?","yesno":true,"noRecommendation":{"reason":"n"}}],"title":"file"}`
	c, _, err := runDecision(t, map[string]string{
		"AmendDecision": `{"data":{"amendDecision":` + decisionJSON + `}}`,
	}, "amend", "d1", "--expected-revision", "3", "--reason", "r", "--input-file", writeJSONFile(t, in), "--title", "flag")
	if err != nil {
		t.Fatal(err)
	}
	input, _ := decisionOnlyOp(t, c, "AmendDecision")["input"].(map[string]any)
	if input["title"] != "flag" || input["addParts"] == nil {
		t.Fatalf("input: %v", input)
	}
}

// --- get / list / verify ---

func TestDecisionGetByRefOrByCode(t *testing.T) {
	resp := map[string]string{"Decision": `{"data":{"decision":` + decisionJSON + `}}`}
	c, _, err := runDecision(t, resp, "get", "hrn:node:acme.com:kb:decisions:DA")
	if err != nil {
		t.Fatal(err)
	}
	v := decisionOnlyOp(t, c, "Decision")
	if v["ref"] != "hrn:node:acme.com:kb:decisions:DA" {
		t.Fatalf("vars: %v", v)
	}
	decisionAbsent(t, v, "code", "logRef")

	c, _, err = runDecision(t, resp, "get", "DA-1", "--log", "l1")
	if err != nil {
		t.Fatal(err)
	}
	v = decisionOnlyOp(t, c, "Decision")
	if v["code"] != "DA-1" || v["logRef"] != "l1" {
		t.Fatalf("vars: %v", v)
	}
	decisionAbsent(t, v, "ref")

	_, _, err = runDecision(t, map[string]string{"Decision": `{"data":{"decision":null}}`}, "get", "x")
	if err == nil || exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("null decision must be not-found, got %v", err)
	}
}

func TestDecisionGetJSONShape(t *testing.T) {
	_, out, err := runDecision(t, map[string]string{"Decision": `{"data":{"decision":` + decisionJSON + `}}`}, "get", "d1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if got["code"] != "DA" || got["state"] != "OPEN" || got["revision"] != float64(3) {
		t.Fatalf("dto: %v", got)
	}
	for _, k := range []string{"blocks", "followUps", "sources", "identityMerges"} {
		if l, ok := got[k].([]any); !ok || len(l) != 0 {
			t.Fatalf("%s must render [], got %v", k, got[k])
		}
	}
	parts, _ := got["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("parts: %v", got["parts"])
	}
	p := parts[0].(map[string]any)
	if p["citation"] != "DA-1" || p["answer"] != nil {
		t.Fatalf("part: %v", p)
	}
	if h, ok := p["answerHistory"].([]any); !ok || len(h) != 0 {
		t.Fatalf("answerHistory must render [], got %v", p["answerHistory"])
	}
	if _, ok := got["withdrawn"]; !ok || got["withdrawn"] != nil {
		t.Fatalf("withdrawn must render null, got %v", got["withdrawn"])
	}
}

func TestDecisionListNeedsAScope(t *testing.T) {
	c, _, err := runDecision(t, nil, "list")
	if err == nil || exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
		t.Fatalf("want usage error, no call; err=%v ops=%v", err, c.ops)
	}
}

func TestDecisionListSendsFiltersAndOmitsUnset(t *testing.T) {
	resp := map[string]string{"Decisions": `{"data":{"decisions":{"total":0,"items":[]}}}`}
	c, out, err := runDecision(t, resp, "list", "--log", "l1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	v := decisionOnlyOp(t, c, "Decisions")
	if v["logRef"] != "l1" {
		t.Fatalf("vars: %v", v)
	}
	decisionAbsent(t, v, "awaitingMe", "authorizes", "state", "tier", "urgency", "sort", "limit", "offset")
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if l, ok := got["items"].([]any); !ok || len(l) != 0 || got["total"] != float64(0) {
		t.Fatalf("empty page must render items [] and total 0, got %v", got)
	}

	c, _, err = runDecision(t, resp, "list", "--awaiting-me", "--state", "OPEN,STALE", "--urgency", "URGENT",
		"--tier", "SIGNED", "--authorizes", "--sort", "CODE", "--limit", "5", "--offset", "10")
	if err != nil {
		t.Fatal(err)
	}
	v = decisionOnlyOp(t, c, "Decisions")
	if v["awaitingMe"] != true || v["authorizes"] != true || v["tier"] != "SIGNED" || v["sort"] != "CODE" ||
		v["limit"] != float64(5) || v["offset"] != float64(10) ||
		!reflect.DeepEqual(v["state"], []any{"OPEN", "STALE"}) || !reflect.DeepEqual(v["urgency"], []any{"URGENT"}) {
		t.Fatalf("vars: %v", v)
	}
	decisionAbsent(t, v, "logRef")
}

func TestDecisionListNoAuthorizesSendsFalse(t *testing.T) {
	resp := map[string]string{"Decisions": `{"data":{"decisions":{"total":0,"items":[]}}}`}
	c, _, err := runDecision(t, resp, "list", "--log", "l1", "--authorizes=false")
	if err != nil {
		t.Fatal(err)
	}
	if v := decisionOnlyOp(t, c, "Decisions"); v["authorizes"] != false {
		t.Fatalf("an explicit --authorizes=false must be sent, got %v", v)
	}
}

func TestDecisionListTable(t *testing.T) {
	_, out, err := runDecision(t, map[string]string{
		"Decisions": `{"data":{"decisions":{"total":1,"items":[` + decisionJSON + `]}}}`,
	}, "list", "--log", "l1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CODE", "STATE", "DA", "OPEN", "Pick a db"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q:\n%s", want, out)
		}
	}
}

func TestDecisionVerify(t *testing.T) {
	c, out, err := runDecision(t, map[string]string{
		"VerifyDecision": `{"data":{"verifyDecision":{"state":"OPEN","revision":3,"approvedRevision":null,
		"approvalIntegrity":"NOT_APPROVED","approvalByDecider":false,"codec":"v1","renderer":"v1","findings":[]}}}`,
	}, "verify", "d1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if v := decisionOnlyOp(t, c, "VerifyDecision"); v["ref"] != "d1" {
		t.Fatalf("vars: %v", v)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if l, ok := got["findings"].([]any); !ok || len(l) != 0 || got["approvedRevision"] != nil {
		t.Fatalf("dto: %v", got)
	}
}

// --- withdraw / purge: destructive, confirmed before the call ---

func TestDecisionWithdrawConfirmsThenSendsOneCall(t *testing.T) {
	c, _, err := runDecision(t, nil, "withdraw", "d1", "--expected-revision", "3", "--reason", "moot")
	if err == nil || !strings.Contains(err.Error(), "--yes") || len(c.ops) != 0 {
		t.Fatalf("non-TTY withdraw without --yes must refuse before any call: err=%v ops=%v", err, c.ops)
	}
	for _, args := range [][]string{
		{"withdraw", "d1", "--reason", "moot", "--yes"},
		{"withdraw", "d1", "--expected-revision", "3", "--yes"},
		{"withdraw", "d1", "--expected-revision", "3", "--reason", "  ", "--yes"},
	} {
		c, _, err := runDecision(t, nil, args...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
			t.Fatalf("%v: want usage error, no call; err=%v ops=%v", args, err, c.ops)
		}
	}
	c, _, err = runDecision(t, map[string]string{
		"WithdrawDecision": `{"data":{"withdrawDecision":` + decisionJSON + `}}`,
	}, "withdraw", "d1", "--expected-revision", "3", "--reason", "moot", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	v := decisionOnlyOp(t, c, "WithdrawDecision")
	if v["ref"] != "d1" || v["reason"] != "moot" || v["expectedRevision"] != float64(3) {
		t.Fatalf("vars: %v", v)
	}
}

func TestDecisionPurgeConfirmsAndRequiresReason(t *testing.T) {
	c, _, err := runDecision(t, nil, "purge", "d1", "--reason", "spam")
	if err == nil || !strings.Contains(err.Error(), "--yes") || len(c.ops) != 0 {
		t.Fatalf("non-TTY purge without --yes must refuse before any call: err=%v ops=%v", err, c.ops)
	}
	c, _, err = runDecision(t, nil, "purge", "d1", "--yes")
	if err == nil || exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
		t.Fatalf("purge without --reason: want usage error, no call; err=%v ops=%v", err, c.ops)
	}
	c, out, err := runDecision(t, map[string]string{
		"PurgeUnansweredDecision": `{"data":{"purgeUnansweredDecision":true}}`,
	}, "purge", "d1", "--reason", "spam", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	v := decisionOnlyOp(t, c, "PurgeUnansweredDecision")
	if v["ref"] != "d1" || v["reason"] != "spam" {
		t.Fatalf("vars: %v", v)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["purged"] != true || got["ref"] != "d1" {
		t.Fatalf("json: %v %v", err, out)
	}
}

func TestDecisionPurgeFalseIsAnError(t *testing.T) {
	_, _, err := runDecision(t, map[string]string{
		"PurgeUnansweredDecision": `{"data":{"purgeUnansweredDecision":false}}`,
	}, "purge", "d1", "--reason", "spam", "--yes")
	if err == nil || exitCodeFor(err) != exitcode.Error {
		t.Fatalf("a false purge result must not report success, got %v", err)
	}
}

// --- refusals map through api.MapError, with no retry or follow-up call ---

func TestDecisionRefusalsMapAndDoNothingElse(t *testing.T) {
	for _, tc := range []struct {
		name string
		op   string
		args []string
		code string
		want int
	}{
		{"purge not admin", "PurgeUnansweredDecision", []string{"purge", "d1", "--reason", "r", "--yes"}, "FORBIDDEN", exitcode.Forbidden},
		{"amend conflict", "AmendDecision", []string{"amend", "d1", "--expected-revision", "1", "--reason", "r", "--title", "t"}, "NODE_WRITE_CONFLICT", exitcode.Conflict},
		{"ask invalid", "AskDecision", []string{"ask", "l1", "--input-file", writeJSONFile(t, `{"title":"t","parts":[]}`)}, "BAD_USER_INPUT", exitcode.Usage},
		{"log missing", "DecisionLog", []string{"log", "get", "l9"}, "NOT_FOUND", exitcode.NotFound},
	} {
		c, out, err := runDecision(t, map[string]string{
			tc.op: `{"errors":[{"message":"refused","extensions":{"code":"` + tc.code + `"}}]}`,
		}, tc.args...)
		if err == nil || exitCodeFor(err) != tc.want {
			t.Fatalf("%s: want exit %d, got %v (%d)", tc.name, tc.want, err, exitCodeFor(err))
		}
		if len(c.ops) != 1 || out != "" {
			t.Fatalf("%s: one call, no output: ops=%v out=%q", tc.name, c.ops, out)
		}
	}
}
