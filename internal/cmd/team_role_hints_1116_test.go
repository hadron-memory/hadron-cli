package cmd

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func roleHint1116(t *testing.T, source, role string) string {
	t.Helper()
	responses := map[string]string{
		"TeamRoles":       teamRolesJSON,
		"TeamAppIdentity": teamAppIdentityJSON,
		"AppAgentRoster":  replaceMetadataLeaf1112(t, engTeamRosterJSON, "data/app/agents/1/personaRole", role),
		"CastWorker":      `{"data":{"castWorker":` + irisWorkerJSON + `}}`,
	}
	srv, _ := captureGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := []string{"--server", srv.URL, "--app", "acme.com:eng-team", "team", "role", "list"}
	if source == "cast" {
		args = []string{"--server", srv.URL, "--app", "acme.com:eng-team", "team", "worker", "cast", "--name", "Iris", "--role", role, "--json"}
	}
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if source == "cast" {
		return f.IOStreams.ErrOut.(*strings.Builder).String()
	}
	return out.String()
}

// Replay the printed command through a real POSIX shell, capturing argv in a
// local function rather than invoking any external CLI or real mutation.
func TestRoleCreateHintReplay1116(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX copyable suggestions are not emitted on Windows")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	for _, source := range []string{"list", "cast"} {
		for _, role := range []string{"--help", "-x", "ios-app-engineer", "two words", "role' $HOME; echo NEVER"} {
			t.Run(source+"/"+role, func(t *testing.T) {
				rendered := roleHint1116(t, source, role)
				const prefix = "`hadron team role create "
				start := strings.Index(rendered, prefix)
				if start < 0 {
					t.Fatalf("missing create hint: %s", rendered)
				}
				command := rendered[start+1:]
				end := strings.Index(command, "`")
				if end < 0 {
					t.Fatal("unterminated command")
				}
				command = command[:end]
				cmd := exec.Command(bash, "--noprofile", "--norc", "-c", `hadron() { printf '%s\0' "$@"; }; `+command)
				raw, err := cmd.Output()
				if err != nil {
					t.Fatal(err)
				}
				argv := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
				responses := map[string]string{"TeamAppIdentity": teamAppIdentityJSON, "CreateTeamRole": `{"data":{"createTeamRole":{"role":"fixture","loc":"roles:fixture","nodeId":"role1","description":null,"roleAgent":null}}}`}
				calls, out, err := runTeamTask(t, responses, argv...)
				create := stopCalls(calls, "CreateTeamRole")
				if err != nil || len(create) != 1 || create[0].Vars["role"] != role {
					t.Fatalf("copied hint %q parsed as %q: err=%v create=%v output=%s", command, argv, err, create, out)
				}
				// Only the intended create is a write, never a flag side effect.
				for _, call := range *calls {
					if call.Op != "CreateTeamRole" && strings.Contains(call.Query, "mutation ") {
						t.Fatalf("unexpected write %s", call.Op)
					}
				}
			})
		}
	}
}

func TestRoleCreateHintAlteredValue1116(t *testing.T) {
	for _, source := range []string{"list", "cast"} {
		for _, role := range []string{"role\nchanged", "role\x1b[31m", strings.Repeat("r", 300)} {
			t.Run(source+"/"+role[:4], func(t *testing.T) {
				out := roleHint1116(t, source, role)
				if strings.Contains(out, "`hadron team role create ") || !strings.Contains(out, "--json") {
					t.Fatalf("altered role must not become copyable: %s", out)
				}
			})
		}
	}
}
