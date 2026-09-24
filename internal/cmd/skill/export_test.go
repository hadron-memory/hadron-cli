package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/skilldoc"
)

// These pin the WRITER's own behaviour — the filesystem guard and the I/O it
// performs for each planned action — against a faked plan. The server's
// judgment is not under test here (it has its own suite), and the command-level
// acceptance cases (matrix P-cases) live in internal/cmd, owned separately.

// home returns a resolved temp home: on darwin t.TempDir() sits under the
// symlinked /var, which the guard would (correctly) never see because $HOME is
// resolved before checking — so the tests resolve it the same way.
func home(t *testing.T) string {
	t.Helper()
	h, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, p, body string) {
	t.Helper()
	mkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	mkdir(t, filepath.Dir(link))
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func str(s string) *string { return &s }

// entry builds one planned entry. body == "" means no rendered body.
func entry(name string, action gen.SkillExportAction, body, movedFrom string) *gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry {
	e := &gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{
		Urn: "hrn:node:example.com:demo:tasks:" + name, NodeId: "id-" + name, Name: name,
		ExportPlan: &gen.SkillExportPlanSkillPlanEntriesSkillPlanEntryExportPlanSkillExportPlanEntry{
			Action: action,
			Reasons: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntryExportPlanSkillExportPlanEntryReasonsSkillExportReason{
				{Code: "server-code", Message: "server says " + string(action)},
			},
		},
	}
	if body != "" {
		e.RenderedBody = str(body)
	}
	if movedFrom != "" {
		e.MovedFrom = str(movedFrom)
	}
	return e
}

// fakePlan answers with fixed entries and orphans, and records the files the
// writer submitted (nil = the writer asked with no files).
type fakePlan struct {
	entries []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry
	orphans []*gen.SkillExportPlanSkillPlanOrphansSkillOrphan
	calls   int
	sent    []*gen.SkillFileFactsInput
}

func (p *fakePlan) fn(files []*gen.SkillFileFactsInput) (*gen.SkillExportPlanSkillPlan, error) {
	p.calls++
	p.sent = files
	return &gen.SkillExportPlanSkillPlan{Entries: p.entries, Orphans: p.orphans}, nil
}

func names(items []exportItemDTO) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Name)
	}
	return out
}

func codes(rs []exportReasonDTO) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.Code)
	}
	return out
}

func TestExportRootsCoverEveryHost(t *testing.T) {
	for _, h := range skilldoc.Hosts {
		if _, ok := exportRoots[h.Key]; !ok {
			t.Errorf("host %s has no export root: its items would all fail (P07)", h.Key)
		}
	}
}

// P03: the first export writes an absent root, creating it — and the file is
// the server's rendered body byte for byte.
func TestExportWritesAbsentRootByteForByte(t *testing.T) {
	h := home(t)
	body := "---\nname: hadron-demo\n---\n\nbody\n"
	p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{entry("hadron-demo", gen.SkillExportActionWrite, body, "")}}
	hd, _, err := exportHost(h, skilldoc.HostCodexSkill, p.fn, exportOpts{})
	if err != nil || hd.Failure != nil {
		t.Fatalf("err=%v failure=%+v", err, hd.Failure)
	}
	got, err := os.ReadFile(filepath.Join(h, ".agents", "skills", "hadron-demo", "SKILL.md"))
	if err != nil || string(got) != body {
		t.Fatalf("file = %q, %v; want the rendered body verbatim", got, err)
	}
	if !reflect.DeepEqual(names(hd.Written), []string{"hadron-demo"}) {
		t.Errorf("written = %v", names(hd.Written))
	}
	if exists(filepath.Join(h, ".codex")) {
		t.Error("the deprecated ~/.codex root must never be created (P20/P21)")
	}
}

// --dry-run reports the same outcome and creates nothing, not even the root.
func TestExportDryRunTouchesNothing(t *testing.T) {
	h := home(t)
	p := &fakePlan{
		entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{
			entry("a", gen.SkillExportActionWrite, "x", ""),
		},
		orphans: []*gen.SkillExportPlanSkillPlanOrphansSkillOrphan{{DirName: "old"}},
	}
	write(t, filepath.Join(h, ".claude", "skills", "old", "SKILL.md"), "orphan")
	hd, _, err := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{dryRun: true, prune: true})
	if err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(h, ".claude", "skills", "a")) {
		t.Error("dry run created a skill directory")
	}
	if !exists(filepath.Join(h, ".claude", "skills", "old", "SKILL.md")) {
		t.Error("dry run pruned an orphan")
	}
	if len(hd.Written) != 1 || len(hd.Pruned) != 1 {
		t.Errorf("dry run must report what it would do: written=%v pruned=%v", names(hd.Written), hd.Pruned)
	}
	hd2, _, _ := exportHost(h, skilldoc.HostCodexSkill, p.fn, exportOpts{dryRun: true})
	if exists(filepath.Join(h, ".agents")) || len(hd2.Written) != 1 {
		t.Error("dry run must not create an absent root")
	}
}

