package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
			write(t, filepath.Join(root, "kept", "SKILL.md"), "hand edit")
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
			if got, _ := os.ReadFile(filepath.Join(root, "kept", "SKILL.md")); string(got) != "hand edit" {
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
