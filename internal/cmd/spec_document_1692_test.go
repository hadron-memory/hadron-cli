package cmd

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1692: `hadron spec document`, the thin read-only consumer of
// hadron-server#1302's specDocument. The server renders; the CLI prints its
// bytes and assembles, reorders and rewrites nothing.

// A document whose Markdown has no trailing newline, so a CLI that "tidies"
// the text shows in the byte comparison.
const docMarkdown1692 = "# cor:spc:030 — Feature\n\n## Group\n- b\n- a\n\n{{kept}} \t trailing spaces  "

func specDocumentResponse1692(entries string) string {
	md, _ := json.Marshal(docMarkdown1692)
	return `{"data":{"specDocument":{"loc":"cor:spc:030","memoryId":"mem1","renderedAt":"2026-11-06T05:00:00.000Z","markdown":` + string(md) + `,"entries":` + entries + `}}}`
}

const (
	approvedEntry1692 = `{"loc":"cor:spc:030:02","urn":"hrn:node:acme.com:specs:cor:spc:030:02","name":"Format","revision":4,"content":"body 02","isPlaceholder":false,
		"approvalStatus":{"state":"APPROVED","approval":{"revision":4,"approvedAt":"2026-11-01T00:00:00Z","approvedBy":{"handle":"vera","urn":"hrn:user:acme.com:vera"},"approvedByInfo":null,"hash":"h4"}},
		"mintStatus":{"minted":true,"mintedAt":"2026-11-02T00:00:00Z","mintedBy":{"handle":"holger","urn":null},"mintedByInfo":null,"revision":4,"hash":"m4"}}`
	unapprovedEntry1692 = `{"loc":"cor:spc:030:01","urn":"hrn:node:acme.com:specs:cor:spc:030:01","name":"Operation","revision":2,"content":"body 01","isPlaceholder":false,
		"approvalStatus":{"state":"NOT_APPROVED","approval":null},"mintStatus":{"minted":false,"mintedAt":null,"mintedBy":null,"mintedByInfo":null,"revision":null,"hash":null}}`
	backfilledEntry1692 = `{"loc":"cor:spc:030:03","urn":"hrn:node:acme.com:specs:cor:spc:030:03","name":"Old","revision":1,"content":"body 03","isPlaceholder":false,
		"approvalStatus":{"state":"APPROVED","approval":null},"mintStatus":{"minted":true,"mintedAt":null,"mintedBy":null,"mintedByInfo":"backfill","revision":null,"hash":null}}`
	placeholderEntry1692 = `{"loc":"cor:spc:030:04","urn":"hrn:node:acme.com:specs:cor:spc:030:04","name":"Reserved","revision":1,"content":null,"isPlaceholder":true,
		"approvalStatus":{"state":"NOT_APPROVED","approval":null},"mintStatus":{"minted":false,"mintedAt":null,"mintedBy":null,"mintedByInfo":null,"revision":null,"hash":null}}`
)

// docEntries1692 is deliberately NOT in citation order: 02, 01, 04, 03.
var docEntries1692 = "[" + strings.Join([]string{approvedEntry1692, unapprovedEntry1692, placeholderEntry1692, backfilledEntry1692}, ",") + "]"

type docRun1692 struct {
	stdout, stderr string
	code           int
	ops            []string
}

// runDocument1692 runs `spec document` against a fake server. Only the memory
// reads and the one document query are allowed; any other operation (a
// mutation included) fails the test.
func runDocument1692(t *testing.T, documentBody string, args ...string) docRun1692 {
	t.Helper()
	var mu sync.Mutex
	var ops []string
	srv := newVarServer(t, func(op string, _ json.RawMessage) string {
		mu.Lock()
		ops = append(ops, op)
		mu.Unlock()
		switch op {
		case "Memories":
			return memListJSON
		case "GetMemory": // a read: resolving a URN the listing spells differently
			return memGetJSON(`null`)
		case "SpecDocument":
			return documentBody
		}
		t.Errorf("a read-only command sent %q", op)
		return `{"errors":[{"message":"unexpected operation"}]}`
	})
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"spec", "document"}, args...), "--server", srv.URL))
	code := exitcode.OK
	if err := root.Execute(); err != nil {
		code = renderError(f, err)
	}
	mu.Lock()
	defer mu.Unlock()
	return docRun1692{stdout: out.String(), stderr: errOut.String(), code: code, ops: append([]string(nil), ops...)}
}

