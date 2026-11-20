// Package pinserverdl holds no code — only the test that drives
// scripts/pin-server-sdl.sh's credential scrub (hadron-cli#2696).
//
// The script's promise: when HADRON_SERVER_FETCH_TOKEN is set (the nightly
// pin-verify job), it authenticates its git fetch with a derived header, and
// no npm-launched process ever sees the credential — not through the derived
// GIT_CONFIG_* header, and not through the variable itself. --ignore-scripts
// stops lifecycle scripts, but tsx and graphql still execute with the script's
// environment, so a scrub that unsets only the header leaves the raw token
// readable by everything the install runs: exactly the P2 this pins against
// (the first version of the scrub did that while its comments claimed the
// whole credential was gone).
//
// A Go test rather than a shell harness because the repo has one test runner
// and `make test` is the gate; scripts/writeifproduced is the precedent for a
// test-only package under scripts/.
//
// Method — the reviewer's repro made permanent: the REAL script bytes, read
// from the checkout and copied into a fixture root with a fixture
// schema/SOURCE, run against a local git origin, with `npm` shadowed on PATH
// by a stub that records its arguments and environment and installs a stub
// `tsx` that records its environment. The fixture SOURCE is completed from a
// probe run's deterministic merge SHA so the --verify run exits green — the
// whole path (arg parse → fetch → worktree → merge → scrub → install →
// export → compare) is driven, not a fragment. Kills at least: dropping
// HADRON_SERVER_FETCH_TOKEN from the unset (the token appears in the tsx and
// npm dumps), dropping the unset wholesale (GIT_CONFIG_VALUE_0 appears), and
// dropping --ignore-scripts from the install (it vanishes from the recorded
// npm arguments).
package pinserverdl

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tokenValue is the fake credential handed to the script under test. If the
// scrub regresses, this exact string shows up in the stubs' environment dumps.
const tokenValue = "SECRETTOKEN-pin-verify-fixture"

// stubExport is what the stub tsx prints as the "exported SDL". Its sha256 and
// line count go into the fixture SOURCE and its bytes into the fixture
// snapshot, so the verify leg compares against exactly it and ends green.
const stubExport = "stubbed SDL export\n"

// script reads the real script bytes from the checkout.
func script(t *testing.T) []byte {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(wd, "..", "pin-server-sdl.sh"))
	if err != nil {
		t.Fatalf("read pin-server-sdl.sh: %v", err)
	}
	return b
}

// git runs git -C dir and fails the test on any error, returning trimmed stdout.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git -C %s %s: %v\n%s", dir, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// cleanEnviron drops the scrubbed names from the inherited environment so the
// run starts from a known state (a developer exporting the token globally
// must not make the assertions pass or fail by accident).
func cleanEnviron() []string {
	drop := map[string]bool{
		"HADRON_SERVER_FETCH_TOKEN": true,
		"GIT_CONFIG_COUNT":          true,
		"GIT_CONFIG_KEY_0":          true,
		"GIT_CONFIG_VALUE_0":        true,
	}
	var env []string
	for _, kv := range os.Environ() {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if !drop[name] {
			env = append(env, kv)
		}
	}
	return env
}

// runScript runs the fixture copy of the real script and returns combined
// output plus the exit code (0 expected by callers).
func runScript(t *testing.T, scriptPath, repo, out string, verify bool, extraEnv ...string) (string, int) {
	t.Helper()
	args := []string{scriptPath, repo, out}
	if verify {
		args = append(args, "--verify")
	}
	cmd := exec.Command("bash", args...)
	cmd.Env = append(cleanEnviron(), extraEnv...)
	out2, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running the script: %v", err)
		}
		code = ee.ExitCode()
	}
	return string(out2), code
}

