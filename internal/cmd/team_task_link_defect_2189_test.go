package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2189 (S12b, companion to hadron-server#4280): `team task
// link-defect` is a thin client over linkDefectToPullRequest (Proposed
// cor:agt:130:05). It sends the mutation's three arguments, never an
// instruction, prints the server's link, and maps each refusal to the CLI's
// exit contract without retrying.

const head2189 = "0123456789abcdef0123456789abcdef01234567"
const defect2189 = "https://github.com/acme/app/issues/77"

func linkResponse2189(created bool, head any) string {
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"linkDefectToPullRequest": map[string]any{
		"taskId": "task-pr-1", "defect": defect2189, "introducedAtHead": head,
		"eventId": "ev-9", "linkedAt": "2026-11-17T15:00:00Z", "created": created,
	}}})
	return string(b)
}

func linkCalls2189(t *testing.T, calls *[]attnCall) []attnCall {
	t.Helper()
	var got []attnCall
	for _, c := range *calls {
		if c.Op == "TaskLinkDefect" {
			got = append(got, c)
		}
	}
	return got
}

func TestTaskLinkDefect2189Linked(t *testing.T) {
	r := taskResponses()
	r["TaskLinkDefect"] = linkResponse2189(true, head2189)
	calls, out, err := runTeamTask(t, r, "team", "task", "link-defect", "AT1.2", "--defect", defect2189, "--introduced-at-head", head2189)
	if err != nil {
		t.Fatal(err)
	}
	got := linkCalls2189(t, calls)
	if len(got) != 1 {
		t.Fatalf("want exactly one TaskLinkDefect, got %d (%v)", len(got), *calls)
	}
	v := got[0].Vars
	if v["prTaskRef"] != "AT1.2" || v["defectRef"] != defect2189 || v["introducedAtHead"] != head2189 || len(v) != 3 {
		t.Fatalf("variables = %v, want exactly prTaskRef, defectRef and introducedAtHead", v)
	}
	if strings.Contains(got[0].Query, "instruction") {
		t.Errorf("this slice never sends an instruction: %s", got[0].Query)
	}
	if got[0].Session != "s-new" {
		t.Errorf("the link must be attributed to the bound session, header %q", got[0].Session)
	}
	for _, s := range []string{"linked", "task-pr-1", defect2189, head2189, "ev-9", "2026-11-17T15:00:00Z"} {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q:\n%s", s, out)
		}
	}
	if strings.Contains(out, "already linked") {
		t.Errorf("a new link reads as already linked:\n%s", out)
	}
}

func TestTaskLinkDefect2189AlreadyLinked(t *testing.T) {
	r := taskResponses()
	r["TaskLinkDefect"] = linkResponse2189(false, nil)
	calls, out, err := runTeamTask(t, r, "team", "task", "link-defect", "AT1.2", "--defect", defect2189)
	if err != nil {
		t.Fatalf("re-linking the same pair exits 0: %v", err)
	}
	got := linkCalls2189(t, calls)
	if len(got) != 1 {
		t.Fatalf("an existing link must not be retried: %d calls", len(got))
	}
	if _, ok := got[0].Vars["introducedAtHead"]; ok {
		t.Errorf("an unset --introduced-at-head must be omitted, not sent: %v", got[0].Vars)
	}
	for _, s := range []string{"already linked", "task-pr-1", defect2189, "ev-9", "none"} {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q:\n%s", s, out)
		}
	}
}

