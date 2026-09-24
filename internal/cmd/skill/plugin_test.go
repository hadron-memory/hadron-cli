package skill

import (
	"archive/zip"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/skilldoc"
)

// These pin the plugin producer's own logic (#653) against faked plans: how a
// plan is bucketed, how the artifact is laid out, and the filesystem rules.
// The command-level runs (flags, requests sent, exit codes) are in
// internal/cmd/skill_plugin_test.go.

type planEntry = gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry

func withFinding(e *planEntry, severity, rule string) *planEntry {
	e.Findings = append(e.Findings, &gen.SkillExportPlanSkillPlanEntriesSkillPlanEntryFindingsSkillFinding{
		Rule: rule, Severity: severity, Message: rule + " message", Urn: e.Urn,
	})
	return e
}

func withClass(e *planEntry, class string) *planEntry {
	e.Class = &class
	return e
}

func plan(entries ...*planEntry) *gen.SkillExportPlanSkillPlan {
	return &gen.SkillExportPlanSkillPlan{Scanned: len(entries), Judged: len(entries), Entries: entries}
}

func TestBundleHostBucketsByTheServersAction(t *testing.T) {
	noBody := entry("no-body", gen.SkillExportActionWrite, "", "")
	noPlan := entry("no-plan", gen.SkillExportActionWrite, "b", "")
	noPlan.ExportPlan = nil
	p := plan(
		entry("w", gen.SkillExportActionWrite, "body-w", ""),
		noBody,
		entry("../escape", gen.SkillExportActionWrite, "b", ""),
		entry("dup", gen.SkillExportActionWrite, "b1", ""),
		entry("dup", gen.SkillExportActionWrite, "b2", ""),
		entry("s", gen.SkillExportActionSkip, "", ""),
		entry("r", gen.SkillExportActionRefuse, "", ""),
		entry("f", gen.SkillExportActionFail, "", ""),
		entry("m", gen.SkillExportActionMove, "b", "old"),
		entry("rm", gen.SkillExportActionRemove, "", ""),
		noPlan,
		nil,
	)
	hd := newPluginHost(skilldoc.HostClaudeSkill, "claude-plugin")
	files := bundleHost(&hd, p, nil)

	if !reflect.DeepEqual(files, map[string]string{"w": "body-w"}) {
		t.Errorf("files = %v, want only the one clean WRITE", files)
	}
	for label, c := range map[string]struct {
		got, want []string
	}{
		"included": {names(hd.Included), []string{"w"}},
		"skipped":  {names(hd.Skipped), []string{"s"}},
		"refused":  {names(hd.Refused), []string{"r"}},
		"failed":   {names(hd.Failed), []string{"no-body", "../escape", "dup", "dup", "f", "m", "rm", "no-plan"}},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s = %v, want %v", label, c.got, c.want)
		}
	}
	want := map[string]string{
		"no-body": reasonNoBody, "../escape": reasonUnsafeName, "dup": reasonDuplicateName,
		"m": reasonUnknownAction, "rm": reasonUnknownAction, "no-plan": reasonNoExportPlan,
	}
	for _, it := range hd.Failed {
		code, ok := want[it.Name]
		if !ok {
			// A server FAIL keeps the server's reason, verbatim, and nothing else.
			if !reflect.DeepEqual(codes(it.Reasons), []string{"server-code"}) {
				t.Errorf("%s reasons = %v, want the server's only", it.Name, codes(it.Reasons))
			}
			continue
		}
		last := it.Reasons[len(it.Reasons)-1]
		if last.Code != code || last.Origin != originClient {
			t.Errorf("%s: last reason = %+v, want client %s", it.Name, last, code)
		}
	}
	if hd.Scanned != 12 || hd.Judged != 12 {
		t.Errorf("scanned/judged = %d/%d, want the plan's counts", hd.Scanned, hd.Judged)
	}
}

