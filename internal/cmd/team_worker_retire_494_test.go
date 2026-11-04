package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#494: `worker retire` shows a truthful, server-owned handoff advisory
// BEFORE the confirmation. Synthetic targets only. The advisory is a warning:
// it never refuses, adds no prompt, and never changes the retire request.

const (
	advCurrent = `{"data":{"worker":{"continuity":{"status":"CURRENT","handoff":` +
		`{"urn":"hrn:node:acme.com:iris:handoffs:h1","loc":"handoffs:h1","createdAt":"2026-10-01T01:15:00Z"}}}}}`
	advFirst   = `{"data":{"worker":{"continuity":{"status":"FIRST_STINT","handoff":null}}}}`
	advMissing = `{"data":{"worker":{"continuity":{"status":"MISSING","handoff":null}}}}`
	advNull    = `{"data":{"worker":{"continuity":null}}}`
	advFuture  = `{"data":{"worker":{"continuity":{"status":"SOMETHING_NEW","handoff":null}}}}`
)

func retireAdvisoryRun(t *testing.T, advisory, live string, extra ...string) (stdout, stderr string, ops []string, captured map[string]json.RawMessage, err error) {
	t.Helper()
	worker := withLiveness(irisWorkerJSON, live)
	retired := strings.Replace(worker, `"retiredAt":null`, `"retiredAt":"2026-08-14T10:00:00Z"`, 1)
	staff := `{"data":{"workers":{"total":1,"items":[` + worker + `]}}}`
	responses := map[string]string{
		"Workers": staff, "WorkersRoster": staff, "GetWorkerInApp": `{"data":{"worker":` + worker + `}}`,
		"GetWorkerRetireAdvisory": advisory,
		"RetireWorker":            `{"data":{"retireWorker":` + retired + `}}`,
	}
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		ops = append(ops, op)
		return responses[op]
	})
	f, out, errOut := testFactoryTTY(t, "y\n")
	_ = out
	root := NewRootCmd(f)
	args := append([]string{"team", "worker", "retire", "Iris", "--app", "acme.com:eng-team", "--server", gql.URL}, extra...)
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errOut.String(), ops, captured, err
}

func opIndex(ops []string, op string) int {
	for i, o := range ops {
		if o == op {
			return i
		}
	}
	return -1
}

func TestRetireAdvisoryStatesText(t *testing.T) {
	for _, tc := range []struct {
		name, advisory, live string
		want, dontWant       []string
	}{
		{"current", advCurrent, "false",
			[]string{"hrn:node:acme.com:iris:handoffs:h1", "2026-10-01T01:15:00Z", "retirement stops new bindings and deletes no handoff nodes"},
			[]string{"unconsumed", "unread", "lost", "may still write", "no session"}},
		{"first stint", advFirst, "false",
			[]string{"first stint"}, []string{"unconsumed", "no session", "none was written"}},
		{"missing", advMissing, "false",
			[]string{"no handoff from the last stint that you can see", "not proof none was written"}, []string{"unconsumed", "lost"}},
		{"null is unavailable not absence", advNull, "false",
			[]string{"unavailable to you"}, []string{"no handoff from the last stint", "first stint", "none was written"}},
		{"unknown future status is unavailable", advFuture, "false",
			[]string{"unavailable to you"}, []string{"SOMETHING_NEW", "no handoff from the last stint"}},
		{"open session", advCurrent, "true",
			[]string{"open session may still write"}, nil},
		{"live unknown says nothing about sessions", advMissing, "null",
			nil, []string{"open session", "no session", "never"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, _, _, err := retireAdvisoryRun(t, tc.advisory, tc.live, "--yes")
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tc.want {
				if !strings.Contains(stderr, w) {
					t.Errorf("advisory (stderr) missing %q: %s", w, stderr)
				}
			}
			for _, w := range tc.dontWant {
				if strings.Contains(stderr, w) {
					t.Errorf("advisory must not say %q: %s", w, stderr)
				}
			}
			if strings.Contains(stdout, "handoff") || strings.Contains(stdout, "Handoff") {
				t.Errorf("the human advisory belongs on stderr, not stdout: %s", stdout)
			}
		})
	}
}

