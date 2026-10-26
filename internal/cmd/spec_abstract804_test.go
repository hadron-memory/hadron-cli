package cmd

import (
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An unchanged explicit abstract is not an attestation. Read requests are
// allowed, but a body edit that would arm stale must refuse before any write.
func TestSpecEditUnchangedAbstract804Matrix(t *testing.T) {
	const stored = "Win back users who never engaged after signup."
	const body = "# rewritten body\n"
	for _, tc := range []struct {
		name                                             string
		abstract                                         string
		supplied, file, body, dry, reaffirm, blankStored bool
		code                                             int
		write                                            bool
		wantAbstract                                     *string
	}{
		{name: "same file with body", abstract: stored, supplied: true, file: true, body: true, code: 2},
		{name: "same flag with body", abstract: stored, supplied: true, body: true, code: 2},
		{name: "same file dry run", abstract: stored, supplied: true, file: true, body: true, dry: true, code: 2},
		{name: "same flag dry run", abstract: stored, supplied: true, body: true, dry: true, code: 2},
		{name: "changed file", abstract: "A new abstract.", supplied: true, file: true, body: true, write: true, wantAbstract: str804("A new abstract.")},
		{name: "explicit attestation", body: true, reaffirm: true, write: true, wantAbstract: str804(stored)},
		{name: "omitted abstract", body: true, write: true},
		{name: "same file alone", abstract: stored, supplied: true, file: true},
		{name: "clear file", supplied: true, file: true, body: true, write: true, wantAbstract: str804("")},
		{name: "same file and attestation", abstract: stored, supplied: true, file: true, body: true, reaffirm: true, code: 2},
		{name: "blank stored blank file", supplied: true, file: true, body: true, blankStored: true, write: true},
		{name: "changed file dry run", abstract: "A new abstract.", supplied: true, file: true, body: true, dry: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mocks := editMocksStoring(specOriginHash(body))
			if tc.blankStored {
				mocks["GetSpecNodeForEdit"] = strings.Replace(mocks["GetSpecNodeForEdit"], `"abstract":"`+stored+`"`, `"abstract":null`, 1)
			}
			gql, captured := captureGraphQL(t, mocks)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"spec", "edit", "msg:010:02", "-m", specMem, "--server", gql.URL, "--json"}
			if tc.body {
				if tc.file {
					path := filepath.Join(t.TempDir(), "body.md")
					if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "--content-file", path)
				} else {
					args = append(args, "--content", body)
				}
			}
			if tc.supplied {
				if tc.file {
					p := filepath.Join(t.TempDir(), "abstract.txt")
					if err := os.WriteFile(p, []byte(tc.abstract), 0600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "--abstract-file", p)
				} else {
					args = append(args, "--abstract", tc.abstract)
				}
			}
			if tc.dry {
				args = append(args, "--dry-run")
			}
			if tc.reaffirm {
				args = append(args, "--abstract-still-accurate")
			}
			root.SetArgs(args)
			err := root.Execute()
			if got := exitCodeFor(err); got != tc.code {
				t.Fatalf("exit %d want %d: %v", got, tc.code, err)
			}
			if tc.code == exitcode.Usage && !tc.reaffirm && (err == nil || !strings.Contains(err.Error(), "--abstract-still-accurate")) {
				t.Fatalf("missing explicit remedy: %v", err)
			}
			raw, wrote := captured["UpdateSpecNode"]
			if wrote != tc.write {
				t.Fatalf("write=%t want %t", wrote, tc.write)
			}
			if !tc.write {
				assertNoWrites(t, captured)
				return
			}
			var vars struct {
				Input map[string]json.RawMessage `json:"input"`
			}
			if err := json.Unmarshal(raw, &vars); err != nil {
				t.Fatal(err)
			}
			if _, ok := vars.Input["expectedRevision"]; !ok {
				t.Fatal("revision guard lost")
			}
			abs, present := vars.Input["abstract"]
			if present != (tc.wantAbstract != nil) {
				t.Fatalf("abstract presence %t: %s", present, raw)
			}
			if present {
				var got string
				if err := json.Unmarshal(abs, &got); err != nil {
					t.Fatal(err)
				}
				if got != *tc.wantAbstract {
					t.Fatalf("abstract=%q", got)
				}
			}
			if tc.body {
				var got string
				_ = json.Unmarshal(vars.Input["content"], &got)
				if got != body {
					t.Fatalf("body=%q", got)
				}
			}
			if !tc.reaffirm && strings.Contains(out.String(), `"abstractVerification":"refreshed"`) {
				t.Fatal("false verification claim")
			}
		})
	}
}
func str804(s string) *string { return &s }
