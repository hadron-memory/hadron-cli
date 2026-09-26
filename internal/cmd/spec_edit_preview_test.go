package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/spec"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#737: `spec edit --dry-run` shows the change itself, read from the STORED
// (raw) body, and writes nothing.

// placeholderBody is a stored spec body with literal Mustache — the text a
// rendered read would silently drop.
const placeholderBody = "# W2 — win-back\n\nHello {{name}}, you are the {{role}}.\n\n{{> footer}}\n\nOld rule line.\n"

// storedAbstract is the abstract previewMocks stores beside placeholderBody.
const storedAbstract = "Win back."

// previewMocks serves placeholderBody and storedAbstract as the stored spec,
// over the raw read.
func previewMocks() map[string]string {
	b, _ := json.Marshal(placeholderBody)
	a, _ := json.Marshal(storedAbstract)
	m := editMocks()
	delete(m, "GetNode")
	m["GetSpecNodeRaw"] = `{"data":{"node":{"id":"sp1","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — W2",` +
		`"tags":["spec"],"role":null,"content":` + string(b) + `,"abstract":` + string(a) + `}}}`
	return m
}

type previewDTO struct {
	Changed            bool `json:"changed"`
	BodyChanged        bool `json:"bodyChanged"`
	AbstractChanged    bool `json:"abstractChanged"`
	AbstractReaffirmed bool `json:"abstractReaffirmed"`
	DryRun             bool `json:"dryRun"`
	Changes            []struct {
		Field  string `json:"field"`
		Change string `json:"change"`
		Before string `json:"before"`
		After  string `json:"after"`
		Diff   string `json:"diff"`
	} `json:"changes"`
}

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// readOnlyOps is every operation a dry run may send. Anything else — above all
// UpdateSpecNode — is a write the preview must never make.
var readOnlyOps = map[string]bool{"ResolveUrn": true, "GetSpecNodeRaw": true}

func assertNoWrites(t *testing.T, captured map[string]json.RawMessage) {
	t.Helper()
	var sent []string
	for op := range captured {
		if !readOnlyOps[op] {
			sent = append(sent, op)
		}
	}
	sort.Strings(sent)
	if len(sent) > 0 {
		t.Errorf("a dry run sent %v; it may only read (%v)", sent, readOnlyOps)
	}
}

func runEdit(t *testing.T, mocks map[string]string, args ...string) (string, map[string]json.RawMessage) {
	t.Helper()
	gql, captured := captureGraphQL(t, mocks)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"spec", "edit", "msg:010:02", "-m", specMem}, args...), "--server", gql.URL))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out.String())
	}
	return out.String(), captured
}

// The edit reads the STORED body: a rendered read would hand the editor, the
// preview and the write text without its placeholders.
func TestSpecEditReadsTheRawBody(t *testing.T) {
	if !strings.Contains(gen.GetSpecNodeRaw_Operation, "node(ref: $ref, raw: true)") {
		t.Fatalf("the edit read must ask for raw: true, got:\n%s", gen.GetSpecNodeRaw_Operation)
	}
	var seen string
	restore := spec.SetEditorFuncForTest(func(_ *output.IOStreams, current string) (string, error) {
		seen = current
		return strings.Replace(current, "Old rule line.", "New rule line.", 1), nil
	})
	defer restore()

	_, captured := runEdit(t, previewMocks(), "--json")
	if _, rendered := captured["GetNode"]; rendered {
		t.Error("spec edit read through GetNode, which renders Mustache")
	}
	if !strings.Contains(seen, "{{name}}") || !strings.Contains(seen, "{{> footer}}") {
		t.Errorf("the editor must open the stored body, placeholders intact; got:\n%s", seen)
	}
	var up editUpdateInput
	if err := json.Unmarshal(captured["UpdateSpecNode"], &up); err != nil {
		t.Fatalf("UpdateSpecNode vars: %v", err)
	}
	if up.Input.Content == nil || !strings.Contains(*up.Input.Content, "Hello {{name}}, you are the {{role}}.") ||
		!strings.Contains(*up.Input.Content, "{{> footer}}") {
		t.Errorf("the save must keep every placeholder, sent: %v", up.Input.Content)
	}
}

