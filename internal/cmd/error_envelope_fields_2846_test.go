package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2846 item 3: every server-derived STRING in the --json failure envelope is
// neutralised like the message (C1 controls and format characters become a space;
// the C0 controls the encoder already escapes stay as sent), while a valid
// identifier, a number and a null stay exactly as the server sent them, because a
// caller copies ids into its next command (cor:agt:120 FIT condition, Victor).

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
		for i, e := range x {
			walkStrings2846(t, path+"[]", e)
			_ = i
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

func TestEnvelopeStringFieldsAreNeutralisedLikeTheMessage2846(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		keys []string // the envelope keys that must exist, to prove the case reached its field
	}{
		"a superseded session's id": {
			err: exitcode.New(exitcode.Conflict, gqlerror.List{{Message: "superseded", Extensions: map[string]any{
				"code": "SESSION_SUPERSEDED", "sessionId": hostileField2846, "startedAt": "2026-11-21T10:00:00Z"}}}),
			keys: []string{"sessionId"},
		},
		"a comment recovery thread id and class": {
			err: exitcode.New(exitcode.Conflict, gqlerror.List{{Message: "conflict", Extensions: map[string]any{
				"code": "COMMENT_THREAD_OPEN", "threadId": hostileField2846, "class": hostileField2846, "currentRevision": 4}}}),
			keys: []string{"extensions"},
		},
		"a resume mismatch's names": {
			err: exitcode.New(exitcode.Error, gqlerror.List{{Message: "refused", Extensions: map[string]any{
				"code": "RESUME_REFUSED", "reason": "WORKER_MISMATCH", "foundWorkerName": hostileField2846, "picked": hostileField2846}}}),
			keys: []string{"extensions"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := envelope2846(t, tc.err)
			for _, k := range tc.keys {
				if _, ok := e[k]; !ok {
					t.Fatalf("the case did not reach %q: %v", k, e)
				}
			}
			walkStrings2846(t, "error", e)
		})
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
		`{"slot":"` + hostile + `","currentTaskId":"s\u200b1","state":"STAR\u009bTED","verdict":"AP\u202ePROVE","head":"` + prHead1664 + `","counts":false},` +
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
	ok, _ := slots[1].(map[string]any)
	if ok["slot"] != "local" || ok["currentTaskId"] != "s2" || ok["state"] != "FINISHED" || ok["verdict"] != "APPROVE" || ok["head"] != prHead1664 || ok["counts"] != true {
		t.Errorf("a valid slot must be unchanged: %v", ok)
	}
}