func TestRetireAdvisoryComesBeforeConfirmAndDeclineSendsNoRetire(t *testing.T) {
	stdout, stderr, ops, captured, err := retireAdvisoryRun(t, advCurrent, "false") // TTY, answers "y"
	if err != nil {
		t.Fatal(err)
	}
	_ = stdout
	if !strings.Contains(stderr, "Retire worker Iris") || strings.Index(stderr, "hrn:node:acme.com:iris:handoffs:h1") > strings.Index(stderr, "Retire worker Iris") {
		t.Errorf("advisory must precede the confirmation prompt: %s", stderr)
	}
	if opIndex(ops, "GetWorkerRetireAdvisory") < 0 || opIndex(ops, "GetWorkerRetireAdvisory") > opIndex(ops, "RetireWorker") {
		t.Errorf("one advisory read must precede the retire: %v", ops)
	}
	if _, ok := captured["RetireWorker"]; !ok {
		t.Error("confirmed retire must send")
	}

	// declined: advisory read happened, no retire request.
	worker := withLiveness(irisWorkerJSON, "false")
	staff := `{"data":{"workers":{"total":1,"items":[` + worker + `]}}}`
	gql, cap2 := captureGraphQL(t, map[string]string{"Workers": staff, "WorkersRoster": staff, "GetWorkerRetireAdvisory": advCurrent})
	f, _, errOut := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "retire", "Iris", "--app", "acme.com:eng-team", "--server", gql.URL})
	err = root.Execute()
	if code := exitCodeFor(err); code != exitcode.Cancelled {
		t.Errorf("decline exit = %d, want Cancelled; %v", code, err)
	}
	if _, ok := cap2["RetireWorker"]; ok {
		t.Error("declined confirmation must send no retire")
	}
	if !strings.Contains(errOut.String(), "handoffs:h1") {
		t.Errorf("advisory must show before a declined prompt: %s", errOut.String())
	}
}

func TestRetireAdvisoryNonTTYStillNeedsYes(t *testing.T) {
	worker := withLiveness(irisWorkerJSON, "false")
	staff := `{"data":{"workers":{"total":1,"items":[` + worker + `]}}}`
	gql, captured := captureGraphQL(t, map[string]string{"Workers": staff, "WorkersRoster": staff, "GetWorkerRetireAdvisory": advCurrent})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "retire", "Iris", "--app", "acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Errorf("exit = %d, want Usage; %v", code, err)
	}
	if _, ok := captured["RetireWorker"]; ok {
		t.Error("no retire without --yes")
	}
}

func TestRetireAdvisoryJSON(t *testing.T) {
	for _, tc := range []struct {
		name, advisory, live string
		status               any
		live2                any
		urn                  any
	}{
		{"current", advCurrent, "true", "CURRENT", true, "hrn:node:acme.com:iris:handoffs:h1"},
		{"missing", advMissing, "false", "MISSING", false, nil},
		{"null continuity and null liveness", advNull, "null", nil, nil, nil},
		{"unknown status", advFuture, "false", nil, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, _, _, err := retireAdvisoryRun(t, tc.advisory, tc.live, "--yes", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if e := json.Unmarshal([]byte(stdout), &got); e != nil {
				t.Fatalf("stdout must be one JSON document: %v: %s", e, stdout)
			}
			for _, k := range []string{"id", "name", "retiredAt", "status"} {
				if _, ok := got[k]; !ok {
					t.Errorf("existing key %q must be unchanged: %s", k, stdout)
				}
			}
			adv, ok := got["advisory"].(map[string]any)
			if !ok {
				t.Fatalf("advisory object missing: %s", stdout)
			}
			if len(adv) != 3 {
				t.Errorf("advisory carries exactly continuityStatus/hasLiveSession/handoffURN: %v", adv)
			}
			for k, want := range map[string]any{"continuityStatus": tc.status, "hasLiveSession": tc.live2, "handoffURN": tc.urn} {
				v, present := adv[k]
				if !present || v != want {
					t.Errorf("advisory.%s = %v (present=%v), want %v", k, v, present, want)
				}
			}
			if strings.Contains(stdout, "2026-10-01T01:15:00Z") || strings.Contains(stdout, "handoffs:h1\"") && tc.urn == nil {
				t.Errorf("stdout carries only the specified facts: %s", stdout)
			}
			if !strings.Contains(stderr, "retirement") {
				t.Errorf("the human advisory still goes to stderr under --json: %s", stderr)
			}
		})
	}
}