func countOp1692(ops []string, name string) int {
	n := 0
	for _, o := range ops {
		if o == name {
			n++
		}
	}
	return n
}

func TestSpecDocumentPrintsTheServerMarkdownByteForByte(t *testing.T) {
	for _, args := range [][]string{{"cor:spc:030", "-m", "mem1"}, {"cor:spc:030", "-m", "mem1", "-o", "-"}} {
		r := runDocument1692(t, specDocumentResponse1692(docEntries1692), args...)
		if r.code != exitcode.OK || r.stdout != docMarkdown1692 || r.stderr != "" {
			t.Errorf("%v: exit %d, stdout %q (want the Markdown unchanged), stderr %q (want empty)", args, r.code, r.stdout, r.stderr)
		}
		if countOp1692(r.ops, "SpecDocument") != 1 {
			t.Errorf("%v: want exactly one document query, got %v", args, r.ops)
		}
	}
}

func TestSpecDocumentOutFileHoldsExactlyTheMarkdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feature.md")
	r := runDocument1692(t, specDocumentResponse1692(docEntries1692), "cor:spc:030", "-m", "mem1", "-o", path)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.code != exitcode.OK || sha256.Sum256(got) != sha256.Sum256([]byte(docMarkdown1692)) {
		t.Errorf("exit %d: the file must hold exactly the Markdown bytes, got %q", r.code, got)
	}
	if r.stdout != "" {
		t.Errorf("-o <file> prints nothing on stdout, got %q", r.stdout)
	}
	if want := "✓ wrote cor:spc:030 to " + path + " (" + itoa1692(len(docMarkdown1692)) + " bytes)\n"; r.stderr != want {
		t.Errorf("receipt = %q, want %q", r.stderr, want)
	}
}

func itoa1692(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestSpecDocumentJSONCarriesEveryEntryInDocumentOrder(t *testing.T) {
	r := runDocument1692(t, specDocumentResponse1692(docEntries1692), "cor:spc:030", "-m", "mem1", "--json")
	if r.code != exitcode.OK || r.stderr != "" {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(r.stdout), &raw); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, r.stdout)
	}
	for _, k := range []string{"loc", "memoryId", "renderedAt", "markdown", "entries"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing key %q: %s", k, r.stdout)
		}
	}
	var md string
	_ = json.Unmarshal(raw["markdown"], &md)
	if md != docMarkdown1692 {
		t.Errorf("markdown = %q, want the server's string unchanged", md)
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw["entries"], &entries); err != nil {
		t.Fatal(err)
	}
	// An order pin: the server's document order, never sorted by citation.
	wantOrder := []string{"cor:spc:030:02", "cor:spc:030:01", "cor:spc:030:04", "cor:spc:030:03"}
	if len(entries) != len(wantOrder) {
		t.Fatalf("entries = %d, want %d", len(entries), len(wantOrder))
	}
	for i, e := range entries {
		if e["loc"] != wantOrder[i] {
			t.Errorf("entry %d = %v, want %s (document order)", i, e["loc"], wantOrder[i])
		}
		for _, k := range []string{"loc", "urn", "name", "revision", "content", "isPlaceholder", "approvalStatus", "mintStatus"} {
			if _, ok := e[k]; !ok {
				t.Errorf("entry %v lacks %q", e["loc"], k)
			}
		}
	}
	// One fixture each: approved with its record, minted with its record,
	// unapproved, backfilled mint, placeholder.
	appr := entries[0]["approvalStatus"].(map[string]any)
	rec, _ := appr["approval"].(map[string]any)
	if appr["state"] != "APPROVED" || rec == nil || rec["hash"] != "h4" || rec["revision"] != float64(4) {
		t.Errorf("approved entry lost its approval record: %v", appr)
	}
	mint := entries[0]["mintStatus"].(map[string]any)
	if mint["minted"] != true || mint["hash"] != "m4" || mint["revision"] != float64(4) {
		t.Errorf("minted entry lost its mint record: %v", mint)
	}
	if un := entries[1]["approvalStatus"].(map[string]any); un["state"] != "NOT_APPROVED" || un["approval"] != nil {
		t.Errorf("unapproved entry: %v", un)
	}
	if ph := entries[2]; ph["isPlaceholder"] != true || ph["content"] != nil {
		t.Errorf("placeholder entry: %v", ph)
	}
	if bf := entries[3]["mintStatus"].(map[string]any); bf["minted"] != true || bf["revision"] != nil || bf["mintedByInfo"] != "backfill" {
		t.Errorf("backfilled mint: %v", bf)
	}
}

