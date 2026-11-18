package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #2493: a plain `team availability set` whose mutation answers null or leaves
// the field out must not panic. The write was sent, so the outcome is unknown:
// exit 1 like the instructed branches, no success receipt, and advice to read the current state before
// retrying. All four combinations (--worker/--family x null/absent) are pinned.
func TestTeamAvailabilitySetNullResult2493(t *testing.T) {
	shapes := map[string]string{"null": `null`, "absent": ``}
	for _, target := range []struct {
		name, op, field string
		args            []string
	}{
		{"worker", "WorkerAvailabilitySet", "setWorkerAvailability", []string{"--worker", "Rufus", "--state", "available"}},
		{"family", "ModelFamilyAvailabilitySet", "setModelFamilyAvailability", []string{"--family", "codex", "--state", "available"}},
	} {
		for shape, value := range shapes {
			for _, jsonMode := range []bool{false, true} {
				name := fmt.Sprintf("%s/%s/json=%v", target.name, shape, jsonMode)
				t.Run(name, func(t *testing.T) {
					responses := taskResponses()
					body := `{"data":{}}`
					if value != "" {
						body = `{"data":{"` + target.field + `":` + value + `}}`
					}
					responses[target.op] = body
					argv := append([]string{"team", "availability", "set"}, target.args...)
					if jsonMode {
						argv = append(argv, "--json")
					}
					var (
						calls *[]attnCall
						out   string
						err   error
					)
					func() {
						defer func() {
							if r := recover(); r != nil {
								t.Fatalf("panic on a %s mutation result: %v", shape, r)
							}
						}()
						calls, out, err = runTeamTask(t, responses, argv...)
					}()
					sent := 0
					for _, c := range *calls {
						if c.Op == target.op {
							sent++
						}
					}
					if sent != 1 {
						t.Fatalf("the mutation must be sent once and never retried: %d", sent)
					}
					if err == nil {
						t.Fatalf("a missing result must not succeed: stdout=%q", out)
					}
					if code := exitCodeFor(err); code != exitcode.Error {
						t.Fatalf("exit %d, want %d (outcome unknown): %v", code, exitcode.Error, err)
					}
					msg := err.Error()
					for _, want := range []string{"no availability result", "outcome unknown", "read availability before retrying"} {
						if !strings.Contains(msg, want) {
							t.Fatalf("message lacks %q: %q", want, msg)
						}
					}
					if !jsonMode && out != "" {
						t.Fatalf("no receipt on stdout: %q", out)
					}
					if jsonMode {
						f, jout := testFactory(t)
						f.JSON = true
						if code := renderError(f, err); code != exitcode.Error {
							t.Fatalf("envelope exit %d", code)
						}
						var env struct {
							Error map[string]any `json:"error"`
						}
						if e := json.Unmarshal([]byte(jout.String()), &env); e != nil || env.Error["code"] != float64(exitcode.Error) {
							t.Fatalf("no structured failure: %v %s", e, jout.String())
						}
						if out != "" {
							t.Fatalf("a failed command writes no success JSON: %q", out)
						}
					}
				})
			}
		}
	}
}