func TestRetireAdvisoryReadFailureContinuesThroughSameConfirmation(t *testing.T) {
	for _, tc := range []struct{ name, advisory string }{
		{"graphql error", `{"errors":[{"message":"Cannot query field \"continuity\" on type \"Worker\"."}],"data":null}`},
		{"worker null", `{"data":{"worker":null}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, ops, captured, err := retireAdvisoryRun(t, tc.advisory, "false", "--yes", "--json")
			if err != nil {
				t.Fatalf("an advisory read failure must not block retirement: %v", err)
			}
			if _, ok := captured["RetireWorker"]; !ok {
				t.Error("retire must still be sent")
			}
			n := 0
			for _, o := range ops {
				if o == "RetireWorker" {
					n++
				}
			}
			if n != 1 {
				t.Errorf("exactly one retire request, got %d: %v", n, ops)
			}
			var got struct {
				Advisory map[string]any `json:"advisory"`
			}
			_ = json.Unmarshal([]byte(stdout), &got)
			if got.Advisory["continuityStatus"] != nil || got.Advisory["handoffURN"] != nil {
				t.Errorf("unavailable read means null facts, never guessed: %s", stdout)
			}
			if strings.Contains(stdout, "Cannot query") || strings.Contains(stderr, "Cannot query") {
				t.Errorf("no raw provider error: %s | %s", stdout, stderr)
			}
			if !strings.Contains(stderr, "unavailable") {
				t.Errorf("safe unavailable text expected: %s", stderr)
			}
		})
	}
}

func TestRetireAdvisoryDoesNotMaskRetireFailureOrReplayIt(t *testing.T) {
	worker := withLiveness(irisWorkerJSON, "false")
	staff := `{"data":{"workers":{"total":1,"items":[` + worker + `]}}}`
	var ops []string
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		ops = append(ops, op)
		switch op {
		case "Workers", "WorkersRoster":
			return staff
		case "GetWorkerInApp":
			return `{"data":{"worker":` + worker + `}}`
		case "GetWorkerRetireAdvisory":
			return advCurrent
		case "RetireWorker":
			return `{"errors":[{"message":"nope","extensions":{"code":"FORBIDDEN"}}],"data":null}`
		}
		return ""
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "retire", "Iris", "--yes", "--app", "acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a retire refusal must surface")
	}
	n, adv := 0, 0
	for _, o := range ops {
		if o == "RetireWorker" {
			n++
		}
		if o == "GetWorkerRetireAdvisory" {
			adv++
		}
	}
	if n != 1 || adv != 1 {
		t.Errorf("one advisory read, at most one retire: %v", ops)
	}
}

func TestRetireAdvisoryReadSelectsNoHandoffBodyOrSessionInternals(t *testing.T) {
	_, _, _, captured, err := retireAdvisoryRun(t, advCurrent, "false", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	var vars struct {
		Ref string `json:"ref"`
	}
	if e := json.Unmarshal(captured["GetWorkerRetireAdvisory"], &vars); e != nil || vars.Ref != "wkr1" {
		t.Errorf("advisory is read for the resolved worker id: %s", captured["GetWorkerRetireAdvisory"])
	}
	doc := gen_GetWorkerRetireAdvisoryDoc()
	for _, banned := range []string{"content", "previousSession", "autoExpiredAt", "endedAt"} {
		if strings.Contains(doc, banned) {
			t.Errorf("advisory operation must not select %q: %s", banned, doc)
		}
	}
}

func gen_GetWorkerRetireAdvisoryDoc() string { return gen.GetWorkerRetireAdvisory_Operation }