// The preview is the change itself, for a multiline body with literal
// placeholders, byte-exact — and nothing is written.
func TestSpecEditDryRunShowsTheContentChange(t *testing.T) {
	proposed := strings.Replace(placeholderBody, "Old rule line.\n", "New rule line.\nA second new line.\n", 1)
	proposed = strings.Replace(proposed, "Hello {{name}},", "Hi {{name}},", 1)
	out, captured := runEdit(t, previewMocks(),
		"--content-file", writeTemp(t, "body.md", proposed), "--dry-run", "--json")
	assertNoWrites(t, captured)

	var dto previewDTO
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if !dto.DryRun || !dto.Changed || !dto.BodyChanged || dto.AbstractChanged {
		t.Errorf("flags = %+v, want a dry-run body change only", dto)
	}
	if len(dto.Changes) != 1 {
		t.Fatalf("changes = %+v, want exactly the content", dto.Changes)
	}
	c := dto.Changes[0]
	if c.Field != "content" || c.Change != "replaced" || c.Before != placeholderBody || c.After != proposed {
		t.Errorf("change = %+v, want content replaced, stored → proposed byte-exact", c)
	}
	for _, want := range []string{
		"--- content (stored)", "+++ content (proposed)",
		"-Old rule line.", "+New rule line.", "+A second new line.",
		"-Hello {{name}}, you are the {{role}}.", // placeholders verbatim on both sides
		"+Hi {{name}}, you are the {{role}}.",
		" {{> footer}}", // and in a context line
	} {
		if !strings.Contains(c.Diff, want) {
			t.Errorf("diff lacks %q:\n%s", want, c.Diff)
		}
	}
}

// The terminal shows the diff, the abstract-stale consequence, and says a
// preview is not an approval.
func TestSpecEditDryRunTextShowsTheDiff(t *testing.T) {
	proposed := strings.Replace(placeholderBody, "Old rule line.", "New rule line.", 1)
	out, captured := runEdit(t, previewMocks(),
		"--content-file", writeTemp(t, "body.md", proposed), "--dry-run")
	assertNoWrites(t, captured)
	for _, want := range []string{
		"would update msg:010:02 — W2",
		"--- content (stored)", "-Old rule line.", "+New rule line.",
		"would arm abstract-stale",
		"nothing was written, and this preview is not an approval",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output lacks %q:\n%s", want, out)
		}
	}
}

// The abstract is previewed too; clearing it is named as a clear.
func TestSpecEditDryRunShowsAbstractChanges(t *testing.T) {
	for name, tc := range map[string]struct {
		abstract, change string
	}{
		"replaced": {"Win back users who signed up and left.", "replaced"},
		"cleared":  {"", "cleared"},
		// #740 review (Copilot): the server stores a whitespace-only abstract
		// as null, so a file holding only a newline clears it.
		"cleared by whitespace": {"\n", "cleared"},
	} {
		t.Run(name, func(t *testing.T) {
			out, captured := runEdit(t, previewMocks(),
				"--abstract-file", writeTemp(t, "abstract.md", tc.abstract), "--dry-run", "--json")
			assertNoWrites(t, captured)
			var dto previewDTO
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, out)
			}
			if len(dto.Changes) != 1 {
				t.Fatalf("changes = %+v, want the abstract only", dto.Changes)
			}
			c := dto.Changes[0]
			if c.Field != "abstract" || c.Change != tc.change || c.Before != "Win back." || c.After != tc.abstract ||
				!strings.Contains(c.Diff, "-Win back.") {
				t.Errorf("change = %+v, want abstract %s", c, tc.change)
			}
		})
	}
}

// Re-affirming is metadata: the text is re-sent unchanged, so the preview names
// it and has no diff.
func TestSpecEditDryRunShowsAReaffirm(t *testing.T) {
	out, captured := runEdit(t, previewMocks(),
		"--abstract-still-accurate", "--dry-run", "--json")
	assertNoWrites(t, captured)
	var dto previewDTO
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(dto.Changes) != 1 {
		t.Fatalf("changes = %+v, want one reaffirm", dto.Changes)
	}
	if c := dto.Changes[0]; c.Field != "abstract" || c.Change != "reaffirmed" || c.Before != "Win back." ||
		c.After != "Win back." || c.Diff != "" {
		t.Errorf("change = %+v, want an abstract reaffirm with no diff", c)
	}
}

