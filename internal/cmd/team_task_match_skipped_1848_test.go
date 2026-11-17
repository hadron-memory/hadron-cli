package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1848 (hadron-server#3816, V21–V23, cor:agt:050:01): a failed
// `task create --match` answer carries matchSkipped — every skipped candidate
// with every reason, in the server's order. It is returned, never stored, so
// reads stay as they are.

// createFields1848 is the create answer row: matchFailure set when failed,
// matchSkipped as given (raw JSON: a list, or null).
func createFields1848(t *testing.T, failed bool, skipped string) string {
	t.Helper()
	var row map[string]any
	if err := json.Unmarshal([]byte(taskFieldsJSON), &row); err != nil {
		t.Fatal(err)
	}
	if failed {
		row["matchFailure"] = map[string]any{"reason": "NOBODY_FREE", "failedAt": "2026-11-10T15:00:00Z"}
	}
	var s any
	if err := json.Unmarshal([]byte(skipped), &s); err != nil {
		t.Fatal(err)
	}
	row["matchSkipped"] = s
	b, _ := json.Marshal(row)
	return string(b)
}

// FUTURE_REASON is not in the snapshot's enum: V21 is closed-additive, so a
// later server value must still print as itself.
const skipped1848 = `[
  {"workerId":"wkr2","name":"Gene","reasons":["USES_NO_TASKS","HOLDS_TASK","FUTURE_REASON"]},
  {"workerId":"wkr3","name":"Gloria","reasons":["FAMILY_EXCLUDED"]}
]`

func runCreate1848(t *testing.T, row string, extra ...string) (*[]attnCall, string, error) {
	t.Helper()
	r := taskResponses()
	r["AgentTaskCreate"] = `{"data":{"createAgentTask":` + row + `}}`
	return runTeamTask(t, r, append([]string{"team", "task", "create", "example"}, extra...)...)
}

func TestTaskMatchSkipped1848CreateSelectsTheField(t *testing.T) {
	calls, _, err := runCreate1848(t, createFields1848(t, false, `null`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskCreate" {
			if !strings.Contains(c.Query, "matchSkipped") || !strings.Contains(c.Query, "reasons") || !strings.Contains(c.Query, "workerId") {
				t.Fatalf("create does not select matchSkipped{workerId name reasons}:\n%s", c.Query)
			}
			return
		}
	}
	t.Fatal("no create call")
}

func TestTaskMatchSkipped1848HumanRendersEveryCandidateInServerOrder(t *testing.T) {
	_, out, err := runCreate1848(t, createFields1848(t, true, skipped1848), "--match")
	if exitCodeFor(err) != 5 || !errors.Is(err, exitcode.ErrSilent) {
		t.Fatalf("want silent 5, err=%v out=%s", err, out)
	}
	failLine := "  match failed: NOBODY_FREE at 2026-11-10T15:00:00Z — task created unassigned; automatic matching skips it\n"
	if !strings.Contains(out, failLine) {
		t.Fatalf("coarse matchFailure line changed:\n%s", out)
	}
	want := failLine +
		"  skipped: Gene (wkr2): USES_NO_TASKS, HOLDS_TASK, FUTURE_REASON\n" +
		"  skipped: Gloria (wkr3): FAMILY_EXCLUDED\n"
	if !strings.Contains(out, want) {
		t.Fatalf("want skip lines under the failure line, in server order:\n%s\ngot:\n%s", want, out)
	}
}

func TestTaskMatchSkipped1848HumanEmptyListSaysSo(t *testing.T) {
	_, out, err := runCreate1848(t, createFields1848(t, true, `[]`), "--match")
	if exitCodeFor(err) != 5 {
		t.Fatalf("want 5, err=%v", err)
	}
	if !strings.Contains(out, "  skipped: no candidates\n") {
		t.Fatalf("empty skip list not rendered:\n%s", out)
	}
}

func TestTaskMatchSkipped1848JSONIsAnArrayOnTheMatchAnswer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failed  bool
		skipped string
		want    string
	}{
		{"populated", true, skipped1848, `[{"workerId":"wkr2","name":"Gene","reasons":["USES_NO_TASKS","HOLDS_TASK","FUTURE_REASON"]},{"workerId":"wkr3","name":"Gloria","reasons":["FAMILY_EXCLUDED"]}]`},
		{"empty", true, `[]`, `[]`},
		{"assigned, server null", false, `null`, `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, out, err := runCreate1848(t, createFields1848(t, tc.failed, tc.skipped), "--match", "--json")
			if tc.failed && exitCodeFor(err) != 5 || !tc.failed && err != nil {
				t.Fatalf("err=%v out=%s", err, out)
			}
			var row map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &row); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			got, ok := row["matchSkipped"]
			if !ok {
				t.Fatalf("matchSkipped omitted on the match answer: %s", out)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, got); err != nil {
				t.Fatal(err)
			}
			if compact.String() != tc.want {
				t.Fatalf("matchSkipped=%s want %s", compact.String(), tc.want)
			}
		})
	}
}

func TestTaskMatchSkipped1848NoMatchAndReadsNeverFabricate(t *testing.T) {
	// A create without --match: the server sends null; the CLI adds nothing.
	_, out, err := runCreate1848(t, createFields1848(t, false, `null`), "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "matchSkipped") {
		t.Fatalf("create without --match emitted matchSkipped: %s", out)
	}
	// Reads: never selected, never emitted, never rendered.
	r := taskResponses()
	for _, argv := range [][]string{{"team", "task", "get", "task1", "--json"}, {"team", "task", "get", "task1"}} {
		calls, out, err := runTeamTask(t, r, argv...)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "matchSkipped") || strings.Contains(out, "skipped:") {
			t.Fatalf("%v rendered skips: %s", argv, out)
		}
		for _, c := range *calls {
			if c.Op != "AgentTaskCreate" && strings.Contains(c.Query, "matchSkipped") {
				t.Fatalf("%s selects matchSkipped", c.Op)
			}
		}
	}
}

func TestTaskMatchSkipped1848SafeCells(t *testing.T) {
	row := createFields1848(t, true, `[{"workerId":"w\u001b[2Jx","name":"Ev\u001b[31mil","reasons":["A\u0007B"]}]`)
	_, out, _ := runCreate1848(t, row, "--match")
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Fatalf("control characters reached the terminal: %q", out)
	}
}
