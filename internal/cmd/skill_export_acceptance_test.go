package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/skilldoc"
)

// Command-level acceptance tests for `hadron skill export` (#621): the writer
// cases of the cross-host acceptance matrix
// (internal/skilldoc/testdata/crosshost-acceptance.json), one subtest per
// case, named by its id, plus the destination and dry-run cases from team chat
// #1332.
//
// WHAT THIS PROVES, AND WHAT IT DOES NOT. The server plans every action; the
// client walks the disk, submits file facts, guards the filesystem, performs
// the plan and reports it. So each case asserts the CLIENT half end to end:
//   - what the command SENDS for the files on disk (the facts the planner
//     judges), and that it asks once per host;
//   - what it DOES with the plan the contract calls for: bytes on disk, the
//     report's classification, and the exit code (5 on refused/failed, #1326);
//   - every decision the client owns outright: roots, links, foreign files,
//     the deprecated ~/.codex/skills, dry-run.
// Whether the SERVER returns that plan for those facts is the resolver
// suite's to prove (hadron-server resolvers.skillPlan tests). A fake plan
// here stands for the contract's answer, never for an implementation's.
//
// Every case runs the real command against a disposable HOME, resolved as
// the command resolves it (on darwin a temp dir sits under a symlinked /var).

const (
	accSource = "hrn:node:example.com:demo:tasks:demo"
	accNodeID = "01a0000000000000000000000000d001"
	accName   = "hadron-demo"
)

// accHome is a disposable, resolved HOME for one export run. The suite is
// Unix-only: these cases build symbolic links, and on Windows os.UserHomeDir
// reads USERPROFILE — set too, so a run there could never reach a real
// profile even if the skip were removed (#699 review).
func accHome(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the export acceptance cases build symbolic links and a POSIX home; Unix only")
	}
	h, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	return h
}

func claudeRoot(home string) string { return filepath.Join(home, ".claude", "skills") }
func codexRoot(home string) string  { return filepath.Join(home, ".agents", "skills") }

// rendered is the file the current exporter writes for a host. The two hosts
// get different descriptions, so a file written through the wrong host's
// destination is visible byte for byte (P06).
func rendered(t *testing.T, host string) string {
	t.Helper()
	body, err := skilldoc.Render(accNodeID, accName, accSource, "Use when demoing the "+host+" host.", "# Demo\n\nBody.")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// legacyNoID is a file from the retired by-hand export: a `Generated from`
// header and NO id= (P08/P12–P15).
func legacyNoID(extraFrontmatter bool) string {
	fm := "---\nname: " + accName + "\ndescription: Use when demoing.\n"
	if extraFrontmatter {
		fm += "allowed-tools: Bash\n" // a key the renderer never writes: a hand edit
	}
	return fm + "---\n\n<!-- Generated from " + accSource + " -->\n\n# Demo\n"
}

// foreignSkill is somebody else's skill: it parses and carries no Hadron
// header at all.
const foreignSkill = "---\nname: " + accName + "\ndescription: Somebody else's skill.\n---\n\n# Not ours\n"

func put(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, target, at string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading %s: %v", p, err)
	}
	return string(b)
}

func absent(p string) bool {
	_, err := os.Lstat(p)
	return os.IsNotExist(err)
}

// ---- the planner, faked per host ----

type planReason struct{ Code, Message string }

// planEntry is one entry of a host's plan, in the shape SkillExportPlan
// returns it.
func planEntry(name, action, body, movedFrom string, reasons ...planReason) map[string]any {
	rs := []map[string]any{}
	for _, r := range reasons {
		rs = append(rs, map[string]any{"code": r.Code, "message": r.Message})
	}
	e := map[string]any{
		"urn": accSource, "nodeId": accNodeID, "name": name, "class": nil, "parseFailure": false,
		"movedFrom": nil, "renderedBody": nil, "findings": []any{},
		"exportPlan": map[string]any{
			"action":                action,
			"preservesExistingFile": action == "SKIP" || action == "REFUSE" || action == "FAIL",
			"reasons":               rs,
		},
	}
	if body != "" {
		e["renderedBody"] = body
	}
	if movedFrom != "" {
		e["movedFrom"] = movedFrom
	}
	return e
}

type hostPlan struct {
	entries      []map[string]any
	orphans      []map[string]any
	unrecognized []map[string]any
}

func (p hostPlan) json() map[string]any {
	ent, orph, unr := p.entries, p.orphans, p.unrecognized
	if ent == nil {
		ent = []map[string]any{}
	}
	if orph == nil {
		orph = []map[string]any{}
	}
	if unr == nil {
		unr = []map[string]any{}
	}
	return map[string]any{"scanned": 1, "judged": len(ent), "entries": ent, "orphans": orph, "unrecognized": unr}
}

// exportCall is one SkillExportPlan request as the fake received it.
type exportCall struct {
	Host   string           `json:"host"`
	Intent string           `json:"intent"`
	Files  []map[string]any `json:"files"`
	Force  *bool            `json:"force"`
}

// exportServer answers SkillExportPlan per host. plan is consulted on every
// call, with the call itself, so a case can answer --force differently.
func exportServer(t *testing.T, plan func(c exportCall) hostPlan) (string, *[]exportCall) {
	t.Helper()
	calls := &[]exportCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Input json.RawMessage `json:"input"`
			} `json:"variables"`
		}
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		if body.OperationName != "SkillExportPlan" {
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected operation"}]}`))
			return
		}
		var c exportCall
		_ = json.Unmarshal(body.Variables.Input, &c)
		*calls = append(*calls, c)
		out, _ := json.Marshal(map[string]any{"data": map[string]any{"skillPlan": plan(c).json()}})
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, calls
}

