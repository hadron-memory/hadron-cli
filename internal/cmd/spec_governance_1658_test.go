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
