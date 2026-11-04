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
	raw  map[string]json.RawMessage // each operation's variables as sent
}

func runDecision(t *testing.T, responses map[string]string, args ...string) (*decisionCall, string, error) {
	t.Helper()
	calls := &decisionCall{vars: map[string]map[string]any{}, raw: map[string]json.RawMessage{}}
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		calls.ops = append(calls.ops, op)
		return responses[op]
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"decision"}, args...), "--server", gql.URL))
	err := root.Execute()
	for op, raw := range captured {
		calls.raw[op] = raw
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
	}, "log", "create", "hrn:mem:acme.com:kb", "--loc", "decisions", "--decider", "u2", "--decider", "u3")
	if err != nil {
		t.Fatal(err)
	}
	v := decisionOnlyOp(t, c, "CreateDecisionLog")
	if v["memoryRef"] != "hrn:mem:acme.com:kb" || v["loc"] != "decisions" {
		t.Fatalf("vars: %v", v)
	}
	if !reflect.DeepEqual(v["defaultDeciders"], []any{"u2", "u3"}) {
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
	// No --eligible: sent as null, which the server reads as "keep the set".
	if e, ok := v["eligibleDeciders"]; !ok || e != nil {
		t.Fatalf("no --eligible must send eligibleDeciders null (keep), got %v (present %t)", e, ok)
	}
}