// callFor is the request the fake received for host (the last one).
func callFor(t *testing.T, calls []exportCall, host string) exportCall {
	t.Helper()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Host == host {
			return calls[i]
		}
	}
	t.Fatalf("no SkillExportPlan call for host %s; calls: %+v", host, calls)
	return exportCall{}
}

// ---- running the command, reading the report ----

type reportReason struct {
	Code, Message, Origin string
}

type reportItem struct {
	Node    string         `json:"node"`
	NodeID  string         `json:"nodeId"`
	Name    string         `json:"name"`
	Reasons []reportReason `json:"reasons"`
	Kept    []string       `json:"kept"`
	From    string         `json:"from"`
}

type reportOrphan struct {
	Dir    string `json:"dir"`
	NodeID string `json:"nodeId"`
}

type reportHost struct {
	Host     string         `json:"host"`
	Root     string         `json:"root"`
	Failure  *reportReason  `json:"failure"`
	Written  []reportItem   `json:"written"`
	Moved    []reportItem   `json:"moved"`
	Removed  []reportItem   `json:"removed"`
	Skipped  []reportItem   `json:"skipped"`
	Refused  []reportItem   `json:"refused"`
	Failed   []reportItem   `json:"failed"`
	Orphaned []reportOrphan `json:"orphaned"`
	Pruned   []reportOrphan `json:"pruned"`
}

type exportReport struct {
	DryRun       bool         `json:"dryRun"`
	Hosts        []reportHost `json:"hosts"`
	Unrecognized []struct {
		Node     string   `json:"node"`
		NodeID   string   `json:"nodeId"`
		KeyCount int      `json:"keyCount"`
		Keys     []string `json:"keys"`
	} `json:"unrecognized"`
}

func (r exportReport) host(t *testing.T, key string) reportHost {
	t.Helper()
	for _, h := range r.Hosts {
		if h.Host == key {
			return h
		}
	}
	t.Fatalf("no %s host in the report: %+v", key, r.Hosts)
	return reportHost{}
}

// classOf names the one report class an item for name landed in on a host.
func (h reportHost) classOf(name string) string {
	var got []string
	for class, items := range map[string][]reportItem{
		"written": h.Written, "moved": h.Moved, "removed": h.Removed,
		"skipped": h.Skipped, "refused": h.Refused, "failed": h.Failed,
	} {
		for _, it := range items {
			if it.Name == name {
				got = append(got, class)
			}
		}
	}
	sort.Strings(got)
	return strings.Join(got, "+")
}

// reasonCodes are the reason codes on name's item, wherever it landed.
func (h reportHost) reasonCodes(name string) []string {
	var out []string
	for _, items := range [][]reportItem{h.Written, h.Moved, h.Removed, h.Skipped, h.Refused, h.Failed} {
		for _, it := range items {
			if it.Name == name {
				for _, r := range it.Reasons {
					out = append(out, r.Code)
				}
			}
		}
	}
	return out
}

func runExport(t *testing.T, url string, args ...string) (exportReport, error) {
	t.Helper()
	rep, _, err := runExportRaw(t, url, args...)
	return rep, err
}

// runExportRaw also returns the report's raw JSON, for assertions a typed
// decode cannot make (an unknown key is silently dropped by json.Unmarshal).
func runExportRaw(t *testing.T, url string, args ...string) (exportReport, string, error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"skill", "export", "--json", "--server", url}, args...))
	err := root.Execute()
	var rep exportReport
	if uerr := json.Unmarshal([]byte(out.String()), &rep); uerr != nil {
		t.Fatalf("export --json did not print a report (err=%v): %v\n%s", err, uerr, out.String())
	}
	return rep, out.String(), err
}

func wantExit(t *testing.T, err error, want int) {
	t.Helper()
	if got := exitCodeFor(err); got != want {
		t.Fatalf("exit code = %d, want %d (err: %v)", got, want, err)
	}
}

// writesBoth answers WRITE with each host's own rendering: the plan for a
// fresh, enabled D01 declaration.
func writesBoth(t *testing.T) func(c exportCall) hostPlan {
	return func(c exportCall) hostPlan {
		return hostPlan{entries: []map[string]any{planEntry(accName, "WRITE", rendered(t, c.Host), "")}}
	}
}

// ---- the matrix writer cases ----

