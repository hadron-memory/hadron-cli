package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3236 (host-file C2): `team host doctor [--host <id>] [--json]`. One line per fact, each accounts-file
// entry, and with --host each assigned worker. It runs against a fake `orca` on PATH and a hermetic HOME, so
// the whole command is exercised the way the binary runs it. Exit 0 the host can run every assigned worker,
// 5 something is missing, 10 Orca did not answer (10 dominates). Orca's own JSON is recorded in
// internal/hostfacts/testdata (hadron-server#5210).

func oneLine3236(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "hostfacts", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

type doctorHost3236 struct {
	bin, cfg, state, home string
}

// newDoctorHost3236 builds the machine: a hermetic HOME, XDG dirs, and a PATH holding only what the test puts in
// bin. withOrca installs a fake `orca`; programs are empty executables.
func newDoctorHost3236(t *testing.T, withOrca bool, programs ...string) *doctorHost3236 {
	t.Helper()
	root := t.TempDir()
	h := &doctorHost3236{bin: filepath.Join(root, "bin"), home: filepath.Join(root, "home")}
	h.cfg, h.state = filepath.Join(h.home, ".config"), filepath.Join(h.home, ".state")
	for _, d := range []string{h.bin, h.home, h.cfg, h.state} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if withOrca {
		script := "#!/bin/sh\ncase \"$*\" in\n" +
			" \"--version\") printf '%s\\n' '1.4.247' ;;\n" +
			" \"repo list --json\") printf '%s\\n' '" + oneLine3236(t, "orca_repo_list.linux.json") + "' ;;\n" +
			" \"worktree list --json --limit 5000\") printf '%s\\n' '" + oneLine3236(t, "orca_worktree_list.linux.json") + "' ;;\n" +
			" *) echo \"unexpected orca call: $*\" >&2; exit 3 ;;\nesac\n"
		if err := os.WriteFile(filepath.Join(h.bin, "orca"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range programs {
		if err := os.WriteFile(filepath.Join(h.bin, p), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", h.home)
	t.Setenv("PATH", h.bin)
	t.Setenv("XDG_CONFIG_HOME", h.cfg)
	t.Setenv("XDG_STATE_HOME", h.state)
	return h
}

func (h *doctorHost3236) accounts(t *testing.T, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(h.cfg, "hadron"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(h.cfg, "hadron", "accounts.yaml")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

func assignment3236(workdir, family, account string) string {
	acc := "null"
	if account != "" {
		acc = `"` + account + `"`
	}
	return `{"id":"a-` + strings.ReplaceAll(workdir, "/", "-") + `","hostId":"orca1","workerId":"w1","worker":{"name":"Iris"},"workdir":"` + workdir +
		`","family":"` + family + `","model":null,"effort":null,"accountProfile":` + acc + `,"createdAt":"2026-11-26T00:00:00Z","updatedAt":"2026-11-26T00:00:00Z"}`
}

func setup3236(source string, assignments ...string) map[string]string {
	return map[string]string{"TeamHostSetupWithProfile": `{"data":{"hostSetup":{"source":"` + source + `","assignments":[` + strings.Join(assignments, ",") + `]}}}`}
}

func doctor3236(t *testing.T, responses map[string]string, args ...string) (string, int, *[]attnCall) {
	t.Helper()
	calls, out, err := runTeamTask(t, responses, append([]string{"team", "host", "doctor"}, args...)...)
	code := 0
	if err != nil {
		code = exitOf(err)
	}
	return out, code, calls
}

func doctorJSON3236(t *testing.T, out string) map[string]any {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("--json must print one document: %v\n%s", err, out)
	}
	return d
}

func TestDoctorOrcaMissingExits10AndNamesTheInstallStep(t *testing.T) {
	newDoctorHost3236(t, false, "claude")
	out, code, _ := doctor3236(t, nil)
	if code != exitcode.OrcaUnreachable {
		t.Fatalf("exit %d, want 10 when Orca is not on PATH\n%s", code, out)
	}
	if !strings.Contains(out, "Orca") || !strings.Contains(out, "install Orca") || !strings.Contains(out, "PATH") {
		t.Errorf("the line names Orca and the install step:\n%s", out)
	}
	// --json prints the whole DTO at exit 10 (the report is the answer; a failed read prints only an error).
	newDoctorHost3236(t, false, "claude")
	jout, jcode, _ := doctor3236(t, nil, "--json")
	d := doctorJSON3236(t, jout)
	if jcode != exitcode.OrcaUnreachable || d["ok"] != false || d["orca"].(map[string]any)["bin"] != nil {
		t.Errorf("--json at exit 10: code %d, %v", jcode, d["orca"])
	}
}

func TestDoctorHealthyHostWithoutHostFlagIsFullyOfflineAndExitsZero(t *testing.T) {
	newDoctorHost3236(t, true, "claude", "codex", "claude-glm", "claude-kimi")
	out, code, calls := doctor3236(t, nil)
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "1.4.247") || !strings.Contains(out, "orca") {
		t.Errorf("the Orca fact is shown with its version:\n%s", out)
	}
	if len(*calls) != 0 {
		t.Errorf("without --host the command makes no request: %v", *calls)
	}
	if strings.Contains(strings.ToLower(out), "accounts file") && strings.Contains(strings.ToLower(out), "missing") {
		t.Errorf("no accounts file and no named account is no complaint:\n%s", out)
	}
}

func TestDoctorJSONShapeAndEmptiesAreArrays(t *testing.T) {
	newDoctorHost3236(t, true, "claude", "codex", "claude-glm", "claude-kimi")
	out, code, _ := doctor3236(t, nil, "--json")
	d := doctorJSON3236(t, out)
	if code != 0 || d["ok"] != true {
		t.Fatalf("code %d, ok %v", code, d["ok"])
	}
	for _, k := range []string{"orca", "facts", "accountsFile", "accounts", "workers", "ok"} {
		if _, has := d[k]; !has {
			t.Errorf("--json lacks %q: %v", k, d)
		}
	}
	for _, k := range []string{"facts", "accounts", "workers"} {
		if _, isArr := d[k].([]any); !isArr {
			t.Errorf("%s must be [] not null: %v", k, d[k])
		}
	}
	if len(d["workers"].([]any)) != 0 || len(d["accounts"].([]any)) != 0 {
		t.Errorf("no --host and no file: workers and accounts are empty: %v", d)
	}
	if d["accountsFile"].(map[string]any)["present"] != false {
		t.Errorf("accountsFile.present: %v", d["accountsFile"])
	}
}

func TestDoctorPerWorkerWorkdirVerdicts(t *testing.T) {
	newDoctorHost3236(t, true, "claude")
	out, code, _ := doctor3236(t, setup3236("SERVER",
		assignment3236("hadron-server/antonio", "claude", ""),
		assignment3236("hadron-server/ghost", "claude", ""),
		assignment3236("hadron-server/wt", "claude", "")), "--host", "orca1", "--app", "hrn:app:acme.com:ops")
	if code != exitcode.Conflict {
		t.Fatalf("exit %d, want 5\n%s", code, out)
	}
	for _, want := range []string{"WORKDIR_NOT_ON_HOST", "hadron-server/ghost", "team worker onboard", "WORKDIR_AMBIGUOUS_ON_HOST",
		"/home/u/orca/workspaces/hadron-server/wt", "/home/u/scratch/qa/wt"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor must say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "hadron-server/antonio") && strings.Contains(out, "WORKDIR_NOT_ON_HOST: hadron-server/antonio") {
		t.Errorf("a workdir Orca knows is no problem:\n%s", out)
	}
}

func TestDoctorAccountsAreResolvedPerAssignment(t *testing.T) {
	cfgDir := t.TempDir()
	for name, tc := range map[string]struct {
		file string
		want []string // must appear
		code int
	}{
		"no file but a named account":  {"", []string{"ACCOUNT_NOT_ON_HOST", "work"}, exitcode.Conflict},
		"a file without that account":  {"accounts:\n  other:\n    family: claude\n", []string{"ACCOUNT_NOT_ON_HOST", "work"}, exitcode.Conflict},
		"entry family differs":         {"accounts:\n  work:\n    family: codex\n", []string{"ACCOUNT_NOT_ON_HOST", "work", "codex", "claude"}, exitcode.Conflict},
		"entry configDir missing":      {"accounts:\n  work:\n    family: claude\n    configDir: /nonexistent/dir-3236\n", []string{"ACCOUNT_NOT_ON_HOST", "work", "configDir"}, exitcode.Conflict},
		"entry program missing":        {"accounts:\n  work:\n    family: claude\n    program: /nonexistent/prog-3236\n", []string{"ACCOUNT_NOT_ON_HOST", "work", "program"}, exitcode.Conflict},
		"a resolvable account is fine": {"accounts:\n  work:\n    family: claude\n    configDir: " + cfgDir + "\n", nil, 0},
	} {
		t.Run(name, func(t *testing.T) {
			h := newDoctorHost3236(t, true, "claude")
			if tc.file != "" {
				h.accounts(t, tc.file, 0o600)
			}
			out, code, _ := doctor3236(t, setup3236("SERVER", assignment3236("hadron-server/antonio", "claude", "work")), "--host", "orca1", "--app", "hrn:app:acme.com:ops")
			if code != tc.code {
				t.Fatalf("exit %d, want %d\n%s", code, tc.code, out)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("must say %q:\n%s", w, out)
				}
			}
		})
	}
}

func TestDoctorRefusesAnAccountsFileWithAWorkersKey(t *testing.T) {
	h := newDoctorHost3236(t, true, "claude")
	h.accounts(t, "accounts:\n  w:\n    family: claude\nworkers:\n  iris: {}\n", 0o600)
	out, code, _ := doctor3236(t, nil)
	if code != exitcode.Conflict || !strings.Contains(out, "workers") || !strings.Contains(out, "accounts only") {
		t.Fatalf("exit %d, the refusal names the key:\n%s", code, out)
	}
	h2 := newDoctorHost3236(t, true, "claude")
	h2.accounts(t, "accounts:\n  w:\n    family: claude\n    tool: claude-code\n", 0o600)
	out, code, _ = doctor3236(t, nil)
	if code != exitcode.Conflict || !strings.Contains(out, "needs a newer hadron") || strings.Contains(strings.ToLower(out), "unknown key") {
		t.Fatalf("a reserved key is answered 'needs a newer hadron' (exit %d):\n%s", code, out)
	}
	h3 := newDoctorHost3236(t, true, "claude")
	h3.accounts(t, "accounts:\n  w:\n    family: claude\n", 0o600)
	jout, _, _ := doctor3236(t, nil, "--json")
	d := doctorJSON3236(t, jout)
	if len(d["accounts"].([]any)) != 1 {
		t.Errorf("a valid entry is listed: %v", d["accounts"])
	}
}

func TestDoctorUnderHostSetupSourceHOSTSaysSoAndGivesNoVerdicts(t *testing.T) {
	newDoctorHost3236(t, true, "claude")
	out, code, _ := doctor3236(t, setup3236("HOST", assignment3236("hadron-server/ghost", "claude", "")), "--host", "orca1", "--app", "hrn:app:acme.com:ops")
	if code != 0 {
		t.Fatalf("exit %d, want 0 (nothing to check per worker)\n%s", code, out)
	}
	if n := strings.Count(out, "App does not take its setup from the platform"); n != 1 {
		t.Errorf("the one line appears once, got %d:\n%s", n, out)
	}
	if strings.Contains(out, "WORKDIR_NOT_ON_HOST") || strings.Contains(out, "ghost") {
		t.Errorf("no per-worker verdicts under HOST:\n%s", out)
	}
	jout, _, _ := doctor3236(t, setup3236("HOST", assignment3236("hadron-server/ghost", "claude", "")), "--host", "orca1", "--app", "hrn:app:acme.com:ops", "--json")
	if d := doctorJSON3236(t, jout); len(d["workers"].([]any)) != 0 {
		t.Errorf("workers is empty under HOST: %v", d["workers"])
	}
}

// Only names leave the machine (epic R7): the one existing read carries appRef and hostId, nothing of the
// accounts file, and (later) no tool value.
func TestDoctorSendsNoAccountDetailToTheServer(t *testing.T) {
	h := newDoctorHost3236(t, true, "claude")
	secretDir := t.TempDir()
	h.accounts(t, "accounts:\n  work:\n    family: claude\n    program: /opt/secret-program-3236\n    configDir: "+secretDir+"\n", 0o600)
	_, _, calls := doctor3236(t, setup3236("SERVER", assignment3236("hadron-server/antonio", "claude", "work")), "--host", "orca1", "--app", "hrn:app:acme.com:ops")
	if len(*calls) == 0 {
		t.Fatal("--host makes the one assignments read")
	}
	for _, c := range *calls {
		if c.Op != "TeamHostSetupWithProfile" {
			t.Errorf("only the existing assignments read is made, got %s", c.Op)
		}
		raw := c.RawOp + " " + string(c.RawVars) + " " + c.Query
		for _, secret := range []string{"secret-program-3236", secretDir, "/opt/", "configDir", "program"} {
			if strings.Contains(raw, secret) {
				t.Errorf("%s leaves the machine in %s: %s", secret, c.Op, raw)
			}
		}
		for k := range c.Vars {
			if k != "appRef" && k != "hostId" {
				t.Errorf("the read carries only appRef and hostId, got variable %q", k)
			}
		}
	}
}

// doctor never reads a host file: assignments come from the signed-in person's read or the binding.
func TestDoctorNeverReadsAHostFile(t *testing.T) {
	h := newDoctorHost3236(t, true, "claude")
	for _, p := range []string{filepath.Join(h.cfg, "hadron", "host.yaml"), filepath.Join(h.home, "host.yaml"), "host.yaml"} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err == nil {
			_ = os.WriteFile(p, []byte("app: not-a-ref\nkey_file: /nonexistent/key\nworkers: [broken\n"), 0o600)
			t.Cleanup(func() { _ = os.Remove(p) })
		}
	}
	out, code, _ := doctor3236(t, nil)
	if code != 0 {
		t.Fatalf("a malformed host.yaml must not affect doctor: exit %d\n%s", code, out)
	}
	src, err := os.ReadFile(filepath.Join("team", "host_doctor.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"hostpoller", "key_file", "LoadConfig"} {
		if strings.Contains(string(src), banned) {
			t.Errorf("host_doctor.go must not touch the host file: it mentions %q", banned)
		}
	}
}

// A server that predates S1b answers the setup read without the profile: the account is UNKNOWN, never absent.
func TestDoctorAnOlderServerLeavesTheAccountUnknownNotAbsent(t *testing.T) {
	newDoctorHost3236(t, true, "claude")
	r := map[string]string{
		"TeamHostSetupWithProfile": oldServerProfileRefusal2157,
		"TeamHostSetup": `{"data":{"hostSetup":{"source":"SERVER","assignments":[{"id":"a1","hostId":"orca1","workerId":"w1","worker":{"name":"Iris"},` +
			`"workdir":"hadron-server/antonio","family":"claude","model":null,"createdAt":"2026-11-26T00:00:00Z","updatedAt":"2026-11-26T00:00:00Z"}]}}}`,
	}
	out, code, _ := doctor3236(t, r, "--host", "orca1", "--app", "hrn:app:acme.com:ops")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if strings.Contains(out, "ACCOUNT_NOT_ON_HOST") || !strings.Contains(strings.ToLower(out), "unknown") {
		t.Errorf("the account is unknown on an older server, not a problem:\n%s", out)
	}
}

func TestDoctorAFailedAssignmentsReadPrintsTheLocalSectionsThenExitsWithTheMappedCode(t *testing.T) {
	newDoctorHost3236(t, true, "claude")
	out, code, _ := doctor3236(t, map[string]string{"TeamHostSetupWithProfile": gqlErrorJSON("FORBIDDEN")}, "--host", "orca1", "--app", "hrn:app:acme.com:ops")
	if code != exitcode.Forbidden {
		t.Fatalf("exit %d, want 8\n%s", code, out)
	}
	if !strings.Contains(out, "1.4.247") {
		t.Errorf("the local sections are printed before the refusal:\n%s", out)
	}
}

func TestDoctorAnUnassignedFamilysMissingProgramIsInformational(t *testing.T) {
	newDoctorHost3236(t, true, "claude")
	out, code, _ := doctor3236(t, nil)
	if code != 0 {
		t.Fatalf("a missing codex nobody is assigned is no problem: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "codex") {
		t.Errorf("the fact is still shown:\n%s", out)
	}
	_, code, _ = doctor3236(t, setup3236("SERVER", assignment3236("hadron-server/antonio", "codex", "")), "--host", "orca1", "--app", "hrn:app:acme.com:ops")
	if code != exitcode.Conflict {
		t.Errorf("a missing program an assigned worker needs is exit 5, got %d", code)
	}
}