// fixtureServer builds the local git "server": a work repo with two commits
// shaped like the SDL source paths, a bare origin that allows filtered fetch
// by exact SHA (what the script's authenticated fetch needs), and a clone of
// it to hand the script. Returns the clone's path and the two commit SHAs.
func fixtureServer(t *testing.T, tmp string) (repo, base, head string) {
	t.Helper()
	work := filepath.Join(tmp, "originwork")
	for _, d := range []string{
		filepath.Join(work, "scripts"),
		filepath.Join(work, "src", "api", "graphql", "schema"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	git(t, work, "init", "--quiet", "-b", "main")
	write := func(p, content string) {
		if err := os.WriteFile(filepath.Join(work, p), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	identity := []string{"-c", "user.name=pin", "-c", "user.email=pin@example.invalid"}
	write("scripts/export-graphql-sdl.mjs", "// fixture exporter\n")
	write("src/api/graphql/schema/typeDefs.ts", "fixture SDL\n")
	git(t, work, append(identity, "add", "-A")...)
	git(t, work, append(identity, "commit", "--quiet", "-m", "base")...)
	base = git(t, work, "rev-parse", "HEAD")
	write("src/api/graphql/schema/typeDefs.ts", "fixture SDL\n// second pin commit\n")
	git(t, work, append(identity, "add", "-A")...)
	git(t, work, append(identity, "commit", "--quiet", "-m", "head")...)
	head = git(t, work, "rev-parse", "HEAD")

	origin := filepath.Join(tmp, "origin")
	git(t, tmp, "clone", "--quiet", "--bare", work, origin)
	git(t, origin, "config", "uploadpack.allowfilter", "true")
	git(t, origin, "config", "uploadpack.allowAnySHA1InWant", "true")
	repo = filepath.Join(tmp, "server")
	git(t, tmp, "clone", "--quiet", origin, repo)
	return repo, base, head
}

// envHas reports whether an `env`-style dump carries a line `name=...`.
func envHas(dump, name string) bool {
	for _, line := range strings.Split(dump, "\n") {
		if strings.HasPrefix(line, name+"=") {
			return true
		}
	}
	return false
}

// pinFixture is the fixture root the script runs from: its copy of the real
// script and a SOURCE completed so --verify can end green, a local git origin,
// the capture dir the stubs write to, and the dir shadowing npm on PATH.
type pinFixture struct {
	tmp, scriptPath, repo, capture, bin string
}

func newPinFixture(t *testing.T) pinFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}

	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	capture := filepath.Join(tmp, "capture")
	bin := filepath.Join(tmp, "bin")
	for _, d := range []string{filepath.Join(root, "scripts"), filepath.Join(root, "schema"), capture, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	scriptPath := filepath.Join(root, "scripts", "pin-server-sdl.sh")
	if err := os.WriteFile(scriptPath, script(t), 0o755); err != nil {
		t.Fatalf("write fixture script: %v", err)
	}
	repo, base, head := fixtureServer(t, tmp)

	// A probe run without the token learns the deterministic merge SHA (the
	// script warns it differs from the placeholder recorded here); the verify
	// run must then reproduce it byte-identically.
	probeSource := "base=" + base + "\nhead=" + head + "\nmerge_sha=probe\ntsx=stub-tsx\ngraphql=stub-graphql\n"
	if err := os.WriteFile(filepath.Join(root, "schema", "SOURCE"), []byte(probeSource), 0o644); err != nil {
		t.Fatalf("write fixture SOURCE: %v", err)
	}
	probeOut := filepath.Join(tmp, "probe-out")
	probeLog, code := runScript(t, scriptPath, repo, probeOut, false)
	if code != 0 {
		t.Fatalf("probe run failed (%d):\n%s", code, probeLog)
	}
	m := regexp.MustCompile(`merge produced ([0-9a-f]{7,40})`).FindStringSubmatch(probeLog)
	if m == nil {
		t.Fatalf("probe run printed no merge SHA to learn from:\n%s", probeLog)
	}
	sum := sha256.Sum256([]byte(stubExport))
	source := "base=" + base + "\nhead=" + head + "\nmerge_sha=" + m[1] +
		"\nsdl_sha256=" + hex.EncodeToString(sum[:]) + "\nsdl_lines=1\ntsx=stub-tsx\ngraphql=stub-graphql\n"
	if err := os.WriteFile(filepath.Join(root, "schema", "SOURCE"), []byte(source), 0o644); err != nil {
		t.Fatalf("write fixture SOURCE: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "schema", "schema.graphql"), []byte(stubExport), 0o644); err != nil {
		t.Fatalf("write fixture snapshot: %v", err)
	}
	return pinFixture{tmp: tmp, scriptPath: scriptPath, repo: repo, capture: capture, bin: bin}
}

// verify runs the fixture script with --verify, the token set and the npm stub
// first on PATH; t.TempDir removes the worktrees with everything else.
func (f pinFixture) verify(t *testing.T, out string) (string, int) {
	t.Helper()
	return runScript(t, f.scriptPath, f.repo, out, true,
		"PATH="+f.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HADRON_SERVER_FETCH_TOKEN="+tokenValue,
		"PIN_TEST_CAPTURE="+f.capture,
	)
}

// The stubs: npm records what it is called with and what its environment
// holds, then installs a tsx that records its environment (or, with
// npmFails, exits 1 without installing anything). PIN_TEST_CAPTURE doubles as
// the canary proving the dumps are live.
func writeNpmStub(t *testing.T, bin string, npmFails bool) {
	t.Helper()
	failure := ""
	if npmFails {
		failure = "exit 1\n"
	}
	npmStub := `#!/bin/sh
env > "$PIN_TEST_CAPTURE/npm.env"
printf '%s\n' "$*" > "$PIN_TEST_CAPTURE/npm.args"
` + failure + `prefix=
prev=
for a in "$@"; do
  if [ "$prev" = "--prefix" ]; then prefix=$a; fi
  prev=$a
done
mkdir -p "$prefix/node_modules/.bin"
cat > "$prefix/node_modules/.bin/tsx" <<'TSX'
#!/bin/sh
env > "$PIN_TEST_CAPTURE/tsx.env"
printf '` + stubExport + `'
TSX
chmod +x "$prefix/node_modules/.bin/tsx"
`
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(npmStub), 0o755); err != nil {
		t.Fatalf("write npm stub: %v", err)
	}
}

func TestNpmNeverSeesTheFetchToken2696(t *testing.T) {
	f := newPinFixture(t)
	writeNpmStub(t, f.bin, false)
	out := filepath.Join(f.tmp, "verify-out")
	log, code := f.verify(t, out)
	if code != 0 {
		t.Fatalf("verify run failed (%d):\n%s", code, log)
	}
	capture := f.capture

	npmEnv := readFile(t, filepath.Join(capture, "npm.env"))
	tsxEnv := readFile(t, filepath.Join(capture, "tsx.env"))
	npmArgs := readFile(t, filepath.Join(capture, "npm.args"))

	// The canary first: an empty or stale dump must not read as "absent".
	if !envHas(npmEnv, "PIN_TEST_CAPTURE") || !envHas(tsxEnv, "PIN_TEST_CAPTURE") {
		t.Fatalf("stub environment dumps are not live (canary missing):\nnpm:\n%s\ntsx:\n%s", npmEnv, tsxEnv)
	}
	for _, dump := range []struct{ who, body string }{
		{"npm", npmEnv},
		{"tsx", tsxEnv},
	} {
		if envHas(dump.body, "HADRON_SERVER_FETCH_TOKEN") {
			t.Errorf("P2 regression: %s ran with HADRON_SERVER_FETCH_TOKEN in its environment:\n%s", dump.who, dump.body)
		}
		if envHas(dump.body, "GIT_CONFIG_VALUE_0") {
			t.Errorf("derived auth header leaked to %s:\n%s", dump.who, dump.body)
		}
	}
	// The token must not survive under ANOTHER name either (a re-export, a
	// renamed variable): its value, and the base64 of the header the script
	// derives from it, appear nowhere in what either stub saw.
	encoded := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + tokenValue))
	for _, dump := range []struct{ who, body string }{{"npm", npmEnv}, {"tsx", tsxEnv}} {
		for _, secret := range []struct{ what, val string }{
			{"token value", tokenValue},
			{"base64 of its auth header", encoded},
		} {
			if strings.Contains(dump.body, secret.val) {
				t.Errorf("P2 regression: the %s reached %s's environment under some name:\n%s", secret.what, dump.who, dump.body)
			}
		}
	}
	if !strings.Contains(npmArgs, "--ignore-scripts") {
		t.Errorf("npm install lost --ignore-scripts (recorded args: %s)", strings.TrimSpace(npmArgs))
	}
	// The green path must also leave the worktree without node_modules — the
	// symlink comes down with the export (that promise is #2696's too).
	if _, err := os.Lstat(filepath.Join(out, "node_modules")); err == nil {
		t.Errorf("worktree kept a node_modules entry after a green verify")
	}
}

// A failing npm install must stop the run with a nonzero exit and a message that
// says where the scratch dir is kept, take the worktree down with it (the exit
// trap), and still never print the credential.
func TestNpmInstallFailureCleansUpAndStaysQuiet2712(t *testing.T) {
	f := newPinFixture(t)
	writeNpmStub(t, f.bin, true)
	out := filepath.Join(f.tmp, "verify-out")
	log, code := f.verify(t, out)
	if code != 1 {
		t.Fatalf("a failing npm install exited %d, want 1:\n%s", code, log)
	}
	if !strings.Contains(log, "npm install failed") {
		t.Errorf("the failure did not say the install failed:\n%s", log)
	}
	if _, err := os.Lstat(out); err == nil {
		t.Errorf("the worktree %s was left behind after the failed install", out)
	}
	if strings.Contains(log, tokenValue) {
		t.Errorf("the failure output leaked the token:\n%s", log)
	}
	// The failed run unregisters its worktree, so the same path is reusable.
	if wts := git(t, f.repo, "worktree", "list", "--porcelain"); strings.Contains(wts, out) {
		t.Errorf("the failed run left %s registered:\n%s", out, wts)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s (the stub did not run?): %v", p, err)
	}
	return string(b)
}
