package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// `hadron skill plugin` (#653) run end to end against a fake server: which
// requests it sends, what it writes, and how it exits. The bucketing and
// filesystem rules are pinned in internal/cmd/skill/plugin_test.go.

type pluginCall struct {
	op   string
	vars map[string]any
}

// pluginServer answers every skillPlan by host, and ScopeExplain with scope,
// recording EVERY call in order — captureGraphQL keeps only the last call per
// operation, and this command sends SkillExportPlan once per host.
func pluginServer(t *testing.T, plans map[string]string, scope string) (*httptest.Server, func() []pluginCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []pluginCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, pluginCall{body.OperationName, body.Variables})
		mu.Unlock()
		var resp string
		switch body.OperationName {
		case "SkillExportPlan":
			host, _ := body.Variables["input"].(map[string]any)["host"].(string)
			p, ok := plans[host]
			if !ok {
				t.Errorf("no plan for host %q", host)
			}
			resp = `{"data":{"skillPlan":` + p + `}}`
		case "ScopeExplain":
			resp = `{"data":{"scopeExplain":` + scope + `}}`
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []pluginCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]pluginCall(nil), calls...)
	}
}

func planEntryJSON(name, action, body string) string {
	b := "null"
	if body != "" {
		bb, _ := json.Marshal(body)
		b = string(bb)
	}
	return `{"urn":"hrn:node:example.com:demo:tasks:` + name + `","nodeId":"id-` + name + `","name":"` + name +
		`","class":"never-exported","parseFailure":null,"movedFrom":null,"renderedBody":` + b +
		`,"exportPlan":{"action":"` + action + `","preservesExistingFile":false,"reasons":[{"code":"c","message":"m"}]},"findings":[]}`
}

func planJSON(entries ...string) string {
	return `{"scanned":` + itoa(len(entries)) + `,"judged":` + itoa(len(entries)) + `,"entries":[` + strings.Join(entries, ",") + `],"orphans":[],"unrecognized":[]}`
}

type pluginReport struct {
	DryRun bool   `json:"dryRun"`
	Name   string `json:"name"`
	Out    string `json:"out"`
	Scope  *struct {
		Name         string `json:"name"`
		MemoryCount  int    `json:"memoryCount"`
		DroppedCount int    `json:"droppedCount"`
	} `json:"scope"`
	Hosts []struct {
		Host       string                  `json:"host"`
		Artifact   *string                 `json:"artifact"`
		Version    *string                 `json:"version"`
		Failure    *struct{ Code string }  `json:"failure"`
		Included   []struct{ Name string } `json:"included"`
		Refused    []struct{ Name string } `json:"refused"`
		NotForHost []struct{ Name string } `json:"notForHost"`
	} `json:"hosts"`
}

// pluginHome is a resolved, disposable HOME: the host-root check reads it.
func pluginHome(t *testing.T) string {
	t.Helper()
	h, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", h)
	return h
}

func runPlugin(t *testing.T, url string, args ...string) (pluginReport, string, error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"skill", "plugin", "--json", "--server", url}, args...))
	err := root.Execute()
	var rep pluginReport
	if out.Len() > 0 {
		if uerr := json.Unmarshal([]byte(out.String()), &rep); uerr != nil {
			t.Fatalf("plugin --json printed no report (err=%v): %v\n%s", err, uerr, out.String())
		}
	}
	return rep, out.String(), err
}

