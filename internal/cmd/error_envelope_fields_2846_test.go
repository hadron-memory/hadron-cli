package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2846 item 3: every server-derived STRING in the --json failure envelope is
// neutralised like the message (C1 controls and format characters become a space;
// the C0 controls the encoder already escapes stay as sent), while a valid
// identifier, a number and a null stay exactly as the server sent them, because a
// caller copies ids into its next command (the spec review's round-trip condition).

const hostileField2846 = "id\u009b31m\u202e\u200bX"

// rawFormat2846 reports a character a terminal printing the document could act on and the
// JSON encoder leaves raw: a C1 control or a format character.
func rawFormat2846(s string) (rune, bool) {
	for _, r := range s {
		if (r >= 0x80 && r <= 0x9f) || unicode.In(r, unicode.Cf) {
			return r, true
		}
	}
	return 0, false
}

func walkStrings2846(t *testing.T, path string, v any) {
	t.Helper()
	switch x := v.(type) {
	case string:
		if r, bad := rawFormat2846(x); bad {
			t.Errorf("%s carries %U: %q", path, r, x)
		}
	case map[string]any:
		for k, e := range x {
			walkStrings2846(t, path+"."+k, e)
		}
	case []any:
		for _, e := range x {
			walkStrings2846(t, path+"[]", e)
		}
	}
}

func envelope2846(t *testing.T, err error) map[string]any {
	t.Helper()
	out, _ := renderEnvelope2739(t, err, false)
	var doc map[string]any
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &doc); jerr != nil {
		t.Fatalf("envelope: %v\n%s", jerr, out)
	}
	e, _ := doc["error"].(map[string]any)
	if e == nil {
		t.Fatalf("no error object: %s", out)
	}
	return e
}

// want2846 is hostileField2846 after neutralisation: each C1 control and format character is one space.
const want2846 = "id 31m  X"

func TestEnvelopeStringFieldsAreNeutralisedLikeTheMessage2846(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		path []string // where the neutralised value must be, with its exact text
	}{
		"a superseded session's id": {
			err: exitcode.New(exitcode.Conflict, gqlerror.List{{Message: "superseded", Extensions: map[string]any{
				"code": "SESSION_SUPERSEDED", "sessionId": hostileField2846, "startedAt": "2026-11-21T10:00:00Z"}}}),
			path: []string{"sessionId"},
		},
		"a comment recovery thread id": {
			err: exitcode.New(exitcode.Conflict, gqlerror.List{{Message: "conflict", Extensions: map[string]any{
				"code": "COMMENT_THREAD_OPEN", "threadId": hostileField2846, "currentRevision": 4}}}),
			path: []string{"extensions", "threadId"},
		},
		"a comment recovery class": {
			err: exitcode.New(exitcode.Conflict, gqlerror.List{{Message: "conflict", Extensions: map[string]any{
				"code": "COMMENT_THREAD_OPEN", "class": hostileField2846, "currentRevision": 4}}}),
			path: []string{"extensions", "class"},
		},
		"a resume mismatch's names": {
			err: exitcode.New(exitcode.Error, gqlerror.List{{Message: "refused", Extensions: map[string]any{
				"code": "RESUME_REFUSED", "reason": "WORKER_MISMATCH", "foundWorkerName": hostileField2846, "picked": "p"}}}),
			path: []string{"extensions", "foundWorkerName"},
		},
		"a server code carried by the error": {
			err:  exitcode.New(exitcode.Error, hostileErr2846{code: hostileField2846}),
			path: []string{"serverCode"},
		},
		"a batch reason carried by the error": {
			err:  exitcode.New(exitcode.Error, hostileErr2846{reason: hostileField2846}),
			path: []string{"reason"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := envelope2846(t, tc.err)
			var cur any = e
			for _, k := range tc.path {
				m, ok := cur.(map[string]any)
				if !ok {
					t.Fatalf("no object before %q: %v", k, cur)
				}
				cur = m[k]
			}
			if cur != want2846 {
				t.Errorf("%v = %q, want exactly %q (the whole value, not an emptied one)", tc.path, cur, want2846)
			}
			walkStrings2846(t, "error", e)
		})
	}
}

// hostileErr2846 is a CLI error carrying a server code and a batch reason, the two ways those
// reach the envelope besides the typed refusals.
type hostileErr2846 struct{ code, reason string }

func (hostileErr2846) Error() string         { return "refused" }
func (e hostileErr2846) ServerCode() string  { return e.code }
func (e hostileErr2846) BatchReason() string { return e.reason }
func (hostileErr2846) BatchPosition() int    { return 3 }