// P17 (the root is a link) and P18 (an ancestor is): nothing is written for
// that host, every item is named as failed, and nothing is read through it.
func TestExportRefusesALinkedRootOrAncestor(t *testing.T) {
	for _, tc := range []struct {
		name string
		link func(t *testing.T, h string)
	}{
		{"root", func(t *testing.T, h string) {
			mkdir(t, filepath.Join(h, ".claude", "skills"))
			mkdir(t, filepath.Join(h, ".agents"))
			symlink(t, filepath.Join(h, ".claude", "skills"), filepath.Join(h, ".agents", "skills"))
		}},
		{"ancestor", func(t *testing.T, h string) {
			mkdir(t, filepath.Join(h, ".claude", "skills"))
			symlink(t, filepath.Join(h, ".claude"), filepath.Join(h, ".agents"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := home(t)
			tc.link(t, h)
			write(t, filepath.Join(h, ".claude", "skills", "a", "SKILL.md"), "claude's")
			p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{
				entry("a", gen.SkillExportActionWrite, "codex's", ""),
				entry("b", gen.SkillExportActionWrite, "codex's", ""),
			}}
			hd, _, err := exportHost(h, skilldoc.HostCodexSkill, p.fn, exportOpts{})
			if err != nil {
				t.Fatal(err)
			}
			if hd.Failure == nil || hd.Failure.Code != reasonRootIsLink {
				t.Fatalf("failure = %+v, want %s", hd.Failure, reasonRootIsLink)
			}
			if !reflect.DeepEqual(names(hd.Failed), []string{"a", "b"}) || len(hd.Written) != 0 {
				t.Errorf("every item must be named as failed: failed=%v written=%v", names(hd.Failed), names(hd.Written))
			}
			if p.sent != nil {
				t.Errorf("nothing may be read through the link, but %d file facts were sent", len(p.sent))
			}
			if got, _ := os.ReadFile(filepath.Join(h, ".claude", "skills", "a", "SKILL.md")); string(got) != "claude's" {
				t.Errorf("the other host's file changed: %q", got)
			}
		})
	}
}

func TestExportRefusesARootThatIsNotADirectory(t *testing.T) {
	h := home(t)
	write(t, filepath.Join(h, ".agents"), "a file")
	p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{entry("a", gen.SkillExportActionWrite, "x", "")}}
	hd, _, _ := exportHost(h, skilldoc.HostCodexSkill, p.fn, exportOpts{})
	if hd.Failure == nil || hd.Failure.Code != reasonRootNotDir || len(hd.Failed) != 1 {
		t.Errorf("failure=%+v failed=%v", hd.Failure, names(hd.Failed))
	}
}

// P06: a skill directory that is a link. Its facts are withheld from the
// server, and whatever the plan says — even SKIP "current" — the item is
// refused, because the file behind it belongs to another host.
func TestExportRefusesALinkedSkillDirectory(t *testing.T) {
	h := home(t)
	claudeFile := filepath.Join(h, ".claude", "skills", "demo", "SKILL.md")
	body, err := skilldoc.Render("id-demo", "demo", "hrn:node:example.com:demo:tasks:demo", "does demo things", "content")
	if err != nil {
		t.Fatal(err)
	}
	write(t, claudeFile, body)
	symlink(t, filepath.Join(h, ".claude", "skills", "demo"), filepath.Join(h, ".agents", "skills", "demo"))
	for _, action := range []gen.SkillExportAction{gen.SkillExportActionSkip, gen.SkillExportActionWrite} {
		t.Run(string(action), func(t *testing.T) {
			p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{entry("demo", action, "codex's", "")}}
			hd, _, err := exportHost(h, skilldoc.HostCodexSkill, p.fn, exportOpts{})
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range p.sent {
				if f.DirName == "demo" {
					t.Error("the linked directory's facts were sent to the server")
				}
			}
			if len(hd.Refused) != 1 || codes(hd.Refused[0].Reasons)[len(hd.Refused[0].Reasons)-1] != reasonDirIsLink {
				t.Fatalf("refused = %+v", hd.Refused)
			}
			if len(hd.Skipped)+len(hd.Written) != 0 {
				t.Errorf("a linked directory must not read as skipped or written: skipped=%v written=%v", names(hd.Skipped), names(hd.Written))
			}
			if got, _ := os.ReadFile(claudeFile); string(got) != body {
				t.Error("the Claude file behind the link changed")
			}
		})
	}
}

// MOVE writes the new directory first, then removes the old SKILL.md — and
// keeps the old directory when it holds anything else.
func TestExportMoveKeepsOtherFiles(t *testing.T) {
	h := home(t)
	root := filepath.Join(h, ".claude", "skills")
	write(t, filepath.Join(root, "old", "SKILL.md"), "old")
	write(t, filepath.Join(root, "old", "helper.sh"), "#!/bin/sh")
	p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{entry("new", gen.SkillExportActionMove, "new body", "old")}}
	hd, _, err := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "new", "SKILL.md")); string(got) != "new body" {
		t.Errorf("new file = %q", got)
	}
	if exists(filepath.Join(root, "old", "SKILL.md")) {
		t.Error("the old SKILL.md survived the move")
	}
	if !exists(filepath.Join(root, "old", "helper.sh")) {
		t.Error("a file that is not SKILL.md was deleted")
	}
	if len(hd.Moved) != 1 || hd.Moved[0].From != "old" || !reflect.DeepEqual(hd.Moved[0].Kept, []string{"helper.sh"}) {
		t.Fatalf("moved = %+v", hd.Moved)
	}
	if c := codes(hd.Moved[0].Reasons); c[len(c)-1] != reasonDirKept {
		t.Errorf("the kept directory must be reported: %v", c)
	}
}