func TestSkillPluginBuildsEveryHostFromNoFilesPlans(t *testing.T) {
	h := pluginHome(t)
	out := filepath.Join(h, "dist")
	srv, calls := pluginServer(t, map[string]string{
		"claudeSkill": planJSON(planEntryJSON("alpha", "WRITE", "# alpha"), planEntryJSON("claude-only", "WRITE", "# c"), planEntryJSON("bad", "REFUSE", "")),
		"codexSkill":  planJSON(planEntryJSON("alpha", "WRITE", "# alpha codex")),
	}, "")

	rep, raw, err := runPlugin(t, srv.URL, "--out", out, "--zip")
	wantExit(t, err, 5) // one REFUSE → exit 5 after the full report

	var hosts []string
	for _, c := range calls() {
		if c.op != "SkillExportPlan" {
			t.Errorf("unexpected call %s", c.op)
			continue
		}
		in := c.vars["input"].(map[string]any)
		hosts = append(hosts, in["host"].(string))
		if in["intent"] != "EXPORT" {
			t.Errorf("intent = %v, want EXPORT", in["intent"])
		}
		for _, k := range []string{"files", "memories", "force"} {
			if _, ok := in[k]; ok {
				t.Errorf("%s was sent (%v); a bundle submits no files and, unscoped, no memories", k, in[k])
			}
		}
	}
	if strings.Join(hosts, ",") != "claudeSkill,codexSkill" {
		t.Errorf("plans fetched for %v, want one per host", hosts)
	}

	// Decoding cannot tell `null` from `[]` (review:stable-json-dto), so the
	// array fields are checked on the raw output, including an empty host's.
	for _, k := range []string{"hosts", "included", "skipped", "refused", "failed", "notForHost", "findings", "reasons", "kept", "unrecognized"} {
		if strings.Contains(raw, `"`+k+`": null`) {
			t.Errorf("%s rendered as null, want []", k)
		}
	}
	for _, k := range []string{"skipped", "failed", "findings", "unrecognized"} {
		if !strings.Contains(raw, `"`+k+`": []`) {
			t.Errorf("empty %s not rendered as []", k)
		}
	}
	if !strings.Contains(raw, `"scope": null`) {
		t.Errorf("an unscoped run reports scope null:\n%s", raw)
	}
	if len(rep.Hosts) != 2 || rep.Hosts[0].Version == nil || rep.Hosts[1].Version != nil {
		t.Fatalf("hosts = %+v", rep.Hosts)
	}
	// Declared for Claude only, so absent from the Codex plan — whatever
	// Claude's plan did with them.
	if got := rep.Hosts[1].NotForHost; len(got) != 2 || got[0].Name != "claude-only" || got[1].Name != "bad" {
		t.Errorf("codex notForHost = %+v, want claude-only and bad", got)
	}
	for p, want := range map[string]string{
		"hadron/skills/alpha/SKILL.md":       "# alpha",
		"hadron/skills/claude-only/SKILL.md": "# c",
		"hadron-codex/alpha/SKILL.md":        "# alpha codex",
	} {
		if b, err := os.ReadFile(filepath.Join(out, p)); err != nil || string(b) != want {
			t.Errorf("%s = %q (%v), want %q", p, b, err, want)
		}
	}
	for _, p := range []string{"hadron/.claude-plugin/plugin.json", "hadron/.claude-plugin/marketplace.json", "hadron.zip", "hadron-codex.zip"} {
		if _, err := os.Stat(filepath.Join(out, p)); err != nil {
			t.Errorf("%s missing: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "hadron", "skills", "bad")); err == nil {
		t.Error("a REFUSED skill was bundled")
	}
}

func TestSkillPluginDryRunWritesNothingAndReportsTheSameRefusal(t *testing.T) {
	h := pluginHome(t)
	out := filepath.Join(h, "dist")
	plans := map[string]string{"claudeSkill": planJSON(planEntryJSON("alpha", "WRITE", "# a")), "codexSkill": planJSON()}

	srv, _ := pluginServer(t, plans, "")
	rep, _, err := runPlugin(t, srv.URL, "--out", out, "--dry-run")
	wantExit(t, err, 0)
	if !rep.DryRun || len(rep.Hosts[0].Included) != 1 {
		t.Errorf("dry run report = %+v", rep)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a dry run created --out")
	}

	// A foreign directory at the artifact path: the dry run must say what the
	// real run would (#694's parity rule), and neither may touch it.
	if err := os.MkdirAll(filepath.Join(out, "hadron"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "hadron", "mine.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--out", out, "--dry-run"}, {"--out", out}} {
		srv, _ := pluginServer(t, plans, "")
		rep, _, err := runPlugin(t, srv.URL, args...)
		wantExit(t, err, 5)
		if f := rep.Hosts[0].Failure; f == nil || f.Code != "artifact-not-ours" {
			t.Errorf("%v: claude failure = %+v, want artifact-not-ours", args, f)
		}
		if b, _ := os.ReadFile(filepath.Join(out, "hadron", "mine.txt")); string(b) != "keep" {
			t.Errorf("%v: the foreign file was touched", args)
		}
	}
}

func TestSkillPluginEmptyScopeNeverPlans(t *testing.T) {
	h := pluginHome(t)
	scope := `{"resolvedVia":"APP","droppedCount":2,"scope":{"id":"0123456789abcdef0123456789abcdef","name":"research"},"memories":[],"winner":null,"shadowed":[]}`
	srv, calls := pluginServer(t, nil, scope)

	rep, _, err := runPlugin(t, srv.URL, "--out", filepath.Join(h, "dist"), "--scope", "0123456789abcdef0123456789abcdef")
	wantExit(t, err, 2)
	for _, c := range calls() {
		if c.op == "SkillExportPlan" {
			t.Fatal("an empty scope called skillPlan: an omitted memories list means EVERY memory (@codex P1 on #700)")
		}
	}
	if rep.Scope == nil || rep.Scope.MemoryCount != 0 || rep.Scope.DroppedCount != 2 {
		t.Errorf("scope = %+v, want the empty scope and its droppedCount reported", rep.Scope)
	}
	if _, err := os.Stat(filepath.Join(h, "dist")); err == nil {
		t.Error("an empty scope wrote an artifact")
	}
}

func TestSkillPluginScopeNarrowsTheMemories(t *testing.T) {
	h := pluginHome(t)
	scope := `{"resolvedVia":"APP","droppedCount":1,"scope":{"id":"0123456789abcdef0123456789abcdef","name":"research"},"memories":[{"id":"m1","urn":"u1","name":"one"},{"id":"m2","urn":"u2","name":"two"}],"winner":null,"shadowed":[]}`
	srv, calls := pluginServer(t, map[string]string{"claudeSkill": planJSON(), "codexSkill": planJSON()}, scope)

	// Padded, as a quoted shell value arrives: still an id, sent trimmed.
	rep, _, err := runPlugin(t, srv.URL, "--out", filepath.Join(h, "dist"), "--scope", " 0123456789abcdef0123456789abcdef ")
	wantExit(t, err, 0)
	for _, c := range calls() {
		if c.op == "ScopeExplain" && c.vars["scopeRef"] != "0123456789abcdef0123456789abcdef" {
			t.Errorf("scopeRef = %q, want it trimmed", c.vars["scopeRef"])
		}
	}
	n := 0
	for _, c := range calls() {
		if c.op != "SkillExportPlan" {
			continue
		}
		n++
		got, _ := json.Marshal(c.vars["input"].(map[string]any)["memories"])
		if string(got) != `["m1","m2"]` {
			t.Errorf("memories = %s, want the scope's readable memories in order", got)
		}
	}
	if n != 2 {
		t.Errorf("%d plans, want 2", n)
	}
	if rep.Scope == nil || rep.Scope.MemoryCount != 2 || rep.Scope.DroppedCount != 1 {
		t.Errorf("scope = %+v", rep.Scope)
	}
}

func TestSkillPluginRefusesAHostSkillsRootBeforeAnyRequest(t *testing.T) {
	h := pluginHome(t)
	srv, calls := pluginServer(t, nil, "")
	// An artifact path that is a link into a skills root.
	if err := os.MkdirAll(filepath.Join(h, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h, "linked"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(h, ".agents", "skills"), filepath.Join(h, "linked", "hadron")); err != nil {
		t.Fatal(err)
	}
	// An existing artifact that holds a project skills root is "above" one.
	if err := os.MkdirAll(filepath.Join(h, "dist", "hadron", "proj", ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--out", filepath.Join(h, ".claude"), "--name", "skills"},
		{"--out", filepath.Join(h, "proj", ".agents", "skills")},
		{"--out", filepath.Join(h, "dist")},
		{"--out", filepath.Join(h, "linked")},
	} {
		_, _, err := runPlugin(t, srv.URL, args...)
		wantExit(t, err, 2)
	}
	if n := len(calls()); n != 0 {
		t.Errorf("%d requests sent; a refused destination must stop before planning", n)
	}
	if _, err := os.Stat(filepath.Join(h, ".claude")); err == nil {
		t.Error("a refused run created a directory")
	}
	if _, err := os.Stat(filepath.Join(h, "dist", "hadron", "proj", ".claude", "skills")); err != nil {
		t.Error("the project skills root inside the artifact was touched")
	}
}

func TestSkillPluginFlagErrors(t *testing.T) {
	h := pluginHome(t)
	srv, calls := pluginServer(t, nil, "")
	for _, args := range [][]string{
		{},
		{"--out", filepath.Join(h, "d"), "--name", "Bad_Name"},
		{"--out", filepath.Join(h, "d"), "--name", strings.Repeat("a", 65)},
		{"--out", ""},
		{"--out", " "},
		{"--out", filepath.Join(h, "d"), "--scope", ""},
		{"--out", filepath.Join(h, "d"), "--name", ""},
		{"--out", filepath.Join(h, "d"), "--name", "con"},
		{"--out", filepath.Join(h, "d"), "--name", "com1"},
	} {
		_, _, err := runPlugin(t, srv.URL, args...)
		wantExit(t, err, 2)
	}
	if n := len(calls()); n != 0 {
		t.Errorf("%d requests sent for a usage error", n)
	}
}

func TestSkillPluginNamesWhereAScopeNameResolved(t *testing.T) {
	h := pluginHome(t)
	scope := `{"resolvedVia":"APP","droppedCount":0,"scope":{"id":"0123456789abcdef0123456789abcdef","name":"research"},"memories":[{"id":"m1","urn":"u1","name":"one"}],"winner":null,"shadowed":[]}`
	srv, calls := pluginServer(t, map[string]string{"claudeSkill": planJSON(), "codexSkill": planJSON()}, scope)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"skill", "plugin", "--server", srv.URL, "--app", "hrn:app:example.com:team", "--out", filepath.Join(h, "d"), "--scope", "research", "--dry-run"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Scope research (in App hrn:app:example.com:team, from --app)") {
		t.Errorf("the render does not say which App the scope name resolved in:\n%s", out.String())
	}
	for _, c := range calls() {
		if c.op == "ScopeExplain" && c.vars["appRef"] != "hrn:app:example.com:team" {
			t.Errorf("ScopeExplain appRef = %v", c.vars["appRef"])
		}
	}
}

// An auth failure on the SECOND host still stops the run before anything is
// written: no plan is acted on until both are in.
func TestSkillPluginSecondHostAuthFailureWritesNothing(t *testing.T) {
	h := pluginHome(t)
	out := filepath.Join(h, "dist")
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		call := n
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			_, _ = w.Write([]byte(`{"data":{"skillPlan":` + planJSON(planEntryJSON("alpha", "WRITE", "# a")) + `}}`))
			return
		}
		_, _ = w.Write([]byte(`{"errors":[{"message":"token expired","extensions":{"code":"UNAUTHENTICATED"}}]}`))
	}))
	t.Cleanup(srv.Close)
	_, _, err := runPlugin(t, srv.URL, "--out", out)
	wantExit(t, err, exitcode.AuthRequired)
	if _, err := os.Stat(out); err == nil {
		t.Error("a run that could not plan every host wrote a partial bundle")
	}
}
