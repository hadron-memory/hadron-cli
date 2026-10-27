package cmd

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

type slice3TableCase struct {
	name  string
	args  []string
	stubs map[string]string
}

// Each case executes a real command against synthetic GraphQL responses. Only
// server response strings change: inputs, request decisions and JSON stay raw.
func TestSlice3SafeTableCommands(t *testing.T) {
	for _, value := range []string{"a\nb\x1b[31mc\u0085\x7f\u202e\u200b\u2028\u2029", strings.Repeat("日", 1000), "café e\u0301 😀", ""} {
		q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
		v := q(value)
		node := `{"id":"n1","memoryId":"mem1","loc":` + v + `,"urn":` + v + `,"name":` + v + `,"nodeType":"info","tags":[],"updatedAt":"2026-01-01T00:00:00Z"}`
		edge := `{"id":"e1","name":` + v + `,"loc":` + v + `,"source":{"id":"n1","loc":` + v + `},"target":{"id":"n2","loc":` + v + `}}`
		cases := []slice3TableCase{
			{"node add", []string{"node", "add", "-m", "acme.com::kb", "--loc", "x", "--name", "N"}, map[string]string{"CreateNode": `{"data":{"createNode":` + node + `}}`}},
			{"node update", []string{"node", "update", "n1", "--name", "N"}, map[string]string{"GetNode": `{"data":{"node":{"id":"n1"}}}`, "UpdateNode": `{"data":{"updateNode":` + node + `}}`}},
			{"node clone", []string{"node", "clone", "n1", "--to-memory", "acme.com::kb"}, map[string]string{"CloneNode": `{"data":{"cloneNode":` + node + `}}`}},
			{"node move", []string{"node", "move", "n1", "--to-memory", "acme.com::kb"}, map[string]string{"MoveNode": `{"data":{"moveNode":` + node + `}}`}},
			{"node merge", []string{"node", "merge", "n1", "--into", "n2", "--yes"}, map[string]string{"MergeNodes": `{"data":{"mergeNodes":` + node + `}}`}},
			{"node list", []string{"node", "list", "-m", "acme.com::kb"}, map[string]string{"FindNodes": `{"data":{"findNodes":{"total":1,"hits":[{"node":` + node + `}]}}}`}},
			{"node recovery list", []string{"node", "recovery", "list", "-m", "acme.com::kb"}, map[string]string{"RecoveryNodes": `{"data":{"recoveryNodes":{"total":1,"items":[` + strings.TrimSuffix(node, "}") + `,"deletedAt":"2026-01-01T00:00:00Z"}]}}}`}},
			{"node recovery graph", []string{"node", "recovery", "graph", "-m", "acme.com::kb"}, map[string]string{"RecoveryEdges": `{"data":{"recoveryEdges":{"total":1,"items":[` + edge + `]}}}`}},
			{"node revision list", []string{"node", "revision", "list", "n1"}, map[string]string{"NodeRevisionsStamped": `{"data":{"nodeRevisions":[{"id":"r1","name":` + v + `,"createdAt":"2026-01-01T00:00:00Z","editedByInfo":` + v + `}]}}`}},
			{"node signing-request list", []string{"node", "signing-request", "list", "n1"}, map[string]string{"NodeSigningRequests": `{"data":{"node":{"id":"n1","signingRequests":[{"id":` + v + `,"state":"OPEN","revision":1,"createdAt":"2026-01-01T00:00:00Z","expiresAt":"2026-01-02T00:00:00Z"}]}}}`}},
			{"edge add", []string{"edge", "add", "--from", "n1", "--to", "n2", "--name", "rel"}, map[string]string{"CreateEdge": `{"data":{"createEdge":` + edge + `}}`}},
			{"edge update", []string{"edge", "update", "e1", "--name", "rel"}, map[string]string{"UpdateEdge": `{"data":{"updateEdge":` + edge + `}}`}},
			{"edge list", []string{"edge", "list", "n1"}, map[string]string{"GetNode": `{"data":{"node":{"id":"n1","outgoingEdges":[` + edge + `],"incomingEdges":[]}}}`}},
			{"search", []string{"search", "x", "-m", "acme.com::kb"}, map[string]string{"SearchNodes": `{"data":{"findNodes":{"total":1,"hits":[{"node":` + node + `}]}}}`}},
			{"replace text", []string{"replace", "text", "a", "b", "-m", "acme.com::kb", "--dry-run", "--field", "content"}, map[string]string{"SearchReplaceInNodes": `{"data":{"searchReplaceInNodes":{"nodesScanned":1,"nodesChanged":1,"totalReplacements":1,"dryRun":true,"results":[{"nodeId":"n1","loc":` + v + `,"replacements":1,"fields":[{"field":"content","matches":1}]}]}}}`}},
			{"spec backlinks", []string{"spec", "backlinks", "msg:010:02", "-m", "mem1"}, map[string]string{"Memories": memListJSON, "SpecBacklinks": `{"data":{"specBacklinks":[{"kind":"URN","field":"content","sourceLoc":"msg:010:01","targetLoc":"msg:010:02","text":` + v + `}]}}`}},
			{"spec unresolved", []string{"spec", "unresolved", "-m", "mem1"}, map[string]string{"Memories": memListJSON, "SpecUnresolvedReferences": `{"data":{"specUnresolvedReferences":[{"kind":"URN","field":"content","reason":"MISSING","sourceLoc":"msg:010:01","targetLoc":"msg:010:02","text":` + v + `}]}}`}},
			{"spec renumber", []string{"spec", "renumber", "msg:010:02", "msg:010:03", "-m", "mem1", "--dry-run"}, map[string]string{"Memories": memListJSON, "RenumberSpec": `{"data":{"renumberSpec":{"dryRun":true,"moved":[],"rewrites":[{"nodeId":"n1","loc":"msg:010:01","status":"PLANNED","count":1,"fields":["content"],"error":` + v + `}],"textCitations":[{"sourceLoc":"msg:010:01","field":"content","citation":"msg:010:02","excerpt":` + v + `}]}}}`}},
			{"spec mint", []string{"spec", "mint", "-m", "mem1", "--dry-run"}, map[string]string{"Memories": memListJSON, "MintMemoryNodes": `{"data":{"mintMemoryNodes":{"memoryId":"mem1","dryRun":true,"minted":false,"mintLocs":[],"blockers":[{"kind":"PLACEHOLDER","loc":"msg:010:02","message":` + v + `}],"staleAbstracts":[],"openQuestions":[]}}}`}},
		}
		listed := strings.Replace(specNodeList("msg:010:02", `["spec"]`), q("msg:010:02 — T"), v, 1)
		listResp := `{"data":{"nodes":[` + listed + `]}}`
		cases = append(cases,
			slice3TableCase{"spec list", []string{"spec", "list", "-m", specMem}, map[string]string{"FindNodes": listResp}},
			slice3TableCase{"spec find", []string{"spec", "find", "x", "-m", specMem}, map[string]string{"FindNodes": listResp}},
			slice3TableCase{"spec replace", []string{"spec", "replace", "a", "b", "-m", specMem, "--dry-run"}, map[string]string{"FindNodes": listResp, "SearchReplaceInNodes": strings.Replace(searchReplaceDryJSON, q("findings:flaky-ci"), v, 1)}},
		)
		batchNode := strings.ReplaceAll(specBatchNode("msg:010:02"), q("msg:010:02"), v)
		batchNode = strings.Replace(batchNode, q(specBatchContent), q("hadron_unregistered_tool"), 1)
		remoteLocList := strings.ReplaceAll(listResp, q("msg:010:02"), v)
		for _, command := range []string{"lint", "check-tools"} {
			cases = append(cases, slice3TableCase{"spec " + command, []string{"spec", command, "-m", specMem}, map[string]string{"FindNodes": remoteLocList, "NodeBatch": `{"data":{"nodeBatch":{"nodes":[` + batchNode + `],"truncated":false,"omitted":[],"unavailable":[]}}}`}})
		}

		// A remote supersession target is rendered inside the citation message.
		src := srcTree(t, map[string]string{"a.go": "// Spec: msg:010:02\n"})
		cases = append(cases, slice3TableCase{"spec citations", []string{"spec", "citations", "-m", specMem, "--src", src}, map[string]string{"NodeBatch": citationBatch([]string{strings.Replace(citationBatchNode("msg:010:02", `["spec","superseded"]`, "", "replacement-marker"), q("replacement-marker"), v, 1)}, "")}})

		for _, tc := range cases {
			if tc.name == "spec lint" {
				tc.args = append(tc.args, "--all")
			}
			if tc.name == "spec citations" && value == "" {
				continue
			}
			t.Run(tc.name+"/"+q(string([]rune(value)[:min(utf8.RuneCountInString(value), 8)])), func(t *testing.T) {
				run := func(asJSON bool) string {
					tc.stubs["ResolveUrn"] = resolveNodeJSON
					tc.stubs["GetMemory"] = `{"data":{"memory":{"id":"mem1","vectorIndexEnabled":true}}}`
					if _, ok := tc.stubs["Memories"]; !ok {
						tc.stubs["Memories"] = memListJSON
					}
					gql := fakeGraphQL(t, tc.stubs)
					defer gql.Close()
					f, out := testFactory(t)
					root := NewRootCmd(f)
					args := append(append([]string{}, tc.args...), "--server", gql.URL)
					if (strings.HasPrefix(tc.name, "node ") && tc.name != "node add" && tc.name != "node list" && !strings.HasPrefix(tc.name, "node recovery")) || tc.name == "edge add" || tc.name == "edge list" {
						args = append(args, "-m", "acme.com::kb")
					}
					if asJSON {
						args = append(args, "--json")
					}
					root.SetArgs(args)
					err := root.Execute()
					if err != nil && exitCodeFor(err) != 5 {
						t.Fatalf("execute: %v", err)
					}
					return out.String()
				}
				text := run(false)
				if value != "" && strings.Count(text, "\n") != slice3ExpectedLines[tc.name] {
					t.Errorf("logical row count drift: got %d lines, want %d: %q", strings.Count(text, "\n"), slice3ExpectedLines[tc.name], text)
				}
				if strings.Contains(value, "\n") && !strings.Contains(text, "a b [31mc") {
					t.Errorf("controls must become spaces, not disappear: %q", text)
				}
				if strings.HasPrefix(value, "café") && !strings.Contains(text, value) {
					t.Errorf("ordinary Unicode changed: %q", text)
				}
				raw := run(true)
				var decoded any
				if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
					t.Fatalf("JSON: %v: %q", err, raw)
				}
				if value != "" && !slice3ContainsValue(decoded, value) {
					t.Fatalf("JSON lost original %q: %s", value, raw)
				}
				for _, r := range text {
					if r != '\n' && (unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)) {
						t.Errorf("hidden rune %U survived: %q", r, text)
						break
					}
				}
				if strings.Contains(value, "\n") && strings.Contains(text, "a\nb") {
					t.Errorf("server value forged a row: %q", text)
				}
				if utf8.RuneCountInString(value) > 256 {
					if strings.Contains(text, strings.Repeat("日", 256)) || !strings.Contains(text, "…") {
						t.Errorf("missing cell bound: %q", text)
					}
				}
				// Table columns are separated by at least two spaces. Check the emitted
				// cell, rather than re-testing the helper or counting UTF-8 bytes.
				for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
					for _, cell := range regexp.MustCompile(` {2,}`).Split(line, -1) {
						if utf8.RuneCountInString(cell) > 256 {
							t.Errorf("cell exceeds256 runes: %q", cell)
						}
					}
				}
			})
		}
	}
}
func slice3ContainsValue(x any, want string) bool {
	switch v := x.(type) {
	case string:
		return strings.Contains(v, want)
	case []any:
		for _, a := range v {
			if slice3ContainsValue(a, want) {
				return true
			}
		}
	case map[string]any:
		for _, a := range v {
			if slice3ContainsValue(a, want) {
				return true
			}
		}
	}
	return false
}

// Headers and any fixed summary lines included; each fixture has one record
// per result table. Pinning the actual emitted line count catches row forgery.
var slice3ExpectedLines = map[string]int{
	"edge add":                  1,
	"edge list":                 2,
	"edge update":               1,
	"node add":                  1,
	"node clone":                1,
	"node list":                 2,
	"node merge":                1,
	"node move":                 1,
	"node recovery graph":       2,
	"node recovery list":        2,
	"node revision list":        2,
	"node signing-request list": 2,
	"node update":               1,
	"replace text":              3,
	"search":                    2,
	"spec backlinks":            2,
	"spec check-tools":          2,
	"spec citations":            2,
	"spec find":                 2,
	"spec lint":                 2,
	"spec list":                 2,
	"spec mint":                 8,
	"spec renumber":             7,
	"spec replace":              3,
	"spec unresolved":           2,
}
