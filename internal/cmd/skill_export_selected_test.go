package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/skilldoc"
)

type selectedExportCall struct {
	Host  string                     `json:"host"`
	Nodes []string                   `json:"nodes"`
	Files []map[string]any           `json:"files"`
	Force *bool                      `json:"force"`
	Raw   map[string]json.RawMessage `json:"-"`
}

func selectedServer(t *testing.T, plan func(selectedExportCall) map[string]any) (string, *[]selectedExportCall) {
	t.Helper()
	calls := &[]selectedExportCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Input json.RawMessage `json:"input"`
			} `json:"variables"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if req.OperationName != "SelectedSkillFilePlan" {
			t.Errorf("operation = %q, want SelectedSkillFilePlan", req.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected operation"}]}`))
			return
		}
		var c selectedExportCall
		_ = json.Unmarshal(req.Variables.Input, &c)
		_ = json.Unmarshal(req.Variables.Input, &c.Raw)
		*calls = append(*calls, c)
		out, _ := json.Marshal(map[string]any{"data": map[string]any{"selectedSkillFilePlan": plan(c)}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, calls
}

func selectedPlanJSON(entries []map[string]any, results []map[string]any) map[string]any {
	return map[string]any{
		"scanned": 1, "judged": len(entries), "entries": entries,
		"orphans": []any{}, "orphanAssessmentSkipped": true, "selectionResults": results,
	}
}

func TestSkillExportSelectedWritesOnlyNamedTask(t *testing.T) {
	home := accHome(t)
	otherID := "01a0000000000000000000000000d002"
	otherName := "hadron-other"
	otherURN := "hrn:node:example.com:demo:tasks:other"
	otherBody, err := skilldoc.Render(otherID, otherName, otherURN, "Use when testing another skill.", "# Other\n")
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1_600_000_000, 0)
	for _, root := range []string{claudeRoot(home), codexRoot(home)} {
		p := filepath.Join(root, otherName, "SKILL.md")
		put(t, p, otherBody)
		if err := os.Chtimes(p, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	url, calls := selectedServer(t, func(c selectedExportCall) map[string]any {
		return selectedPlanJSON([]map[string]any{planEntry(accName, "WRITE", rendered(t, c.Host), "")}, nil)
	})
	rep, raw, err := runExportRaw(t, url, "--node", accSource, "--node", accSource)
	wantExit(t, err, 0)
	if !reflect.DeepEqual(rep.SelectedNodes, []string{accSource}) || !rep.OrphanAssessmentSkipped {
		t.Fatalf("selected scope missing from report: %+v", rep)
	}
	if !strings.Contains(raw, `"selectedNodes": [`) || strings.Contains(raw, `"selectedNodes": null`) {
		t.Fatalf("selected node array is not stable JSON: %s", raw)
	}
	if len(*calls) != 2 {
		t.Fatalf("requests = %d, want two hosts", len(*calls))
	}
	for _, c := range *calls {
		if !reflect.DeepEqual(c.Nodes, []string{accSource}) {
			t.Errorf("nodes = %v", c.Nodes)
		}
		if len(c.Files) != 1 || c.Files[0]["nodeId"] != otherID {
			t.Errorf("%s did not submit all local file facts: %+v", c.Host, c.Files)
		}
	}
	for _, host := range []struct{ key, root string }{{"claudeSkill", claudeRoot(home)}, {"codexSkill", codexRoot(home)}} {
		if got := rep.host(t, host.key).classOf(accName); got != "written" {
			t.Errorf("%s class = %s", host.key, got)
		}
		if got := read(t, filepath.Join(host.root, accName, "SKILL.md")); got != rendered(t, host.key) {
			t.Errorf("%s rendered body changed", host.key)
		}
		p := filepath.Join(host.root, otherName, "SKILL.md")
		if got := read(t, p); got != otherBody {
			t.Errorf("%s unselected bytes changed", host.key)
		}
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(stamp) {
			t.Errorf("%s unselected mtime changed: %s", host.key, info.ModTime())
		}
	}
}

func TestSkillExportSelectedUnavailableAndHostSkips(t *testing.T) {
	home := accHome(t)
	missing := "01a0000000000000000000000000d099"
	url, calls := selectedServer(t, func(c selectedExportCall) map[string]any {
		return selectedPlanJSON(nil, []map[string]any{
			{"ref": missing, "nodeId": nil, "action": "FAIL", "reason": map[string]any{"code": "node-unavailable", "message": "node unavailable"}},
			{"ref": accSource, "nodeId": accNodeID, "action": "SKIP", "reason": map[string]any{"code": "host-not-declared", "message": "host not declared"}},
		})
	})
	rep, err := runExport(t, url, "--node", missing, "--node", accSource)
	wantExit(t, err, 5)
	if len(*calls) != 2 || len(rep.Selections) != 3 {
		t.Fatalf("calls=%d selections=%+v", len(*calls), rep.Selections)
	}
	for _, c := range *calls {
		if _, ok := c.Raw["force"]; ok {
			t.Errorf("unset force was sent to %s: %s", c.Host, c.Raw["force"])
		}
		if _, ok := c.Raw["files"]; ok {
			t.Errorf("empty file facts were sent to %s: %s", c.Host, c.Raw["files"])
		}
	}
	if rep.Selections[0].Ref != missing || rep.Selections[0].Host != "" || rep.Selections[0].Reason.Code != "node-unavailable" {
		t.Errorf("unavailable result leaked or changed: %+v", rep.Selections[0])
	}
	if rep.Selections[1].Host != "claudeSkill" || rep.Selections[2].Host != "codexSkill" {
		t.Errorf("host skips were not distinct: %+v", rep.Selections)
	}
	for _, root := range []string{claudeRoot(home), codexRoot(home)} {
		if !absent(root) {
			t.Errorf("created %s for result-only plan", root)
		}
	}
}

func TestSkillExportSelectedAcceptsLegacyFullyQualifiedURN(t *testing.T) {
	home := accHome(t)
	legacy := "hrn:node:example.com::demo::tasks:demo"
	url, calls := selectedServer(t, func(c selectedExportCall) map[string]any {
		return selectedPlanJSON([]map[string]any{planEntry(accName, "WRITE", rendered(t, c.Host), "")}, nil)
	})
	_, err := runExport(t, url, "--node", legacy)
	wantExit(t, err, 0)
	if len(*calls) != 2 || !reflect.DeepEqual((*calls)[0].Nodes, []string{legacy}) {
		t.Fatalf("legacy request changed: %+v", *calls)
	}
	if absent(filepath.Join(claudeRoot(home), accName, "SKILL.md")) || absent(filepath.Join(codexRoot(home), accName, "SKILL.md")) {
		t.Fatal("legacy selector did not match server's v2 entry URN")
	}
}

func TestSkillExportSelectedValidatesBeforeIO(t *testing.T) {
	home := accHome(t)
	url, calls := selectedServer(t, func(c selectedExportCall) map[string]any { return selectedPlanJSON(nil, nil) })
	for _, args := range [][]string{
		{"--node", ""}, {"--node", "tasks:bare"}, {"--node", "hrn:mem:example.com:demo"},
		{"--node", accSource + "#data"},
		{"--node", accSource, "--prune"},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"skill", "export", "--server", url}, args...))
		wantExit(t, root.Execute(), int(exitcode.Usage))
	}
	if len(*calls) != 0 {
		t.Fatalf("invalid selectors made %d requests", len(*calls))
	}
	if !absent(claudeRoot(home)) || !absent(codexRoot(home)) {
		t.Fatal("invalid selectors touched skill roots")
	}
}

