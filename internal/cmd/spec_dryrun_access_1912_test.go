package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1912: `spec edit|new|rescind --dry-run` are client-computed plans, so a
// caller who cannot write used to get a full plan and exit 0, then the real run
// was refused. A dry run now answers as the real run would: the server's own
// verdict (effectiveAccess), the real run's sentence, exit 8. The target is read
// FIRST, so an unreadable memory or spec gives the real run's not-found and the
// refusal never confirms that a target exists (cor:api:140:03).

const writeRefusal = "Forbidden: no write access to this memory"

func effectiveAccessJSON(canWrite bool) string {
	return `{"data":{"effectiveAccess":{"user":{"id":"u1","name":"U","email":null,"handle":"u"},"resourceUrn":"hrn:mem:micromentor.org:platform-specs","resourceKind":"memory","canRead":true,"canWrite":` +
		map[bool]string{true: "true", false: "false"}[canWrite] + `,"canManage":false,"canDelete":false,"role":null,"grants":[]}}}`
}

const authUserJSON = `{"data":{"authContext":{"principalType":"USER","appId":null,"agentId":null,"user":{"id":"u1","name":"U","email":null,"handle":"u","githubUsername":null,"roles":[]},"apiKey":null,"impersonation":null}}}`

func accessMocks(m map[string]string, canWrite bool) map[string]string {
	m["AuthContext"] = authUserJSON
	m["EffectiveAccess"] = effectiveAccessJSON(canWrite)
	return m
}

type dryRunCase struct {
	name string
	args func(t *testing.T) []string
	mock func() map[string]string
}

func newDryMocks() map[string]string {
	return map[string]string{
		"FindNodes":  `{"data":{"nodes":[` + specNodeList("msg", `["spec","p1"]`) + `,` + specNodeList("msg:010", `["spec","p1"]`) + `]}}`,
		"ResolveUrn": notFoundResolve,
	}
}

func dryRunCases() []dryRunCase {
	return []dryRunCase{
		{"edit", func(t *testing.T) []string {
			return []string{"spec", "edit", "msg:010:02", "-m", specMem, "--content-file", writeTemp(t, "b.md", placeholderBody+"\nNew line.\n"), "--dry-run"}
		}, previewMocks},
		{"new scaffold", func(*testing.T) []string {
			return []string{"spec", "new", "-m", specMem, "--module", "msg", "--feature", "010", "--title", "Test", "--dry-run"}
		}, newDryMocks},
		{"new at loc", func(*testing.T) []string {
			return []string{"spec", "new", "glossary", "-m", specMem, "--title", "G", "--dry-run"}
		}, newDryMocks},
		{"new path", func(*testing.T) []string {
			return []string{"spec", "new", "msg:010:01", "--new-path", "--title", "Send", "-m", specMem, "--dry-run"}
		}, newDryMocks},
	}
}

func runDry(t *testing.T, args []string, mocks map[string]string) (string, map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, mocks)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", gql.URL))
	err := root.Execute()
	return out.String(), captured, err
}

// A caller the server says cannot write gets the real run's refusal and exit 8,
// and no plan is printed.
func TestSpecDryRunRefusesACallerWithoutWriteAccess(t *testing.T) {
	for _, c := range dryRunCases() {
		t.Run(c.name, func(t *testing.T) {
			out, captured, err := runDry(t, c.args(t), accessMocks(c.mock(), false))
			if got := exitCodeFor(err); got != exitcode.Forbidden {
				t.Fatalf("exit = %d (%v), want Forbidden %d", got, err, exitcode.Forbidden)
			}
			if err == nil || err.Error() != writeRefusal {
				t.Errorf("message %q, want exactly the real run's %q", err, writeRefusal)
			}
			if strings.Contains(out, "would create") || strings.Contains(out, "dry run") {
				t.Errorf("a refused dry run printed a plan:\n%s", out)
			}
			var vars struct{ User, Resource string }
			if json.Unmarshal(captured["EffectiveAccess"], &vars) != nil || vars.User != "u1" || !strings.Contains(vars.Resource, "platform-specs") {
				t.Errorf("effectiveAccess must be asked for the caller on the memory, got %s", captured["EffectiveAccess"])
			}
		})
	}
}

// A caller who can write still gets the plan, exactly as before.
func TestSpecDryRunStillPlansForAWriter(t *testing.T) {
	for _, c := range dryRunCases() {
		t.Run(c.name, func(t *testing.T) {
			out, _, err := runDry(t, c.args(t), accessMocks(c.mock(), true))
			if err != nil {
				t.Fatalf("a writer's dry run failed: %v", err)
			}
			if strings.TrimSpace(out) == "" {
				t.Error("no plan printed")
			}
		})
	}
}

// Without a user (an App key) the server cannot be asked; the dry run stays
// the plan it was and the real write's gate stays the authority.
func TestSpecDryRunWithoutAUserIsUnchanged(t *testing.T) {
	for _, c := range dryRunCases() {
		t.Run(c.name, func(t *testing.T) {
			_, captured, err := runDry(t, c.args(t), c.mock())
			if err != nil {
				t.Fatalf("unchanged dry run failed: %v", err)
			}
			if _, asked := captured["EffectiveAccess"]; asked {
				t.Error("effectiveAccess asked for a caller with no user")
			}
		})
	}
}

// notFoundExit is the exit code the CLI gives a server NOT_FOUND, the real
// run's answer for a target the caller cannot read.
func notFoundExit() int {
	return exitCodeFor(api.MapError(&gqlerror.Error{Message: "x", Extensions: map[string]any{"code": "NOT_FOUND"}}))
}