func TestBundleHostFindings(t *testing.T) {
	p := plan(
		withFinding(entry("ok", gen.SkillExportActionWrite, "b", ""), "warning", "skill-description-no-trigger"),
		withFinding(entry("refused", gen.SkillExportActionRefuse, "", ""), "error", "skill-name-collision"),
		withFinding(entry("refused", gen.SkillExportActionRefuse, "", ""), "warning", "w-on-refused"),
		withFinding(entry("disabled", gen.SkillExportActionSkip, "", ""), "error", "skill-description-too-long"),
		withFinding(entry("odd", gen.SkillExportActionWrite, "b2", ""), "notice", "future-rule"),
	)
	hd := newPluginHost(skilldoc.HostClaudeSkill, "claude-plugin")
	bundleHost(&hd, p, nil)

	var got []string
	for _, f := range hd.Findings {
		got = append(got, f.Name+"/"+f.Severity+"/"+f.Rule)
	}
	want := []string{
		"ok/warning/skill-description-no-trigger",
		"refused/warning/w-on-refused",
		"disabled/error/skill-description-too-long",
		"odd/notice/future-rule",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %v\nwant %v (an error on a refused entry is in its reasons; everything else is surfaced)", got, want)
	}
	if !reflect.DeepEqual(names(hd.Skipped), []string{"disabled"}) || len(hd.Failed) != 0 {
		t.Errorf("an error finding on a SKIP must not be charged: skipped=%v failed=%v", names(hd.Skipped), names(hd.Failed))
	}
	for _, it := range hd.Included {
		for _, r := range it.Reasons {
			if strings.Contains(r.Code, "no-trigger") {
				t.Errorf("a finding must not be repeated in the item's reasons: %+v", it)
			}
		}
	}
}

func TestBundleHostNotForHostComesFromTheOtherPlan(t *testing.T) {
	both := entry("both", gen.SkillExportActionWrite, "b", "")
	here := plan(both)
	other := plan(
		entry("both", gen.SkillExportActionWrite, "b", ""),
		entry("claude-only", gen.SkillExportActionWrite, "b", ""),
		withClass(entry("off", gen.SkillExportActionSkip, "", ""), "disabled"),
		entry("claude-only", gen.SkillExportActionWrite, "b", ""),
	)
	hd := newPluginHost(skilldoc.HostCodexSkill, "codex-skills")
	bundleHost(&hd, here, []*gen.SkillExportPlanSkillPlan{other})
	if got := names(hd.NotForHost); !reflect.DeepEqual(got, []string{"claude-only"}) {
		t.Errorf("notForHost = %v, want the other host's undisabled entries this plan did not judge, once", got)
	}
}

var semverRE = regexp.MustCompile(`^0\.0\.0-[0-9A-Za-z-]+$`)

