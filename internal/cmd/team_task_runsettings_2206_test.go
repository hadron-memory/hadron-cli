package cmd

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2206: a task's run settings (family, model or tier, effort) at
// creation. They say what to launch, not whom to match; they are create-only,
// so nothing here can clear one, and no flag means no runSettings key at all.
func createInput2206(t *testing.T, args ...string) (map[string]any, error) {
	t.Helper()
	calls, _, err := runTeamTask(t, taskResponses(), append([]string{"team", "task", "create", "example", "--json"}, args...)...)
	if err != nil {
		return nil, err
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskCreate" {
			return c.Vars["input"].(map[string]any), nil
		}
	}
	t.Fatal("no create sent")
	return nil, nil
}

func TestTaskCreateSendsOnlyTheRunSettingsGiven2206(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		want map[string]any
	}{
		"model and effort": {[]string{"--model", "claude-sonnet-5-5", "--effort", "HIGH"}, map[string]any{"model": "claude-sonnet-5-5", "effort": "HIGH"}},
		"tier":             {[]string{"--tier", "low"}, map[string]any{"tier": "low"}},
		"family and unset": {[]string{"--family", "codex", "--effort", "UNSET"}, map[string]any{"family": "codex", "effort": "UNSET"}},
		"all but tier":     {[]string{"--family", "claude", "--model", "opus", "--effort", "MAX"}, map[string]any{"family": "claude", "model": "opus", "effort": "MAX"}},
	} {
		t.Run(name, func(t *testing.T) {
			input, err := createInput2206(t, c.args...)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := input["runSettings"].(map[string]any)
			if len(got) != len(c.want) {
				t.Fatalf("runSettings = %#v, want exactly %#v (a flag not given must be absent, never null)", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("runSettings[%s] = %#v, want %#v", k, got[k], v)
				}
			}
		})
	}
}

func TestTaskCreateWithoutRunSettingsSendsNone2206(t *testing.T) {
	input, err := createInput2206(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := input["runSettings"]; present {
		t.Fatalf("no flag, so no runSettings key: %#v", input["runSettings"])
	}
}

// The CLI refuses, before any write, the shapes the server always refuses.
func TestTaskCreateRefusesInvalidRunSettings2206(t *testing.T) {
	for name, args := range map[string][]string{
		"model with tier":   {"--model", "opus", "--tier", "high"},
		"effort NOT_APPLIC": {"--effort", "NOT_APPLICABLE"},
		"tier outside":      {"--tier", "ultra"},
		"effort outside":    {"--effort", "ULTRA"},
		"empty model":       {"--model", " "},
	} {
		t.Run(name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, taskResponses(), append([]string{"team", "task", "create", "example"}, args...)...)
			var ce *exitcode.CodedError
			if !errors.As(err, &ce) || ce.Code != exitcode.Usage {
				t.Fatalf("want a usage error (exit 2), got %v", err)
			}
			for _, c := range *calls {
				if c.Op == "AgentTaskCreate" {
					t.Fatal("a refused shape must send nothing")
				}
			}
		})
	}
}

func TestTaskCreateHelpSaysWhatRunSettingsAreFor2206(t *testing.T) {
	_, out, err := runTeamTask(t, taskResponses(), "team", "task", "create", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--tier", "--effort", "short-lived"} {
		if !strings.Contains(out, want) {
			t.Errorf("help must mention %q", want)
		}
	}
}

// A batch item's task carries runSettings exactly as the file has it, and gets
// the same pre-checks as the flags.
func TestTaskBatchCarriesRunSettings2206(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/batch.json"
	if err := os.WriteFile(path, []byte(`[{"task":{"title":"a","runSettings":{"tier":"mid","effort":"HIGH"}}},{"task":{"title":"b"}}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	calls, _, err := runTeamTask(t, batchResponses1565(2, ""), "team", "task", "create", "--batch-file", path, "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op != "AgentTaskCreateBatch" {
			continue
		}
		items, _ := c.Vars["tasks"].([]any)
		if len(items) != 2 {
			t.Fatalf("tasks = %#v", c.Vars["tasks"])
		}
		first, _ := items[0].(map[string]any)["task"].(map[string]any)
		rs, _ := first["runSettings"].(map[string]any)
		if rs["tier"] != "mid" || rs["effort"] != "HIGH" || len(rs) != 2 {
			t.Fatalf("first item runSettings = %#v", first["runSettings"])
		}
		second, _ := items[1].(map[string]any)["task"].(map[string]any)
		if _, present := second["runSettings"]; present {
			t.Fatalf("an item without runSettings must send none: %#v", second)
		}
		return
	}
	t.Fatal("no batch call")
}

func TestTaskBatchRefusesInvalidRunSettings2206(t *testing.T) {
	for name, body := range map[string]string{
		"model and tier": `[{"task":{"title":"a","runSettings":{"model":"opus","tier":"high"}}}]`,
		"not applicable": `[{"task":{"title":"a","runSettings":{"effort":"NOT_APPLICABLE"}}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			path := t.TempDir() + "/batch.json"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "create", "--batch-file", path)
			var ce *exitcode.CodedError
			if !errors.As(err, &ce) || ce.Code != exitcode.Usage {
				t.Fatalf("want exit 2, got %v", err)
			}
			for _, c := range *calls {
				if c.Op == "AgentTaskCreateBatch" {
					t.Fatal("nothing may be sent")
				}
			}
		})
	}
}

