package workflowguards

// hadron-cli#3021 / hadron-server#4916: the drift workflow builds its sparse server checkout from the
// server-owned manifest (scripts/sdl-export-files.txt) at the same ref as the checkout, merged with the CLI's
// own entries, and fails loudly (an infrastructure failure, never a drift verdict) when the manifest is
// absent or unreadable. There is no hand-kept fallback.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const manifestStepName = "Read the server's SDL-export manifest"

func TestSDLSparseList3021(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	script := repoFile(t, "scripts/sdl-sparse-list.sh")
	good := "# generated\n# more\nscripts/export-graphql-sdl.mjs\nsrc/api/graphql/schema/typeDefs.ts\nsrc/lib/agentTasks/teamMetricsCaps.ts\n"
	own := []string{"src/mcp", "src/lib/runner/tools", "src/lib/hostOperations/types.ts"}
	for _, tc := range []struct {
		name, manifest string
		own            []string
		want           int
		wantOut        string
	}{
		{"manifest entries then the CLI's own, in that order", good, own, 0,
			"scripts/export-graphql-sdl.mjs\nsrc/api/graphql/schema/typeDefs.ts\nsrc/lib/agentTasks/teamMetricsCaps.ts\nsrc/mcp\nsrc/lib/runner/tools\nsrc/lib/hostOperations/types.ts\n"},
		{"a path in both is listed once", good + "src/mcp\n", own, 0,
			"scripts/export-graphql-sdl.mjs\nsrc/api/graphql/schema/typeDefs.ts\nsrc/lib/agentTasks/teamMetricsCaps.ts\nsrc/mcp\nsrc/lib/runner/tools\nsrc/lib/hostOperations/types.ts\n"},
		{"blank lines and comments are skipped", "\n# c\n\nscripts/a.mjs\n  \n", nil, 0, "scripts/a.mjs\n"},
		{"an empty manifest", "", own, 2, ""},
		{"only comments", "# a\n# b\n", own, 2, ""},
		{"an absolute path", "/etc/passwd\n", own, 2, ""},
		{"a parent segment", "src/../../x\n", own, 2, ""},
		{"a glob would widen the checkout", "src/*\n", own, 2, ""},
		{"a negation", "!src/mcp\n", own, 2, ""},
		{"a path with a space", "src/a b.ts\n", own, 2, ""},
		{"a carriage return", "src/a.ts\r\n", own, 2, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := filepath.Join(t.TempDir(), "manifest")
			if err := os.WriteFile(m, []byte(tc.manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			out, code := run(t, "", nil, "bash", append([]string{script, m}, tc.own...)...)
			if code != tc.want {
				t.Fatalf("exit %d, want %d:\n%s", code, tc.want, out)
			}
			if tc.want == 0 && out != tc.wantOut {
				t.Errorf("printed %q, want %q", out, tc.wantOut)
			}
			if tc.want != 0 && strings.TrimSpace(out) == "" {
				t.Errorf("a refusal must say why")
			}
		})
	}
	// an unreadable manifest path
	if out, code := run(t, "", nil, "bash", script, filepath.Join(t.TempDir(), "absent")); code != 2 || !strings.Contains(out, "absent") {
		t.Errorf("a missing manifest file is exit 2 naming the path: %d %q", code, out)
	}
}

func rawSteps3021(t *testing.T) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(repoFile(t, ".github/workflows/schema-drift.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []map[string]any `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	return wf.Jobs["schema-drift"].Steps
}

// Structure: the manifest is read BEFORE the checkout, is a hard step, and the checkout takes both its ref and
// its sparse list from it. No hand-kept server path survives in the workflow except the CLI's own entries.
func TestDriftCheckoutIsDrivenByTheManifest3021(t *testing.T) {
	steps := rawSteps3021(t)
	mi, ci := -1, -1
	for i, s := range steps {
		switch s["name"] {
		case manifestStepName:
			mi = i
		case "Check out hadron-server SDL source":
			ci = i
		}
	}
	if mi < 0 || ci < 0 || mi >= ci {
		t.Fatalf("the manifest step (%d) must exist and precede the checkout (%d)", mi, ci)
	}
	if s := steps[mi]; s["continue-on-error"] == true || s["id"] != "manifest" {
		t.Errorf("the manifest step is a hard step with id manifest: %v", s)
	}
	with, _ := steps[ci]["with"].(map[string]any)
	if with["ref"] != "${{ steps.manifest.outputs.sha }}" {
		t.Errorf("the checkout is of the very ref the manifest was read at: ref = %v", with["ref"])
	}
	sparse, _ := with["sparse-checkout"].(string)
	if strings.TrimSpace(sparse) != "${{ steps.manifest.outputs.sparse }}" {
		t.Errorf("the sparse list is the manifest step's output, with no hand-kept server path beside it:\n%s", sparse)
	}
}

func manifestStepRun3021(t *testing.T) string {
	t.Helper()
	for _, s := range rawSteps3021(t) {
		if s["name"] == manifestStepName {
			run, _ := s["run"].(string)
			return run
		}
	}
	t.Fatal("no manifest step")
	return ""
}

// The step's script, run for real against a stub gh: a good read prints the sha and the merged sparse list; an
// absent manifest, an unreadable one (the token cannot read it) and an unresolvable ref each fail the step with
// a message that says what is wrong and that there is no fallback.
func TestManifestStepFailsLoudlyAndNeverFallsBack3021(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	script := manifestStepRun3021(t)
	manifest := "# generated\nscripts/export-graphql-sdl.mjs\nsrc/api/graphql/schema/typeDefs.ts\nsrc/lib/agentTasks/teamMetricsCaps.ts\n"
	for _, tc := range []struct {
		name, gh     string
		wantFail     bool
		wantContains []string
	}{
		{"good", "case \"$2\" in */commits/HEAD) echo abc123def456;; *contents*) printf '%s' '" + manifest + "';; esac", false, nil},
		{"manifest absent at that ref", "case \"$2\" in */commits/HEAD) echo abc123def456;; *contents*) echo 'gh: Not Found (HTTP 404)' >&2; exit 1;; esac", true,
			[]string{"scripts/sdl-export-files.txt", "abc123def456", "no hand-kept", "hadron-server#4916"}},
		{"token cannot read it", "case \"$2\" in */commits/HEAD) echo abc123def456;; *contents*) echo 'gh: Resource not accessible (HTTP 403)' >&2; exit 1;; esac", true,
			[]string{"HADRON_SERVER_RO_TOKEN", "no hand-kept"}},
		{"the ref cannot be resolved", "exit 1", true, []string{"HADRON_SERVER_RO_TOKEN"}},
		{"an empty manifest", "case \"$2\" in */commits/HEAD) echo abc123def456;; *contents*) true;; esac", true, []string{"no hand-kept"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			bin := filepath.Join(tmp, "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\n"+tc.gh+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			outFile := filepath.Join(tmp, "github-output")
			out, code := run(t, repoFile(t, "."), []string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"RUNNER_TEMP=" + tmp, "GITHUB_OUTPUT=" + outFile,
			}, "bash", "-e", "-c", script)
			if (code != 0) != tc.wantFail {
				t.Fatalf("exit %d, wantFail=%v:\n%s", code, tc.wantFail, out)
			}
			for _, w := range tc.wantContains {
				if !strings.Contains(out, w) {
					t.Errorf("the failure lacks %q:\n%s", w, out)
				}
			}
			got, _ := os.ReadFile(outFile)
			if tc.wantFail && len(got) != 0 {
				t.Errorf("a failed read must leave no outputs behind: %q", got)
			}
			if !tc.wantFail {
				for _, want := range []string{"sha=abc123def456", "scripts/export-graphql-sdl.mjs", "src/lib/agentTasks/teamMetricsCaps.ts", "src/mcp", "src/lib/runner/tools", "src/lib/hostOperations/types.ts"} {
					if !strings.Contains(string(got), want) {
						t.Errorf("the output lacks %q:\n%s", want, got)
					}
				}
			}
		})
	}
}
