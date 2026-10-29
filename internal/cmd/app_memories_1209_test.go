package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1209 (companion of hadron-server#2190): `hadron app memories` reads
// appMemoryScope. The fixture carries the three rows the issue names: an
// AGENT_SYSTEM row, an INHERITED_BASE-only row whose viewerCanWrite is true
// (advisory: NOT a write target, per the SDL rule on
// AppScopedMemory.viewerCanWrite) and a TEAM_SPACE row with a null agent.
const appMemoryScopeResp = `{"data":{"appMemoryScope":{"total":3,"items":[
	{"memoryId":"m1","memoryUrn":"hrn:mem:acme.com:iris-system","name":"Iris system","class":"system",
	 "sources":[{"kind":"AGENT_SYSTEM","agentId":"agt-iris"}],"viewerCanWrite":true},
	{"memoryId":"m2","memoryUrn":"hrn:mem:acme.com:base-system","name":"Base system","class":"system",
	 "sources":[{"kind":"INHERITED_BASE","agentId":"agt-base"}],"viewerCanWrite":true},
	{"memoryId":"m3","memoryUrn":"hrn:mem:acme.com:eng-team","name":"Eng team space","class":"app",
	 "sources":[{"kind":"TEAM_SPACE","agentId":null}],"viewerCanWrite":true}]}}}`

// appMemoriesFake answers the scope read, then the per-agent name lookups in
// first-appearance order: agt-iris resolves, agt-base is not readable (null).
func appMemoriesFake(t *testing.T, scope string) (string, map[string]json.RawMessage, *[]string) {
	t.Helper()
	lookups := 0
	order := []string{}
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		order = append(order, op)
		switch op {
		case "AppMemoryScope":
			return scope
		case "AppScopeAgentName":
			lookups++
			if lookups == 1 {
				return `{"data":{"agent":{"id":"agt-iris","name":"Iris"}}}`
			}
			return `{"data":{"agent":null}}`
		}
		return ""
	})
	return gql.URL, captured, &order
}

func TestAppMemories1209TextNamesSourcesAndAdvisoryWrite(t *testing.T) {
	url, captured, _ := appMemoriesFake(t, appMemoryScopeResp)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "memories", "hrn:app:acme.com:eng-team", "--server", url})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]json.RawMessage
	if err := json.Unmarshal(captured["AppMemoryScope"], &vars); err != nil {
		t.Fatalf("captured vars: %v", err)
	}
	if string(vars["appRef"]) != `"hrn:app:acme.com:eng-team"` {
		t.Errorf("appRef = %s", vars["appRef"])
	}
	if _, sent := vars["offset"]; sent {
		t.Errorf("offset 0 is omitted, not sent: %s", captured["AppMemoryScope"])
	}
	if string(vars["limit"]) != "20" {
		t.Errorf("limit is always sent (default 20): %s", captured["AppMemoryScope"])
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("want header + 3 rows:\n%s", out.String())
	}
	if !strings.Contains(lines[0], "ADVISORY") {
		t.Errorf("the write column must be labelled advisory: %q", lines[0])
	}
	row := func(urn string) string {
		for _, l := range lines[1:] {
			if strings.Contains(l, urn) {
				return l
			}
		}
		t.Fatalf("no row for %s:\n%s", urn, out.String())
		return ""
	}
	sys := row("hrn:mem:acme.com:iris-system")
	if !strings.Contains(sys, "AGENT_SYSTEM (Iris)") || !strings.HasSuffix(strings.TrimSpace(sys), "yes") {
		t.Errorf("agent-system row: %q", sys)
	}
	base := row("hrn:mem:acme.com:base-system")
	if !strings.Contains(base, "INHERITED_BASE") || strings.Contains(base, "INHERITED_BASE (") {
		t.Errorf("an unresolved agent shows the kind alone: %q", base)
	}
	if !strings.HasSuffix(strings.TrimSpace(base), "inherited") {
		t.Errorf("an inherited-only row is not a write target even with viewerCanWrite true: %q", base)
	}
	team := row("hrn:mem:acme.com:eng-team")
	if !strings.Contains(team, "TEAM_SPACE") || !strings.HasSuffix(strings.TrimSpace(team), "yes") {
		t.Errorf("team-space row: %q", team)
	}
}