// A no-op is explicit: changed false, `changes: []` on the raw output, no write.
func TestSpecEditDryRunNoOp(t *testing.T) {
	out, captured := runEdit(t, previewMocks(),
		"--content-file", writeTemp(t, "body.md", placeholderBody), "--dry-run", "--json")
	assertNoWrites(t, captured)
	if !strings.Contains(out, `"changes": []`) || !strings.Contains(out, `"changed": false`) {
		t.Errorf("a no-op must say so explicitly, with changes: []:\n%s", out)
	}
}

// A no-op dry run closes like every other dry run (#740 review, Copilot).
func TestSpecEditDryRunNoOpTextKeepsTheDisclaimer(t *testing.T) {
	out, captured := runEdit(t, previewMocks(),
		"--content-file", writeTemp(t, "body.md", placeholderBody), "--dry-run")
	assertNoWrites(t, captured)
	if !strings.Contains(out, "no changes") || !strings.Contains(out, "this preview is not an approval") {
		t.Errorf("a no-op dry run must still say it is not an approval:\n%s", out)
	}
}

// The preview IS the save: the same flags, run for real, send exactly the
// previewed text and report the same changes.
func TestSpecEditPreviewMatchesTheWrite(t *testing.T) {
	proposed := strings.Replace(placeholderBody, "Old rule line.", "New rule line.", 1)
	file := writeTemp(t, "body.md", proposed)
	args := []string{"--content-file", file, "--abstract", "Win back, placeholders and all.", "--json"}

	dryOut, _ := runEdit(t, previewMocks(), append(args, "--dry-run")...)
	realOut, captured := runEdit(t, previewMocks(), args...)

	var dry, real previewDTO
	if err := json.Unmarshal([]byte(dryOut), &dry); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(realOut), &real); err != nil {
		t.Fatal(err)
	}
	if len(dry.Changes) != 2 || len(real.Changes) != 2 {
		t.Fatalf("changes: dry %+v, real %+v; want content and abstract in both", dry.Changes, real.Changes)
	}
	for i := range dry.Changes {
		if dry.Changes[i] != real.Changes[i] {
			t.Errorf("change %d: previewed %+v, written %+v", i, dry.Changes[i], real.Changes[i])
		}
	}
	var up editUpdateInput
	if err := json.Unmarshal(captured["UpdateSpecNode"], &up); err != nil {
		t.Fatalf("UpdateSpecNode vars: %v", err)
	}
	if up.Input.Content == nil || *up.Input.Content != dry.Changes[0].After ||
		up.Input.Abstract == nil || *up.Input.Abstract != dry.Changes[1].After {
		t.Errorf("the write sent content=%v abstract=%v, not the previewed after-texts", up.Input.Content, up.Input.Abstract)
	}
}

// Nothing can go stale without a kept abstract over a non-empty body, so
// neither the dry-run note nor the saved-edit reminder may claim it
// (#740 review, Codex): abstractVerification calls both not-applicable, and on
// a spec with no abstract the suggested --abstract-still-accurate is refused.
func TestSpecEditClaimsAbstractStaleOnlyWhenItCanArm(t *testing.T) {
	noAbstract := func() map[string]string {
		m := previewMocks()
		m["GetSpecNodeRaw"] = strings.Replace(m["GetSpecNodeRaw"], `"abstract":"Win back."`, `"abstract":null`, 1)
		return m
	}
	edited := strings.Replace(placeholderBody, "Old rule line.", "New rule line.", 1)
	for name, tc := range map[string]struct {
		mocks map[string]string
		body  string
		args  []string
	}{
		"dry run, no abstract":    {noAbstract(), edited, []string{"--dry-run"}},
		"dry run, body emptied":   {previewMocks(), "", []string{"--dry-run"}},
		"saved edit, no abstract": {noAbstract(), edited, nil},
	} {
		t.Run(name, func(t *testing.T) {
			args := append([]string{"--content-file", writeTemp(t, "body.md", tc.body)}, tc.args...)
			out, _ := runEdit(t, tc.mocks, args...)
			if strings.Contains(out, "abstract-stale") || strings.Contains(out, "--abstract-still-accurate") {
				t.Errorf("claims abstract-stale where nothing can arm it:\n%s", out)
			}
		})
	}
}