// REMOVE (a disabled declaration) deletes SKILL.md and the now-empty directory.
func TestExportRemoveDeletesAnEmptyDirectory(t *testing.T) {
	h := home(t)
	root := filepath.Join(h, ".claude", "skills")
	write(t, filepath.Join(root, "gone", "SKILL.md"), "x")
	p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{entry("gone", gen.SkillExportActionRemove, "", "gone")}}
	hd, _, err := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(root, "gone")) {
		t.Error("the empty skill directory was left behind")
	}
	if !reflect.DeepEqual(names(hd.Removed), []string{"gone"}) {
		t.Errorf("removed = %v", names(hd.Removed))
	}
}

// --prune removes an orphan; without it the orphan is only reported.
func TestExportPruneOnlyWhenAsked(t *testing.T) {
	for _, prune := range []bool{false, true} {
		t.Run(fmt.Sprintf("prune=%v", prune), func(t *testing.T) {
			h := home(t)
			f := filepath.Join(h, ".claude", "skills", "stray", "SKILL.md")
			write(t, f, "x")
			p := &fakePlan{orphans: []*gen.SkillExportPlanSkillPlanOrphansSkillOrphan{{DirName: "stray"}}}
			hd, _, _ := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{prune: prune})
			if exists(f) == prune {
				t.Errorf("file exists=%v with prune=%v", exists(f), prune)
			}
			if prune && len(hd.Pruned) != 1 || !prune && len(hd.Orphaned) != 1 {
				t.Errorf("pruned=%v orphaned=%v", hd.Pruned, hd.Orphaned)
			}
		})
	}
}

// The client's own guards: each fails its item, touches nothing, and never
// stops the next item (cor:agt:030:00, an export runs to the end).
func TestExportClientGuardsFailTheItemAndContinue(t *testing.T) {
	preserve := entry("kept", gen.SkillExportActionWrite, "new", "")
	preserve.ExportPlan.PreservesExistingFile = true
	unknown := entry("future", gen.SkillExportActionWrite, "x", "")
	unknown.ExportPlan.Action = "TELEPORT"
	noPlan := entry("noplan", gen.SkillExportActionWrite, "x", "")
	noPlan.ExportPlan = nil

	for _, tc := range []struct {
		e    *gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry
		code string
	}{
		{preserve, reasonPlanContradiction},
		{entry("../escape", gen.SkillExportActionWrite, "x", ""), reasonUnsafeName},
		{entry("nobody", gen.SkillExportActionWrite, "", ""), reasonNoBody},
		{entry("m", gen.SkillExportActionMove, "x", ""), reasonNoSource},
		{entry("m2", gen.SkillExportActionMove, "x", "../../etc"), reasonUnsafeName},
		{entry("r", gen.SkillExportActionRemove, "", ""), reasonNoSource},
		{unknown, reasonUnknownAction},
		{noPlan, reasonNoExportPlan},
	} {
		t.Run(tc.code+"/"+tc.e.Name, func(t *testing.T) {
			h := home(t)
			root := filepath.Join(h, ".claude", "skills")
			// A real Hadron file, so it is SUBMITTED (a foreign file would be
			// refused by the foreign-file guard before these guards run).
			handEdit, err := skilldoc.Render("id-kept", "kept", "hrn:node:example.com:demo:tasks:kept", "d", "hand edit")
			if err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(root, "kept", "SKILL.md"), handEdit)
			p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{tc.e, entry("next", gen.SkillExportActionWrite, "ok", "")}}
			hd, _, err := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{})
			if err != nil {
				t.Fatal(err)
			}
			if len(hd.Failed) != 1 {
				t.Fatalf("failed = %+v", hd.Failed)
			}
			if c := codes(hd.Failed[0].Reasons); c[len(c)-1] != tc.code {
				t.Errorf("reason codes = %v, want last %s", c, tc.code)
			}
			if !reflect.DeepEqual(names(hd.Written), []string{"next"}) {
				t.Errorf("the next item must still run: written=%v", names(hd.Written))
			}
			if got, _ := os.ReadFile(filepath.Join(root, "kept", "SKILL.md")); string(got) != handEdit {
				t.Error("a guarded item changed a file")
			}
			if exists(filepath.Join(h, ".claude", "escape")) || exists(filepath.Join(h, "etc")) {
				t.Error("an unsafe name escaped the root")
			}
		})
	}
}

// Server reasons arrive verbatim, marked as the server's.
func TestExportCarriesServerReasonsVerbatim(t *testing.T) {
	h := home(t)
	p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{entry("r", gen.SkillExportActionRefuse, "", "")}}
	hd, _, _ := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{})
	want := []exportReasonDTO{{Code: "server-code", Message: "server says REFUSE", Origin: originServer}}
	if len(hd.Refused) != 1 || !reflect.DeepEqual(hd.Refused[0].Reasons, want) {
		t.Errorf("refused = %+v", hd.Refused)
	}
}