func TestBuildArtifactClaudeLayoutAndVersion(t *testing.T) {
	a := buildArtifact(skilldoc.HostClaudeSkill, "hadron", nil, map[string]string{"b": "B", "a": "A"})
	if !semverRE.MatchString(a.version) || regexp.MustCompile(`-0\d`).MatchString(a.version) {
		t.Errorf("version %q is not a valid semver pre-release", a.version)
	}
	wantDir := []string{".claude-plugin/marketplace.json", ".claude-plugin/plugin.json", ".hadron-plugin", "skills/a/SKILL.md", "skills/b/SKILL.md"}
	wantZip := []string{".claude-plugin/plugin.json", "skills/a/SKILL.md", "skills/b/SKILL.md"}
	if got := keys(a.dirFiles); !reflect.DeepEqual(got, wantDir) {
		t.Errorf("dir files = %v, want %v", got, wantDir)
	}
	if got := keys(a.zipFiles); !reflect.DeepEqual(got, wantZip) {
		t.Errorf("zip files = %v, want %v (the portal's zip: manifest + skills only)", got, wantZip)
	}
	for _, f := range []string{".claude-plugin/plugin.json", ".claude-plugin/marketplace.json"} {
		if !strings.Contains(string(a.dirFiles[f]), `"version": "`+a.version+`"`) {
			t.Errorf("%s does not carry the version %s:\n%s", f, a.version, a.dirFiles[f])
		}
	}
	if !strings.Contains(string(a.dirFiles[".claude-plugin/marketplace.json"]), `"source": "./"`) {
		t.Errorf("the marketplace must list the plugin as itself:\n%s", a.dirFiles[".claude-plugin/marketplace.json"])
	}

	same := buildArtifact(skilldoc.HostClaudeSkill, "hadron", nil, map[string]string{"a": "A", "b": "B"})
	changed := buildArtifact(skilldoc.HostClaudeSkill, "hadron", nil, map[string]string{"a": "A", "b": "B!"})
	renamed := buildArtifact(skilldoc.HostClaudeSkill, "hadron", nil, map[string]string{"a": "A", "c": "B"})
	if same.version != a.version {
		t.Errorf("unchanged content must keep its version: %s vs %s", same.version, a.version)
	}
	if changed.version == a.version || renamed.version == a.version {
		t.Error("a changed body or a renamed skill must move the version, or Claude users never get the update")
	}
}

