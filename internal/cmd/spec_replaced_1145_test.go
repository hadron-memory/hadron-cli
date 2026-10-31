package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// cli#1145: a spec `spec supersede` retired reads as "replaced" in `spec list`
// and `spec register`, with additive replaced/replacedBy JSON. The persisted
// convention (tag `superseded` + `superseded-by` edge) is unchanged, and the
// approval state SUPERSEDED is a different thing that must not show it.

// replacedBatchNode is a nodeBatch node whose outgoing superseded-by edge
// points at target (loc, memoryID); an empty loc yields no edge, and
// concealed yields an edge whose target the caller cannot read (null).
func replacedBatchNode(edges ...string) string {
	const loc = "msg:010:01"
	return fmt.Sprintf(`{"id":"id-%s","memoryId":"mem1","loc":%q,"name":%q,"alias":null,"nodeType":"info",`+
		`"objectType":null,"isRunnable":false,"description":null,"abstract":null,"abstractOriginHash":null,`+
		`"tags":["spec","superseded"],"seq":null,"data":null,"properties":null,"content":"body",`+
		`"createdAt":"2026-08-05T00:00:00Z","updatedAt":"2026-08-05T00:00:00Z",`+
		`"outgoingEdges":[%s],"incomingEdges":[]}`, loc, loc, loc+" — T", strings.Join(edges, ","))
}

func supersededByEdge(targetLoc, targetMem string) string {
	target := "null"
	if targetLoc != "" {
		loc, _ := json.Marshal(targetLoc)
		target = fmt.Sprintf(`{"id":"t1","loc":%s,"memoryId":%q}`, loc, targetMem)
	}
	return fmt.Sprintf(`{"id":"e1","name":"superseded-by","loc":"l","description":null,"isRunnable":false,"priority":0,"condition":null,"target":%s}`, target)
}

func replacedList(t *testing.T, tags string, batch string, args ...string) (string, []map[string]any) {
	t.Helper()
	gql := fakeGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList("msg:010:01", tags) + `,` + specNodeList("msg:010:03", `["spec"]`) + `]}}`,
		"NodeBatch": citationBatch([]string{batch}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"spec", "list", "-m", specMem, "--server", gql.URL}, args...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
	}
	return out.String(), rows
}

func TestSpecListMarksAReplacedSpec(t *testing.T) {
	text, _ := replacedList(t, `["spec","superseded"]`, replacedBatchNode(supersededByEdge("msg:010:03", "mem1")))
	var marked, plain int
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "msg:010:01") && strings.Contains(line, "(replaced by msg:010:03)"):
			marked++
		case strings.HasPrefix(line, "msg:010:03") && !strings.Contains(line, "replaced"):
			plain++
		}
	}
	if marked != 1 || plain != 1 {
		t.Fatalf("marker only on the replaced spec:\n%s", text)
	}
}

func TestSpecListReplacedJSONIsAdditive(t *testing.T) {
	_, replaced := replacedList(t, `["spec","superseded"]`, replacedBatchNode(supersededByEdge("msg:010:03", "mem1")), "--json")
	_, baseline := replacedList(t, `["spec"]`, replacedBatchNode(), "--json")
	if replaced[0]["replaced"] != true || replaced[0]["replacedBy"] != "msg:010:03" {
		t.Fatalf("replaced row: %v", replaced[0])
	}
	if baseline[0]["replaced"] != false || baseline[0]["replacedBy"] != nil {
		t.Fatalf("an active spec is replaced:false, replacedBy:null: %v", baseline[0])
	}
	if _, has := baseline[0]["replacedBy"]; !has {
		t.Fatalf("replacedBy must be present as null: %v", baseline[0])
	}
	for _, row := range []map[string]any{replaced[0], baseline[0]} {
		for _, k := range []string{"citation", "memoryId", "name", "nodeType", "tags", "updatedAt"} {
			if _, ok := row[k]; !ok {
				t.Errorf("existing key %q dropped: %v", k, row)
			}
		}
		if len(row) != 9 { // the existing keys (incl. memoryUrn) + exactly two
			t.Errorf("unexpected key set: %v", row)
		}
	}
	if baseline[0]["tags"].([]any)[0] != "spec" || len(replaced[0]["tags"].([]any)) != 2 {
		t.Errorf("tags changed: %v / %v", baseline[0]["tags"], replaced[0]["tags"])
	}
}

