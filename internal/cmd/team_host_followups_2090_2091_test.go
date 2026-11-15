package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2091 (hadron-server#4241): `team host run` surfaces registerHost's
// per-worker refusals, selecting the field on registerHost's own answer only.

func TestRegisterHostSelectsRefusedWorkersAndNothingSharedChanges(t *testing.T) {
	if !strings.Contains(gen.RegisterHost_Operation, "refusedWorkers") {
		t.Fatal("registerHost must select refusedWorkers")
	}
	if strings.Contains(gen.HostRegistrations_Operation, "refusedWorkers") {
		t.Fatal("the shared registration fields (reads) must not select refusedWorkers: it is empty on every read and absent on older servers")
	}
}

func registrationWithRefused2091(workers, refused string) string {
	return `{"data":{"registerHost":{"hostId":"h1","label":"box1","pollIntervalSeconds":30,
	"registeredAt":"2026-10-14T12:00:00Z","refreshedAt":"2026-10-14T12:00:00Z","live":true,
	"workers":[` + workers + `],"refusedWorkers":[` + refused + `]}}}`
}

const irisRegistered2091 = `{"workerId":"wkr1","worker":{"id":"wkr1","urn":"hrn:worker:acme.com:eng-team:iris","name":"Iris"},"family":"claude","worktrees":[]}`

func TestTeamHostRunSurfacesRefusedWorkersWithoutFailing(t *testing.T) {
	for _, tc := range []struct {
		name, workers, refused string
		wantEvents             int
	}{
		{"mixed accepted and refused", irisRegistered2091, `{"worker":"Synthetic","code":"HOST_WORKER_NOT_ASSIGNED"}`, 1},
		{"all refused", ``, `{"worker":"Iris","code":"HOST_WORKER_NOT_ASSIGNED"},{"worker":"Synthetic","code":"HOST_WORKER_NOT_ASSIGNED"}`, 2},
		{"none refused", irisRegistered2091, ``, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := writeHostConfig(t, "")
			srv, calls := hostServerFake(t, map[string]string{
				"RegisterHost":          registrationWithRefused2091(tc.workers, tc.refused),
				"CollectHostOperations": `{"data":{"collectHostOperations":[]}}`,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("a per-row refusal must not fail the poll: %v", err)
			}
			got := calls()
			if len(got) != 2 || got[0].op != "RegisterHost" || got[1].op != "CollectHostOperations" {
				t.Fatalf("collection proceeds after a refusal: %+v", got)
			}
			for _, c := range got {
				if c.auth != "Bearer hk_host_key" || c.session != "" {
					t.Fatalf("%s: host-key attribution and no worker session: auth %q session %q", c.op, c.auth, c.session)
				}
			}
			n := strings.Count(out.String(), "worker-refused")
			if n != tc.wantEvents {
				t.Fatalf("%d refusal events, want %d:\n%s", n, tc.wantEvents, out.String())
			}
			if tc.wantEvents > 0 && !strings.Contains(out.String(), "Synthetic: HOST_WORKER_NOT_ASSIGNED") {
				t.Fatalf("the event names the worker and the server's code:\n%s", out.String())
			}
		})
	}
}

// A control character in the server's text never reaches the terminal.
func TestTeamHostRunRefusalTextIsMadeSafeToPrint(t *testing.T) {
	cfg := writeHostConfig(t, "")
	srv, _ := hostServerFake(t, map[string]string{
		"RegisterHost":          registrationWithRefused2091(``, `{"worker":"Evil\u001b[31m","code":"X\u0007"}`),
		"CollectHostOperations": `{"data":{"collectHostOperations":[]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\x07") {
		t.Fatalf("control characters reached the output: %q", out.String())
	}
}

// hadron-cli#2090: a missing or null answer to unassign is an unknown outcome,
// never "there was none".
func TestTeamHostUnassignMissingOrNullAnswerIsAFailureNotRemovedFalse(t *testing.T) {
	for _, tc := range []struct {
		name, op, resp string
		extra          []string
	}{
		{"missing", "TeamHostUnassign", `{"data":{}}`, nil},
		{"null", "TeamHostUnassign", `{"data":{"unassignHostWorker":null}}`, nil},
		{"instructed missing", "TeamHostUnassignInstructed", `{"data":{}}`, []string{"--instruction", "task:hrn:task:acme.com:eng:7"}},
		{"instructed null", "TeamHostUnassignInstructed", `{"data":{"unassignHostWorker":null}}`, []string{"--instruction", "task:hrn:task:acme.com:eng:7"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := responses2080()
			r[tc.op] = tc.resp
			argv := append([]string{"team", "host", "unassign", "Jonas", "--host", "orca1", "--json"}, tc.extra...)
			calls, out, err := runTeamTask(t, r, argv...)
			if err == nil || exitCodeFor(err) != exitcode.Error {
				t.Fatalf("want exit %d, got %v (exit %d)", exitcode.Error, err, exitCodeFor(err))
			}
			if strings.TrimSpace(out) != "" || strings.Contains(out, "removed") {
				t.Fatalf("no removed:false may be published for an unknown outcome: %q", out)
			}
			n := 0
			for _, c := range *calls {
				if strings.HasPrefix(c.Op, "TeamHostUnassign") {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("the mutation is sent once and never retried as another principal, got %d", n)
			}
		})
	}
}
