package cmd

import (
	"encoding/json"
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
	r := NewRootCmd(f)
	r.SetArgs([]string{"drive", "export", "f", "--connection", "c", "--server", srv.URL})
	if e := r.Execute(); e != nil {
		t.Fatal(e)
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