// acceptanceCases is every matrix writer case this file executes, keyed by
// its matrix id. TestSkillExportAcceptanceCoversTheMatrix holds it to the
// matrix's `writer` section in both directions, so neither can drift.
func acceptanceCases() map[string]func(t *testing.T) {
	cases := map[string]func(t *testing.T){}

	// P01: export asks the planner once PER HOST, naming the host. Whether the
	// server accepts codexSkill is the resolver suite's; this is the client
	// half the matrix can see.
	cases["P01"] = func(t *testing.T) {
		accHome(t)
		url, calls := exportServer(t, writesBoth(t))
		_, err := runExport(t, url)
		wantExit(t, err, 0)
		seen := map[string]int{}
		for _, c := range *calls {
			seen[c.Host]++
		}
		for _, h := range skilldoc.Hosts {
			if seen[h.Key] != 1 {
				t.Errorf("host %s was planned %d time(s), want exactly 1 (calls: %v)", h.Key, seen[h.Key], seen)
			}
		}
		if len(seen) != len(skilldoc.Hosts) {
			t.Errorf("planned hosts %v, want exactly %d", seen, len(skilldoc.Hosts))
		}
	}

	// P03: the first export writes both hosts' files, creating an absent root
	// and an absent ~/.agents, with no detection and no prompt; never the
	// deprecated ~/.codex/skills.
	cases["P03"] = func(t *testing.T) {
		home := accHome(t)
		url, calls := exportServer(t, writesBoth(t))
		rep, err := runExport(t, url)
		wantExit(t, err, 0)
		if len(*calls) != len(skilldoc.Hosts) {
			t.Errorf("want one plan per host (%d), got %d", len(skilldoc.Hosts), len(*calls))
		}
		for _, c := range *calls {
			if c.Intent != "EXPORT" || len(c.Files) != 0 || c.Force != nil {
				t.Errorf("%s: an empty disk sends intent EXPORT, no files and no force; got %+v", c.Host, c)
			}
		}
		if got := read(t, filepath.Join(claudeRoot(home), accName, "SKILL.md")); got != rendered(t, "claudeSkill") {
			t.Errorf("claude file is not the claude rendering:\n%s", got)
		}
		if got := read(t, filepath.Join(codexRoot(home), accName, "SKILL.md")); got != rendered(t, "codexSkill") {
			t.Errorf("codex file is not the codex rendering:\n%s", got)
		}
		if !absent(filepath.Join(home, ".codex")) {
			t.Error("the deprecated ~/.codex was created")
		}
		for _, h := range []string{"claudeSkill", "codexSkill"} {
			if c := rep.host(t, h).classOf(accName); c != "written" {
				t.Errorf("%s: %s reported %q, want written", h, accName, c)
			}
		}
	}

	// P04: an unedited Codex file from an earlier export is REMOVED when the
	// Codex declaration no longer exports; the Claude file is written.
	cases["P04"] = func(t *testing.T) {
		home := accHome(t)
		put(t, filepath.Join(codexRoot(home), accName, "SKILL.md"), rendered(t, "codexSkill"))
		url, calls := exportServer(t, func(c exportCall) hostPlan {
			if c.Host == "codexSkill" {
				return hostPlan{entries: []map[string]any{planEntry(accName, "REMOVE", "", accName, planReason{"declaration-disabled", "disabled"})}}
			}
			return hostPlan{entries: []map[string]any{planEntry(accName, "WRITE", rendered(t, c.Host), "")}}
		})
		rep, err := runExport(t, url)
		wantExit(t, err, 0)
		// An UNEDITED file: the hash recomputed from its bytes equals the one
		// its header records. That equality is what lets the planner tell it
		// from P05's hand-edited file, so it is asserted, not just presence.
		sent := callFor(t, *calls, "codexSkill").Files
		if len(sent) != 1 || sent[0]["nodeId"] != accNodeID {
			t.Fatalf("the earlier export's file must be submitted with its id; sent %v", sent)
		}
		if fh, hh := sent[0]["fileHash"], sent[0]["headerHash"]; fh == nil || fh == "" || fh != hh {
			t.Errorf("an unedited file must send equal, non-empty fileHash and headerHash; got %v / %v", fh, hh)
		}
		if !absent(filepath.Join(codexRoot(home), accName)) {
			t.Error("the removed skill's directory is still there")
		}
		if c := rep.host(t, "codexSkill").classOf(accName); c != "removed" {
			t.Errorf("codex: reported %q, want removed", c)
		}
		if c := rep.host(t, "claudeSkill").classOf(accName); c != "written" {
			t.Errorf("claude: reported %q, want written", c)
		}
	}

	// P05: a HAND-EDITED Codex file is refused, kept byte for byte and
	// reported (exit 5); --force travels on the request and removes it.
	cases["P05"] = func(t *testing.T) {
		home := accHome(t)
		edited := rendered(t, "codexSkill") + "\nA hand edit.\n"
		p := filepath.Join(codexRoot(home), accName, "SKILL.md")
		put(t, p, edited)
		plan := func(c exportCall) hostPlan {
			if c.Host != "codexSkill" {
				return hostPlan{entries: []map[string]any{planEntry(accName, "WRITE", rendered(t, c.Host), "")}}
			}
			if c.Force != nil && *c.Force {
				return hostPlan{entries: []map[string]any{planEntry(accName, "REMOVE", "", accName, planReason{"forced", "forced"})}}
			}
			return hostPlan{entries: []map[string]any{planEntry(accName, "REFUSE", "", "", planReason{"locally-edited", "edited by hand"})}}
		}
		url, calls := exportServer(t, plan)
		rep, err := runExport(t, url)
		wantExit(t, err, exitcode.Conflict)
		// The hand edit shows as a hash that no longer matches its header: the
		// evidence the planner refuses on (P04's file sends them equal).
		sent := callFor(t, *calls, "codexSkill").Files
		if len(sent) != 1 {
			t.Fatalf("want the edited file submitted, got %v", sent)
		}
		// Both NON-EMPTY: the planner's locally-edited test is gated on a
		// non-empty header hash, so "" would slip the refusal (#699 review).
		if fh, hh := sent[0]["fileHash"], sent[0]["headerHash"]; fh == nil || hh == nil || fh == "" || hh == "" || fh == hh {
			t.Errorf("a hand-edited file must send differing, non-empty fileHash and headerHash; got %v / %v", fh, hh)
		}
		if read(t, p) != edited {
			t.Error("the hand-edited file changed")
		}
		if c := rep.host(t, "codexSkill").classOf(accName); c != "refused" {
			t.Errorf("codex: reported %q, want refused", c)
		}

		url2, calls2 := exportServer(t, plan)
		rep2, err := runExport(t, url2, "--force")
		wantExit(t, err, 0)
		if f := callFor(t, *calls2, "codexSkill").Force; f == nil || !*f {
			t.Error("--force was not sent")
		}
		if !absent(p) {
			t.Error("--force did not remove the hand-edited file")
		}
		if c := rep2.host(t, "codexSkill").classOf(accName); c != "removed" {
			t.Errorf("codex with --force: reported %q, want removed", c)
		}
	}

	// P06: ~/.agents/skills/<name> is a link INTO Claude's root. The Codex
	// write is refused (exit 5); the linked directory's facts are never sent
	// for Codex; the Claude file is Claude's rendering, never Codex's.
	cases["P06"] = func(t *testing.T) {
		home := accHome(t)
		claudeFile := filepath.Join(claudeRoot(home), accName, "SKILL.md")
		put(t, claudeFile, rendered(t, "claudeSkill"))
		link(t, filepath.Join(claudeRoot(home), accName), filepath.Join(codexRoot(home), accName))
		url, calls := exportServer(t, writesBoth(t))
		rep, err := runExport(t, url)
		wantExit(t, err, exitcode.Conflict)
		if n := len(callFor(t, *calls, "codexSkill").Files); n != 0 {
			t.Errorf("codex was sent %d file(s) read through the link", n)
		}
		codex := rep.host(t, "codexSkill")
		if c := codex.classOf(accName); c != "refused" {
			t.Errorf("codex: reported %q, want refused", c)
		}
		if !contains(codex.reasonCodes(accName), "skill-dir-is-link") {
			t.Errorf("codex reasons %v, want skill-dir-is-link", codex.reasonCodes(accName))
		}
		if got := read(t, claudeFile); got != rendered(t, "claudeSkill") {
			t.Errorf("the claude file is not the claude rendering (written through the link?):\n%s", got)
		}
	}

	// P08: a no-id legacy file in EITHER root. The client submits it with its
	// source and NOTHING hashed (no id, no fileHash, no headerHash); a SKIP is
	// honoured and reported, the file untouched; --force travels and the
	// regenerated file carries a full header.
	cases["P08"] = func(t *testing.T) {
		for _, host := range []string{"claudeSkill", "codexSkill"} {
			t.Run(host, func(t *testing.T) {
				home := accHome(t)
				root := claudeRoot(home)
				if host == "codexSkill" {
					root = codexRoot(home)
				}
				p := filepath.Join(root, accName, "SKILL.md")
				put(t, p, legacyNoID(false))
				plan := func(c exportCall) hostPlan {
					if c.Host != host {
						return hostPlan{entries: []map[string]any{planEntry(accName, "WRITE", rendered(t, c.Host), "")}}
					}
					if c.Force != nil && *c.Force {
						return hostPlan{entries: []map[string]any{planEntry(accName, "WRITE", rendered(t, c.Host), "")}}
					}
					return hostPlan{entries: []map[string]any{planEntry(accName, "SKIP", "", "", planReason{"missing-provenance-id", "no node id"})}}
				}
				url, calls := exportServer(t, plan)
				rep, err := runExport(t, url)
				wantExit(t, err, 0)
				sent := callFor(t, *calls, host).Files
				if len(sent) != 1 {
					t.Fatalf("want the no-id file submitted, got %v", sent)
				}
				f := sent[0]
				if f["dirName"] != accName || f["sourceUrn"] != accSource {
					t.Errorf("the no-id file must travel with its dir and source; sent %v", f)
				}
				for _, k := range []string{"nodeId", "fileHash", "headerHash"} {
					if _, ok := f[k]; ok {
						t.Errorf("%s sent for a no-id file (nothing is hashed for one): %v", k, f)
					}
				}
				if read(t, p) != legacyNoID(false) {
					t.Error("a SKIP changed the no-id file")
				}
				if c := rep.host(t, host).classOf(accName); c != "skipped" {
					t.Errorf("%s: reported %q, want skipped", host, c)
				}

				url2, _ := exportServer(t, plan)
				_, err = runExport(t, url2, "--force")
				wantExit(t, err, 0)
				if got := read(t, p); got != rendered(t, host) || !strings.Contains(got, "hadron-skill id="+accNodeID) {
					t.Errorf("--force did not regenerate a full-header file:\n%s", got)
				}
			})
		}
	}

	// P09: an over-limit Codex declaration is SKIPPED and the earlier Codex
	// file is left in place; Claude is written.
	cases["P09"] = func(t *testing.T) {
		home := accHome(t)
		earlier := rendered(t, "codexSkill")
		p := filepath.Join(codexRoot(home), accName, "SKILL.md")
		put(t, p, earlier)
		url, _ := exportServer(t, func(c exportCall) hostPlan {
			if c.Host == "codexSkill" {
				return hostPlan{entries: []map[string]any{planEntry(accName, "SKIP", "", "", planReason{"over-limit", "description exceeds 1,024"})}}
			}
			return hostPlan{entries: []map[string]any{planEntry(accName, "WRITE", rendered(t, c.Host), "")}}
		})
		rep, err := runExport(t, url)
		wantExit(t, err, 0)
		if read(t, p) != earlier {
			t.Error("a skipped over-limit declaration changed the earlier file")
		}
		codex := rep.host(t, "codexSkill")
		if c := codex.classOf(accName); c != "skipped" {
			t.Errorf("codex: reported %q, want skipped", c)
		}
		// ...and the report NAMES the 1,024 limit: the server's reason travels
		// verbatim, code and message both.
		var named bool
		for _, it := range codex.Skipped {
			for _, r := range it.Reasons {
				if r.Code == "over-limit" && strings.Contains(r.Message, "1,024") && r.Origin == "server" {
					named = true
				}
			}
		}
		if !named {
			t.Errorf("the skip must name the 1,024 limit (server reason, verbatim); got %+v", codex.Skipped)
		}
	}

	// P12: a no-id file whose node renamed its skill. The MOVE is skipped:
	// the file stays at the old name and nothing is written at the new one;
	// --force writes the new file and removes the old one.
	cases["P12"] = func(t *testing.T) {
		home := accHome(t)
		const newName = "hadron-demo-renamed"
		old := filepath.Join(claudeRoot(home), accName, "SKILL.md")
		put(t, old, legacyNoID(false))
		plan := func(c exportCall) hostPlan {
			if c.Host != "claudeSkill" {
				return hostPlan{}
			}
			if c.Force != nil && *c.Force {
				return hostPlan{entries: []map[string]any{planEntry(newName, "MOVE", rendered(t, c.Host), accName)}}
			}
			return hostPlan{entries: []map[string]any{planEntry(newName, "SKIP", "", accName, planReason{"missing-provenance-id", "no node id"})}}
		}
		url, _ := exportServer(t, plan)
		rep, err := runExport(t, url)
		wantExit(t, err, 0)
		if read(t, old) != legacyNoID(false) || !absent(filepath.Join(claudeRoot(home), newName)) {
			t.Error("a skipped move touched the disk")
		}
		if c := rep.host(t, "claudeSkill").classOf(newName); c != "skipped" {
			t.Errorf("reported %q, want skipped", c)
		}

		url2, _ := exportServer(t, plan)
		rep2, err := runExport(t, url2, "--force")
		wantExit(t, err, 0)
		if !absent(filepath.Join(claudeRoot(home), accName)) {
			t.Error("--force left the old directory behind")
		}
		if got := read(t, filepath.Join(claudeRoot(home), newName, "SKILL.md")); got != rendered(t, "claudeSkill") {
			t.Errorf("--force did not write the new file:\n%s", got)
		}
		if c := rep2.host(t, "claudeSkill").classOf(newName); c != "moved" {
			t.Errorf("with --force: reported %q, want moved", c)
		}
	}

	// P13: a no-id file with no sign of a hand edit, declaration switched off:
	// REMOVED without --force, and the removal is reported.
	cases["P13"] = func(t *testing.T) {
		home := accHome(t)
		p := filepath.Join(claudeRoot(home), accName, "SKILL.md")
		put(t, p, legacyNoID(false))
		url, calls := exportServer(t, func(c exportCall) hostPlan {
			if c.Host != "claudeSkill" {
				return hostPlan{}
			}
			return hostPlan{entries: []map[string]any{planEntry(accName, "REMOVE", "", accName, planReason{"declaration-disabled", "disabled"})}}
		})
		rep, err := runExport(t, url)
		wantExit(t, err, 0)
		c := callFor(t, *calls, "claudeSkill")
		if c.Force != nil {
			t.Error("force was sent without --force")
		}
		// No hand edit, so no hand-edit evidence: the planner would refuse
		// this removal if it were claimed (P14 sends it true).
		if len(c.Files) != 1 || c.Files[0]["hasExtraFrontmatter"] == true {
			t.Errorf("a plain no-id file must not claim hand-edit evidence; sent %v", c.Files)
		}
		if !absent(p) {
			t.Error("the disabled no-id file was not removed")
		}
		if c := rep.host(t, "claudeSkill").classOf(accName); c != "removed" {
			t.Errorf("reported %q, want removed", c)
		}
	}

	// P14: a no-id file that SHOWS a hand edit (a frontmatter key the renderer
	// never writes). The evidence travels as hasExtraFrontmatter, and a
	// rewrite, a move AND a removal are each refused and kept unless --force is
	// given; with --force each one is carried out (#699 review: all three, both
	// ways).
	cases["P14"] = func(t *testing.T) {
		const newName = "hadron-demo-renamed"
		for _, op := range []struct {
			name, forced, planName, movedFrom string
			after                             func(t *testing.T, home string) // after the FORCED run
		}{
			{"rewrite", "WRITE", accName, "", func(t *testing.T, home string) {
				if got := read(t, filepath.Join(claudeRoot(home), accName, "SKILL.md")); got != rendered(t, "claudeSkill") {
					t.Errorf("--force did not rewrite:\n%s", got)
				}
			}},
			{"move", "MOVE", newName, accName, func(t *testing.T, home string) {
				if !absent(filepath.Join(claudeRoot(home), accName)) {
					t.Error("--force left the moved file's source behind")
				}
				if got := read(t, filepath.Join(claudeRoot(home), newName, "SKILL.md")); got != rendered(t, "claudeSkill") {
					t.Errorf("--force did not write the moved file:\n%s", got)
				}
			}},
			{"remove", "REMOVE", accName, accName, func(t *testing.T, home string) {
				if !absent(filepath.Join(claudeRoot(home), accName)) {
					t.Error("--force did not remove the file")
				}
			}},
		} {
			t.Run(op.name, func(t *testing.T) {
				home := accHome(t)
				p := filepath.Join(claudeRoot(home), accName, "SKILL.md")
				put(t, p, legacyNoID(true))
				plan := func(c exportCall) hostPlan {
					if c.Host != "claudeSkill" {
						return hostPlan{}
					}
					if c.Force != nil && *c.Force {
						body := ""
						if op.forced != "REMOVE" {
							body = rendered(t, c.Host)
						}
						return hostPlan{entries: []map[string]any{planEntry(op.planName, op.forced, body, op.movedFrom)}}
					}
					return hostPlan{entries: []map[string]any{planEntry(op.planName, "REFUSE", "", op.movedFrom, planReason{"locally-edited", "edited by hand"})}}
				}
				url, calls := exportServer(t, plan)
				rep, err := runExport(t, url)
				wantExit(t, err, exitcode.Conflict)
				sent := callFor(t, *calls, "claudeSkill").Files
				if len(sent) != 1 || sent[0]["hasExtraFrontmatter"] != true {
					t.Errorf("the hand-edit evidence must travel as hasExtraFrontmatter; sent %v", sent)
				}
				if read(t, p) != legacyNoID(true) || !absent(filepath.Join(claudeRoot(home), newName)) {
					t.Errorf("a refused %s touched the disk", op.name)
				}
				if c := rep.host(t, "claudeSkill").classOf(op.planName); c != "refused" {
					t.Errorf("reported %q, want refused", c)
				}

				url2, calls2 := exportServer(t, plan)
				_, err = runExport(t, url2, "--force")
				wantExit(t, err, 0)
				if f := callFor(t, *calls2, "claudeSkill").Force; f == nil || !*f {
					t.Error("--force was not sent")
				}
				op.after(t, home)
			})
		}
	}

	// P15: a disabled AND over-limit no-id file is skipped and left in place:
	// skipping never deletes.
	cases["P15"] = func(t *testing.T) {
		home := accHome(t)
		p := filepath.Join(claudeRoot(home), accName, "SKILL.md")
		put(t, p, legacyNoID(false))
		url, _ := exportServer(t, func(c exportCall) hostPlan {
			if c.Host != "claudeSkill" {
				return hostPlan{}
			}
			return hostPlan{entries: []map[string]any{planEntry(accName, "SKIP", "", "", planReason{"over-limit", "over its host's limit"})}}
		})
		rep, err := runExport(t, url)
		wantExit(t, err, 0)
		if read(t, p) != legacyNoID(false) {
			t.Error("a skip deleted or changed the file")
		}
		if c := rep.host(t, "claudeSkill").classOf(accName); c != "skipped" {
			t.Errorf("reported %q, want skipped", c)
		}
	}

	// P16: an `exports.codex` key names no host. The server lists it in EVERY
	// host's plan; the report names it ONCE, attributed to no host, and the
	// Claude file is still written.
	cases["P16"] = func(t *testing.T) {
		home := accHome(t)
		unr := map[string]any{
			"urn": accSource, "nodeId": accNodeID, "name": accName, "memory": "hrn:mem:example.com:demo",
			"keyCount": 1, "keys": []string{"codex"},
			"findings": []map[string]any{{"rule": "skill-unknown-host-key", "severity": "warning", "message": "names no host", "urn": accSource, "memory": "hrn:mem:example.com:demo"}},
		}
		url, _ := exportServer(t, func(c exportCall) hostPlan {
			hp := hostPlan{unrecognized: []map[string]any{unr}}
			if c.Host == "claudeSkill" {
				hp.entries = []map[string]any{planEntry(accName, "WRITE", rendered(t, c.Host), "")}
			}
			return hp
		})
		rep, raw, err := runExportRaw(t, url)
		wantExit(t, err, 0)
		// HOST-FREE, checked on the raw JSON: a typed decode would silently
		// drop a `host` key, so attribution to a host could never fail here.
		var doc struct {
			Unrecognized []map[string]json.RawMessage `json:"unrecognized"`
			Hosts        []map[string]json.RawMessage `json:"hosts"`
		}
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		for _, u := range doc.Unrecognized {
			if _, ok := u["host"]; ok {
				t.Errorf("an unrecognized key is attributed to a host: %s", raw)
			}
		}
		for _, h := range doc.Hosts {
			if _, ok := h["unrecognized"]; ok {
				t.Errorf("an unrecognized key is reported inside a host: %s", raw)
			}
		}
		if len(rep.Unrecognized) != 1 || rep.Unrecognized[0].NodeID != accNodeID || rep.Unrecognized[0].KeyCount != 1 ||
			len(rep.Unrecognized[0].Keys) != 1 || rep.Unrecognized[0].Keys[0] != "codex" {
			t.Errorf("want the unrecognized key `codex` named once, host-free; got %+v", rep.Unrecognized)
		}
		if got := read(t, filepath.Join(claudeRoot(home), accName, "SKILL.md")); got != rendered(t, "claudeSkill") {
			t.Error("the claude file was not written")
		}
	}

	// P17 / P18: the Codex root, or an ANCESTOR of it, is a link into Claude's
	// directory. Every Codex item FAILS and is named; nothing is read through
	// the link; every Claude item is written unchanged (exit 5).
	for _, tc := range []struct{ id, from, to string }{
		{"P17", filepath.Join(".agents", "skills"), filepath.Join(".claude", "skills")},
		{"P18", ".agents", ".claude"},
	} {
		cases[tc.id] = func(t *testing.T) {
			home := accHome(t)
			if err := os.MkdirAll(claudeRoot(home), 0o755); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(claudeRoot(home), "other-skill", "SKILL.md"), rendered(t, "claudeSkill"))
			link(t, filepath.Join(home, tc.to), filepath.Join(home, tc.from))
			url, calls := exportServer(t, writesBoth(t))
			rep, err := runExport(t, url)
			wantExit(t, err, exitcode.Conflict)
			if n := len(callFor(t, *calls, "codexSkill").Files); n != 0 {
				t.Errorf("codex was sent %d file(s) read through the link", n)
			}
			codex := rep.host(t, "codexSkill")
			if codex.Failure == nil || codex.Failure.Code != "root-is-link" {
				t.Errorf("codex host failure = %+v, want root-is-link", codex.Failure)
			}
			if c := codex.classOf(accName); c != "failed" {
				t.Errorf("codex: %s reported %q, want failed (named, not silently dropped)", accName, c)
			}
			if got := read(t, filepath.Join(claudeRoot(home), accName, "SKILL.md")); got != rendered(t, "claudeSkill") {
				t.Errorf("the claude file is not the claude rendering:\n%s", got)
			}
		}
	}

	// P20: a Hadron copy in the DEPRECATED ~/.codex/skills is never written,
	// read or submitted; Codex writes only ~/.agents/skills.
	// P21: the same name in both roots: the same, and the older copy is kept.
	for _, id := range []string{"P20", "P21"} {
		cases[id] = func(t *testing.T) {
			home := accHome(t)
			deprecated := filepath.Join(home, ".codex", "skills", accName, "SKILL.md")
			put(t, deprecated, rendered(t, "codexSkill")+"\nolder copy\n")
			if id == "P21" {
				put(t, filepath.Join(codexRoot(home), accName, "SKILL.md"), rendered(t, "codexSkill"))
			}
			url, calls := exportServer(t, writesBoth(t))
			_, err := runExport(t, url)
			wantExit(t, err, 0)
			if read(t, deprecated) != rendered(t, "codexSkill")+"\nolder copy\n" {
				t.Error("the deprecated ~/.codex/skills copy changed")
			}
			if got := read(t, filepath.Join(codexRoot(home), accName, "SKILL.md")); got != rendered(t, "codexSkill") {
				t.Errorf("codex was not written to ~/.agents/skills:\n%s", got)
			}
			wantFiles := 0
			if id == "P21" {
				wantFiles = 1
			}
			if n := len(callFor(t, *calls, "codexSkill").Files); n != wantFiles {
				t.Errorf("codex was sent %d file(s), want %d: only ~/.agents/skills is walked", n, wantFiles)
			}
		}
	}
	return cases
}