// No edge, an unreadable target, another memory's target and two different
// targets all say "replaced" and name nothing.
func TestSpecListReplacedNeverGuessesTheTarget(t *testing.T) {
	for name, batch := range map[string]string{
		"no edge":        replacedBatchNode(),
		"concealed":      replacedBatchNode(supersededByEdge("", "")),
		"other memory":   replacedBatchNode(supersededByEdge("msg:010:03", "mem2")),
		"two targets":    replacedBatchNode(supersededByEdge("msg:010:03", "mem1"), supersededByEdge("msg:010:04", "mem1")),
		"unrelated edge": replacedBatchNode(strings.Replace(supersededByEdge("msg:010:03", "mem1"), "superseded-by", "see-also", 1)),
	} {
		t.Run(name, func(t *testing.T) {
			text, _ := replacedList(t, `["spec","superseded"]`, batch)
			if !strings.Contains(text, "(replaced)") || strings.Contains(text, "replaced by") {
				t.Fatalf("want bare (replaced):\n%s", text)
			}
			_, rows := replacedList(t, `["spec","superseded"]`, batch, "--json")
			if rows[0]["replaced"] != true || rows[0]["replacedBy"] != nil {
				t.Fatalf("row: %v", rows[0])
			}
		})
	}
}

func TestSpecListReplacedIsSeparateFromApprovalSuperseded(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"FindNodes":                 `{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec"]`) + `]}}`,
		"NodeLiveRevisionsApproved": `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[{"id":"id-msg:010:02","revision":3,"authorship":null,"contentValidation":null,"approvalStatus":{"state":"SUPERSEDED","approval":null}}]}}}`,
		"NodeLiveRevisionsMinted":   `{"errors":[{"message":"Cannot query field \"mintStatus\" on type \"Node\"."}]}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "list", "-m", specMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "replaced") {
		t.Fatalf("approval SUPERSEDED must not read as replaced:\n%s", out.String())
	}
}

func TestSpecListReplacedMarkerIsTerminalSafe(t *testing.T) {
	text, _ := replacedList(t, `["spec","superseded"]`, replacedBatchNode(supersededByEdge("msg:010:03\u001b[31m\nFORGED", "mem1")))
	if strings.ContainsRune(text, 0x1b) || strings.Contains(text, "\nFORGED") {
		t.Fatalf("unsanitized loc: %q", text)
	}
}

func replacedRegister(t *testing.T, args ...string) string {
	t.Helper()
	gql := fakeGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList("msg", `["spec"]`) + `,` + specNodeList("msg:010", `["spec"]`) + `,` +
			specNodeList("msg:010:01", `["spec","superseded"]`) + `,` + specNodeList("msg:010:02", `["spec"]`) + `,` + specNodeList("msg:010:03", `["spec"]`) + `]}}`,
		"NodeBatch":  citationBatch([]string{replacedBatchNode(supersededByEdge("msg:010:03", "mem1"))}, ""),
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"reg1","kind":"node","memoryId":"mem1"}}}`,
		"GetNode": `{"data":{"node":{"id":"reg1","memoryId":"mem1","loc":"register","name":"register","description":null,"abstract":null,` +
			`"abstractOriginHash":null,"nodeType":"info","tags":["index"],"content":"x","data":null,"seq":null,` +
			`"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z","outgoingEdges":[],"incomingEdges":[]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"spec", "register", "-m", specMem, "--server", gql.URL}, args...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestSpecRegisterMarksAReplacedRule(t *testing.T) {
	text := replacedRegister(t)
	if !strings.Contains(text, "rules: 01 (replaced by 03), 02, 03  ") {
		t.Fatalf("rules line:\n%s", text)
	}
}

func TestSpecRegisterReplacedJSONIsAdditive(t *testing.T) {
	var dto struct {
		Modules []struct {
			Features []map[string]any `json:"features"`
		} `json:"modules"`
	}
	if err := json.Unmarshal([]byte(replacedRegister(t, "--json")), &dto); err != nil {
		t.Fatal(err)
	}
	fe := dto.Modules[0].Features[0]
	if fmt.Sprint(fe["rules"]) != "[01 02 03]" || fe["nextRule"] != "04" || fe["feature"] != "010" {
		t.Fatalf("existing keys changed: %v", fe)
	}
	want := []any{map[string]any{"rule": "01", "replaced": true, "replacedBy": "msg:010:03"}}
	if fmt.Sprint(fe["replacedRules"]) != fmt.Sprint(want) {
		t.Fatalf("replacedRules: %v", fe["replacedRules"])
	}
}