func TestTaskLinkDefect2189JSON(t *testing.T) {
	for _, tc := range []struct {
		created bool
		head    any
		want    string
	}{
		{true, head2189, `{"taskId":"task-pr-1","defect":"` + defect2189 + `","introducedAtHead":"` + head2189 + `","eventId":"ev-9","linkedAt":"2026-11-17T15:00:00Z","created":true}`},
		{false, nil, `{"taskId":"task-pr-1","defect":"` + defect2189 + `","introducedAtHead":null,"eventId":"ev-9","linkedAt":"2026-11-17T15:00:00Z","created":false}`},
	} {
		t.Run(fmt.Sprintf("created=%v", tc.created), func(t *testing.T) {
			r := taskResponses()
			r["TaskLinkDefect"] = linkResponse2189(tc.created, tc.head)
			_, out, err := runTeamTask(t, r, "team", "task", "link-defect", "AT1.2", "--defect", defect2189, "--json")
			if err != nil {
				t.Fatal(err)
			}
			var compact bytes.Buffer
			if e := json.Compact(&compact, []byte(out)); e != nil {
				t.Fatalf("%v: %s", e, out)
			}
			if compact.String() != tc.want {
				t.Fatalf("json = %s\nwant   %s", compact.String(), tc.want)
			}
		})
	}
}

func TestTaskLinkDefect2189Refusals(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{
		{"DEFECT_LINK_FORBIDDEN", exitcode.Forbidden},
		{"TASK_NOT_FOUND", exitcode.NotFound},
		{"PR_REFERENCE_MISSING", exitcode.Conflict},
		{"DEFECT_HEAD_NOT_FROZEN", exitcode.Conflict},
		{"BAD_USER_INPUT", exitcode.Usage},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := taskResponses()
			r["TaskLinkDefect"] = gqlErrorJSON(tc.code)
			calls, out, err := runTeamTask(t, r, "team", "task", "link-defect", "AT1.2", "--defect", defect2189, "--introduced-at-head", head2189)
			if got := exitCodeFor(err); got != tc.exit {
				t.Fatalf("exit = %d, want %d (%v)", got, tc.exit, err)
			}
			got := linkCalls2189(t, calls)
			if len(got) != 1 {
				t.Fatalf("a refusal is never retried: %d calls", len(got))
			}
			// Jake's pin: a refused worker write is not retried as the person.
			if got[0].Session != "s-new" {
				t.Errorf("the refused call must carry the bound session, header %q", got[0].Session)
			}
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Errorf("the refusal must name the server's code: %v", err)
			}
			if strings.Contains(out, "linked") {
				t.Errorf("a refused link printed a link:\n%s", out)
			}
		})
	}
}

// An unknown task, one in another App and one the caller may not read are
// answered the same by the server; the CLI adds nothing that tells them apart,
// such as the ref it was given.
func TestTaskLinkDefect2189NotFoundIdentical(t *testing.T) {
	var msgs []string
	for _, ref := range []string{"AT1.2", "hrn:agenttask:other.com:team:AT9.9", "01a1ffffffffffffffffffffffffffff"} {
		r := taskResponses()
		r["TaskLinkDefect"] = gqlErrorJSON("TASK_NOT_FOUND")
		_, out, err := runTeamTask(t, r, "team", "task", "link-defect", ref, "--defect", defect2189)
		if exitCodeFor(err) != exitcode.NotFound || err == nil {
			t.Fatalf("%s: exit %d (%v)", ref, exitCodeFor(err), err)
		}
		msgs = append(msgs, out+"|"+err.Error())
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i] != msgs[0] {
			t.Fatalf("not-found output differs by ref:\n%q\n%q", msgs[0], msgs[i])
		}
	}
}

func TestTaskLinkDefect2189EmptyDefectRefusedLocally(t *testing.T) {
	for name, args := range map[string][]string{
		"missing": {},
		"empty":   {"--defect", ""},
		"spaces":  {"--defect", "   "},
	} {
		t.Run(name, func(t *testing.T) {
			r := taskResponses()
			calls, _, err := runTeamTask(t, r, append([]string{"team", "task", "link-defect", "AT1.2"}, args...)...)
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Fatalf("exit = %d, want Usage (%v)", got, err)
			}
			if n := len(linkCalls2189(t, calls)); n != 0 {
				t.Fatalf("an empty --defect must be refused before any request, sent %d", n)
			}
		})
	}
}

