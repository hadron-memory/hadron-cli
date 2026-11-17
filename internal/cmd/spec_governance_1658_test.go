package cmd

import (
	"encoding/json"
	"strings"
	"sync"
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

func TestTagOnlyNodeRemedy1658(t *testing.T) {
	var mu sync.Mutex
	role := ""
	writes := 0
	srv := newVarServer(t, func(op string, vars json.RawMessage) string {
		mu.Lock()
		defer mu.Unlock()
		switch op {
		case "ResolveUrn":
			return resolveNodeJSON
		case "GetNode", "GetNodeRaw":
			var n map[string]any
			if err := json.Unmarshal([]byte(nodeDetailJSON), &n); err != nil {
				t.Error(err)
			}
			n["role"] = nil
			n["tags"] = []string{"spec"}
			n["isRunnable"] = false
			if role != "" {
				n["role"] = role
			}
			return `{"data":{"node":` + string(mustJSON1550(t, n)) + `}}`
		case "UpdateSpecNode":
			var v struct {
				Input struct {
					Role *string `json:"role"`
				} `json:"input"`
			}
			if err := json.Unmarshal(vars, &v); err != nil {
				t.Error(err)
			}
			if v.Input.Role == nil || *v.Input.Role != "spec" {
				t.Errorf("missing role claim: %s", vars)
			} else {
				role = *v.Input.Role
			}
			writes++
			return `{"data":{"updateSpecNode":` + nodeDetailJSON + `}}`
		case "FindNodes":
			n := specNodeList("tag-only", `["spec"]`)
			if role != "" {
				n = strings.Replace(specNodeListWithRole("tag-only", `["spec"]`), `"spec.rule"`, `"`+role+`"`, 1)
			}
			return translateFindNodes(op, pageBeyondFirst(op, vars, `{"data":{"nodes":[`+n+`]}}`))
		case "Memories":
			return memListJSON
		default:
			if b, ok := unstubbedDefault(op); ok {
				return b
			}
			t.Errorf("unexpected operation %s", op)
			return `{"errors":[{"message":"unexpected"}]}`
		}
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN, "--role", "spec", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	f, out := testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"spec", "list", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["governed"] != true {
		t.Fatalf("post-remedy=%s", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if writes != 1 || role != "spec" {
		t.Fatalf("writes=%d role=%q", writes, role)
	}
	// Server assignment is simulated here; actual governedKind agreement is private-stack QA.
}
