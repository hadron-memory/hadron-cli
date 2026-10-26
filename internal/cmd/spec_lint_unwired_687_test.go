package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ---- spec lint --unwired: report the edges legacy `spec new` plans (#687) ----
//
// Ruling (Vera, Eden; team chat #36507/#36508): opt-in, legacy-grammar
// citations only, info level (never fails --strict), and each finding prints
// the exact `spec link` that adds the edge. The default lint is unchanged, so
// #708's "a spec owes no parent" holds.

// unwiredSpec is a batch node at loc titled title with the given outgoing
// edges, each "<label>|<target loc>".
func unwiredSpec(loc, title string, edges ...string) string {
	var out []string
	for i, e := range edges {
		label, target, _ := strings.Cut(e, "|")
		out = append(out, fmt.Sprintf(`{"name":%q,"priority":0,"condition":null,"target":{"id":"t%d-%s","loc":%q,"memoryId":"mem1"}}`,
			label, i, loc, target))
	}
	body, _ := json.Marshal(specBatchContent)
	return fmt.Sprintf(`{"id":"id-%s","memoryId":"mem1","loc":%q,"name":%q,"alias":null,"nodeType":"info",`+
		`"description":null,"abstract":"An abstract for %s.","abstractOriginHash":%q,`+
		`"tags":["spec","p1"],"seq":null,"data":{"version":"0.0.1"},"properties":null,"content":%s,`+
		`"updatedAt":"2026-06-14T00:00:00Z","outgoingEdges":[%s],"incomingEdges":[]}`,
		loc, loc, loc+" — "+title, loc, specOriginHash(specBatchContent), string(body), strings.Join(out, ","))
}

const inheritLabel = "inherits the shared contract (general provisions)"

// unwiredCorpus is the feature msg:010 with its contract and three rules:
//   - msg:010:00, the contract: wired to its feature, inherits nothing;
//   - msg:010:01: fully wired;
//   - msg:010:02: wired to its feature, missing its inheritance edge;
//   - msg:010:03: no edges at all (the customer's orphan).
//
// msg:010's own planned targets (msg, msg:000) do not exist, so it plans
// nothing.
func unwiredCorpus(t *testing.T) map[string]string {
	t.Helper()
	nodes := []string{
		unwiredSpec("msg:010", "Win-back"),
		unwiredSpec("msg:010:00", "Win-back general provisions", "Win-back general provisions|msg:010"),
		unwiredSpec("msg:010:01", "First", "First|msg:010", inheritLabel+"|msg:010:00"),
		unwiredSpec("msg:010:02", "Second", "Second|msg:010"),
		unwiredSpec("msg:010:03", "Third"),
	}
	var list []string
	for _, loc := range []string{"msg:010", "msg:010:00", "msg:010:01", "msg:010:02", "msg:010:03"} {
		list = append(list, specNodeList(loc, `["spec","p1"]`))
	}
	return map[string]string{
		"FindNodes": `{"data":{"nodes":[` + strings.Join(list, ",") + `]}}`,
		"NodeBatch": `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` +
			strings.Join(nodes, ",") + `]}}}`,
		"ResolveUrn": resolveNullJSON, // out-of-scope targets: msg, msg:000
		"Memories":   memListJSON,
		"GetMemory":  memGetVectorEnabledJSON,
	}
}

type lintFinding687 struct {
	Citation string `json:"citation"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

func runLint687(t *testing.T, extra ...string) ([]lintFinding687, error) {
	t.Helper()
	gql, _ := captureGraphQL(t, unwiredCorpus(t))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"spec", "lint", "--prefix", "msg:010", "-m", specMem, "--json", "--server", gql.URL}, extra...))
	err := root.Execute()
	var fs []lintFinding687
	if jerr := json.Unmarshal([]byte(out.String()), &fs); jerr != nil {
		t.Fatalf("lint output not JSON (err=%v): %v\n%s", err, jerr, out.String())
	}
	return fs, err
}

func unwired(fs []lintFinding687) []lintFinding687 {
	var out []lintFinding687
	for _, f := range fs {
		if f.Rule == "unwired" {
			out = append(out, f)
		}
	}
	return out
}

func TestSpecLintUnwiredReportsEachMissingPlannedEdge(t *testing.T) {
	fs, err := runLint687(t, "--unwired", "--strict")
	if err != nil {
		t.Fatalf("unwired findings are info and must not fail --strict: %v\n%+v", err, fs)
	}
	got := unwired(fs)
	want := map[string][]string{
		"msg:010:02": {"hadron spec link msg:010:02 msg:010:00 -m " + specMem + ` --label '` + inheritLabel + `'`},
		"msg:010:03": {
			"hadron spec link msg:010:03 msg:010 -m " + specMem + ` --label Third`,
			"hadron spec link msg:010:03 msg:010:00 -m " + specMem + ` --label '` + inheritLabel + `'`,
		},
	}
	count := 0
	for loc, cmds := range want {
		for _, c := range cmds {
			count++
			found := false
			for _, f := range got {
				if f.Citation == loc && strings.Contains(f.Message, "`"+c+"`") {
					found = true
					if f.Severity != "info" {
						t.Errorf("%s: unwired is info, got %q", loc, f.Severity)
					}
				}
			}
			if !found {
				t.Errorf("%s: want a finding printing `%s`; got %+v", loc, c, got)
			}
		}
	}
	if len(got) != count {
		t.Errorf("want exactly %d unwired findings (none for the wired rule, the contract or the feature), got %d: %+v",
			count, len(got), got)
	}
}

func TestSpecLintDefaultHasNoUnwiredFindings(t *testing.T) {
	// #708: a spec owes no parent. Without the opt-in, the same corpus reports
	// no wiring at all.
	fs, _ := runLint687(t)
	if got := unwired(fs); len(got) != 0 {
		t.Errorf("default lint must not report unwired specs, got %+v", got)
	}
}

// The printed spec link carries the spec's title as its label; pasted into a
// shell it must arrive as written, metacharacters and all (#1083 review).
func TestSpecLintUnwiredRemedyLabelSurvivesTheShell(t *testing.T) {
	c := unwiredCorpus(t)
	c["NodeBatch"] = strings.Replace(c["NodeBatch"], `"msg:010:03 — Third"`,
		strconvQuote("msg:010:03 — "+shellyLabel), 1)
	gql, _ := captureGraphQL(t, c)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "--prefix", "msg:010", "-m", specMem, "--json", "--unwired", "--server", gql.URL})
	_ = root.Execute()
	var fs []lintFinding687
	if err := json.Unmarshal([]byte(out.String()), &fs); err != nil {
		t.Fatalf("lint output not JSON: %v\n%s", err, out.String())
	}
	for _, f := range unwired(fs) {
		if f.Citation != "msg:010:03" || !strings.Contains(f.Message, " msg:010 -m ") {
			continue
		}
		args := shellWords(t, printedCommand(t, f.Message, "hadron spec link "))
		if got := args[len(args)-1]; args[len(args)-2] != "--label" || got != shellyLabel {
			t.Fatalf("the pasted remedy labels the edge %q, want %q (argv %q)", got, shellyLabel, args)
		}
		return
	}
	t.Fatalf("no parent-edge finding for msg:010:03: %+v", fs)
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

// An out-of-scope planned target that exists but is not a spec — here the
// module root msg — is still wiring spec new plans, but spec link refuses a
// non-spec endpoint, so the printed remedy is the edge create that runs.
func TestSpecLintUnwiredNonSpecTargetPrintsEdgeCreate(t *testing.T) {
	c := unwiredCorpus(t)
	q := map[string][]string{}
	for op, r := range c {
		q[op] = []string{r}
	}
	q["ResolveUrn"] = []string{`{"data":{"resolveUrn":{"id":"x1","kind":"node","memoryId":"mem1"}}}`, resolveNullJSON}
	q["GetNode"] = []string{strings.Replace(linkNonSpecDetail, `"loc":"register"`, `"loc":"msg"`, 1)}
	calls := map[string]int{}
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		r := q[op]
		if len(r) == 0 {
			return ""
		}
		i := min(calls[op], len(r)-1)
		calls[op]++
		return translateFindNodes(op, r[i])
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "--prefix", "msg:010", "-m", specMem, "--json", "--unwired", "--server", gql.URL})
	runErr := root.Execute()
	var fs []lintFinding687
	if err := json.Unmarshal([]byte(out.String()), &fs); err != nil {
		t.Fatalf("lint output not JSON (run err %v): %v\n%s", runErr, err, out.String())
	}
	want := "`hadron edge create -m " + specMem + " --from msg:010 --to msg --name Win-back`"
	for _, f := range unwired(fs) {
		if f.Citation == "msg:010" {
			if !strings.Contains(f.Message, want) {
				t.Errorf("want %s, got %s", want, f.Message)
			}
			return
		}
	}
	t.Fatalf("no finding for msg:010's planned edge to the non-spec module root: %+v", fs)
}