// A server-chosen KEY is a server string too, and a null stays null.
func TestEnvelopeKeysAreNeutralisedAndNullsStayNull2846(t *testing.T) {
	e := envelope2846(t, exitcode.New(exitcode.Error, gqlerror.List{{Message: "refused", Extensions: map[string]any{
		"code": "RESUME_REFUSED", "reason": "WORKER_MISMATCH", "expectedWorkerName": nil, "picked": "p"}}}))
	ext, _ := e["extensions"].(map[string]any)
	if v, present := ext["expectedWorkerName"]; !present || v != nil {
		t.Errorf("a null the server sent must come back as a null: %v", ext)
	}
	bad := neutraliseEnvelopeValue(map[string]any{"k\u202e\u009bx": "v"}).(map[string]any)
	if _, ok := bad["k  x"]; !ok || len(bad) != 1 {
		t.Errorf("a map key holding a bidi override or a C1 control must be neutralised: %v", bad)
	}
}

// A typed nil map or slice stays nil (it marshals as null, not {} or []).
func TestNeutraliseEnvelopeValueKeepsTypedNilsNil2846(t *testing.T) {
	var m map[string]any
	var sl []string
	var ms []map[string]any
	for name, v := range map[string]any{"map": m, "slice": sl, "slice of maps": ms} {
		out := neutraliseEnvelopeValue(v)
		b, _ := json.Marshal(out)
		if string(b) != "null" {
			t.Errorf("%s: a typed nil must marshal as null, got %s", name, b)
		}
	}
}

// What must not change: a valid id, a number and a null pass byte for byte and keep their JSON types.
func TestEnvelopeValidIDsNumbersAndNullsRoundTrip2846(t *testing.T) {
	const id = "01a1fe48037471c09b91e874d2ec37c3"
	e := envelope2846(t, exitcode.New(exitcode.Conflict, gqlerror.List{{Message: "superseded", Extensions: map[string]any{
		"code": "SESSION_SUPERSEDED", "sessionId": id, "startedAt": "2026-11-21T10:00:00Z"}}}))
	if e["sessionId"] != id || e["startedAt"] != "2026-11-21T10:00:00Z" {
		t.Errorf("a valid session id and time must be byte-identical: %v", e)
	}
	e = envelope2846(t, exitcode.New(exitcode.Conflict, gqlerror.List{{Message: "conflict", Extensions: map[string]any{
		"code": "COMMENT_THREAD_OPEN", "threadId": "thr_01a1fe48", "class": "open", "currentRevision": 4}}}))
	ext, _ := e["extensions"].(map[string]any)
	if ext["threadId"] != "thr_01a1fe48" || ext["class"] != "open" || ext["currentRevision"] != float64(4) {
		t.Errorf("valid detail values must be unchanged (a number stays a number): %v", ext)
	}
}