// Access-query mapping: when the server conceals the memory from effectiveAccess
// (NOT_FOUND), the dry run gives exactly the not-found exit, never the write refusal.
func TestSpecDryRunConcealedMemoryFromAccessIsNotFound(t *testing.T) {
	for _, c := range dryRunCases() {
		t.Run(c.name, func(t *testing.T) {
			m := accessMocks(c.mock(), false)
			m["EffectiveAccess"] = `{"errors":[{"message":"Memory not found","extensions":{"code":"NOT_FOUND"}}]}`
			_, _, err := runDry(t, c.args(t), m)
			if got := exitCodeFor(err); err == nil || got != notFoundExit() || strings.Contains(err.Error(), "write access") {
				t.Fatalf("got %v (exit %d), want the not-found exit %d", err, got, notFoundExit())
			}
		})
	}
}

// A lookup that FAILS is never read as "allowed": the reader does not get a
// plan and exit 0 (cli#1912 BLIND P2). Both operations, every dry-run path.
func TestSpecDryRunAccessLookupFailureIsNotASuccessfulPlan(t *testing.T) {
	boom := `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
	for _, op := range []string{"AuthContext", "EffectiveAccess"} {
		for _, c := range dryRunCases() {
			t.Run(op+"/"+c.name, func(t *testing.T) {
				m := accessMocks(c.mock(), true)
				m[op] = boom
				out, _, err := runDry(t, c.args(t), m)
				if err == nil {
					t.Fatalf("a failed %s lookup still produced a plan:\n%s", op, out)
				}
				if exitCodeFor(err) == exitcode.Forbidden {
					t.Errorf("a failed lookup must not read as a refusal: %v", err)
				}
			})
		}
	}
}

// The ordering guarantee itself: when the TARGET read is the not-found (an
// unreadable memory or spec; `new <loc>` reads only "is the loc free", so its
// concealed-memory case is the access-query one above), the dry run gives the real run's not-found and
// neither access operation is sent.
func TestSpecDryRunUnreadableTargetNeverAsksAccess(t *testing.T) {
	nf := `{"errors":[{"message":"not found","extensions":{"code":"NOT_FOUND"}}]}`
	for _, c := range []struct {
		name string
		args func(t *testing.T) []string
		mock func() map[string]string
		op   string
	}{
		{"new scaffold", func(*testing.T) []string {
			return []string{"spec", "new", "-m", specMem, "--module", "msg", "--feature", "010", "--title", "T", "--dry-run"}
		}, newDryMocks, "FindNodes"},
		{"edit", func(t *testing.T) []string {
			return []string{"spec", "edit", "msg:010:02", "-m", specMem, "--content-file", writeTemp(t, "b.md", "x\n"), "--dry-run"}
		}, previewMocks, "GetSpecNodeForEdit"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := accessMocks(c.mock(), false)
			m[c.op] = nf
			_, captured, err := runDry(t, c.args(t), m)
			if err == nil || exitCodeFor(err) != notFoundExit() {
				t.Fatalf("got %v (exit %d), want the real run's not-found exit %d", err, exitCodeFor(err), notFoundExit())
			}
			for _, op := range []string{"AuthContext", "EffectiveAccess"} {
				if _, asked := captured[op]; asked {
					t.Errorf("%s sent although the target read failed first", op)
				}
			}
		})
	}
}

// The real (non-dry) run is untouched: no client-side access check, the write is sent.
func TestSpecEditRealRunDoesNotConsultAccess(t *testing.T) {
	_, captured, err := runDry(t, []string{"spec", "edit", "msg:010:02", "-m", specMem, "--content-file", writeTemp(t, "b.md", placeholderBody+"\nNew line.\n")}, accessMocks(previewMocks(), false))
	if err != nil {
		t.Fatalf("real run: %v", err)
	}
	if _, asked := captured["EffectiveAccess"]; asked {
		t.Error("a real run must leave the gate to the server write")
	}
	if _, wrote := captured["UpdateSpecNode"]; !wrote {
		t.Error("the real run must still send its write")
	}
}

func TestSpecRescindDryRunAccess(t *testing.T) {
	dry := append([]string{"--dry-run"}, rescindArgs[:len(rescindArgs)-1]...) // no --yes
	_, s, err := runRescind(t, &rescindServer{authUser: "u1", canWrite: false}, "", dry...)
	if exitCodeFor(err) != exitcode.Forbidden || err.Error() != writeRefusal {
		t.Fatalf("reader: %v, want %q exit 8", err, writeRefusal)
	}
	if s.calls["UpdateSpecNode"] != 0 {
		t.Error("a dry run wrote")
	}
	if out, _, err := runRescind(t, &rescindServer{authUser: "u1", canWrite: true}, "", dry...); err != nil || !strings.Contains(out, "dry run") {
		t.Fatalf("writer: %v\n%s", err, out)
	}
	if _, _, err := runRescind(t, &rescindServer{}, "", dry...); err != nil {
		t.Fatalf("no user: %v", err)
	}
}

// An App key resolves (authContext present) but to no user: the check is skipped.
func TestSpecDryRunAppKeyCallerIsUnchanged(t *testing.T) {
	for _, c := range dryRunCases() {
		t.Run(c.name, func(t *testing.T) {
			m := c.mock()
			m["AuthContext"] = `{"data":{"authContext":{"principalType":"APP","appId":"a1","agentId":null,"user":null,"apiKey":null,"impersonation":null}}}`
			if _, _, err := runDry(t, c.args(t), m); err != nil {
				t.Fatalf("app-key dry run failed: %v", err)
			}
		})
	}
}