func TestSpecDocumentJSONEmptyEntriesIsAnEmptyList(t *testing.T) {
	r := runDocument1692(t, specDocumentResponse1692(`[]`), "cor:spc:030", "-m", "mem1", "--json")
	if r.code != exitcode.OK || !strings.Contains(r.stdout, `"entries": []`) {
		t.Errorf("exit %d: entries must be [] never null:\n%s", r.code, r.stdout)
	}
}

func TestSpecDocumentJSONWithOutFileIsRefusedBeforeAnyRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.json")
	r := runDocument1692(t, specDocumentResponse1692(docEntries1692), "cor:spc:030", "-m", "mem1", "--json", "-o", path)
	if r.code != exitcode.Usage || len(r.ops) != 0 {
		t.Errorf("exit %d (want %d) with ops %v (want none)", r.code, exitcode.Usage, r.ops)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the refused call must not create the file")
	}
}

func TestSpecDocumentRefusalsMapToTheirExit(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"memory not found", `{"errors":[{"message":"Memory not found","extensions":{"code":"MEMORY_NOT_FOUND"}}]}`, exitcode.NotFound},
		{"no live node at the citation", `{"errors":[{"message":"No spec at cor:spc:030","extensions":{"code":"NODE_NOT_FOUND"}}]}`, exitcode.NotFound},
		{"encrypted", `{"errors":[{"message":"encrypted","extensions":{"code":"SPEC_CORPUS_ENCRYPTED_UNSUPPORTED"}}]}`, exitcode.Conflict},
		{"over the cap", `{"errors":[{"message":"too many","extensions":{"code":"SPEC_DOCUMENT_TOO_LARGE","cap":300}}]}`, exitcode.Usage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runDocument1692(t, tc.body, "cor:spc:030", "-m", "mem1")
			if r.code != tc.code || r.stdout != "" {
				t.Errorf("exit %d (want %d), stdout %q (want none)", r.code, tc.code, r.stdout)
			}
			if tc.name == "over the cap" && !strings.Contains(r.stderr, "300") {
				t.Errorf("the message must name the cap: %q", r.stderr)
			}
			if n := countOp1692(r.ops, "SpecDocument"); n != 1 {
				t.Errorf("a refusal is never retried: %d document queries", n)
			}
		})
	}
}

// An unreadable memory and a missing one are ONE answer, by id and by URN, and
// a deleted spec reads the same as a never-written one.
func TestSpecDocumentUnreadableAndMissingMemoryAreIdentical(t *testing.T) {
	body := `{"errors":[{"message":"Memory not found","extensions":{"code":"MEMORY_NOT_FOUND"}}]}`
	for _, ref := range []string{"mem1", "hrn:mem:hadronmemory.com:platform-specs"} {
		a := runDocument1692(t, body, "cor:spc:030", "-m", ref)
		b := runDocument1692(t, body, "cor:spc:030", "-m", ref)
		if a.code != exitcode.NotFound || a.stdout != b.stdout || a.stderr != b.stderr || a.code != b.code {
			t.Errorf("%s: two refusals differ: %+v vs %+v", ref, a, b)
		}
	}
}