func TestExportDTOHasNoNilSlices(t *testing.T) {
	var walk func(name string, v reflect.Value)
	walk = func(name string, v reflect.Value) {
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(name+"."+v.Type().Field(i).Name, v.Field(i))
			}
		case reflect.Slice:
			if v.IsNil() {
				t.Errorf("%s is a nil slice — --json renders it as null, not []", name)
				return
			}
			for i := 0; i < v.Len(); i++ {
				walk(fmt.Sprintf("%s[%d]", name, i), v.Index(i))
			}
		}
	}
	h := home(t)
	write(t, filepath.Join(h, ".claude", "skills", "old", "SKILL.md"), "x")
	write(t, filepath.Join(h, ".claude", "skills", "old", "extra"), "x")
	p := &fakePlan{
		entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{
			entry("w", gen.SkillExportActionWrite, "x", ""),
			entry("m", gen.SkillExportActionMove, "x", "old"),
			entry("s", gen.SkillExportActionSkip, "", ""),
			entry("f", gen.SkillExportActionFail, "", ""),
		},
		orphans: []*gen.SkillExportPlanSkillPlanOrphansSkillOrphan{{DirName: "o"}},
	}
	hd, _, _ := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{})
	blocked, _, _ := exportHost(h, "noSuchHost", p.fn, exportOpts{})
	dto := exportDTO{Hosts: []exportHostDTO{hd, blocked, newExportHost("x", "")}, Unrecognized: toUnrecognizedDTO(
		[]*gen.SkillExportPlanSkillPlanUnrecognized{{Urn: "u", KeyCount: 1}})}
	walk("exportDTO", reflect.ValueOf(dto))
	if blocked.Failure == nil || blocked.Failure.Code != reasonHostNoRoot {
		t.Errorf("a host with no root row must fail its items (P07): %+v", blocked.Failure)
	}
}

// Matrix P07 (cor:agt:030:00/:02): a host the CLI has no root for. It is
// unreachable at command level, because the command plans only the hosts in
// its root table (Jane, #1379), so it is pinned here.
//   - Every item the server planned for that host FAILS and is NAMED, whatever
//     its action, with the no-root reason first and the server's reasons kept.
//   - The plan is still fetched, with no files, because the report must name
//     the items; nothing is read from or written to disk for the host.
//   - A blocked host returns no error, so the command's per-host loop
//     (newCmdExport) carries on; this test stands in for that loop by
//     running the next host against the same home, which is still written.
//     The DTO of both then reports failure (exit 5), not success.
func TestExportHostWithNoRootFailsAndNamesEveryItem(t *testing.T) {
	h := home(t)
	blockedPlan := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{
		entry("w", gen.SkillExportActionWrite, "body-w", ""),
		entry("m", gen.SkillExportActionMove, "body-m", "old-m"),
		entry("r", gen.SkillExportActionRemove, "", ""),
		entry("s", gen.SkillExportActionSkip, "", ""),
		nil,
		entry("f", gen.SkillExportActionFail, "", ""),
	}}
	blocked, _, err := exportHost(h, "noSuchHost", blockedPlan.fn, exportOpts{})
	if err != nil {
		t.Fatalf("a missing root is an item failure, not a run error: %v", err)
	}
	if blockedPlan.calls != 1 || blockedPlan.sent != nil {
		t.Errorf("the plan must be fetched once with no files: calls=%d sent=%v", blockedPlan.calls, blockedPlan.sent)
	}
	if blocked.Failure == nil || blocked.Failure.Code != reasonHostNoRoot || blocked.Root != "" {
		t.Errorf("host failure = %+v, root = %q", blocked.Failure, blocked.Root)
	}
	if got, want := names(blocked.Failed), []string{"w", "m", "r", "s", "f"}; !reflect.DeepEqual(got, want) {
		t.Errorf("failed = %v, want every planned item named, in plan order: %v", got, want)
	}
	for _, it := range blocked.Failed {
		action := map[string]string{"w": "WRITE", "m": "MOVE", "r": "REMOVE", "s": "SKIP", "f": "FAIL"}[it.Name]
		want := []exportReasonDTO{*blocked.Failure, {Code: "server-code", Message: "server says " + action, Origin: originServer}}
		if !reflect.DeepEqual(it.Reasons, want) {
			t.Errorf("%s reasons = %+v, want the no-root reason then the server's verbatim: %+v", it.Name, it.Reasons, want)
		}
	}
	for label, list := range map[string][]exportItemDTO{"written": blocked.Written, "removed": blocked.Removed, "skipped": blocked.Skipped, "refused": blocked.Refused} {
		if len(list) != 0 {
			t.Errorf("%s = %v; a host with no root does nothing", label, names(list))
		}
	}
	if len(blocked.Moved) != 0 {
		t.Errorf("moved = %+v; a host with no root does nothing", blocked.Moved)
	}
	if ents, err := os.ReadDir(h); err != nil || len(ents) != 0 {
		t.Fatalf("home after the blocked host = %v (err %v); nothing may be created", ents, err)
	}

	claudePlan := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{entry("w", gen.SkillExportActionWrite, "body-w", "")}}
	claude, _, err := exportHost(h, skilldoc.HostClaudeSkill, claudePlan.fn, exportOpts{})
	if err != nil || claude.Failure != nil || len(claude.Failed) != 0 {
		t.Fatalf("the next host must still run: err=%v failure=%+v failed=%v", err, claude.Failure, names(claude.Failed))
	}
	if got, err := os.ReadFile(filepath.Join(h, ".claude", "skills", "w", "SKILL.md")); err != nil || string(got) != "body-w" {
		t.Errorf("the next host's file = %q (err %v), want it written", got, err)
	}

	dto := exportDTO{Hosts: []exportHostDTO{blocked, claude}, Unrecognized: []exportUnrecognizedDTO{}}
	if !exportHasFailures(dto) {
		t.Error("a run with a failed host must exit 5, not 0")
	}
	var out strings.Builder
	if err := renderExport(&out, dto); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing written for this host: this CLI has no skills directory for host noSuchHost") {
		t.Errorf("report does not say why the host failed:\n%s", out.String())
	}
	var failedRows []string
	for _, line := range strings.Split(out.String(), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "failed" {
			failedRows = append(failedRows, f[1])
		}
	}
	if want := []string{"w", "m", "r", "s", "f"}; !reflect.DeepEqual(failedRows, want) {
		t.Errorf("report's failed rows = %v, want %v:\n%s", failedRows, want, out.String())
	}
}