// hadron-cli#2296 item 1: an empty or blank --introduced-at-head is refused
// locally with exit 2, like an empty --defect, before any request.
func TestTaskLinkDefect2296EmptyHeadRefusedLocally(t *testing.T) {
	for name, head := range map[string]string{"empty": "", "spaces": "   "} {
		t.Run(name, func(t *testing.T) {
			r := taskResponses()
			r["TaskLinkDefect"] = linkResponse2189(true, head2189)
			calls, _, err := runTeamTask(t, r, "team", "task", "link-defect", "AT1.2", "--defect", defect2189, "--introduced-at-head", head)
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Fatalf("exit = %d, want Usage (%v)", got, err)
			}
			// hadron-cli#2329 F3: no request of any kind, so a lookup placed
			// before the guard is caught too.
			if len(*calls) != 0 {
				t.Fatalf("an empty --introduced-at-head must be refused before any request, sent %v", *calls)
			}
		})
	}
}

// hadron-cli#2296 item 2: a response with no link (null, or a link without
// taskId or eventId) exits 1, says to read the task's events before retrying
// and names the task ref it was given.
func TestTaskLinkDefect2296EmptyResponse(t *testing.T) {
	empty := func(taskID, eventID string) string {
		b, _ := json.Marshal(map[string]any{"data": map[string]any{"linkDefectToPullRequest": map[string]any{
			"taskId": taskID, "defect": defect2189, "introducedAtHead": nil,
			"eventId": eventID, "linkedAt": "2026-11-17T15:00:00Z", "created": true,
		}}})
		return string(b)
	}
	for name, resp := range map[string]string{
		"null":         `{"data":{"linkDefectToPullRequest":null}}`,
		"emptyTaskId":  empty("", "ev-9"),
		"emptyEventId": empty("task-pr-1", ""),
	} {
		t.Run(name, func(t *testing.T) {
			r := taskResponses()
			r["TaskLinkDefect"] = resp
			calls, _, err := runTeamTask(t, r, "team", "task", "link-defect", "AT1.2", "--defect", defect2189)
			if got := exitCodeFor(err); got != exitcode.Error {
				t.Fatalf("exit = %d, want Error (%v)", got, err)
			}
			if n := len(linkCalls2189(t, calls)); n != 1 {
				t.Fatalf("want exactly one TaskLinkDefect and no retry, got %d", n)
			}
			// hadron-cli#2329 F4: the advice and the ref are one string.
			if want := recovery2329(); !strings.HasSuffix(err.Error(), want) {
				t.Fatalf("message must end with %q, got %q", want, err.Error())
			}
		})
	}
}

// recovery2329 is the no-link recovery advice: the command, without the task ref typed (cli#3114).
func recovery2329() string {
	return "read the task's events before retrying, with hadron team events --task <task> (the ref given is not shown)"
}

// hadron-cli#2329 F1, as cli#3114 leaves it: whatever the ref holds, the advice is the same fixed text,
// so no part of the ref (cut short, spliced or raw) reaches the terminal.
func TestTaskLinkDefect2329RecoveryNeverShowsTheRef(t *testing.T) {
	for name, ref := range map[string]string{
		"long":            "hrn:agenttask:" + strings.Repeat("deep.sub.domain.", 20) + "acme.com:team-app:AT1.2",
		"multibyte":       "hrn:agenttask:" + strings.Repeat("é", 200) + ":AT1.2",
		"shell metachars": "AT1.2;rm -rf $(x) 'q'",
		"control chars":   "AT1.2\x1b[31m\nforged",
	} {
		t.Run(name, func(t *testing.T) {
			r := taskResponses()
			r["TaskLinkDefect"] = `{"data":{"linkDefectToPullRequest":null}}`
			_, _, err := runTeamTask(t, r, "team", "task", "link-defect", ref, "--defect", defect2189)
			if got := exitCodeFor(err); got != exitcode.Error {
				t.Fatalf("exit = %d, want Error (%v)", got, err)
			}
			if !strings.HasSuffix(err.Error(), recovery2329()) || strings.Contains(err.Error(), "AT1.2") {
				t.Fatalf("message must end with %q and hold no ref, got %q", recovery2329(), err.Error())
			}
		})
	}
}
