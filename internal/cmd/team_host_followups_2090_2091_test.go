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
	if !strings.Contains(gen.RegisterHostWithRefused_Operation, "refusedWorkers") {
		t.Fatal("the extended registration must select refusedWorkers")
	}
	// A server without hadron-server#4241 refuses any document naming the
	// field, which would take every host poller down: the plain registration
	// and the shared fields never name it (Wolfgang, #108251).
	for name, doc := range map[string]string{"RegisterHost": gen.RegisterHost_Operation, "HostRegistrations": gen.HostRegistrations_Operation} {
		if strings.Contains(doc, "refusedWorkers") {
			t.Fatalf("%s must not select refusedWorkers", name)
		}
	}
}

func registrationWithRefused2091(workers, refused string) string {
	return `{"data":{"registerHost":{"hostId":"h1","label":"box1","pollIntervalSeconds":30,
	"registeredAt":"2026-10-14T12:00:00Z","refreshedAt":"2026-10-14T12:00:00Z","live":true,
	"workers":[` + workers + `],"refusedWorkers":[` + refused + `]}}}`
}

const irisRegistered2091 = `{"workerId":"wkr1","worker":{"id":"wkr1","urn":"hrn:worker:acme.com:eng-team:iris","name":"Iris"},"family":"claude","worktrees":[]}`

const skewRefusal2091 = `{"errors":[{"message":"Cannot query field \"refusedWorkers\" on type \"HostRegistration\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`

// An old server (no hadron-server#4241): the extended document is refused as
// schema skew, the plain registration is made and succeeds, collection goes
// on, and the poller says once that refusals are unknown.
func TestTeamHostRunAgainstAnOldServerStillRegistersAndSaysRefusalsAreUnknown(t *testing.T) {
	cfg := writeHostConfig(t, "")
	srv, calls := hostServerFake(t, map[string]string{
		"RegisterHostWithRefused": skewRefusal2091,
		"RegisterHost":            hostRegistrationAnswer,
		"CollectHostOperations":   `{"data":{"collectHostOperations":[]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("an old server must not fail the registration: %v", err)
	}
	var ops []string
	for _, c := range calls() {
		ops = append(ops, c.op)
		if c.auth != "Bearer hk_host_key" || c.session != "" {
			t.Fatalf("%s: host key only, no session: %q %q", c.op, c.auth, c.session)
		}
	}
	if strings.Join(ops, ",") != "RegisterHostWithRefused,RegisterHost,CollectHostOperations" {
		t.Fatalf("calls = %v", ops)
	}
	if strings.Count(out.String(), "refusals-unknown") != 1 || strings.Contains(out.String(), "worker-refused") {
		t.Fatalf("refusals are reported as unknown, once:\n%s", out.String())
	}
}

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
				"RegisterHostWithRefused": registrationWithRefused2091(tc.workers, tc.refused),
				"CollectHostOperations":   `{"data":{"collectHostOperations":[]}}`,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("a per-row refusal must not fail the poll: %v", err)
			}
			got := calls()
			if len(got) != 2 || got[0].op != "RegisterHostWithRefused" || got[1].op != "CollectHostOperations" {
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
		"RegisterHostWithRefused": registrationWithRefused2091(``, `{"worker":"Evil\u001b[31m","code":"X\u0007"}`),
		"CollectHostOperations":   `{"data":{"collectHostOperations":[]}}`,
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
