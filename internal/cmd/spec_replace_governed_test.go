package cmd

import (
	"encoding/json"
	"fmt"
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

func TestSpecReplaceReportsTheGovernedSpecsItCouldNotSearch(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"whole memory", nil},
		{"with --prefix", []string{"--prefix", "cor:sec"}},
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
