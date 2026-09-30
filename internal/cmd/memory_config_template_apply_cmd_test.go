package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#716 slice 3: `memory config template apply` over hadron-server#1334's
// applyMemoryConfigTemplate. The command PREVIEWS (dryRun) first, confirms when
// the preview would REPLACE a rule, then applies the previewed template
// revision, so every test records EACH call's variables, in order.

func applyEntry(role, outcome, rule string) string {
	if rule == "" {
		rule = "null"
	}
	return `{"role":"` + role + `","outcome":"` + outcome + `","rule":` + rule + `}`
}

func applyPayload(dryRun, required bool, revision int, entries ...string) string {
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	return `{"data":{"applyMemoryConfigTemplate":{"memoryId":"mem1",
		"template":{"id":"t1","name":"spec-rules","deleted":false},"templateRevision":` + itoa(revision) + `,
		"required":` + b(required) + `,"dryRun":` + b(dryRun) + `,"entries":[` + strings.Join(entries, ",") + `],"warnings":[]}}}`
}

type applyCall struct {
	DryRun   bool `json:"dryRun"`
	Expected *int `json:"expectedTemplateRevision"`
	Template string
	Memory   string
	sentExp  bool
}

// applyServer answers each ApplyMemoryConfigTemplate call with preview (for
// dryRun: true) or applied (dryRun: false), and records every call.
func applyServer(t *testing.T, preview, applied string) (*httptest.Server, *[]applyCall) {
	t.Helper()
	var mu sync.Mutex
	calls := []applyCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string                     `json:"operationName"`
			Variables     map[string]json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.OperationName != "ApplyMemoryConfigTemplate" {
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
			return
		}
		var c applyCall
		_ = json.Unmarshal(body.Variables["dryRun"], &c.DryRun)
		_ = json.Unmarshal(body.Variables["templateRef"], &c.Template)
		_ = json.Unmarshal(body.Variables["memoryRef"], &c.Memory)
		if raw, ok := body.Variables["expectedTemplateRevision"]; ok {
			c.sentExp = true
			_ = json.Unmarshal(raw, &c.Expected)
		}
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		if c.DryRun {
			_, _ = w.Write([]byte(preview))
		} else {
			_, _ = w.Write([]byte(applied))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// --dry-run is ONE previewing call, the report is the payload, nothing more.
func TestTemplateApplyDryRun(t *testing.T) {
	preview := applyPayload(true, false, 3,
		applyEntry("spec", "APPLIED", ""),
		applyEntry("review", "SKIPPED_CONFLICT", ruleJSON("r9", "review", 2, "NONE", "NONE")))
	srv, calls := applyServer(t, preview, "")
	out, code := runConfig(t, srv.URL, "memory", "config", "template", "apply", "t1", configMemoryRef, "--dry-run", "--json")
	if code != exitcode.OK {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if len(*calls) != 1 || !(*calls)[0].DryRun || (*calls)[0].sentExp {
		t.Fatalf("calls = %+v, want one dryRun call with no expectedTemplateRevision", *calls)
	}
	if c := (*calls)[0]; c.Template != "t1" || c.Memory != configMemoryRef {
		t.Errorf("refs sent = %q/%q", c.Template, c.Memory)
	}
	var dto struct {
		DryRun           bool `json:"dryRun"`
		TemplateRevision int  `json:"templateRevision"`
		Entries          []struct {
			Role    string          `json:"role"`
			Outcome string          `json:"outcome"`
			Rule    json.RawMessage `json:"rule"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if !dto.DryRun || dto.TemplateRevision != 3 || len(dto.Entries) != 2 ||
		dto.Entries[0].Outcome != "APPLIED" || string(dto.Entries[0].Rule) != "null" ||
		dto.Entries[1].Outcome != "SKIPPED_CONFLICT" {
		t.Errorf("report = %+v", dto)
	}
}

// The text preview names the revision to pin.
func TestTemplateApplyDryRunText(t *testing.T) {
	srv, _ := applyServer(t, applyPayload(true, true, 4, applyEntry("spec", "REPLACED", ruleJSON("r1", "spec", 2, "NONE", "NONE"))), "")
	out, code := runConfig(t, srv.URL, "memory", "config", "template", "apply", "t1", configMemoryRef, "--dry-run")
	if code != exitcode.OK {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	for _, want := range []string{"Would apply required template spec-rules", "REPLACED", "nothing was written", "--expected-revision 4"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output lacks %q:\n%s", want, out)
		}
	}
}

// Nothing replaced: preview, then apply with the PREVIEWED revision, no prompt.
func TestTemplateApplyWithoutReplacementNeedsNoConfirmation(t *testing.T) {
	srv, calls := applyServer(t,
		applyPayload(true, false, 3, applyEntry("spec", "APPLIED", "")),
		applyPayload(false, false, 3, applyEntry("spec", "APPLIED", ruleJSON("r1", "spec", 1, "NONE", "NONE"))))
	out, code := runConfig(t, srv.URL, "memory", "config", "template", "apply", "t1", configMemoryRef, "--json")
	if code != exitcode.OK {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if len(*calls) != 2 || !(*calls)[0].DryRun || (*calls)[1].DryRun {
		t.Fatalf("calls = %+v, want a preview then the apply", *calls)
	}
	if e := (*calls)[1].Expected; e == nil || *e != 3 {
		t.Errorf("the apply must pin the previewed revision 3, sent %v", e)
	}
	if !strings.Contains(out, `"dryRun": false`) {
		t.Errorf("the reported result must be the apply, not the preview:\n%s", out)
	}
}

// A preview that REPLACES a rule needs confirmation: non-interactively,
// without --yes, it stops after the preview with nothing written.
func TestTemplateApplyReplacementNeedsYes(t *testing.T) {
	srv, calls := applyServer(t,
		applyPayload(true, true, 5, applyEntry("spec", "REPLACED", ruleJSON("r1", "spec", 2, "NONE", "NONE"))), "")
	_, code := runConfig(t, srv.URL, "memory", "config", "template", "apply", "t1", configMemoryRef)
	if code != exitcode.Usage {
		t.Errorf("exit = %d, want %d without --yes", code, exitcode.Usage)
	}
	if len(*calls) != 1 || !(*calls)[0].DryRun {
		t.Errorf("calls = %+v, want only the preview", *calls)
	}
}

func TestTemplateApplyReplacementWithYes(t *testing.T) {
	srv, calls := applyServer(t,
		applyPayload(true, true, 5, applyEntry("spec", "REPLACED", ruleJSON("r1", "spec", 2, "NONE", "NONE"))),
		applyPayload(false, true, 5, applyEntry("spec", "REPLACED", ruleJSON("r1", "spec", 3, "NONE", "NONE"))))
	_, code := runConfig(t, srv.URL, "memory", "config", "template", "apply", "t1", configMemoryRef, "--yes")
	if code != exitcode.OK {
		t.Fatalf("exit = %d", code)
	}
	if len(*calls) != 2 || (*calls)[1].DryRun || (*calls)[1].Expected == nil || *(*calls)[1].Expected != 5 {
		t.Errorf("calls = %+v, want the apply pinned to the previewed revision 5", *calls)
	}
}

// --expected-revision pins the PREVIEW too, so a changed template is refused
// before anything is shown as applicable; a non-positive one is refused locally.
func TestTemplateApplyExpectedRevision(t *testing.T) {
	srv, calls := applyServer(t,
		applyPayload(true, false, 7, applyEntry("spec", "APPLIED", "")),
		applyPayload(false, false, 7, applyEntry("spec", "APPLIED", ruleJSON("r1", "spec", 1, "NONE", "NONE"))))
	if _, code := runConfig(t, srv.URL, "memory", "config", "template", "apply", "t1", configMemoryRef, "--expected-revision", "7"); code != exitcode.OK {
		t.Fatalf("exit = %d", code)
	}
	for i, c := range *calls {
		if c.Expected == nil || *c.Expected != 7 {
			t.Errorf("call %d sent expectedTemplateRevision %v, want 7", i, c.Expected)
		}
	}
	for _, bad := range []string{"0", "-2"} {
		srv2, calls2 := applyServer(t, "", "")
		if _, code := runConfig(t, srv2.URL, "memory", "config", "template", "apply", "t1", configMemoryRef, "--expected-revision", bad); code != exitcode.Usage || len(*calls2) != 0 {
			t.Errorf("--expected-revision %s: exit %d, %d call(s); want %d and none", bad, code, len(*calls2), exitcode.Usage)
		}
	}
}

// Skipped entries are the report, not a failure.
func TestTemplateApplySkippedLockedIsNotAFailure(t *testing.T) {
	locked := strings.Replace(ruleJSON("r1", "spec", 2, "NONE", "NONE"), `"locked":false`, `"locked":true`, 1)
	srv, _ := applyServer(t,
		applyPayload(true, true, 2, applyEntry("spec", "SKIPPED_LOCKED", locked)),
		applyPayload(false, true, 2, applyEntry("spec", "SKIPPED_LOCKED", locked)))
	out, code := runConfig(t, srv.URL, "memory", "config", "template", "apply", "t1", configMemoryRef)
	if code != exitcode.OK || !strings.Contains(out, "SKIPPED_LOCKED") {
		t.Errorf("exit = %d, want 0 with SKIPPED_LOCKED reported:\n%s", code, out)
	}
}

// An empty template is an explicit empty report.
func TestTemplateApplyEmptyTemplate(t *testing.T) {
	srv, _ := applyServer(t, applyPayload(true, false, 1), "")
	out, _ := runConfig(t, srv.URL, "memory", "config", "template", "apply", "t1", configMemoryRef, "--dry-run", "--json")
	if !strings.Contains(out, `"entries": []`) || !strings.Contains(out, `"warnings": []`) {
		t.Errorf("want entries: [] and warnings: [] on the raw output:\n%s", out)
	}
}

// Server refusals, each with its documented exit.
func TestTemplateApplyServerRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		resp string
		want int
	}{
		"template not found or unmanaged": {gqlError("MEMORY_CONFIG_TEMPLATE_NOT_FOUND", ""), exitcode.NotFound},
		"memory not found or unmanaged":   {gqlError("MEMORY_NOT_FOUND", ""), exitcode.NotFound},
		"template changed":                {gqlError("CONFLICT", `"currentRevision":8`), exitcode.Conflict},
		"a template task was deleted":     {gqlError("NODE_ROLE_RULE_REF_BROKEN", `"role":"spec"`), exitcode.Conflict},
		"a referenced task unreadable":    {gqlError("NODE_NOT_FOUND", ""), exitcode.NotFound},
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := applyServer(t, tc.resp, "")
			if _, code := runConfig(t, srv.URL, "memory", "config", "template", "apply", "t1", configMemoryRef); code != tc.want {
				t.Errorf("exit = %d, want %d", code, tc.want)
			}
			if len(*calls) != 1 {
				t.Errorf("a refused preview must not be followed by an apply; calls = %+v", *calls)
			}
		})
	}
}

// Since Holger's 2026-09-27 ruling a LOCKED rule refuses EVERY caller's
// ordinary update or delete: the rule's state, so exit 5, and the message
// names the deliberate remedy, `rule unlock`.
func TestMemoryConfigRuleLockedIsAConflictNamingUnlock(t *testing.T) {
	locked := gqlError("RULE_LOCKED", `"ruleId":"r1","role":"spec","sourceTemplateId":"t1"`)
	for name, tc := range map[string]struct {
		op   string
		args []string
	}{
		"update": {"UpdateNodeRoleRule", []string{"update", configMemoryRef, "spec", "--enabled=false"}},
		"rm":     {"DeleteNodeRoleRule", []string{"rm", configMemoryRef, "spec", "--yes"}},
	} {
		t.Run(name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{
				"MemoryConfig": configJSON(ruleJSON("r1", "spec", 1, "NONE", "NONE")),
				tc.op:          locked,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"memory", "config", "rule"}, tc.args...), "--server", gql.URL))
			err := root.Execute()
			if got := exitCodeFor(err); got != exitcode.Conflict {
				t.Errorf("exit = %d, want %d", got, exitcode.Conflict)
			}
			if err == nil || !strings.Contains(err.Error(), "hadron memory config rule unlock "+configMemoryRef+" spec") {
				t.Errorf("the refusal must name the unlock command, got %v", err)
			}
		})
	}
}

// lockedSpecRule is the "spec" rule r1 at revision 3, locked by a required template.
var lockedSpecRule = strings.Replace(ruleJSON("r1", "spec", 3, "NONE", "NONE"), `"locked":false`, `"locked":true`, 1)

const unlockedResp = `{"data":{"unlockNodeRoleRule":{"rule":` + "%s" + `,"warnings":[]}}}`

func TestMemoryConfigRuleUnlockSendsTheRevisionReadFirst(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MemoryConfig":       configJSON(lockedSpecRule),
		"UnlockNodeRoleRule": fmt.Sprintf(unlockedResp, ruleJSON("r1", "spec", 4, "NONE", "NONE")),
	})
	out, code := runConfig(t, gql.URL, "memory", "config", "rule", "unlock", configMemoryRef, "spec", "--yes", "--json")
	if code != exitcode.OK {
		t.Fatalf("unlock --yes: exit %d\n%s", code, out)
	}
	v := vars(t, captured["UnlockNodeRoleRule"])
	if v["ref"] != "r1" || v["expectedRevision"] != float64(3) {
		t.Errorf("unlock must address the rule by id at the revision just read: %v", v)
	}
	if !strings.Contains(out, `"locked": false`) || !strings.Contains(out, `"warnings": []`) {
		t.Errorf("unexpected result: %s", out)
	}
}

// Unlocking is the deliberate step, so a non-interactive caller must say so.
func TestMemoryConfigRuleUnlockNeedsYesNonInteractively(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MemoryConfig": configJSON(lockedSpecRule),
	})
	if _, code := runConfig(t, gql.URL, "memory", "config", "rule", "unlock", configMemoryRef, "spec"); code != exitcode.Usage {
		t.Errorf("unlock without --yes non-interactively should exit 2, got %d", code)
	}
	if _, sent := captured["UnlockNodeRoleRule"]; sent {
		t.Error("nothing may be unlocked without confirmation")
	}
}

// On unlock, RULE_LOCKED means the caller lacks LOCK AUTHORITY — a permission.
func TestMemoryConfigRuleUnlockWithoutAuthorityIsForbidden(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"MemoryConfig":       configJSON(lockedSpecRule),
		"UnlockNodeRoleRule": gqlError("RULE_LOCKED", `"ruleId":"r1","role":"spec"`),
	})
	if _, code := runConfig(t, gql.URL, "memory", "config", "rule", "unlock", configMemoryRef, "spec", "--yes"); code != exitcode.Forbidden {
		t.Errorf("unlock without lock authority should exit 8, got %d", code)
	}
}

// An unlocked rule: nothing to confirm, so no prompt — and the server, asked
// anyway, changes nothing. Said on stderr so the caller isn't misled.
func TestMemoryConfigRuleUnlockOfAnUnlockedRuleDoesNotPrompt(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MemoryConfig":       configJSON(ruleJSON("r1", "spec", 3, "NONE", "NONE")),
		"UnlockNodeRoleRule": fmt.Sprintf(unlockedResp, ruleJSON("r1", "spec", 3, "NONE", "NONE")),
	})
	f, _, errOut := testFactoryTTY(t, "")
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "config", "rule", "unlock", configMemoryRef, "spec", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("unlocking an unlocked rule: %v", err)
	}
	if _, sent := captured["UnlockNodeRoleRule"]; !sent {
		t.Error("the unlock is still sent")
	}
	if strings.Contains(errOut.String(), "(y/N)") || !strings.Contains(errOut.String(), "is not locked") {
		t.Errorf("no prompt, and a note that nothing changes: %q", errOut.String())
	}
}

func TestMemoryConfigRuleUnlockTTYDeclineWritesNothing(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MemoryConfig": configJSON(lockedSpecRule),
	})
	f, _, _ := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "config", "rule", "unlock", configMemoryRef, "spec", "--server", gql.URL})
	if got := exitCodeFor(root.Execute()); got != exitcode.Cancelled {
		t.Errorf("declining should exit 6, got %d", got)
	}
	if _, sent := captured["UnlockNodeRoleRule"]; sent {
		t.Error("a declined unlock must write nothing")
	}
}

// A dry run's LOCKED column is the state AFTER applying: its `rule` is the
// existing one, whose lock is the state before (#747 review, Copilot).
func TestTemplateApplyDryRunShowsLockAfterApplying(t *testing.T) {
	unlocked := ruleJSON("r1", "spec", 2, "NONE", "NONE") // "locked":false today
	for name, tc := range map[string]struct {
		required bool
		entry    string
		want     string
	}{
		"replaced will be locked":               {true, applyEntry("spec", "REPLACED", unlocked), "yes"},
		"applied by a required template":        {true, applyEntry("spec", "APPLIED", ""), "yes"},
		"applied by a non-required template":    {false, applyEntry("spec", "APPLIED", ""), "no"},
		"skipped keeps the rule's current lock": {false, applyEntry("spec", "SKIPPED_CONFLICT", unlocked), "no"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := applyServer(t, applyPayload(true, tc.required, 2, tc.entry), "")
			out, code := runConfig(t, srv.URL, "memory", "config", "template", "apply", "t1", configMemoryRef, "--dry-run")
			if code != exitcode.OK {
				t.Fatalf("exit = %d\n%s", code, out)
			}
			var row string
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(line, "spec ") {
					row = line
				}
			}
			fields := strings.Fields(row)
			if len(fields) != 3 || fields[2] != tc.want {
				t.Errorf("row %q: LOCKED = %v, want %q", row, fields, tc.want)
			}
		})
	}
}

// On a terminal a REPLACED preview prompts: yes applies (pinned), no cancels
// with nothing written (#747 review, Copilot).
func TestTemplateApplyReplacementPromptsOnATerminal(t *testing.T) {
	preview := applyPayload(true, true, 5, applyEntry("spec", "REPLACED", ruleJSON("r1", "spec", 2, "NONE", "NONE")))
	applied := applyPayload(false, true, 5, applyEntry("spec", "REPLACED", ruleJSON("r1", "spec", 3, "NONE", "NONE")))
	t.Run("yes applies", func(t *testing.T) {
		srv, calls := applyServer(t, preview, applied)
		f, _, errOut := testFactoryTTY(t, "y\n")
		root := NewRootCmd(f)
		root.SetArgs([]string{"memory", "config", "template", "apply", "t1", configMemoryRef, "--server", srv.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(errOut.String(), "REPLACES the memory's rule for spec") {
			t.Errorf("the prompt must name what is replaced:\n%s", errOut.String())
		}
		if len(*calls) != 2 || (*calls)[1].Expected == nil || *(*calls)[1].Expected != 5 {
			t.Errorf("calls = %+v, want the apply pinned to 5", *calls)
		}
	})
	t.Run("no cancels", func(t *testing.T) {
		srv, calls := applyServer(t, preview, applied)
		f, _, _ := testFactoryTTY(t, "n\n")
		root := NewRootCmd(f)
		root.SetArgs([]string{"memory", "config", "template", "apply", "t1", configMemoryRef, "--server", srv.URL})
		if code := exitCodeFor(root.Execute()); code != exitcode.Cancelled {
			t.Errorf("exit = %d, want %d", code, exitcode.Cancelled)
		}
		if len(*calls) != 1 {
			t.Errorf("a declined prompt must not apply; calls = %+v", *calls)
		}
	})
}