func TestBuildArtifactCodexLayout(t *testing.T) {
	a := buildArtifact(skilldoc.HostCodexSkill, "hadron", nil, map[string]string{"a": "A"})
	if a.version != "" {
		t.Errorf("the codex skill folders have no manifest, so no version: %q", a.version)
	}
	if got := keys(a.dirFiles); !reflect.DeepEqual(got, []string{".hadron-plugin", "a/SKILL.md"}) {
		t.Errorf("dir files = %v", got)
	}
	if got := keys(a.zipFiles); !reflect.DeepEqual(got, []string{"a/SKILL.md"}) {
		t.Errorf("zip files = %v", got)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sample(body string) artifact {
	return buildArtifact(skilldoc.HostClaudeSkill, "hadron", nil, map[string]string{"a": body})
}

func TestWritePluginArtifactWritesAndReplacesItsOwn(t *testing.T) {
	out := filepath.Join(home(t), "out")
	dir, zp := filepath.Join(out, "hadron"), filepath.Join(out, "hadron.zip")
	if _, r := writePluginArtifact(out, dir, zp, sample("v1")); r != nil {
		t.Fatalf("first write: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "skills", "a", "SKILL.md")); string(b) != "v1" {
		t.Fatalf("skill = %q", b)
	}
	write(t, filepath.Join(dir, "skills", "stale", "SKILL.md"), "left over")
	if _, r := writePluginArtifact(out, dir, zp, sample("v2")); r != nil {
		t.Fatalf("replacing its own artifact: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "skills", "a", "SKILL.md")); string(b) != "v2" {
		t.Errorf("skill after replace = %q, want v2", b)
	}
	if exists(filepath.Join(dir, "skills", "stale")) {
		t.Error("a replace must be wholesale: a stale skill survived (plan §7 Q5)")
	}
	zr, err := zip.OpenReader(zp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	var inZip []string
	for _, f := range zr.File {
		inZip = append(inZip, f.Name)
	}
	if want := []string{".claude-plugin/plugin.json", "skills/a/SKILL.md"}; !reflect.DeepEqual(inZip, want) {
		t.Errorf("zip = %v, want %v", inZip, want)
	}
	ents, _ := os.ReadDir(out)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("temp leftover in --out: %s", e.Name())
		}
	}
}

func TestWritePluginArtifactRefusesWhatItDidNotWrite(t *testing.T) {
	h := home(t)
	cases := map[string]func(out string){
		"foreign dir":      func(out string) { write(t, filepath.Join(out, "hadron", "mine.txt"), "keep") },
		"file at dir path": func(out string) { write(t, filepath.Join(out, "hadron"), "keep") },
		"forged marker":    func(out string) { write(t, filepath.Join(out, "hadron", pluginMarker), `{"producer":"someone else"}`) },
		"link at dir path": func(out string) {
			mkdir(t, filepath.Join(h, "elsewhere"))
			symlink(t, filepath.Join(h, "elsewhere"), filepath.Join(out, "hadron"))
		},
		"foreign zip": func(out string) { write(t, filepath.Join(out, "hadron.zip"), "not a zip") },
		"someone's real zip": func(out string) {
			mkdir(t, out)
			zf, err := os.Create(filepath.Join(out, "hadron.zip"))
			if err != nil {
				t.Fatal(err)
			}
			if err := writeZip(zf, map[string][]byte{"theirs.txt": []byte("keep")}); err != nil {
				t.Fatal(err)
			}
			_ = zf.Close()
			// writeZip stamps our comment; strip it so the zip is valid but not ours.
			b, _ := os.ReadFile(filepath.Join(out, "hadron.zip"))
			b = []byte(strings.TrimSuffix(string(b), pluginZipComment))
			b[len(b)-2], b[len(b)-1] = 0, 0 // comment length
			write(t, filepath.Join(out, "hadron.zip"), string(b))
		},
		"link at zip path": func(out string) {
			write(t, filepath.Join(h, "z"), "x")
			symlink(t, filepath.Join(h, "z"), filepath.Join(out, "hadron.zip"))
		},
		"dir at zip path": func(out string) { mkdir(t, filepath.Join(out, "hadron.zip")) },
		"marked dir as zip": func(out string) {
			write(t, filepath.Join(out, "hadron.zip", pluginMarker), `{"producer":"hadron skill plugin"}`)
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(h, strings.ReplaceAll(name, " ", "-"))
			setup(out)
			before := snapshot(t, out)
			_, r := writePluginArtifact(out, filepath.Join(out, "hadron"), filepath.Join(out, "hadron.zip"), sample("v1"))
			if r == nil || (r.Code != reasonArtifactNotOurs && r.Code != reasonArtifactIsLink) {
				t.Fatalf("reason = %+v, want a refusal", r)
			}
			if after := snapshot(t, out); !reflect.DeepEqual(before, after) {
				t.Errorf("a refused run changed --out:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

// TestWritePluginArtifactInterruptedLeavesNothing proves an artifact that
// cannot be completed leaves no half-plugin behind, and keeps the old one.
func TestWritePluginArtifactInterruptedLeavesNothing(t *testing.T) {
	out := filepath.Join(home(t), "out")
	dir := filepath.Join(out, "hadron")
	broken := sample("v1")
	// A file and a directory at one path: the second write must fail.
	broken.dirFiles["skills/a/SKILL.md/x"] = []byte("x")
	if _, r := writePluginArtifact(out, dir, "", broken); r == nil || r.Code != reasonIOError {
		t.Fatalf("reason = %+v, want an io failure", r)
	}
	if exists(dir) {
		t.Error("an interrupted build left an artifact that would install")
	}
	if _, r := writePluginArtifact(out, dir, "", sample("good")); r != nil {
		t.Fatal(r)
	}
	if _, r := writePluginArtifact(out, dir, "", broken); r == nil {
		t.Fatal("want a failure")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "skills", "a", "SKILL.md")); string(b) != "good" {
		t.Errorf("a failed rebuild must keep the previous artifact; skill = %q", b)
	}
	if ents, _ := os.ReadDir(out); len(ents) != 1 {
		t.Errorf("--out holds %d entries, want only the artifact (no temp leftovers)", len(ents))
	}
}

func snapshot(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		entry := rel + " " + fi.Mode().String()
		if fi.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			entry += " " + string(b)
		}
		out = append(out, entry)
		return nil
	})
	return out
}

func TestRefuseHostRoot(t *testing.T) {
	h := home(t)
	for _, p := range []string{
		filepath.Join(h, ".claude", "skills"),
		filepath.Join(h, ".claude", "skills", "hadron"),
		filepath.Join(h, "repo", ".claude", "skills", "x", "hadron"),
		filepath.Join(h, ".agents", "skills", "hadron-codex"),
		filepath.Join(h, ".codex", "skills", "hadron"),
	} {
		if err := refuseHostRoot(p, p, h); exitcode.FromError(err) != exitcode.Usage {
			t.Errorf("%s: err = %v, want a usage refusal", p, err)
		}
	}
	for _, p := range []string{
		filepath.Join(h, ".claude", "hadron"),
		filepath.Join(h, "plugins", "hadron"),
		filepath.Join(h, "skills", "hadron"),
	} {
		if err := refuseHostRoot(p, p, h); err != nil {
			t.Errorf("%s: %v, want it allowed", p, err)
		}
	}

	// A root reached through a link is refused by the resolved check, and so
	// is an artifact ABOVE a live root (a wholesale replace would delete it).
	mkdir(t, filepath.Join(h, "dotfiles", "skills"))
	symlink(t, filepath.Join(h, "dotfiles", "skills"), filepath.Join(h, ".claude", "skills"))
	if err := refuseHostRoot(filepath.Join(h, "dotfiles", "skills", "hadron"), filepath.Join(h, "dotfiles", "skills", "hadron"), h); err == nil {
		t.Error("an artifact inside the resolved ~/.claude/skills must be refused")
	}
	if err := refuseHostRoot(filepath.Join(h, "dotfiles"), filepath.Join(h, "dotfiles"), h); err == nil {
		t.Error("an artifact above a live skills root must be refused")
	}
}

func TestResolveOut(t *testing.T) {
	h := home(t)
	mkdir(t, filepath.Join(h, "real"))
	symlink(t, filepath.Join(h, "real"), filepath.Join(h, "link"))
	got, err := resolveOut("~/link/new/dir", h)
	if err != nil || got != filepath.Join(h, "real", "new", "dir") {
		t.Errorf("resolveOut = %q, %v; want the typed link followed and the rest kept", got, err)
	}
	write(t, filepath.Join(h, "file"), "x")
	if _, err := resolveOut(filepath.Join(h, "file"), h); exitcode.FromError(err) != exitcode.Usage {
		t.Errorf("a file as --out: err = %v, want usage", err)
	}
}

func TestPluginHasFailures(t *testing.T) {
	clean := newPluginHost("a", "x")
	clean.Skipped = []exportItemDTO{{Name: "s"}}
	clean.Findings = []pluginFindingDTO{{Severity: "error"}}
	if pluginHasFailures(pluginDTO{Hosts: []pluginHostDTO{clean}}) {
		t.Error("skips and findings alone must exit 0")
	}
	for _, mut := range []func(*pluginHostDTO){
		func(h *pluginHostDTO) { h.Refused = []exportItemDTO{{}} },
		func(h *pluginHostDTO) { h.Failed = []exportItemDTO{{}} },
		func(h *pluginHostDTO) { h.Failure = &exportReasonDTO{} },
	} {
		h := newPluginHost("a", "x")
		mut(&h)
		if !pluginHasFailures(pluginDTO{Hosts: []pluginHostDTO{clean, h}}) {
			t.Errorf("want exit 5 for %+v", h)
		}
	}
}

// TestSwapIntoRechecksWhatItMovedAside: checkReplaceable runs before the
// build, so a directory swapped in afterwards reaches swapInto unchecked. The
// recheck on the moved-aside copy is what keeps it from being deleted.
func TestSwapIntoRechecksWhatItMovedAside(t *testing.T) {
	h := home(t)
	dir, tmp := filepath.Join(h, "hadron"), filepath.Join(h, ".hadron.tmp-1")
	write(t, filepath.Join(dir, "theirs.txt"), "keep")
	write(t, filepath.Join(tmp, "skills", "a", "SKILL.md"), "new")
	r := swapInto(tmp, dir, true)
	if r == nil || r.Code != reasonArtifactNotOurs {
		t.Fatalf("reason = %+v, want artifact-not-ours", r)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "theirs.txt")); err != nil || string(b) != "keep" {
		t.Errorf("the foreign directory was not put back intact: %q, %v", b, err)
	}
	if exists(filepath.Join(dir, "skills")) {
		t.Error("the new build landed over a directory that is not ours")
	}
}

func TestSwapIntoRechecksAZipMovedAside(t *testing.T) {
	h := home(t)
	dest, tmp := filepath.Join(h, "hadron.zip"), filepath.Join(h, ".hadron.zip.tmp-1")
	write(t, dest, "someone else's file")
	write(t, tmp, "new zip")
	r := swapInto(tmp, dest, false)
	if r == nil || r.Code != reasonArtifactNotOurs {
		t.Fatalf("reason = %+v, want artifact-not-ours", r)
	}
	if b, _ := os.ReadFile(dest); string(b) != "someone else's file" {
		t.Errorf("a foreign file swapped in at the zip path was replaced: %q", b)
	}
}

func TestApplyWriteResult(t *testing.T) {
	fresh := func() pluginHostDTO {
		hd := newPluginHost(skilldoc.HostClaudeSkill, "claude-plugin")
		dir, zp := "/out/hadron", "/out/hadron.zip"
		hd.Artifact, hd.Zip = &dir, &zp
		hd.Included = []exportItemDTO{{Name: "a", Reasons: []exportReasonDTO{}}}
		return hd
	}
	r := &exportReasonDTO{Code: reasonIOError, Message: "boom"}

	partial := fresh()
	applyWriteResult(&partial, true, r)
	if partial.Artifact == nil || partial.Zip != nil || partial.Failure != r || len(partial.Included) != 1 || len(partial.Failed) != 0 {
		t.Errorf("zip-only failure: the live directory and its skills must stay reported: %+v", partial)
	}

	none := fresh()
	applyWriteResult(&none, false, r)
	if none.Artifact != nil || none.Zip != nil || len(none.Included) != 0 || !reflect.DeepEqual(names(none.Failed), []string{"a"}) {
		t.Errorf("nothing written: every included skill must be failed, named: %+v", none)
	}
	if none.Failed[0].Reasons[0] != *r {
		t.Errorf("the host's reason must come first: %+v", none.Failed[0].Reasons)
	}

	ok := fresh()
	applyWriteResult(&ok, true, nil)
	if ok.Failure != nil || ok.Zip == nil || len(ok.Included) != 1 {
		t.Errorf("success changed the report: %+v", ok)
	}
}

func TestBuildArtifactVersionIsUnambiguous(t *testing.T) {
	v := func(scope *pluginScopeDTO, skills map[string]string) string {
		return buildArtifact(skilldoc.HostClaudeSkill, "hadron", scope, skills).version
	}
	// Under a separator-joined hash these two bundles hash the same bytes.
	if v(nil, map[string]string{"a": "x", "b": "y"}) == v(nil, map[string]string{"a": "x\x00b\x00y"}) {
		t.Error("a body can be read as the next name: two different bundles share a version")
	}
	if v(nil, map[string]string{"a": "x"}) == v(&pluginScopeDTO{Name: "research"}, map[string]string{"a": "x"}) {
		t.Error("the manifest's description changed but the version did not, so Claude users never get it")
	}
	if buildArtifact(skilldoc.HostClaudeSkill, "one", nil, map[string]string{"a": "x"}).version ==
		buildArtifact(skilldoc.HostClaudeSkill, "two", nil, map[string]string{"a": "x"}).version {
		t.Error("the plugin name is part of what the version stands for")
	}
}