func TestSkillExportSelectedRejectsUnsafeScopeMarker(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"marker": func(p map[string]any) { p["orphanAssessmentSkipped"] = false },
		"orphan": func(p map[string]any) { p["orphans"] = []map[string]any{{"dirName": "other"}} },
		"unselected entry": func(p map[string]any) {
			e := p["entries"].([]map[string]any)[0]
			e["nodeId"] = "01a0000000000000000000000000d002"
			e["urn"] = "hrn:node:example.com:demo:tasks:other"
		},
	} {
		t.Run(name, func(t *testing.T) {
			home := accHome(t)
			url, _ := selectedServer(t, func(c selectedExportCall) map[string]any {
				p := selectedPlanJSON([]map[string]any{planEntry(accName, "WRITE", rendered(t, c.Host), "")}, nil)
				mutate(p)
				return p
			})
			rep, err := runExport(t, url, "--node", accSource)
			wantExit(t, err, 5)
			for _, host := range []struct{ key, root string }{{"claudeSkill", claudeRoot(home)}, {"codexSkill", codexRoot(home)}} {
				if rep.host(t, host.key).Failure == nil {
					t.Errorf("%s had no failure", host.key)
				}
				if !absent(filepath.Join(host.root, accName, "SKILL.md")) {
					t.Errorf("%s wrote despite unsafe scope", host.key)
				}
			}
		})
	}
}