// A skill "directory" that is a regular file: the write is refused with the
// directory check's own reason, and the file is left alone. This is the check
// that also re-guards against a link appearing after the walk.
func TestExportRefusesASkillPathThatIsNotADirectory(t *testing.T) {
	h := home(t)
	f := filepath.Join(h, ".claude", "skills", "demo")
	write(t, f, "not a directory")
	p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{entry("demo", gen.SkillExportActionWrite, "x", "")}}
	hd, _, _ := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{})
	if len(hd.Failed) != 1 {
		t.Fatalf("failed = %+v", hd.Failed)
	}
	if c := codes(hd.Failed[0].Reasons); c[len(c)-1] != reasonNotDir {
		t.Errorf("reason codes = %v, want last %s", c, reasonNotDir)
	}
	if got, _ := os.ReadFile(f); string(got) != "not a directory" {
		t.Error("the file was changed")
	}
}

// The human table: a host with nothing to do says so, and a name two nodes
// declare is qualified by its node rather than printed twice identically.
func TestRenderExportHumanTable(t *testing.T) {
	a := exportItemDTO{Node: "hrn:node:x:m:tasks:a", Name: "dup", Reasons: []exportReasonDTO{}, Kept: []string{}}
	b := exportItemDTO{Node: "hrn:node:x:m:tasks:b", Name: "dup", Reasons: []exportReasonDTO{}, Kept: []string{}}
	h := newExportHost("claudeSkill", "/r")
	h.Written = []exportItemDTO{a}
	h.Skipped = []exportItemDTO{b}
	var out strings.Builder
	if err := renderExport(&out, exportDTO{Hosts: []exportHostDTO{h, newExportHost("codexSkill", "/c")}}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"dup (hrn:node:x:m:tasks:a)", "dup (hrn:node:x:m:tasks:b)", "nothing to export for this host"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// Codex P1 (#692): a SKILL.md this command did not write is never replaced.
// The walk leaves a foreign file invisible on purpose, so the planner cannot
// know about it and may plan a WRITE onto that name. The writer refuses it,
// for a foreign file, an unattributable unparseable one, and a MOVE's
// destination alike.
func TestExportNeverReplacesAForeignSkillFile(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		e          func() *gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry
	}{
		{"foreign-write", "---\nname: demo\ndescription: someone else's skill\n---\n\nmine\n",
			func() *gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry {
				return entry("demo", gen.SkillExportActionWrite, "hadron's", "")
			}},
		{"unparseable-write", "---\nname: [unclosed\n",
			func() *gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry {
				return entry("demo", gen.SkillExportActionWrite, "hadron's", "")
			}},
		{"foreign-move-destination", "---\nname: demo\ndescription: someone else's skill\n---\n\nmine\n",
			func() *gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry {
				return entry("demo", gen.SkillExportActionMove, "hadron's", "old")
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := home(t)
			root := filepath.Join(h, ".claude", "skills")
			f := filepath.Join(root, "demo", "SKILL.md")
			write(t, f, tc.body)
			write(t, filepath.Join(root, "old", "SKILL.md"), "old")
			p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{tc.e()}}
			hd, _, err := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{})
			if err != nil {
				t.Fatal(err)
			}
			if len(hd.Refused) != 1 {
				t.Fatalf("refused = %+v written=%v moved=%v", hd.Refused, names(hd.Written), hd.Moved)
			}
			if c := codes(hd.Refused[0].Reasons); c[len(c)-1] != reasonForeignFile {
				t.Errorf("reason codes = %v", c)
			}
			if got, _ := os.ReadFile(f); string(got) != tc.body {
				t.Errorf("a foreign file was replaced: %q", got)
			}
			if !exists(filepath.Join(root, "old", "SKILL.md")) {
				t.Error("a refused move removed its source")
			}
		})
	}
}