func TestSkillExportAcceptance(t *testing.T) {
	cases := acceptanceCases()
	ids := make([]string, 0, len(cases))
	for id := range cases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		t.Run(id, cases[id])
	}
}

// ---- destinations and dry-run (team chat #1332) ----

func TestSkillExportAcceptanceDestinations(t *testing.T) {
	// A foreign SKILL.md (no Hadron header) at a WRITE destination is refused
	// and kept byte for byte, even with --force.
	t.Run("foreign WRITE destination", func(t *testing.T) {
		for _, args := range [][]string{nil, {"--force"}} {
			home := accHome(t)
			p := filepath.Join(claudeRoot(home), accName, "SKILL.md")
			put(t, p, foreignSkill)
			url, calls := exportServer(t, writesBoth(t))
			rep, err := runExport(t, url, args...)
			wantExit(t, err, exitcode.Conflict)
			if n := len(callFor(t, *calls, "claudeSkill").Files); n != 0 {
				t.Errorf("%v: a foreign file was submitted as ours (%d)", args, n)
			}
			if read(t, p) != foreignSkill {
				t.Errorf("%v: a foreign skill was replaced", args)
			}
			claude := rep.host(t, "claudeSkill")
			if c := claude.classOf(accName); c != "refused" || !contains(claude.reasonCodes(accName), "foreign-skill-file") {
				t.Errorf("%v: reported %q %v, want refused foreign-skill-file", args, c, claude.reasonCodes(accName))
			}
			// A refusal on the FIRST host never stops the next one: Codex is
			// still planned, written and reported (cor:agt:030:00).
			_ = callFor(t, *calls, "codexSkill")
			if c := rep.host(t, "codexSkill").classOf(accName); c != "written" {
				t.Errorf("%v: codex reported %q after claude's refusal, want written", args, c)
			}
			if got := read(t, filepath.Join(codexRoot(home), accName, "SKILL.md")); got != rendered(t, "codexSkill") {
				t.Errorf("%v: codex was not written after claude's refusal:\n%s", args, got)
			}
		}
	})

	// ...and at a MOVE destination: the source is kept too, since the move
	// never happens.
	t.Run("foreign MOVE destination", func(t *testing.T) {
		home := accHome(t)
		const newName = "hadron-demo-renamed"
		src := filepath.Join(claudeRoot(home), accName, "SKILL.md")
		dst := filepath.Join(claudeRoot(home), newName, "SKILL.md")
		put(t, src, rendered(t, "claudeSkill"))
		put(t, dst, foreignSkill)
		url, _ := exportServer(t, func(c exportCall) hostPlan {
			if c.Host != "claudeSkill" {
				return hostPlan{}
			}
			return hostPlan{entries: []map[string]any{planEntry(newName, "MOVE", rendered(t, c.Host), accName)}}
		})
		rep, err := runExport(t, url)
		wantExit(t, err, exitcode.Conflict)
		if read(t, dst) != foreignSkill || read(t, src) != rendered(t, "claudeSkill") {
			t.Error("a move onto a foreign skill changed either end")
		}
		claude := rep.host(t, "claudeSkill")
		if c := claude.classOf(newName); c != "refused" || !contains(claude.reasonCodes(newName), "foreign-skill-file") {
			t.Errorf("reported %q %v, want refused foreign-skill-file", c, claude.reasonCodes(newName))
		}
	})

	// An UNATTRIBUTABLE SKILL.md (its frontmatter does not parse and no
	// provenance is readable) is reported, never submitted, and never replaced.
	t.Run("unattributable WRITE destination", func(t *testing.T) {
		home := accHome(t)
		broken := "---\nname: " + accName + "\ndescription: Covers: a colon that breaks YAML\n---\n\n# ?\n"
		p := filepath.Join(claudeRoot(home), accName, "SKILL.md")
		put(t, p, broken)
		url, calls := exportServer(t, writesBoth(t))
		rep, err := runExport(t, url)
		wantExit(t, err, exitcode.Conflict)
		if n := len(callFor(t, *calls, "claudeSkill").Files); n != 0 {
			t.Errorf("an unattributable file was submitted (%d)", n)
		}
		if read(t, p) != broken {
			t.Error("an unattributable file was replaced")
		}
		claude := rep.host(t, "claudeSkill")
		if c := claude.classOf(accName); c != "refused" || !contains(claude.reasonCodes(accName), "foreign-skill-file") {
			t.Errorf("reported %q %v, want refused foreign-skill-file", c, claude.reasonCodes(accName))
		}
	})

	// A SKILL.md that is itself a LINK is never written through.
	t.Run("linked SKILL.md", func(t *testing.T) {
		home := accHome(t)
		elsewhere := filepath.Join(home, "elsewhere.md")
		put(t, elsewhere, rendered(t, "claudeSkill"))
		link(t, elsewhere, filepath.Join(claudeRoot(home), accName, "SKILL.md"))
		url, _ := exportServer(t, writesBoth(t))
		rep, err := runExport(t, url)
		wantExit(t, err, exitcode.Conflict)
		if read(t, elsewhere) != rendered(t, "claudeSkill") {
			t.Error("the link's target was written through")
		}
		claude := rep.host(t, "claudeSkill")
		if !contains(claude.reasonCodes(accName), "skill-file-is-link") {
			t.Errorf("reported %q %v, want skill-file-is-link", claude.classOf(accName), claude.reasonCodes(accName))
		}
	})
}

