// Package workflowguards holds no code — only the tests that pin the nightly
// schema-drift workflow's failure routing and its tool-pin reader
// (hadron-cli#2712), the way scripts/pinserverdl pins the pin script.
//
// A workflow cannot be run from a test, so the guards are pinned at the two
// levels that can: scripts/read-tool-pins.sh is run for real against fixture
// SOURCE files, and the workflow's own `run:` blocks are extracted from the
// YAML and run in a temp directory against stub npm/tsx, plus a structural
// check of the step conditions that decide which red a failure becomes.
package workflowguards

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..", filepath.FromSlash(rel))
}

func run(t *testing.T, dir string, env []string, name string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running %s: %v", name, err)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

func TestReadToolPins2712(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	script := repoFile(t, "scripts/read-tool-pins.sh")
	for _, tc := range []struct {
		name, source string
		want         int
		wantOut      string
	}{
		{"exact versions", "base=a\ntsx=4.23.15\ngraphql=16.15.0\n", 0, "tsx=4.23.15\ngraphql=16.15.0\n"},
		{"prerelease and build", "tsx=4.1.0-rc.1\ngraphql=16.9.0+build.5\n", 0, "tsx=4.1.0-rc.1\ngraphql=16.9.0+build.5\n"},
		{"missing tsx", "graphql=16.15.0\n", 2, ""},
		{"missing graphql", "tsx=4.23.15\n", 2, ""},
		{"blank tsx", "tsx=\ngraphql=16.15.0\n", 2, ""},
		{"duplicate tsx", "tsx=4.23.15\ntsx=4.24.0\ngraphql=16.15.0\n", 2, ""},
		{"duplicate graphql", "tsx=4.23.15\ngraphql=16.15.0\ngraphql=16.16.0\n", 2, ""},
		{"a tag", "tsx=latest\ngraphql=16.15.0\n", 2, ""},
		{"a caret range", "tsx=4.23.15\ngraphql=^16.15.0\n", 2, ""},
		{"a partial version", "tsx=4.23\ngraphql=16.15.0\n", 2, ""},
		{"a range of two", "tsx=4.23.15 || 5.0.0\ngraphql=16.15.0\n", 2, ""},
		{"a trailing space", "tsx=4.23.15 \ngraphql=16.15.0\n", 2, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "SOURCE")
			if err := os.WriteFile(src, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			out, code := run(t, "", nil, "bash", script, src)
			if code != tc.want {
				t.Fatalf("exit %d, want %d:\n%s", code, tc.want, out)
			}
			if tc.want == 0 && out != tc.wantOut {
				t.Errorf("printed %q, want %q", out, tc.wantOut)
			}
		})
	}
	// The committed SOURCE must itself pass: the nightly reads it.
	if out, code := run(t, "", nil, "bash", script, repoFile(t, "schema/SOURCE")); code != 0 {
		t.Errorf("the committed schema/SOURCE is not a valid pin set (exit %d):\n%s", code, out)
	}
}

type step struct {
	Name            string            `yaml:"name"`
	ID              string            `yaml:"id"`
	If              string            `yaml:"if"`
	ContinueOnError bool              `yaml:"continue-on-error"`
	Env             map[string]string `yaml:"env"`
	Run             string            `yaml:"run"`
}

func driftSteps(t *testing.T) (map[string]step, []string) {
	t.Helper()
	raw, err := os.ReadFile(repoFile(t, ".github/workflows/schema-drift.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []step `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	steps := map[string]step{}
	var order []string
	for _, s := range wf.Jobs["schema-drift"].Steps {
		if s.Name != "" {
			steps[s.Name] = s
			order = append(order, s.Name)
		}
	}
	return steps, order
}

// The routing that decides which red a failure becomes: the export is a hard
// step before the comparison, the comparison alone is the drift verdict, and
// the infrastructure reporter fires on any failure that is not a drift verdict.
func TestSchemaDriftFailureRouting2712(t *testing.T) {
	steps, order := driftSteps(t)
	idx := map[string]int{}
	for i, n := range order {
		idx[n] = i
	}
	export, drift := steps["Export the hadron-server SDL"], steps["Check for schema drift"]
	if export.Name == "" || drift.Name == "" {
		t.Fatalf("the export or drift step is missing: %v", order)
	}
	if export.ContinueOnError {
		t.Error("the export step is continue-on-error: an install or exporter failure would read as drift")
	}
	if idx["Read the tool pins"] >= idx[export.Name] || idx[export.Name] >= idx[drift.Name] {
		t.Errorf("steps out of order: %v", order)
	}
	if !drift.ContinueOnError {
		t.Error("the drift step must be continue-on-error so both drift checks run")
	}
	if strings.Contains(drift.Run, "npm") || strings.Contains(drift.Run, "tsx") {
		t.Errorf("the drift step still installs or exports; a failure there would read as drift:\n%s", drift.Run)
	}
	infra := steps["File an infrastructure-failure issue"]
	const wantIf = "failure() && steps.check.outcome != 'failure' && steps.tools.outcome != 'failure'"
	if infra.If != wantIf {
		t.Errorf("infrastructure reporter condition = %q, want %q", infra.If, wantIf)
	}
}

// The export step's script, run for real against stub npm/tsx: an install
// failure, an exporter failure and an empty export each fail the step (so the
// job fails without a drift verdict); a good export writes the SDL.
func TestExportStepFailsOnInstallExporterOrEmptyOutput2712(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	steps, _ := driftSteps(t)
	script := steps["Export the hadron-server SDL"].Run
	if script == "" {
		t.Fatal("no export step")
	}
	for _, tc := range []struct {
		name         string
		npmFails     bool
		tsxBody      string
		wantFail     bool
		wantContains string
	}{
		{"install fails", true, "echo SDL", true, ""},
		{"exporter fails", false, "exit 1", true, ""},
		{"exporter prints nothing", false, "true", true, "produced no SDL"},
		{"good export", false, "echo 'type Query { a: Int }'", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			server := filepath.Join(tmp, ".hadron-server")
			bin := filepath.Join(tmp, "bin")
			for _, d := range []string{filepath.Join(server, "scripts"), bin} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			tsxSrc := filepath.Join(tmp, "tsx-stub")
			if err := os.WriteFile(tsxSrc, []byte("#!/bin/sh\n"+tc.tsxBody+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			npm := "#!/bin/sh\nmkdir -p node_modules/.bin\ncp \"" + tsxSrc + "\" node_modules/.bin/tsx\n"
			if tc.npmFails {
				npm = "#!/bin/sh\nexit 1\n"
			}
			if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(npm), 0o755); err != nil {
				t.Fatal(err)
			}
			out, code := run(t, tmp, []string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"RUNNER_TEMP=" + tmp, "PIN_TSX=4.23.15", "PIN_GRAPHQL=16.15.0",
			}, "bash", "-e", "-c", script)
			if (code != 0) != tc.wantFail {
				t.Fatalf("exit %d, wantFail=%v:\n%s", code, tc.wantFail, out)
			}
			if tc.wantContains != "" && !strings.Contains(out, tc.wantContains) {
				t.Errorf("output lacks %q:\n%s", tc.wantContains, out)
			}
			if !tc.wantFail {
				got, err := os.ReadFile(filepath.Join(tmp, "server-sdl.graphql"))
				if err != nil || !strings.Contains(string(got), "type Query") {
					t.Errorf("the good export did not write the SDL: %v %q", err, got)
				}
			}
		})
	}
}
