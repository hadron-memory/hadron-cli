package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSpecListGovernance1658(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		tag := specNodeList("tag-only", `["spec"]`)
		role := specNodeListWithRole("role-only", `[]`)
		both := specNodeListWithRole("both", `["spec"]`)
		gql, _ := captureGraphQL(t, map[string]string{"Memories": memListJSON, "FindNodes": `{"data":{"nodes":[` + tag + `,` + role + `,` + both + `]}}`})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		args := []string{"spec", "list", "--server", gql.URL}
		if jsonMode {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !jsonMode {
			if !strings.Contains(out.String(), "not governed") {
				t.Fatalf("not marked: %s", out)
			}
			continue
		}
		var rows []map[string]any
		if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 3 {
			t.Fatalf("rows=%v", rows)
		}
		for _, r := range rows {
			want := r["citation"] != "tag-only"
			if r["governed"] != want {
				t.Fatalf("row=%v want governed=%v", r, want)
			}
		}
	}
}

func TestSpecDescribeGovernance1658(t *testing.T) {
	nodes := strings.Join([]string{specNodeList("tag-only", `["spec"]`), specNodeListWithRole("role-only", `[]`), specNodeListWithRole("both", `["spec"]`), specNodeList("neither", `[]`)}, ",")
	gql, _ := captureGraphQL(t, map[string]string{"Memories": memListJSON, "GetMemory": memGetJSON(`null`), "FindNodes": `{"data":{"nodes":[` + nodes + `]}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "describe", "-m", specProductMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(out.String()), &d); err != nil {
		t.Fatal(err)
	}
	if d["specs"] != float64(2) || d["notGoverned"] != float64(1) {
		t.Fatalf("inventory=%v", d)
	}
	rows, ok := d["notGovernedNodes"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("not governed rows=%v", d)
	}
	r := rows[0].(map[string]any)
	if r["citation"] != "tag-only" || r["governed"] != false {
		t.Fatalf("row=%v", r)
	}
}

func TestSpecLintTagOnlyCommand1658(t *testing.T) {
	for _, strict := range []bool{false, true} {
		detail := strings.Replace(cleanSpecDetail, `"role":"spec",`, "", 1)
		detail = strings.Replace(detail, `"nodeType":"info"`, `"nodeType":"task"`, 1)
		gql, _ := captureGraphQL(t, map[string]string{"Memories": memListJSON, "GetMemory": memGetVectorEnabledJSON, "ResolveUrn": resolveSpecJSON, "GetNode": `{"data":{"node":` + detail + `}}`, "NodeBatch": specLintRawBodyStub(detail)})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		args := []string{"spec", "lint", "msg:010:02", "-m", specMem, "--json", "--server", gql.URL}
		if strict {
			args = append(args, "--strict")
		}
		root.SetArgs(args)
		err := root.Execute()
		wantCode := 0
		if strict {
			wantCode = 5
		}
		if exitCodeFor(err) != wantCode {
			t.Fatalf("err=%v output=%s", err, out)
		}
		var fs []map[string]any
		if err := json.Unmarshal([]byte(out.String()), &fs); err != nil {
			t.Fatal(err)
		}
		if len(fs) != 1 || fs[0]["rule"] != "not-governed" || fs[0]["governed"] != false {
			t.Fatalf("findings=%v", fs)
		}
	}
}

func TestTagOnlyEditRemedy1658(t *testing.T) {
	_, captured, err := runSpecEdit(t, titleMocks(titleWriteOK), "", "--title", "New title", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(captured["UpdateSpecNode"]) != 1 {
		t.Fatalf("tag-only remedy must use spec door: %v", captured)
	}
	// This fixture begins with role:null. Actual server role assignment is the private-stack QA leg.
	in := sentTitleEditInput(t, captured["UpdateSpecNode"][0])
	if in["name"] != "msg:010:02 — New title" {
		t.Fatalf("input=%v", in)
	}
}
