package cmd

import (
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"strings"
	"testing"
)

const driveFile275 = `{"id":"f","name":"Synthetic","kind":"DOCUMENT","mimeType":"application/vnd.google-apps.document","parents":[],"size":null,"createdAt":null,"modifiedAt":null,"webLink":null}`

func TestDrive275Reads(t *testing.T) {
	for _, tc := range []struct {
		name, op, wire string
		args           []string
		vars           map[string]any
	}{
		{"list", "DriveList", `{"data":{"driveFiles":{"files":[` + driveFile275 + `],"nextPageToken":"next+=/"}}}`, []string{"list"}, map[string]any{"connectionRef": "c", "top": float64(50)}},
		{"search", "DriveList", `{"data":{"driveFiles":{"files":[],"nextPageToken":null}}}`, []string{"list", "--name-contains", "words", "--page-token", " token+=/ "}, map[string]any{"connectionRef": "c", "nameContains": "words", "top": float64(25), "pageToken": " token+=/ "}},
		{"get", "DriveGet", `{"data":{"driveFile":` + driveFile275 + `}}`, []string{"get", "f"}, map[string]any{"connectionRef": "c", "fileId": "f"}},
		{"export", "DriveExport", `{"data":{"driveFileExport":{"file":` + driveFile275 + `,"format":"MARKDOWN","mimeType":"text/markdown","content":"raw\\ntext"}}}`, []string{"export", "f", "--format", "markdown"}, map[string]any{"connectionRef": "c", "fileId": "f", "format": "MARKDOWN"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, ops := captureGraphQL(t, map[string]string{tc.op: tc.wire})
			f, out := testFactory(t)
			r := NewRootCmd(f)
			args := append([]string{"drive"}, tc.args...)
			r.SetArgs(append(args, "--connection", "c", "--json", "--server", srv.URL))
			if e := r.Execute(); e != nil {
				t.Fatal(e)
			}
			var got map[string]any
			_ = json.Unmarshal(ops[tc.op], &got)
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(tc.vars)
			if string(a) != string(b) || len(ops) != 1 {
				t.Fatal(string(a), string(b), ops)
			}
			if tc.name == "search" {
				if !strings.Contains(out.String(), `"files": []`) || !strings.Contains(out.String(), `"nextPageToken": null`) {
					t.Fatal(out.String())
				}
			} else if !strings.Contains(out.String(), `"size": null`) || !strings.Contains(out.String(), `"parents": []`) {
				t.Fatal(out.String())
			}
		})
	}
}

func TestDrive275RawExport(t *testing.T) {
	content := "<script>do not execute</script>\n$(not-a-command)\x00\x1b[31m\nlast line"
	encoded, _ := json.Marshal(content)
	srv, ops := captureGraphQL(t, map[string]string{"DriveExport": `{"data":{"driveFileExport":{"file":` + driveFile275 + `,"format":"HTML","mimeType":"text/html","content":` + string(encoded) + `}}}`})
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	r := NewRootCmd(f)
	r.SetArgs([]string{"drive", "export", "f", "--connection", "c", "--server", srv.URL})
	if e := r.Execute(); e != nil {
		t.Fatal(e)
	}
	if errOut.Len() != 0 {
		t.Fatal("export leaked to stderr", errOut.String())
	}
	if out.String() != content {
		t.Fatalf("raw bytes changed: %q", out.String())
	}
	var v map[string]any
	_ = json.Unmarshal(ops["DriveExport"], &v)
	if _, ok := v["format"]; ok {
		t.Fatal("provider default must be omitted", v)
	}
}

func TestDrive275Invalid(t *testing.T) {
	for _, args := range [][]string{{"list"}, {"list", "--connection", " "}, {"list", "--connection", "c", "--top", "0"}, {"list", "--connection", "c", "--top", "101"}, {"list", "--connection", "c", "--folder", ""}, {"list", "--connection", "c", "--query", " "}, {"list", "--connection", "c", "--page-token", ""}, {"list", "--connection", "c", "--modified-after", "2026-10-23"}, {"get", " ", "--connection", "c"}, {"export", "f", "--connection", "c", "--format", "binary"}, {"export", "f", "--connection", "c", "--out", "any"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			srv, ops := captureGraphQL(t, map[string]string{})
			f, _ := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs(append(append([]string{"drive"}, args...), "--server", srv.URL))
			if e := r.Execute(); exitCodeFor(e) != 2 {
				t.Fatal(e)
			}
			if len(ops) != 0 {
				t.Fatal(ops)
			}
		})
	}
}

