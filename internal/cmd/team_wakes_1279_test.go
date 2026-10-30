package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1279 (CHECKED T1): `team wakes due` lists the wakes due for one worker
// or the App's lane (dueWakes, cor:agt:080:08) and `team wakes ack` records
// their delivery (acknowledgeWakes, cor:agt:080:16). Both run as the person:
// the server refuses a worker's session for either.

const c1279TickWake = `{"proof":"prf-tick-1","worker":{"id":"wkr1","name":"Iris"},"tickText":"Tick: standup.","unread":3,"unreadMentions":1,` +
	`"anchorAt":"2026-10-30T09:00:00Z","deliverAfter":"2026-10-30T09:01:00Z","busyDeadline":"2026-10-30T09:10:00Z",` +
	`"channels":[{"channel":{"id":"ch1","loc":"chats:team"},"fromSeq":40,"throughSeq":42,"latestMentionSeq":41}],` +
	`"reasons":[{"kind":"TICK","tickDueAt":"2026-10-30T09:00:00Z","ticker":{"id":"tkr1","name":"standup"},"task":null,"stop":null},` +
	`{"kind":"TASK","tickDueAt":null,"ticker":null,"task":{"id":"t1","handle":"AT1.4","title":"Fix login","event":"ASSIGNED","chainIndex":0,"eventId":"ev1"},"stop":null}]}`

func c1279Page(status string, withheld int, wakes string) string {
	return `{"data":{"dueWakes":{"status":"` + status + `","withheld":` + jsonInt(withheld) + `,"dormant":[],"wakes":[` + wakes + `],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`
}

func jsonInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func c1279Responses(page string) map[string]string {
	r := taskResponses()
	r["Workers"] = `{"data":{"workers":{"total":1,"items":[` + irisWorkerJSON + `]}}}`
	r["TeamDueWakes"] = page
	r["AcknowledgeWakes"] = `{"data":{"acknowledgeWakes":{"acknowledged":1,"unchanged":0}}}`
	return r
}

func c1279Errors(code string) string {
	return `{"data":null,"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}]}`
}