// Dry-run parity: --dry-run reports the SAME classification as the real run,
// and changes nothing on disk.
func TestSkillExportAcceptanceDryRunParity(t *testing.T) {
	scenario := func(t *testing.T) (string, func(c exportCall) hostPlan) {
		home := accHome(t)
		put(t, filepath.Join(codexRoot(home), accName, "SKILL.md"), rendered(t, "codexSkill"))
		put(t, filepath.Join(claudeRoot(home), "foreign", "SKILL.md"), strings.Replace(foreignSkill, accName, "foreign", 1))
		return home, func(c exportCall) hostPlan {
			if c.Host == "codexSkill" {
				return hostPlan{entries: []map[string]any{planEntry(accName, "REMOVE", "", accName, planReason{"declaration-disabled", "disabled"})}}
			}
			return hostPlan{entries: []map[string]any{
				planEntry(accName, "WRITE", rendered(t, c.Host), ""),
				planEntry("foreign", "WRITE", rendered(t, c.Host), ""),
			}}
		}
	}
	classes := func(rep exportReport) map[string]string {
		out := map[string]string{}
		for _, h := range rep.Hosts {
			for _, n := range []string{accName, "foreign"} {
				out[h.Host+"/"+n] = h.classOf(n)
			}
		}
		return out
	}

	home, plan := scenario(t)
	before := tree(t, home)
	url, _ := exportServer(t, plan)
	dry, dryErr := runExport(t, url, "--dry-run")
	if !dry.DryRun {
		t.Error("the report does not say dryRun")
	}
	if after := tree(t, home); !equalTrees(before, after) {
		t.Errorf("--dry-run changed the disk:\nbefore %v\nafter  %v", before, after)
	}

	_, plan2 := scenario(t)
	url2, _ := exportServer(t, plan2)
	real, realErr := runExport(t, url2)
	if exitCodeFor(dryErr) != exitCodeFor(realErr) {
		t.Errorf("exit codes differ: dry-run %d, real %d", exitCodeFor(dryErr), exitCodeFor(realErr))
	}
	if d, r := classes(dry), classes(real); !equalTrees(d, r) {
		t.Errorf("dry-run classified differently from the real run:\ndry  %v\nreal %v", d, r)
	}
}