// --dry-run must report what the real run reports (Codex P2 / Copilot on
// #692): the files a MOVE or REMOVE keeps, and a linked target refused.
func TestExportDryRunMatchesTheRealReport(t *testing.T) {
	// Real Hadron files: the server only plans a MOVE or REMOVE for a file it
	// was shown, and a SUBMITTED file is not foreign.
	hadronFile := func(t *testing.T, name string) string {
		b, err := skilldoc.Render("id-"+name, name, "hrn:node:example.com:demo:tasks:"+name, "d", "c")
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	setup := func(t *testing.T) (string, *fakePlan) {
		h := home(t)
		root := filepath.Join(h, ".claude", "skills")
		write(t, filepath.Join(root, "old", "SKILL.md"), hadronFile(t, "old"))
		write(t, filepath.Join(root, "old", "helper.sh"), "x")
		write(t, filepath.Join(root, "gone", "SKILL.md"), hadronFile(t, "gone"))
		write(t, filepath.Join(root, "gone", "asset.png"), "x")
		// A SKILL.md that is a link to a Hadron file elsewhere: the walk reads
		// through it and submits it, so it is not foreign, and the write is
		// refused by the link check instead.
		write(t, filepath.Join(h, "elsewhere.md"), hadronFile(t, "linkt"))
		mkdir(t, filepath.Join(root, "linkt"))
		symlink(t, filepath.Join(h, "elsewhere.md"), filepath.Join(root, "linkt", "SKILL.md"))
		return h, &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{
			entry("new", gen.SkillExportActionMove, "b", "old"),
			entry("gone", gen.SkillExportActionRemove, "", "gone"),
			entry("linkt", gen.SkillExportActionWrite, "b", ""),
		}}
	}
	summary := func(hd exportHostDTO) string {
		if len(hd.Moved) != 1 || len(hd.Removed) != 1 || len(hd.Failed) != 1 {
			return fmt.Sprintf("unexpected shape: moved=%v removed=%v failed=%v refused=%v",
				hd.Moved, names(hd.Removed), names(hd.Failed), names(hd.Refused))
		}
		return fmt.Sprintf("moved kept=%v removed=%v kept=%v failed=%v %v",
			hd.Moved[0].Kept, names(hd.Removed), hd.Removed[0].Kept, names(hd.Failed), codes(hd.Failed[0].Reasons))
	}
	hDry, pDry := setup(t)
	dry, _, _ := exportHost(hDry, skilldoc.HostClaudeSkill, pDry.fn, exportOpts{dryRun: true})
	hReal, pReal := setup(t)
	real, _, _ := exportHost(hReal, skilldoc.HostClaudeSkill, pReal.fn, exportOpts{})
	if summary(dry) != summary(real) {
		t.Errorf("dry run and real run disagree:\n dry:  %s\n real: %s", summary(dry), summary(real))
	}
	if len(dry.Moved) != 1 || len(dry.Failed) != 1 {
		t.Fatalf("dry-run shape: %s", summary(dry))
	}
	if !reflect.DeepEqual(dry.Moved[0].Kept, []string{"helper.sh"}) {
		t.Errorf("dry-run move kept = %v", dry.Moved[0].Kept)
	}
	if c := codes(dry.Failed[0].Reasons); c[len(c)-1] != reasonFileIsLink {
		t.Errorf("a linked SKILL.md must fail the dry run too: %v", c)
	}
	if got, _ := os.ReadFile(filepath.Join(hReal, "elsewhere.md")); string(got) != hadronFile(t, "linkt") {
		t.Error("the real run wrote through a linked SKILL.md")
	}
	if !exists(filepath.Join(hDry, ".claude", "skills", "old", "SKILL.md")) {
		t.Error("the dry run removed a file")
	}
}

// The full-path guard runs before every mutation: a root check that fails at
// that moment stops the write and the removal, and nothing is touched.
func TestHostFSGuardStopsEveryMutation(t *testing.T) {
	h := home(t)
	root := filepath.Join(h, ".claude", "skills")
	write(t, filepath.Join(root, "x", "SKILL.md"), "keep")
	fsys := hostFS{root: root, guard: func() *exportReasonDTO {
		return &exportReasonDTO{Code: reasonRootIsLink, Origin: originClient}
	}}
	if r := fsys.write(entry("y", gen.SkillExportActionWrite, "b", "")); r == nil || r.Code != reasonRootIsLink {
		t.Errorf("write = %+v", r)
	}
	if _, r := fsys.remove("x"); r == nil || r.Code != reasonRootIsLink {
		t.Errorf("remove = %+v", r)
	}
	if exists(filepath.Join(root, "y")) || !exists(filepath.Join(root, "x", "SKILL.md")) {
		t.Error("a guarded mutation touched the disk")
	}
}

