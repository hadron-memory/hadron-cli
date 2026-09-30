package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #659 — `spec replace` sends the whole in-scope spec set, but the server's
// bulk replace SKIPS governed nodes (cor:acl:130:02) without refusing and
// without counting them, and every rule-level spec is governed (role: spec).
// Reproduced on clean main: 365 specs sent, 18 scanned (the index nodes), and
// "0 of 18 scanned" read as "no match". The report must now say what was not
// searched, so a zero is never mistaken for an absent text.

// governedSpecNode is a spec hit carrying a role, as rule-level specs do.
func governedSpecNode(loc string) string {
	return fmt.Sprintf(`{"id":%q,"memoryId":"mem1","loc":%q,"name":%q,"nodeType":"info","tags":["spec"],"role":"spec","updatedAt":"2026-09-23T00:00:00Z"}`,
		"id-"+loc, loc, loc+" — T")
}

// The corpus: one ungoverned index node, two governed specs.
var governedCorpus = `{"data":{"nodes":[` + specNodeList("cor:sec", `["spec"]`) + `,` +
	governedSpecNode("cor:sec:040") + `,` + governedSpecNode("cor:sec:040:01") + `]}}`

func noMatchReplaceResp(scanned int) string {
	return fmt.Sprintf(`{"data":{"searchReplaceInNodes":{"nodesScanned":%d,"nodesChanged":0,"totalReplacements":0,"dryRun":true,"results":[]}}}`, scanned)
}