func TestDrive275FiltersAndSizes(t *testing.T) {
	for _, flag := range []string{"query", "name-contains", "mime-type", "modified-after", "folder"} {
		t.Run(flag, func(t *testing.T) {
			value := "filter"
			if flag == "modified-after" {
				value = "2026-10-23T09:00:00+02:00"
			}
			row := strings.ReplaceAll(driveFile275, `"size":null`, `"size":4503599627370495.5`)
			srv, ops := captureGraphQL(t, map[string]string{"DriveList": `{"data":{"driveFiles":{"files":[` + row + `],"nextPageToken":null}}}`})
			f, out := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs([]string{"drive", "list", "--connection", "c", "--" + flag, value, "--json", "--server", srv.URL})
			if e := r.Execute(); e != nil {
				t.Fatal(e)
			}
			var vars map[string]any
			_ = json.Unmarshal(ops["DriveList"], &vars)
			top := float64(25)
			if flag == "folder" {
				top = 50
			}
			if vars["top"] != top {
				t.Fatal(vars)
			}
			key := map[string]string{"name-contains": "nameContains", "mime-type": "mimeType", "modified-after": "modifiedAfter", "folder": "folderId", "query": "query"}[flag]
			if vars[key] != value || len(vars) != 3 {
				t.Fatal(vars)
			}
			var got struct{ Files []struct{ Size *float64 } }
			if e := json.Unmarshal([]byte(out.String()), &got); e != nil {
				t.Fatal(e)
			}
			if len(got.Files) != 1 || got.Files[0].Size == nil || *got.Files[0].Size != 4503599627370495.5 {
				t.Fatal(out.String())
			}
		})
	}
}

func TestDrive275Failures(t *testing.T) {
	for _, tc := range []struct {
		args      []string
		op, field string
	}{{[]string{"list"}, "DriveList", "driveFiles"}, {[]string{"get", "f"}, "DriveGet", "driveFile"}, {[]string{"export", "f"}, "DriveExport", "driveFileExport"}} {
		for _, mode := range []string{"null", "denied", "too-large", "unknown"} {
			t.Run(tc.op+mode, func(t *testing.T) {
				wire := `{"data":{"` + tc.field + `":null}}`
				want := 7
				if mode != "null" {
					code := "EMAIL_TOOL_ERROR"
					emailCode := "other"
					want = 1
					if mode == "denied" {
						code = "FORBIDDEN"
						want = 8
					}
					if mode == "too-large" {
						emailCode = "file_too_large"
					}
					wire = `{"errors":[{"message":"synthetic refusal","extensions":{"code":"` + code + `","emailErrorCode":"` + emailCode + `"}}]}`
				}
				srv, ops := captureGraphQL(t, map[string]string{tc.op: wire})
				f, out := testFactory(t)
				r := NewRootCmd(f)
				a := append([]string{"drive"}, tc.args...)
				r.SetArgs(append(a, "--connection", "c", "--server", srv.URL))
				if e := r.Execute(); exitCodeFor(e) != want {
					t.Fatal(e, out.String())
				}
				if out.Len() != 0 || len(ops) != 1 {
					t.Fatal("partial output or retry", out.String(), ops)
				}
			})
		}
	}
}

func TestDrive275Documents(t *testing.T) {
	for _, tc := range []struct{ text, op, field string }{{gen.DriveList_Operation, "DriveList", "driveFiles"}, {gen.DriveGet_Operation, "DriveGet", "driveFile"}, {gen.DriveExport_Operation, "DriveExport", "driveFileExport"}} {
		doc, e := parser.ParseQuery(&ast.Source{Input: tc.text})
		if e != nil {
			t.Fatal(e)
		}
		op := doc.Operations.ForName(tc.op)
		if op.Operation != ast.Query || len(op.SelectionSet) != 1 || op.SelectionSet[0].(*ast.Field).Name != tc.field {
			t.Fatal("unexpected root or effect", op)
		}
		fragment := doc.Fragments.ForName("DriveReadFile")
		want := []string{"id", "name", "kind", "mimeType", "parents", "size", "createdAt", "modifiedAt", "webLink"}
		if fragment == nil || len(fragment.SelectionSet) != len(want) {
			t.Fatal(fragment)
		}
		for i, n := range want {
			if fragment.SelectionSet[i].(*ast.Field).Name != n {
				t.Fatal(fragment)
			}
		}
	}
}

func TestDrive275MalformedRows(t *testing.T) {
	for _, tc := range []struct {
		op, wire string
		args     []string
	}{
		{"DriveList", `{"data":{"driveFiles":{"files":null,"nextPageToken":"next"}}}`, []string{"list"}},
		{"DriveList", `{"data":{"driveFiles":{"files":[null],"nextPageToken":null}}}`, []string{"list"}},
		{"DriveGet", `{"data":{"driveFile":` + strings.ReplaceAll(driveFile275, `"parents":[]`, `"parents":null`) + `}}`, []string{"get", "f"}},
		{"DriveExport", `{"data":{"driveFileExport":{"file":null,"format":"TEXT","mimeType":"text/plain","content":"never output"}}}`, []string{"export", "f"}},
	} {
		t.Run(tc.op+tc.wire, func(t *testing.T) {
			srv, _ := captureGraphQL(t, map[string]string{tc.op: tc.wire})
			f, out := testFactory(t)
			r := NewRootCmd(f)
			a := append([]string{"drive"}, tc.args...)
			r.SetArgs(append(a, "--connection", "c", "--server", srv.URL))
			if e := r.Execute(); exitCodeFor(e) != 7 || out.Len() != 0 {
				t.Fatal(e, out.String())
			}
		})
	}
}