// Codex P1 on #694: ownership is checked AT THE DESTINATION, not by name. A
// write reaching a foreign SKILL.md is refused even when no name-level guard
// flagged it — here by calling hostFS.write directly, so the name map is not
// involved at all.
func TestHostFSWriteRefusesAForeignDestination(t *testing.T) {
	h := home(t)
	root := filepath.Join(h, ".claude", "skills")
	f := filepath.Join(root, "demo", "SKILL.md")
	foreign := "---\nname: demo\ndescription: not ours\n---\n\nmine\n"
	write(t, f, foreign)
	fsys := hostFS{root: root, guard: func() *exportReasonDTO { return nil }}
	for _, dry := range []bool{true, false} {
		fsys.dryRun = dry
		if r := fsys.write(entry("demo", gen.SkillExportActionWrite, "hadron's", "")); r == nil || r.Code != reasonForeignFile {
			t.Errorf("dryRun=%v: write = %+v, want %s", dry, r, reasonForeignFile)
		}
	}
	if got, _ := os.ReadFile(f); string(got) != foreign {
		t.Error("the foreign file was replaced")
	}
}

// On a case-insensitive filesystem (macOS, Windows by default) a foreign
// "Demo/" is the same directory as "demo/". Runs only where that is true.
func TestExportCaseInsensitiveForeignDirectoryIsProtected(t *testing.T) {
	h := home(t)
	root := filepath.Join(h, ".claude", "skills")
	f := filepath.Join(root, "Demo", "SKILL.md")
	foreign := "---\nname: Demo\ndescription: not ours\n---\n\nmine\n"
	write(t, f, foreign)
	if !exists(filepath.Join(root, "demo", "SKILL.md")) {
		t.Skip("this filesystem is case-sensitive; TestHostFSWriteRefusesAForeignDestination covers the check itself")
	}
	p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{entry("demo", gen.SkillExportActionWrite, "hadron's", "")}}
	hd, _, _ := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{})
	if len(hd.Refused) != 1 || len(hd.Written) != 0 {
		t.Fatalf("refused=%+v written=%v", hd.Refused, names(hd.Written))
	}
	if got, _ := os.ReadFile(f); string(got) != foreign {
		t.Error("the foreign file was replaced through a case-folded name")
	}
}

// Our own files — current header, and a legacy header with no node id — are
// still replaced when the server plans it: ownership is the Hadron header.
func TestExportStillRewritesItsOwnFiles(t *testing.T) {
	h := home(t)
	root := filepath.Join(h, ".claude", "skills")
	current, err := skilldoc.Render("id-a", "a", "hrn:node:example.com:demo:tasks:a", "d", "old")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "a", "SKILL.md"), current)
	legacy := "---\nname: b\ndescription: d\n---\n\n<!-- hadron-skill source=hrn:node:example.com:demo:tasks:b hash=0123456789abcdef -->\nold\n"
	write(t, filepath.Join(root, "b", "SKILL.md"), legacy)
	p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{
		entry("a", gen.SkillExportActionWrite, "new a", ""),
		entry("b", gen.SkillExportActionWrite, "new b", ""),
	}}
	hd, _, _ := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{})
	if !reflect.DeepEqual(names(hd.Written), []string{"a", "b"}) {
		t.Fatalf("written=%v refused=%+v failed=%+v", names(hd.Written), hd.Refused, hd.Failed)
	}
	for n, want := range map[string]string{"a": "new a", "b": "new b"} {
		if got, _ := os.ReadFile(filepath.Join(root, n, "SKILL.md")); string(got) != want {
			t.Errorf("%s = %q", n, got)
		}
	}
}

// A MOVE whose SOURCE SKILL.md is a link fails before the destination is
// written: no partial mutation (Copilot on #694).
func TestExportMoveWithALinkedSourceWritesNothing(t *testing.T) {
	h := home(t)
	root := filepath.Join(h, ".claude", "skills")
	write(t, filepath.Join(h, "elsewhere.md"), "x")
	mkdir(t, filepath.Join(root, "old"))
	symlink(t, filepath.Join(h, "elsewhere.md"), filepath.Join(root, "old", "SKILL.md"))
	p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{entry("new", gen.SkillExportActionMove, "b", "old")}}
	hd, _, _ := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{})
	if len(hd.Failed) != 1 || codes(hd.Failed[0].Reasons)[len(hd.Failed[0].Reasons)-1] != reasonFileIsLink {
		t.Fatalf("failed = %+v", hd.Failed)
	}
	if exists(filepath.Join(root, "new")) {
		t.Error("the destination was written before the source was refused")
	}
}

// A dangling or unattributable linked SKILL.md is reported as a LINK, not as a
// foreign file (Copilot on #694).
func TestExportLinkedSkillFileIsALinkNotForeign(t *testing.T) {
	h := home(t)
	root := filepath.Join(h, ".claude", "skills")
	mkdir(t, filepath.Join(root, "demo"))
	symlink(t, filepath.Join(h, "nowhere.md"), filepath.Join(root, "demo", "SKILL.md"))
	p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{entry("demo", gen.SkillExportActionWrite, "b", "")}}
	hd, _, _ := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{})
	if len(hd.Failed) != 1 || codes(hd.Failed[0].Reasons)[len(hd.Failed[0].Reasons)-1] != reasonFileIsLink {
		t.Fatalf("failed=%+v refused=%+v", hd.Failed, hd.Refused)
	}
}