func TestAppMemories1209JSONIsThePagePlusAdditiveFields(t *testing.T) {
	url, _, order := appMemoriesFake(t, appMemoryScopeResp)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "memories", "hrn:app:acme.com:eng-team", "--json", "--server", url})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var page struct {
		Total int `json:"total"`
		Items []struct {
			MemoryID       string `json:"memoryId"`
			MemoryURN      string `json:"memoryUrn"`
			Name           string `json:"name"`
			Class          string `json:"class"`
			ViewerCanWrite bool   `json:"viewerCanWrite"`
			WriteTarget    *bool  `json:"writeTarget"`
			Sources        []struct {
				Kind      string  `json:"kind"`
				AgentID   *string `json:"agentId"`
				AgentName *string `json:"agentName"`
			} `json:"sources"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out.String()), &page); err != nil {
		t.Fatalf("--json: %v (%s)", err, out.String())
	}
	if page.Total != 3 || len(page.Items) != 3 {
		t.Fatalf("page: %s", out.String())
	}
	base := page.Items[1]
	if !base.ViewerCanWrite || base.WriteTarget == nil || *base.WriteTarget {
		t.Errorf("inherited-only row: viewerCanWrite passes through, writeTarget false: %s", out.String())
	}
	if base.Sources[0].AgentID == nil || *base.Sources[0].AgentID != "agt-base" || base.Sources[0].AgentName != nil {
		t.Errorf("an unresolved agent keeps its id and a null name: %s", out.String())
	}
	if n := page.Items[0].Sources[0].AgentName; n == nil || *n != "Iris" {
		t.Errorf("a resolved agent carries its name: %s", out.String())
	}
	if team := page.Items[2]; team.Sources[0].AgentID != nil || team.WriteTarget == nil || !*team.WriteTarget {
		t.Errorf("team-space row: null agent, a write target: %s", out.String())
	}
	if !strings.Contains(out.String(), `"agentId": null`) {
		t.Errorf("a TEAM_SPACE source renders agentId null, not omitted: %s", out.String())
	}
	lookups := 0
	for _, op := range *order {
		if op == "AppScopeAgentName" {
			lookups++
		}
	}
	if lookups != 2 {
		t.Errorf("one lookup per distinct agent, none for TEAM_SPACE: %d (%v)", lookups, *order)
	}
}

func TestAppMemories1209PagingReachesVariablesAndContinues(t *testing.T) {
	full := `{"data":{"appMemoryScope":{"total":5,"items":[
		{"memoryId":"m3","memoryUrn":"hrn:mem:acme.com:eng-team","name":"Eng","class":"app",
		 "sources":[{"kind":"TEAM_SPACE","agentId":null}],"viewerCanWrite":false},
		{"memoryId":"m4","memoryUrn":"hrn:mem:acme.com:notes","name":"Notes","class":"app",
		 "sources":[{"kind":"TEAM_SPACE","agentId":null}],"viewerCanWrite":false}]}}}`
	url, captured, _ := appMemoriesFake(t, full)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "memories", "hrn:app:acme.com:eng-team", "--limit", "2", "--offset", "2", "--server", url})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["AppMemoryScope"], &vars)
	if vars["limit"] != float64(2) || vars["offset"] != float64(2) {
		t.Errorf("paging must reach the variables: %v", vars)
	}
	stderr := f.IOStreams.ErrOut.(*strings.Builder).String()
	if !strings.Contains(stderr, "--offset 4") {
		t.Errorf("a page short of total prints the continuation line, got stderr %q (stdout %q)", stderr, out.String())
	}
	if strings.Contains(out.String(), "--offset") {
		t.Errorf("the continuation line belongs on stderr: %q", out.String())
	}
}

func TestAppMemories1209LimitBoundsAndMissingApp(t *testing.T) {
	for _, args := range [][]string{
		{"app", "memories", "hrn:app:acme.com:eng-team", "--limit", "0"},
		{"app", "memories", "hrn:app:acme.com:eng-team", "--limit", "201"},
		{"app", "memories", "hrn:app:acme.com:eng-team", "--offset", "-1"},
		{"app", "memories"},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(args, "--server", "http://127.0.0.1:1"))
		if code := exitCodeFor(root.Execute()); code != exitcode.Usage {
			t.Errorf("%v: exit %d, want Usage", args, code)
		}
	}
}

// Refusals are the server's appNodes error, surfaced through the shared map.
func TestAppMemories1209RefusalSurfaces(t *testing.T) {
	url, _, _ := appMemoriesFake(t, `{"errors":[{"message":"You do not have access to this App","extensions":{"code":"FORBIDDEN"}}],"data":null}`)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "memories", "hrn:app:acme.com:eng-team", "--server", url})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Forbidden {
		t.Errorf("exit %d, want Forbidden; err %v", code, err)
	}
	if err == nil || !strings.Contains(err.Error(), "You do not have access to this App") {
		t.Errorf("the server's message surfaces unchanged: %v", err)
	}
}

// An empty scope (also the answer for an App in a soft-deleted organization)
// renders items as [], not null.
func TestAppMemories1209EmptyPageRendersEmptyArray(t *testing.T) {
	url, _, _ := appMemoriesFake(t, `{"data":{"appMemoryScope":{"total":0,"items":[]}}}`)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "memories", "hrn:app:acme.com:eng-team", "--json", "--server", url})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), `"items": []`) || strings.Contains(out.String(), "null") {
		t.Errorf("empty page: %s", out.String())
	}
}

// A null or missing page, a null row, a null source or a row without sources
// is a broken answer (all are non-null / non-empty in the SDL): refused, never
// printed as an empty scope. The genuine empty page above is the control.
func TestAppMemories1209MalformedPageIsRefusedNotEmpty(t *testing.T) {
	row := func(sources string) string {
		return `{"data":{"appMemoryScope":{"total":1,"items":[{"memoryId":"m1","memoryUrn":"hrn:mem:acme.com:x","name":"X","class":"app","sources":` + sources + `,"viewerCanWrite":true}]}}}`
	}
	for name, resp := range map[string]string{
		"null page":    `{"data":{"appMemoryScope":null}}`,
		"missing page": `{"data":{}}`,
		"null row":     `{"data":{"appMemoryScope":{"total":1,"items":[null]}}}`,
		"null source":  row(`[null]`),
		"no sources":   row(`[]`),
	} {
		t.Run(name, func(t *testing.T) {
			url, _, _ := appMemoriesFake(t, resp)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"app", "memories", "hrn:app:acme.com:eng-team", "--json", "--server", url})
			err := root.Execute()
			if code := exitCodeFor(err); code != exitcode.Error {
				t.Errorf("exit %d, want Error; err %v", code, err)
			}
			if strings.Contains(out.String(), `"items"`) {
				t.Errorf("a broken answer must not print a page: %s", out.String())
			}
		})
	}
}