func TestC1279DueShowsTickWake(t *testing.T) {
	calls, out, err := runTeamTask(t, c1279Responses(c1279Page("OK", 0, c1279TickWake)), "team", "wakes", "due", "--worker", "Iris")
	if err != nil {
		t.Fatal(err)
	}
	reads := stopCalls(calls, "TeamDueWakes")
	if len(reads) != 1 {
		t.Fatalf("dueWakes calls %d", len(reads))
	}
	// The name resolves client-side: the server answers an unresolvable ref
	// with silence (no existence oracle), so a bare name must never reach it.
	if got := reads[0].Vars["workerRefs"]; !reflect.DeepEqual(got, []any{"wkr1"}) {
		t.Fatalf("workerRefs %v", got)
	}
	if _, ok := reads[0].Vars["accepts"]; ok {
		t.Fatalf("accepts sent without --accept-stop: %v", reads[0].Vars)
	}
	if reads[0].Session != "" {
		t.Fatalf("read carried worker session %q", reads[0].Session)
	}
	for _, want := range []string{"Iris", "TICK standup", "TASK AT1.4 ASSIGNED", "chats:team 40-42", "prf-tick-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestC1279DueJSON(t *testing.T) {
	_, out, err := runTeamTask(t, c1279Responses(c1279Page("OK", 0, c1279TickWake)), "team", "wakes", "due", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Status   string `json:"status"`
		Withheld int    `json:"withheld"`
		Wakes    []struct {
			WorkerID string `json:"workerId"`
			Worker   string `json:"workerName"`
			Proof    string `json:"proof"`
			Channels []struct {
				Loc      string `json:"loc"`
				FromSeq  int    `json:"fromSeq"`
				Through  int    `json:"throughSeq"`
				LatestMn *int   `json:"latestMentionSeq"`
			} `json:"channels"`
			Reasons []struct {
				Kind   string          `json:"kind"`
				Ticker json.RawMessage `json:"ticker"`
				Task   json.RawMessage `json:"task"`
				Stop   json.RawMessage `json:"stop"`
			} `json:"reasons"`
		} `json:"wakes"`
		Dormant     []any `json:"dormant"`
		HasNextPage bool  `json:"hasNextPage"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if dto.Status != "OK" || len(dto.Wakes) != 1 || dto.Dormant == nil {
		t.Fatalf("dto %+v", dto)
	}
	w := dto.Wakes[0]
	if w.WorkerID != "wkr1" || w.Worker != "Iris" || w.Proof != "prf-tick-1" || len(w.Channels) != 1 || w.Channels[0].Through != 42 {
		t.Fatalf("wake %+v", w)
	}
	if len(w.Reasons) != 2 || w.Reasons[0].Kind != "TICK" || string(w.Reasons[0].Task) != "null" || string(w.Reasons[1].Ticker) != "null" {
		t.Fatalf("reasons %s", out)
	}
}

func TestC1279DueEmptyIsExplicit(t *testing.T) {
	_, out, err := runTeamTask(t, c1279Responses(c1279Page("OK", 0, "")), "team", "wakes", "due")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no wakes due") {
		t.Fatalf("empty read not explicit: %q", out)
	}
	_, out, err = runTeamTask(t, c1279Responses(c1279Page("OK", 0, "")), "team", "wakes", "due", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"wakes": []`) || !strings.Contains(out, `"dormant": []`) {
		t.Fatalf("empty lists must render []: %s", out)
	}
}

// A withheld page proves nothing about what is due: it exits non-zero and
// names the status, in text and --json alike.
func TestC1279DueWithheldExitsNonZero(t *testing.T) {
	for _, argv := range [][]string{{"team", "wakes", "due"}, {"team", "wakes", "due", "--json"}} {
		_, _, err := runTeamTask(t, c1279Responses(c1279Page("WAKE_BUDGET_EXHAUSTED", 4, "")), argv...)
		if err == nil || !strings.Contains(err.Error(), "WAKE_BUDGET_EXHAUSTED") {
			t.Fatalf("%v: err %v", argv, err)
		}
		if exitcode.FromError(err) != exitcode.Conflict {
			t.Fatalf("%v: exit %v, want %d", argv, err, exitcode.Conflict)
		}
	}
}

func TestC1279AcceptStopIsOptIn(t *testing.T) {
	calls, _, err := runTeamTask(t, c1279Responses(c1279Page("OK", 0, "")), "team", "wakes", "due", "--accept-stop")
	if err != nil {
		t.Fatal(err)
	}
	got := stopCalls(calls, "TeamDueWakes")[0].Vars["accepts"]
	if !reflect.DeepEqual(got, []any{"MENTION", "TICK", "TASK", "STOP"}) {
		t.Fatalf("accepts %v", got)
	}
}

func TestC1279UnknownWorkerRefusedLocally(t *testing.T) {
	calls, _, err := runTeamTask(t, c1279Responses(c1279Page("OK", 0, "")), "team", "wakes", "due", "--worker", "Nobody")
	if exitcode.FromError(err) != exitcode.NotFound {
		t.Fatalf("err %v, want exit %d", err, exitcode.NotFound)
	}
	if n := len(stopCalls(calls, "TeamDueWakes")); n != 0 {
		t.Fatalf("dueWakes sent for an unresolved worker (%d)", n)
	}
}

func TestC1279DueRefusalKeepsServerCode(t *testing.T) {
	r := c1279Responses(c1279Errors("APP_NOT_FOUND"))
	_, _, err := runTeamTask(t, r, "team", "wakes", "due")
	if exitcode.FromError(err) != exitcode.NotFound || !strings.Contains(err.Error(), "APP_NOT_FOUND") {
		t.Fatalf("err %v", err)
	}
}

func TestC1279AckSendsProofsAsPerson(t *testing.T) {
	calls, out, err := runTeamTask(t, c1279Responses(""), "team", "wakes", "ack", "prf-tick-1", "prf-2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	acks := stopCalls(calls, "AcknowledgeWakes")
	if len(acks) != 1 || !reflect.DeepEqual(acks[0].Vars["proofs"], []any{"prf-tick-1", "prf-2"}) {
		t.Fatalf("ack calls %+v", acks)
	}
	if acks[0].Session != "" {
		t.Fatalf("ack carried worker session %q", acks[0].Session)
	}
	var dto map[string]int
	if err := json.Unmarshal([]byte(out), &dto); err != nil || dto["acknowledged"] != 1 || dto["unchanged"] != 0 {
		t.Fatalf("%v: %s", err, out)
	}
	_, out, err = runTeamTask(t, c1279Responses(""), "team", "wakes", "ack", "prf-tick-1")
	if err != nil || !strings.Contains(out, "acknowledged 1") {
		t.Fatalf("%v: %q", err, out)
	}
}

func TestC1279AckRefusalsMapped(t *testing.T) {
	for code, want := range map[string]int{
		"WAKE_PROOF_REQUESTER_MISMATCH": exitcode.Forbidden,
		"WAKE_ACK_FORBIDDEN":            exitcode.Forbidden,
		"WAKE_PROOF_STALE":              exitcode.Conflict,
		"WAKE_PROOF_INVALID":            exitcode.Usage,
	} {
		r := c1279Responses("")
		r["AcknowledgeWakes"] = c1279Errors(code)
		_, _, err := runTeamTask(t, r, "team", "wakes", "ack", "prf-x")
		if exitcode.FromError(err) != want || !strings.Contains(err.Error(), code) {
			t.Fatalf("%s: err %v, want exit %d", code, err, want)
		}
	}
}

func TestC1279AckNeedsAProof(t *testing.T) {
	for _, argv := range [][]string{{"team", "wakes", "ack"}, {"team", "wakes", "ack", " "}} {
		calls, _, err := runTeamTask(t, c1279Responses(""), argv...)
		if exitcode.FromError(err) != exitcode.Usage {
			t.Fatalf("%v: err %v", argv, err)
		}
		if n := len(stopCalls(calls, "AcknowledgeWakes")); n != 0 {
			t.Fatalf("%v: sent", argv)
		}
	}
}

func TestC1279HelpPins(t *testing.T) {
	due := c1279Flat(cmdHelpOut(t, "team", "wakes", "due"))
	for _, want := range []string{"--accept-stop", "declares", "cor:agt:090:04", "WAKE_BUDGET_EXHAUSTED", "worker session"} {
		if !strings.Contains(due, want) {
			t.Errorf("due help lacks %q", want)
		}
	}
	ack := c1279Flat(cmdHelpOut(t, "team", "wakes", "ack"))
	for _, want := range []string{"never moves a read position", "cor:agt:080:16", "WAKE_PROOF_REQUESTER_MISMATCH"} {
		if !strings.Contains(ack, want) {
			t.Errorf("ack help lacks %q", want)
		}
	}
	get := c1279Flat(cmdHelpOut(t, "team", "worker", "get"))
	if strings.Contains(get, "No CLI command lists") || !strings.Contains(get, "team wakes due") {
		t.Errorf("worker get's DARK recovery does not name the new route")
	}
}

func c1279Flat(s string) string { return strings.Join(strings.Fields(s), " ") }