// The typed PR readiness slots are server strings too: slot, task id, state, verdict, head.
func TestEnvelopeSlotStringsAreNeutralisedAndAValidSlotIsUnchanged2846(t *testing.T) {
	hostile := `qa:portal\u009b31m\u202e`
	reply := `{"errors":[{"message":"not ready","extensions":{"code":"PR_NOT_READY","slots":[` +
		`{"slot":"` + hostile + `","currentTaskId":"s\u200b1","state":"STAR\u009bTED","verdict":"AP\u202ePROVE","head":"` + prHead1664 + `\u200b","counts":false},` +
		`{"slot":"local","currentTaskId":"s2","state":"FINISHED","verdict":"APPROVE","head":"` + prHead1664 + `","counts":true}]}}]}`
	out, _, _, err := runPR1664(t, "PRReady", reply, "ready", "AT0.7", "--head", prHead1664, "--json")
	if err == nil {
		t.Fatal("expected the refusal")
	}
	var doc struct {
		Error map[string]any `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &doc); jerr != nil {
		t.Fatalf("%v\n%s", jerr, out)
	}
	slots, _ := doc.Error["slots"].([]any)
	if len(slots) != 2 {
		t.Fatalf("slots: %s", out)
	}
	walkStrings2846(t, "error.slots", slots)
	valid, _ := slots[1].(map[string]any)
	if valid["slot"] != "local" || valid["currentTaskId"] != "s2" || valid["state"] != "FINISHED" || valid["verdict"] != "APPROVE" || valid["head"] != prHead1664 || valid["counts"] != true {
		t.Errorf("a valid slot must be unchanged: %v", valid)
	}
}

// The typed shapes the walker must reach, whatever the Go type: the clone-agent blockers are a
// []map[string]any holding another []map[string]any (cli#2846 review F1).
func TestEnvelopeCloneBlockerSubjectNamesAreNeutralised2846(t *testing.T) {
	hostile := "n\u009b31m\u202eX"
	cause := gqlerror.List{{Message: "blocked", Extensions: map[string]any{
		"code": "CLONE_TARGET_UNSUPPORTED", "reason": "REASON", "retryable": false,
		"blockers": []any{map[string]any{"code": "B", "count": float64(2), "subjects": []any{
			map[string]any{"position": float64(1), "name": hostile},
			map[string]any{"position": float64(2), "name": "plain name"}}}}}}}
	e := envelope2846(t, api.MapCloneAgentError(cause))
	walkStrings2846(t, "error", e)
	ext, _ := e["extensions"].(map[string]any)
	blockers, _ := ext["blockers"].([]any)
	if len(blockers) != 1 {
		t.Fatalf("blockers: %v", e)
	}
	subjects, _ := blockers[0].(map[string]any)["subjects"].([]any)
	if len(subjects) != 2 || subjects[1].(map[string]any)["name"] != "plain name" || subjects[1].(map[string]any)["position"] != float64(2) {
		t.Errorf("a plain name and a position must be unchanged: %v", subjects)
	}
}

// The walker keeps types: numbers, booleans, nils, struct field order and raw JSON bytes come
// out as they went in; only string contents change.
func TestNeutraliseEnvelopeValueKeepsTypesAndOnlyChangesStrings2846(t *testing.T) {
	type inner struct {
		Name  string  `json:"name"`
		Count int     `json:"count"`
		Ptr   *string `json:"ptr"`
		Flag  bool    `json:"flag"`
		hid   string
	}
	bad := "a\u009bb\u200bc"
	p := bad
	in := map[string]any{
		"blockers": []map[string]any{{"subjects": []map[string]any{{"name": bad, "position": int64(3)}}}},
		"typed":    []inner{{Name: bad, Count: 7, Ptr: &p, Flag: true, hid: bad}},
		"list":     []string{bad, "ok"},
		"nested":   map[string]any{"x": []any{bad, 1.5, nil, true}},
		"raw":      []byte(bad),
		"nilptr":   (*string)(nil),
		"num":      float64(4),
	}
	out := neutraliseEnvelopeValue(in).(map[string]any)
	want := "a b c"
	if got := out["blockers"].([]map[string]any)[0]["subjects"].([]map[string]any)[0]; got["name"] != want || got["position"] != int64(3) {
		t.Errorf("blockers: %v", got)
	}
	ti := out["typed"].([]inner)[0]
	if ti.Name != want || *ti.Ptr != want || ti.Count != 7 || !ti.Flag {
		t.Errorf("typed struct: %+v", ti)
	}
	if got := out["list"].([]string); got[0] != want || got[1] != "ok" {
		t.Errorf("list: %v", got)
	}
	nested := out["nested"].(map[string]any)["x"].([]any)
	if nested[0] != want || nested[1] != 1.5 || nested[2] != nil || nested[3] != true {
		t.Errorf("nested: %v", nested)
	}
	if string(out["raw"].([]byte)) != bad || out["nilptr"].(*string) != nil || out["num"] != float64(4) {
		t.Errorf("raw bytes, a nil pointer and a number are left alone: %v", out)
	}
	if in["list"].([]string)[0] != bad || p != bad {
		t.Error("the input must not be modified")
	}
}

// A staged secret change's request id and ceremony URL stay byte for byte (cli#2738: a script
// hands the URL to the person who signs), even with a format character in the URL: the one
// exception to neutralising every envelope string.
func TestEnvelopeStagedSecretChangeStaysByteForByte2846(t *testing.T) {
	const tail = "/ceremony/\u200bx\u009b"
	srv, _ := fake2738(t, only2738(200, stagedURL2738("https://hadronmemory.com"+tail)))
	stdout, stderr, code := run2738(t, srv.URL, "protect", "sec-1", "--on", "--json")
	if code != exitcode.Conflict {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	e := envelope2738(t, stdout, stderr)
	if e["requestId"] != req2738 || e["ceremonyUrl"] != "https://hadronmemory.com"+tail {
		t.Errorf("the staged request id and ceremony URL are carried exactly as sent: %v", e)
	}
}