// Only "does not exist" is absent: an unreadable skill directory fails the
// dry run exactly as it fails the real run (Copilot on #694).
func TestExportUnreadableDestinationFailsInBothModes(t *testing.T) {
	// Unix permission semantics only: on Windows, os.Chmod toggles the
	// read-only attribute and 0o000 does not make a directory unreadable, and
	// Geteuid is -1 there (#696 review). Root ignores permissions.
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions are not Unix-like on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	for _, dry := range []bool{true, false} {
		h := home(t)
		dir := filepath.Join(h, ".claude", "skills", "demo")
		mkdir(t, dir)
		if err := os.Chmod(dir, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{entry("demo", gen.SkillExportActionWrite, "b", "")}}
		hd, _, _ := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{dryRun: dry})
		if len(hd.Failed) != 1 || len(hd.Written) != 0 {
			t.Errorf("dryRun=%v: written=%v failed=%+v", dry, names(hd.Written), hd.Failed)
		}
	}
}

// The guard runs again right before each removal, not only at its start: a
// root that turns into a link between the checks stops the delete.
func TestHostFSRemoveRechecksBeforeDeleting(t *testing.T) {
	h := home(t)
	root := filepath.Join(h, ".claude", "skills")
	f := filepath.Join(root, "x", "SKILL.md")
	write(t, f, "keep")
	calls := 0
	fsys := hostFS{root: root, guard: func() *exportReasonDTO {
		calls++
		if calls == 1 {
			return nil
		}
		return &exportReasonDTO{Code: reasonRootIsLink, Origin: originClient}
	}}
	if _, r := fsys.remove("x"); r == nil || r.Code != reasonRootIsLink {
		t.Fatalf("remove = %+v, want the second check to refuse", r)
	}
	if !exists(f) {
		t.Error("the file was deleted although the pre-delete check refused")
	}
}

// Ownership is re-checked immediately before the atomic write (#696 review):
// a foreign SKILL.md that appears after the first checks — planted here on the
// guard's second call, which runs after MkdirAll — is still not replaced.
func TestHostFSWriteRechecksOwnershipBeforeWriting(t *testing.T) {
	h := home(t)
	root := filepath.Join(h, ".claude", "skills")
	mkdir(t, root)
	f := filepath.Join(root, "demo", "SKILL.md")
	foreign := "---\nname: demo\ndescription: arrived late\n---\n\nmine\n"
	calls := 0
	fsys := hostFS{root: root, guard: func() *exportReasonDTO {
		calls++
		if calls == 2 {
			write(t, f, foreign)
		}
		return nil
	}}
	if r := fsys.write(entry("demo", gen.SkillExportActionWrite, "hadron's", "")); r == nil || r.Code != reasonForeignFile {
		t.Fatalf("write = %+v, want %s", r, reasonForeignFile)
	}
	if got, _ := os.ReadFile(f); string(got) != foreign {
		t.Error("a foreign file that appeared before the write was replaced")
	}
}

// sameDirectory is the check that keeps a case-only rename from deleting what
// it just wrote: one directory reached by two spellings of a path.
func TestSameDirectory(t *testing.T) {
	h := home(t)
	a := filepath.Join(h, "a")
	mkdir(t, a)
	mkdir(t, filepath.Join(h, "b"))
	if !sameDirectory(a, h+string(filepath.Separator)+"b"+string(filepath.Separator)+".."+string(filepath.Separator)+"a") {
		t.Error("two paths to one directory must be the same directory")
	}
	if sameDirectory(a, filepath.Join(h, "b")) || sameDirectory(a, filepath.Join(h, "missing")) {
		t.Error("different or missing directories are not the same")
	}
}

// A case-only rename on a case-insensitive filesystem (#696 review): the file
// is rewritten in place and the directory takes its new spelling. Nothing is
// deleted. Runs only where the filesystem folds case.
func TestExportCaseOnlyRenameKeepsTheFile(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprintf("dryRun=%v", dry), func(t *testing.T) {
			h := home(t)
			root := filepath.Join(h, ".claude", "skills")
			old, err := skilldoc.Render("id-demo", "Demo", "hrn:node:example.com:demo:tasks:demo", "d", "old")
			if err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(root, "Demo", "SKILL.md"), old)
			if !exists(filepath.Join(root, "demo")) {
				t.Skip("this filesystem is case-sensitive")
			}
			p := &fakePlan{entries: []*gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry{entry("demo", gen.SkillExportActionMove, "new", "Demo")}}
			hd, _, _ := exportHost(h, skilldoc.HostClaudeSkill, p.fn, exportOpts{dryRun: dry})
			if len(hd.Moved) != 1 || len(hd.Failed)+len(hd.Refused) != 0 {
				t.Fatalf("moved=%+v failed=%+v refused=%+v", hd.Moved, hd.Failed, hd.Refused)
			}
			got, err := os.ReadFile(filepath.Join(root, "demo", "SKILL.md"))
			if err != nil {
				t.Fatalf("the file is gone after a case-only rename: %v", err)
			}
			entries, _ := os.ReadDir(root)
			want, wantDir := "new", "demo"
			if dry {
				want, wantDir = old, "Demo"
			}
			if string(got) != want {
				t.Errorf("content = %q, want %q", got, want)
			}
			if len(entries) != 1 || entries[0].Name() != wantDir {
				t.Errorf("directory = %v, want [%s]", entries, wantDir)
			}
		})
	}
}