// tree snapshots every regular file and link under dir.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			out[rel] = "-> " + target
		case info.IsDir():
			out[rel+"/"] = ""
		default:
			b, _ := os.ReadFile(p)
			out[rel] = string(b)
		}
		return nil
	})
	return out
}

func equalTrees(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// The matrix's `writer` section and acceptanceCases must agree in BOTH
// directions: a writer case with no subtest would read as covered while
// nothing ran, and a subtest with no writer case would assert something no
// contract states. A case executed in ANOTHER package (P07, which needs a host
// the command cannot present) must name a test that package really declares.
// A pending case must not have a subtest here either — it would then be both
// "owed" and "passing".
func TestSkillExportAcceptanceCoversTheMatrix(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "skilldoc", "testdata", "crosshost-acceptance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Writer []struct {
			ID   string `json:"id"`
			Test string `json:"test"`
		} `json:"writer"`
		Pending []struct {
			ID string `json:"id"`
		} `json:"pending"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	const here = "internal/cmd TestSkillExportAcceptance/"
	cases := acceptanceCases()
	executedHere := map[string]bool{}
	for _, w := range m.Writer {
		pkg, name, ok := strings.Cut(w.Test, " ")
		switch {
		case !ok || name == "":
			t.Errorf("%s names test %q; want \"<package dir> <TestName>\"", w.ID, w.Test)
		case pkg == "internal/cmd":
			executedHere[w.ID] = true
			if w.Test != here+w.ID {
				t.Errorf("%s names test %q; want %q", w.ID, w.Test, here+w.ID)
			} else if _, ok := cases[w.ID]; !ok {
				t.Errorf("matrix writer case %s has no subtest here: it would read as covered while nothing ran", w.ID)
			}
		default:
			if !declaresTest(t, filepath.Join("..", "..", pkg), name) {
				t.Errorf("%s names %s in %s, which declares no such test", w.ID, name, pkg)
			}
		}
	}
	for id := range cases {
		if !executedHere[id] {
			t.Errorf("subtest %s has no writer case naming it in the matrix: it asserts something no contract states", id)
		}
	}
	for _, p := range m.Pending {
		if _, ok := cases[p.ID]; ok {
			t.Errorf("%s is pending in the matrix but has a subtest: move it to writer", p.ID)
		}
	}
}

// declaresTest reports whether a _test.go file in dir declares func name.
func declaresTest(t *testing.T, dir, name string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "\nfunc "+name+"(t *testing.T) {") {
			return true
		}
	}
	return false
}