// `task get` shows what the task asked for beside what its run applied, in
// text and in --json, or neither (cor:agt:050:01).
func TestTaskGetShowsRunSettingsAndAppliedTogether2206(t *testing.T) {
	withBoth := strings.Replace(taskFieldsJSON, `"id":"task1"`, `"id":"task1","runSettings":{"family":"claude","model":null,"tier":"high","effort":"HIGH"},"appliedRunSettings":{"family":"claude","model":"opus","effort":"HIGH"}`, 1)
	onlyAsked := strings.Replace(taskFieldsJSON, `"id":"task1"`, `"id":"task1","runSettings":{"family":"codex","model":null,"tier":null,"effort":null},"appliedRunSettings":null`, 1)
	onlyApplied := strings.Replace(taskFieldsJSON, `"id":"task1"`, `"id":"task1","runSettings":null,"appliedRunSettings":{"family":"claude","model":"sonnet","effort":null}`, 1)
	neither := taskFieldsJSON
	for name, c := range map[string]struct {
		fields       string
		wantRun      bool
		wantApplied  string
		wantAskedKey string
	}{
		"both":         {withBoth, true, `"model":"opus"`, `"tier":"high"`},
		"asked only":   {onlyAsked, true, `"appliedRunSettings":{}`, `"family":"codex"`},
		"applied only": {onlyApplied, true, `"runSettings":{}`, `"model":"sonnet"`},
		"neither":      {neither, false, "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			r := taskResponses()
			r["AgentTaskGet"] = `{"data":{"agentTask":` + c.fields + `}}`
			_, out, err := runTeamTask(t, r, "team", "task", "get", "task1", "--json")
			if err != nil {
				t.Fatal(err)
			}
			out = strings.Join(strings.Fields(out), "") // the JSON is indented: compare without whitespace
			if has := strings.Contains(out, `"runSettings"`); has != c.wantRun {
				t.Fatalf("runSettings present = %v, want %v\n%s", has, c.wantRun, out)
			}
			if has := strings.Contains(out, `"appliedRunSettings"`); has != c.wantRun {
				t.Fatalf("appliedRunSettings must appear exactly when runSettings does\n%s", out)
			}
			if c.wantApplied != "" && !strings.Contains(out, c.wantApplied) {
				t.Fatalf("missing %s\n%s", c.wantApplied, out)
			}
			if c.wantAskedKey != "" && !strings.Contains(out, c.wantAskedKey) {
				t.Fatalf("the requested settings must be shown: missing %s\n%s", c.wantAskedKey, out)
			}
			if name == "both" && strings.Contains(out, `"model":null`) {
				t.Fatalf("a null requested field must be omitted, not printed as null\n%s", out)
			}
			_, text, err := runTeamTask(t, r, "team", "task", "get", "task1")
			if err != nil {
				t.Fatal(err)
			}
			if has := strings.Contains(text, "run settings:") && strings.Contains(text, "applied:"); has != c.wantRun {
				t.Fatalf("text output pair present = %v, want %v\n%s", has, c.wantRun, text)
			}
		})
	}
}

// No second model-name rule on the client (Wolfgang #111086): only an empty or
// whitespace value is refused here; a leading '_' or '.' and a long name reach
// the server, which judges them (BAD_VALUE, exit 2, with its reason).
func TestRunSettingTokensAreTheServersToJudge2206(t *testing.T) {
	for _, model := range []string{"_custom-model", ".custom", "-x", "gpt-6.1-sol", strings.Repeat("a", 200), "a/b"} {
		input, err := createInput2206(t, "--model", model)
		if err != nil {
			t.Errorf("model %q must reach the server: %v", model, err)
			continue
		}
		if rs, _ := input["runSettings"].(map[string]any); rs["model"] != model {
			t.Errorf("model %q not sent: %#v", model, input["runSettings"])
		}
	}
	for _, model := range []string{"has space", " ", "tab\tname"} {
		if _, err := createInput2206(t, "--model", model); err == nil {
			t.Errorf("model %q must be refused before any call", model)
		}
	}
}