func TestDecisionLogSetDecidersEmptyEligibleClears(t *testing.T) {
	c, _, err := runDecision(t, map[string]string{
		"SetDecisionLogDeciders": `{"data":{"setDecisionLogDeciders":` + decisionLogJSON + `}}`,
	}, "log", "set-deciders", "l1", "--decider", "u2", "--eligible", "", "--expected-revision", "2", "--reason", "clear")
	if err != nil {
		t.Fatal(err)
	}
	v := decisionOnlyOp(t, c, "SetDecisionLogDeciders")
	if e, ok := v["eligibleDeciders"].([]any); !ok || len(e) != 0 {
		t.Fatalf(`--eligible "" must send eligibleDeciders [] (clear), got %v`, v["eligibleDeciders"])
	}
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
	var want map[string]any
	_ = json.Unmarshal([]byte(in), &want)
	want["deciders"] = nil // no omitempty: null means the log's default deciders
	if !reflect.DeepEqual(v["input"], any(want)) {
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

// TestDecisionAskPassesAHugeNumberInGrant pins the other half of #1558: the
// key scan reads numbers as text, so a value out of float64 range in the
// opaque grant is not the CLI's to refuse; it goes to the server verbatim.
func TestDecisionAskPassesAHugeNumberInGrant(t *testing.T) {
	c, _, err := runDecision(t, map[string]string{
		"AskDecision": `{"data":{"askDecision":` + decisionJSON + `}}`,
	}, "ask", "l1", "--input-file", writeJSONFile(t, `{"grant":{"n":1e999},"title":"a","parts":[]}`))
	if err != nil {
		t.Fatalf("a huge number in grant must not be refused: %v", err)
	}
	decisionOnlyOp(t, c, "AskDecision")
	// Read the bytes as sent: a float64 decode of 1e999 fails, so a decoded
	// map cannot show the token arrived verbatim.
	var vars struct {
		Input struct {
			Title string          `json:"title"`
			Grant json.RawMessage `json:"grant"`
		} `json:"input"`
	}
	if err := json.Unmarshal(c.raw["AskDecision"], &vars); err != nil {
		t.Fatalf("variables: %v", err)
	}
	if vars.Input.Title != "a" || string(vars.Input.Grant) != `{"n":1e999}` {
		t.Fatalf("grant must reach the server verbatim, got title=%q grant=%s", vars.Input.Title, vars.Input.Grant)
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
		"blank flag":         {"ask", "l1", "--input-file", ""},
		"blank flag, title":  {"ask", "l1", "--input-file", " ", "--title", "t"},
		"unknown key":        {"ask", "l1", "--input-file", writeJSONFile(t, `{"titel":"typo","title":"t","parts":[]}`)},
		"nested unknown key": {"ask", "l1", "--input-file", writeJSONFile(t, `{"title":"t","parts":[{"question":"q","yes_no":true}]}`)},
		"duplicate key":      {"ask", "l1", "--input-file", writeJSONFile(t, `{"title":"a","title":"b","parts":[]}`)},
		"mis-cased key":      {"ask", "l1", "--input-file", writeJSONFile(t, `{"Title":"a","parts":[]}`)},
		"nested duplicate":   {"ask", "l1", "--input-file", writeJSONFile(t, `{"title":"t","parts":[{"question":"q","question":"r","yesno":true}]}`)},
		"nested mis-cased":   {"ask", "l1", "--input-file", writeJSONFile(t, `{"title":"t","parts":[{"Question":"q","yesno":true}]}`)},
		// #1558: a number out of float64 range in opaque input must not end
		// the key scan early and let a later duplicate through.
		"duplicate after huge number": {"ask", "l1", "--input-file", writeJSONFile(t, `{"grant":{"n":1e999},"title":"a","title":"b","parts":[]}`)},
		"mis-cased after huge number": {"ask", "l1", "--input-file", writeJSONFile(t, `{"grant":[-1e999],"Title":"a","parts":[]}`)},
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
		"blank flag":  {"amend", "d1", "--expected-revision", "3", "--reason", "r", "--input-file", "", "--title", "t"},
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
	// The reference lists carry no omitempty: absent is sent as null, which
	// the server reads as "unchanged" (only an explicit [] clears).
	want := map[string]any{"urgency": "URGENT", "blocks": nil, "followUps": nil, "sources": nil}
	if !reflect.DeepEqual(v["input"], want) {
		t.Fatalf("amend must send only the given field (omitted = unchanged), got %v", v["input"])
	}
}

func TestDecisionAmendEmptyReferenceListClears(t *testing.T) {
	for _, key := range []string{"blocks", "followUps", "sources"} {
		// Alone: a clear is a change, not "nothing to amend".
		c, _, err := runDecision(t, map[string]string{
			"AmendDecision": `{"data":{"amendDecision":` + decisionJSON + `}}`,
		}, "amend", "d1", "--expected-revision", "3", "--reason", "r", "--input-file", writeJSONFile(t, `{"`+key+`":[]}`))
		if err != nil {
			t.Fatalf("%s: [] alone must be sent, got %v", key, err)
		}
		input, _ := decisionOnlyOp(t, c, "AmendDecision")["input"].(map[string]any)
		if l, ok := input[key].([]any); !ok || len(l) != 0 {
			t.Fatalf("%s: [] must be sent as [] (clear), got %v", key, input)
		}
		// Alongside a scalar change.
		c, _, err = runDecision(t, map[string]string{
			"AmendDecision": `{"data":{"amendDecision":` + decisionJSON + `}}`,
		}, "amend", "d1", "--expected-revision", "3", "--reason", "r", "--title", "t", "--input-file", writeJSONFile(t, `{"`+key+`":[]}`))
		if err != nil {
			t.Fatal(err)
		}
		input, _ = decisionOnlyOp(t, c, "AmendDecision")["input"].(map[string]any)
		if l, ok := input[key].([]any); !ok || len(l) != 0 || input["title"] != "t" {
			t.Fatalf("%s with --title: got %v", key, input)
		}
	}
	// null is "unchanged", like leaving the key out: alone it is nothing to amend.
	c, _, err := runDecision(t, nil, "amend", "d1", "--expected-revision", "3", "--reason", "r", "--input-file", writeJSONFile(t, `{"blocks":null}`))
	if err == nil || exitCodeFor(err) != exitcode.Usage || len(c.ops) != 0 {
		t.Fatalf(`{"blocks":null} alone must be nothing to amend: err=%v ops=%v`, err, c.ops)
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
	// A withdrawal is not a deletion: the refusal must not say it deletes.
	if strings.Contains(err.Error(), "delete") {
		t.Fatalf("withdraw must not be described as a delete: %v", err)
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

// A populated decision: every nested value the mapping copies carries a
// distinct value, so a dropped or crossed field (answer vs history, claimed vs
// authenticated answerer, approval vs withdrawal actor) shows as a diff.
const decisionFullJSON = `{"id":"d9","urn":"hrn:node:acme.com:kb:decisions:DB","code":"DB","n":2,"logId":"l1",
"title":"T","context":"ctx","state":"DECIDED","tier":"APPROVED","urgency":"URGENT","authorizes":true,
"archival":true,"revSeq":7,"portalUrl":"https://example.test/d/DB","viewerIsDecider":false,
"askedAt":"2026-11-01T00:00:00Z","answeredAt":"2026-11-02T00:00:00Z",
"askedBy":{"userId":null,"user":null,"workerId":"w1","worker":{"id":"w1","name":"Ada","handle":null,"urn":"hrn:worker:acme.com:t:ada"},"appId":"a1","app":{"id":"a1","name":"Team","handle":null,"urn":"hrn:app:acme.com:t"}},
"deciderIds":["u2"],"deciders":[{"id":"u2","name":"Bo","handle":"bo","urn":null}],
"identityMerges":[{"fromUserId":"u7","from":{"id":"u7","name":"Old","handle":"old","urn":null},"toUserId":"u2","to":{"id":"u2","name":"Bo","handle":"bo","urn":null},"mergedAt":"2026-10-30T00:00:00Z"}],
"parts":[{"n":1,"citation":"DB-1","question":"Q","options":[{"key":"a","label":"A","meaning":"m","cost":"c"}],
"recommendation":{"key":"a","reason":"why","attributedTo":"Ada","by":{"userId":"u1","user":{"id":"u1","name":"Ann","handle":"ann","urn":null},"workerId":null,"worker":null,"appId":null,"app":null}},
"noRecommendationReason":null,
"answer":{"at":"2026-11-02T00:00:00Z","byUserId":"u2","recordedByUserId":"u5","claimedBy":"Bo via phone","freeform":false,"key":"a","text":null,"reason":"fine","via":"PORTAL","source":{"ref":null,"node":"hrn:node:acme.com:kb:notes:1","spec":null}},
"answerHistory":[{"at":"2026-11-01T12:00:00Z","byUserId":"u3","recordedByUserId":null,"claimedBy":null,"freeform":true,"key":null,"text":"maybe","reason":null,"via":"CLI","source":null}]}],
"blocks":[{"ref":"hadron-server#1","node":null,"spec":null}],
"followUps":[{"ref":null,"node":"hrn:node:acme.com:kb:x","spec":null}],
"sources":[{"ref":null,"node":null,"spec":"cor:api:240:01"}],
"withdrawn":{"at":"2026-11-03T00:00:00Z","reason":"moot","by":{"userId":"u4","user":{"id":"u4","name":"Cy","handle":"cy","urn":null},"workerId":null,"worker":null,"appId":null,"app":null}},
"evidence":{"approval":{"approvedAt":"2026-11-02T01:00:00Z","revision":6,"integrity":"INTACT","byDecider":true,"byUserId":"u2","by":{"id":"u2","name":"Bo","handle":"bo","urn":null}}}}`

func TestDecisionGetJSONMapsPopulatedValues(t *testing.T) {
	_, out, err := runDecision(t, map[string]string{"Decision": `{"data":{"decision":` + decisionFullJSON + `}}`}, "get", "d9", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	const want = `{"id":"d9","urn":"hrn:node:acme.com:kb:decisions:DB","code":"DB","n":2,"logId":"l1",
"title":"T","context":"ctx","state":"DECIDED","tier":"APPROVED","urgency":"URGENT","authorizes":true,
"archival":true,"revision":7,"portalUrl":"https://example.test/d/DB","viewerIsDecider":false,
"askedAt":"2026-11-01T00:00:00Z","answeredAt":"2026-11-02T00:00:00Z",
"askedBy":{"userId":null,"user":null,"workerId":"w1","worker":{"id":"w1","name":"Ada","handle":null,"urn":"hrn:worker:acme.com:t:ada"},"appId":"a1","app":{"id":"a1","name":"Team","handle":null,"urn":"hrn:app:acme.com:t"}},
"deciderIds":["u2"],"deciders":[{"id":"u2","name":"Bo","handle":"bo","urn":null}],
"identityMerges":[{"fromUserId":"u7","from":{"id":"u7","name":"Old","handle":"old","urn":null},"toUserId":"u2","to":{"id":"u2","name":"Bo","handle":"bo","urn":null},"mergedAt":"2026-10-30T00:00:00Z"}],
"parts":[{"n":1,"citation":"DB-1","question":"Q","options":[{"key":"a","label":"A","meaning":"m","cost":"c"}],
"recommendation":{"key":"a","reason":"why","attributedTo":"Ada","by":{"userId":"u1","user":{"id":"u1","name":"Ann","handle":"ann","urn":null},"workerId":null,"worker":null,"appId":null,"app":null}},
"noRecommendationReason":null,
"answer":{"at":"2026-11-02T00:00:00Z","byUserId":"u2","recordedByUserId":"u5","claimedBy":"Bo via phone","freeform":false,"key":"a","text":null,"reason":"fine","via":"PORTAL","source":{"ref":null,"node":"hrn:node:acme.com:kb:notes:1","spec":null}},
"answerHistory":[{"at":"2026-11-01T12:00:00Z","byUserId":"u3","recordedByUserId":null,"claimedBy":null,"freeform":true,"key":null,"text":"maybe","reason":null,"via":"CLI","source":null}]}],
"blocks":[{"ref":"hadron-server#1","node":null,"spec":null}],
"followUps":[{"ref":null,"node":"hrn:node:acme.com:kb:x","spec":null}],
"sources":[{"ref":null,"node":null,"spec":"cor:api:240:01"}],
"withdrawn":{"at":"2026-11-03T00:00:00Z","reason":"moot","by":{"userId":"u4","user":{"id":"u4","name":"Cy","handle":"cy","urn":null},"workerId":null,"worker":null,"appId":null,"app":null}},
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

func TestDecisionAmendContextIsTriState(t *testing.T) {
	ok := map[string]string{"AmendDecision": `{"data":{"amendDecision":` + decisionJSON + `}}`}
	// null alone clears: a change, sent as null.
	c, _, err := runDecision(t, ok, "amend", "d1", "--expected-revision", "3", "--reason", "r", "--input-file", writeJSONFile(t, `{"context":null}`))
	if err != nil {
		t.Fatalf(`{"context":null} alone must be sent (a clear), got %v`, err)
	}
	input, _ := decisionOnlyOp(t, c, "AmendDecision")["input"].(map[string]any)
	if v, present := input["context"]; !present || v != nil {
		t.Fatalf("context null must be sent as null, got %v (present %t)", v, present)
	}
	// Absent: omitted (unchanged).
	c, _, err = runDecision(t, ok, "amend", "d1", "--expected-revision", "3", "--reason", "r", "--title", "t")
	if err != nil {
		t.Fatal(err)
	}
	input, _ = decisionOnlyOp(t, c, "AmendDecision")["input"].(map[string]any)
	if _, present := input["context"]; present {
		t.Fatalf("absent context must be omitted, got %v", input)
	}
	// --context sets a string, overriding the file's null.
	c, _, err = runDecision(t, ok, "amend", "d1", "--expected-revision", "3", "--reason", "r", "--input-file", writeJSONFile(t, `{"context":null}`), "--context", "bg")
	if err != nil {
		t.Fatal(err)
	}
	input, _ = decisionOnlyOp(t, c, "AmendDecision")["input"].(map[string]any)
	if input["context"] != "bg" {
		t.Fatalf("--context must send the string, got %v", input)
	}
	// Gizelle's LOCAL repro: every clear survives alongside a title.
	c, _, err = runDecision(t, ok, "amend", "d1", "--expected-revision", "3", "--reason", "r",
		"--input-file", writeJSONFile(t, `{"title":"new","blocks":[],"followUps":[],"sources":[],"context":null}`))
	if err != nil {
		t.Fatal(err)
	}
	input, _ = decisionOnlyOp(t, c, "AmendDecision")["input"].(map[string]any)
	want := map[string]any{"title": "new", "blocks": []any{}, "followUps": []any{}, "sources": []any{}, "context": nil}
	if !reflect.DeepEqual(input, want) {
		t.Fatalf("clears must all reach the server:\n got %v\nwant %v", input, want)
	}
}