func TestSpecDocumentFromAServerWithoutTheQueryIsExit7AndNoFallback(t *testing.T) {
	body := `{"errors":[{"message":"Cannot query field \"specDocument\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	r := runDocument1692(t, body, "cor:spc:030", "-m", "mem1")
	if r.code != exitcode.Unavailable {
		t.Errorf("exit %d, want %d", r.code, exitcode.Unavailable)
	}
	if !strings.Contains(r.stderr, "does not provide the assembled spec document") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if n := countOp1692(r.ops, "SpecDocument"); n != 1 || len(r.ops) != 2 {
		t.Errorf("no second query, no fallback: ops %v", r.ops)
	}
}

func TestSpecDocumentFileIsNeverTouchedByARefusal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feature.md")
	if err := os.WriteFile(path, []byte("PRECIOUS"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"errors":[{"message":"nope","extensions":{"code":"MEMORY_NOT_FOUND"}}]}`,
		`{"errors":[{"message":"x","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
	} {
		r := runDocument1692(t, body, "cor:spc:030", "-m", "mem1", "-o", path)
		if r.code == exitcode.OK {
			t.Fatalf("the refusal must fail: %+v", r)
		}
		if got, _ := os.ReadFile(path); string(got) != "PRECIOUS" {
			t.Errorf("a refusal changed the file: %q", got)
		}
		if strings.Contains(r.stderr, "wrote") {
			t.Errorf("no receipt before a successful write: %q", r.stderr)
		}
	}
}

func TestSpecDocumentRefusesASymlinkAndLeavesNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.md")
	if err := os.WriteFile(target, []byte("REAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks here")
	}
	r := runDocument1692(t, specDocumentResponse1692(docEntries1692), "cor:spc:030", "-m", "mem1", "-o", link)
	if r.code != exitcode.Error || strings.Contains(r.stderr, "✓ wrote") {
		t.Errorf("a symlink target is refused with exit 1 and no receipt: %+v", r)
	}
	if got, _ := os.ReadFile(target); string(got) != "REAL" {
		t.Errorf("the symlink target was written through: %q", got)
	}

	missing := filepath.Join(dir, "no-such-dir", "f.md")
	r = runDocument1692(t, specDocumentResponse1692(docEntries1692), "cor:spc:030", "-m", "mem1", "-o", missing)
	if r.code != exitcode.Error || !strings.Contains(r.stderr, missing) || strings.Contains(r.stderr, "✓ wrote") {
		t.Errorf("a missing directory is exit 1 naming the path: %+v", r)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("a temp file was left behind: %s", e.Name())
		}
	}
}

func TestSpecDocumentRejectsAnInvalidCitationBeforeAnyRequest(t *testing.T) {
	r := runDocument1692(t, specDocumentResponse1692(`[]`), "bad cite!", "-m", "mem1")
	if r.code != exitcode.Usage || len(r.ops) != 0 {
		t.Errorf("exit %d with ops %v, want usage and no request", r.code, r.ops)
	}
}

// A success reply that is not a document ({}, null, or an object with no loc)
// is a malformed answer: it must fail, never panic, and never replace the file
// with zero bytes behind a success receipt.
func TestSpecDocumentMalformedSuccessReplyIsRefusedAndKeepsTheFile(t *testing.T) {
	for _, body := range []string{
		`{"data":{}}`,
		`{"data":{"specDocument":null}}`,
		`{"data":{"specDocument":{}}}`,
		`{"data":null}`,
	} {
		path := filepath.Join(t.TempDir(), "feature.md")
		if err := os.WriteFile(path, []byte("PRECIOUS"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"cor:spc:030", "-m", "mem1", "-o", path}, {"cor:spc:030", "-m", "mem1"}, {"cor:spc:030", "-m", "mem1", "--json"}} {
			r := runDocument1692(t, body, args...)
			if r.code == exitcode.OK || strings.Contains(r.stderr, "wrote") {
				t.Errorf("%s %v: must fail, got %+v", body, args, r)
			}
		}
		if got, _ := os.ReadFile(path); string(got) != "PRECIOUS" {
			t.Errorf("%s: the file changed: %q", body, got)
		}
	}
}