// #684: replace must select both spec markers before it sends nodeIds to the
// existing bulk door. A role-only spec is still governed and therefore not
// searched by that door; the CLI must count it without claiming an all-clear.
func TestSpecReplaceUnionsRoleOnlySpecsBeforeDryRunAndNoMatch(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		t.Run(fmt.Sprintf("dryRun=%v", dryRun), func(t *testing.T) {
			seen := map[string]bool{}
			var sentIDs []string
			searchCalls := 0
			gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					OperationName string          `json:"operationName"`
					Variables     json.RawMessage `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode request: %v", err)
					return
				}
				if resp, ok := unstubbedDefault(req.OperationName); ok {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(resp))
					return
				}
				var resp string
				switch req.OperationName {
				case "FindNodes":
					var vars findNodesVars
					if err := json.Unmarshal(req.Variables, &vars); err != nil {
						t.Errorf("decode find vars: %v", err)
						return
					}
					if len(vars.Filter.MemoryIds) != 1 || vars.Filter.MemoryIds[0] != specMem || vars.Filter.LocPrefix != "cor:sec" {
						t.Errorf("each stream must keep memory and prefix scope: %+v", vars.Filter)
					}
					switch {
					case len(vars.Filter.Tags) == 1 && vars.Filter.Tags[0] == "spec":
						seen["tag"] = true
						resp = `{"data":{"nodes":[` + specNodeList("cor:sec", `["spec"]`) + `,` + specNodeListWithRole("cor:sec:040", `["spec"]`) + `]}}`
					case vars.Filter.Role != nil && *vars.Filter.Role == "spec":
						seen["role"] = true
						resp = `{"data":{"nodes":[` + specNodeListWithRole("cor:sec:040", `["spec"]`) + `,` + specNodeListWithRole("cor:sec:040:01", `[]`) + `,` + specNodeListWithRole("cor:security:010", `[]`) + `]}}`
					default:
						t.Errorf("unexpected find filter: %+v", vars.Filter)
						return
					}
				case "SearchReplaceInNodes":
					searchCalls++
					var vars struct {
						Input struct {
							NodeIds   []string `json:"nodeIds"`
							MemoryIds []string `json:"memoryIds"`
							DryRun    bool     `json:"dryRun"`
						} `json:"input"`
					}
					if err := json.Unmarshal(req.Variables, &vars); err != nil {
						t.Errorf("decode replace vars: %v", err)
						return
					}
					sentIDs = vars.Input.NodeIds
					if !vars.Input.DryRun || len(vars.Input.MemoryIds) != 0 {
						t.Errorf("replace must preview explicit node ids without a whole-memory write: %+v", vars.Input)
					}
					resp = noMatchReplaceResp(1)
				default:
					t.Errorf("unexpected operation %q", req.OperationName)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(translateFindNodes(req.OperationName, resp)))
			}))
			t.Cleanup(gql.Close)

			f, out, errOut := testFactoryTTY(t, "")
			root := NewRootCmd(f)
			args := []string{"spec", "replace", "ZZZNOPE", "x", "-m", specMem, "--prefix", "cor:sec", "--json", "--server", gql.URL}
			if dryRun {
				args = append(args, "--dry-run")
			} else {
				args = append(args, "--yes")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if !seen["tag"] || !seen["role"] || searchCalls != 1 {
				t.Errorf("expected both discovery streams and one preview, got streams=%v calls=%d", seen, searchCalls)
			}
			wantIDs := []string{"id-cor:sec", "id-cor:sec:040", "id-cor:sec:040:01"}
			if fmt.Sprint(sentIDs) != fmt.Sprint(wantIDs) {
				t.Errorf("explicit ids = %v, want deduped in-branch specs %v", sentIDs, wantIDs)
			}
			var dto struct {
				SpecsInScope      int `json:"specsInScope"`
				SpecsGoverned     int `json:"specsGoverned"`
				SpecsScanned      int `json:"specsScanned"`
				TotalReplacements int `json:"totalReplacements"`
			}
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatalf("output JSON: %v\n%s", err, out.String())
			}
			if dto.SpecsInScope != 3 || dto.SpecsGoverned != 2 || dto.SpecsScanned != 1 || dto.TotalReplacements != 0 {
				t.Errorf("role-only governed spec must be counted but not claimed searched: %+v", dto)
			}
			if !dryRun {
				msg := errOut.String()
				if !strings.Contains(msg, "No matches in the 1 spec(s) searched") || strings.Contains(msg, "No matches — nothing to replace") {
					t.Errorf("real no-match note must name the searched subset: %s", msg)
				}
			}
		})
	}
}

func TestSpecReplaceReportsTheGovernedSpecsItCouldNotSearch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		wantPrefix string // the locPrefix the enumeration must send; "" = none
	}{
		{"whole memory", nil, ""},
		{"with --prefix", []string{"--prefix", "cor:sec"}, "cor:sec"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"FindNodes":            governedCorpus,
				"SearchReplaceInNodes": noMatchReplaceResp(1),
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"spec", "replace", "ZZZNOPE", "x", "-m", specMem, "--dry-run", "--json", "--server", gql.URL}, tc.args...))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			// Enumeration: the WHOLE eligible corpus goes out, governed or not.
			// The CLI does not filter; the server skips.
			var vars struct {
				Input struct {
					NodeIds []string `json:"nodeIds"`
				} `json:"input"`
			}
			_ = json.Unmarshal(captured["SearchReplaceInNodes"], &vars)
			if len(vars.Input.NodeIds) != 3 {
				t.Errorf("all 3 in-scope specs must be sent, got %v", vars.Input.NodeIds)
			}
			// The fake answers the same corpus either way, so the prefix is
			// proven on the QUERY: it must narrow the enumeration on the wire
			// (PR #677 review, Copilot).
			var find struct {
				Filter struct {
					LocPrefix *string `json:"locPrefix"`
				} `json:"filter"`
			}
			_ = json.Unmarshal(captured["FindNodes"], &find)
			sentPrefix := ""
			if find.Filter.LocPrefix != nil {
				sentPrefix = *find.Filter.LocPrefix
			}
			if sentPrefix != tc.wantPrefix {
				t.Errorf("enumeration locPrefix = %q, want %q", sentPrefix, tc.wantPrefix)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
				t.Fatalf("json: %v\n%s", err, out.String())
			}
			for k, want := range map[string]float64{"specsInScope": 3, "specsGoverned": 2, "specsScanned": 1} {
				if got[k] != want {
					t.Errorf("%s = %v, want %v", k, got[k], want)
				}
			}
		})
	}
}

// The human report names the unsearched specs, the rule, and the two commands
// that DO reach them — not a bare "0 of 1 scanned".
func TestSpecReplaceHumanReportSaysWhatWasNotSearched(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes":            governedCorpus,
		"SearchReplaceInNodes": noMatchReplaceResp(1),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "replace", "ZZZNOPE", "x", "-m", specMem, "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"2 of 3 spec(s) in scope are governed and were NOT searched", "cor:acl:130:02", "hadron spec grep", "hadron spec edit"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report must say %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "for a reason it does not report") {
		t.Errorf("every unsearched spec is governed here; no unexplained remainder:\n%s", out.String())
	}
}

// A shortfall the governed count does not explain is reported as unexplained,
// never attributed to a cause the CLI cannot see.
func TestSpecReplaceReportsAnUnexplainedShortfall(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes":            governedCorpus,
		"SearchReplaceInNodes": noMatchReplaceResp(0), // the index node was not scanned either
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "replace", "ZZZNOPE", "x", "-m", specMem, "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "1 more spec(s) in scope were not searched by the server, for a reason it does not report") {
		t.Errorf("an unexplained shortfall must be reported as such:\n%s", out.String())
	}
}

// A real run that matches nothing no longer says "No matches — nothing to
// replace" when specs went unsearched: that is the all-clear #659 is about.
func TestSpecReplaceNoMatchDoesNotClaimTheCorpusWasSearched(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes":            governedCorpus,
		"SearchReplaceInNodes": noMatchReplaceResp(1),
	})
	f, _, errOut := testFactoryTTY(t, "")
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "replace", "ZZZNOPE", "x", "-m", specMem, "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	msg := errOut.String()
	if strings.Contains(msg, "No matches — nothing to replace") {
		t.Errorf("must not read as an all-clear when governed specs were skipped:\n%s", msg)
	}
	if !strings.Contains(msg, "No matches in the 1 spec(s) searched") {
		t.Errorf("must say how many specs were actually searched:\n%s", msg)
	}
}

// PR #677 review (Copilot + Codex): the no-match wording must key on ANY
// shortfall, not on the governed count. Here nothing is governed and the
// server still scans less than was sent.
func TestSpecReplaceNoMatchCountsAnUnexplainedShortfallToo(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes":            `{"data":{"nodes":[` + specNodeList("cor:sec", `["spec"]`) + `]}}`,
		"SearchReplaceInNodes": noMatchReplaceResp(0),
	})
	f, _, errOut := testFactoryTTY(t, "")
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "replace", "ZZZNOPE", "x", "-m", specMem, "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	msg := errOut.String()
	if strings.Contains(msg, "No matches — nothing to replace") {
		t.Errorf("an unexplained shortfall must not read as an all-clear either:\n%s", msg)
	}
	if !strings.Contains(msg, "No matches in the 0 spec(s) searched") {
		t.Errorf("must say how many specs were actually searched:\n%s", msg)
	}
}
