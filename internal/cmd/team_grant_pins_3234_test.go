package cmd

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3234 items 6 and 7: pins on behaviour #3231 shipped. Green on a main with
// that PR; each is there to kill a mutant that survived it.

// Every server string the selection's text render prints, header and item row,
// is neutralised: none of them can act on a terminal.
func TestGrantSelection3234TextCellsNeutralised(t *testing.T) {
	// field + ESC + U+202E, as a JSON string; the field name stays visible.
	bad := func(field string) string { return `"` + field + `\u001b[2J\u202e"` }
	requestedBy := `{"userId":` + bad("u") + `,"appId":` + bad("a") + `,"workerId":` + bad("w") + `,"sessionId":` + bad("s") + `}`
	item := `{"id":` + bad("itemid") + `,"grantId":` + bad("itemgrant") + `,"version":1,"termsHash":"h","payloadVersion":"p","state":` + bad("itemstate") +
		`,"requestedBy":` + requestedBy + `,"createdAt":"c","expiresAt":"e","endedAt":` + bad("itemended") + `,"lastRefusedAt":null,"ceremonyUrl":"https://x/y"}`
	resp := `{"data":{"grantSigningSelection":{"id":` + bad("selid") + `,"state":` + bad("selstate") + `,"payloadVersion":` + bad("selpayload") +
		`,"requestedBy":` + requestedBy + `,"createdAt":` + bad("selcreated") + `,"expiresAt":` + bad("selexpires") + `,"endedAt":` + bad("selended") +
		`,"lastRefusedAt":` + bad("selrefused") + `,"ceremonyUrl":"https://x/y","items":[` + item + `]}}}`
	v := verbs3197()[1]
	_, out, err := runTeamTask(t, map[string]string{v.op: resp}, v.args...)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out, "\x1b\u202e") {
		t.Errorf("a control or format character reached stdout:\n%q", out)
	}
	// Each cell was rendered, so the check above looked at it.
	for _, s := range []string{
		"id: selid", "state: selstate", "payloadVersion: selpayload", "createdAt: selcreated", "expiresAt: selexpires",
		"endedAt: selended", "lastRefusedAt: selrefused", "requestedBy.userId: u", "requestedBy.appId: a",
		"requestedBy.workerId: w", "requestedBy.sessionId: s", "itemgrant", "itemid", "itemstate", "itemended",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%q", s, out)
		}
	}
}

// bothDetails3234 carries a grant selection's refusal extensions over a server
// error that an earlier envelope writer also reads.
type bothDetails3234 struct{ error }

func (e bothDetails3234) Unwrap() error { return e.error }
func (bothDetails3234) RefusalExtensions() map[string]any {
	return map[string]any{"grantRef": "from-selection"}
}

// The envelope's `extensions` has one writer: the selection's refusal fills it
// only when no earlier one did.
func TestRenderError3234RefusalExtensionsYield(t *testing.T) {
	for _, tc := range []struct {
		code string
		ext  map[string]any
		want map[string]any
	}{
		{"COMMENT_NOT_FOUND", map[string]any{"threadId": "t1"}, map[string]any{"code": "COMMENT_NOT_FOUND", "threadId": "t1"}},
		{"WORKER_REPORTS_TO_CYCLE", nil, map[string]any{"code": "WORKER_REPORTS_TO_CYCLE"}},
		// No earlier writer: the refusal's own map is emitted.
		{"GRANT_NOT_FOUND", nil, map[string]any{"grantRef": "from-selection"}},
	} {
		t.Run(tc.code, func(t *testing.T) {
			ext := map[string]any{"code": tc.code}
			for k, v := range tc.ext {
				ext[k] = v
			}
			err := bothDetails3234{exitcode.New(exitcode.Error, fmt.Errorf("refused: %w", gqlerror.List{{Message: "no", Extensions: ext}}))}
			f, out := testFactory(t)
			f.JSON = true
			renderError(f, err)
			all := f.IOStreams.ErrOut.(*strings.Builder).String() + out.String()
			env := envelope3197(t, all)
			var got map[string]any
			b, _ := json.Marshal(env["extensions"])
			_ = json.Unmarshal(b, &got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("extensions %v, want %v\n%s", got, tc.want, all)
			}
		})
	}
}
