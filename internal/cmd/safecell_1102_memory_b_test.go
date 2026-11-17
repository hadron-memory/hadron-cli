package cmd

import (
	"strings"
	"testing"
)

// hadron-cli#1102: text tables of `memory config template ls|apply`,
// `memory transfer` and `memory validate` pass server-supplied cells through
// output.SafeCell. See safecell_1102_helpers_test.go.

func TestSafeCell1102MemoryConfigTemplateList(t *testing.T) {
	item := strings.Replace(templateJSON("t1", "NAME", 1), `"NAME"`, `"`+evilJSON1102+`"`, 1)
	item = strings.Replace(item, `"id":"t1"`, `"id":"`+longJSON1102+`"`, 1)
	control := templateJSON("t2", "control", 1)
	text, js := runBoth1102(t, map[string]string{
		"MemoryConfigTemplates": `{"data":{"memoryConfigTemplates":{"total":2,"items":[` + item + `,` + control + `]}}}`,
	}, "memory", "config", "template", "list")
	assertSafeText1102(t, text, 3, true, true)
	assertJSONUnchanged1102(t, js, true, true)
}

func TestSafeCell1102MemoryConfigTemplateApply(t *testing.T) {
	text, js := runBoth1102(t, map[string]string{
		"ApplyMemoryConfigTemplate": applyPayload(true, false, 3,
			applyEntry(evilJSON1102, longJSON1102, ""),
			applyEntry("control", "APPLIED", "")),
	}, "memory", "config", "template", "apply", "t1", configMemoryRef, "--dry-run")
	for i := 0; i < len(text); i++ {
		if c := text[i]; (c < 0x20 && c != '\n') || c == 0x7f {
			t.Fatalf("control byte %#x in text output:\n%q", c, text)
		}
	}
	// The report has prose around the entries table, so count only the table:
	// header + two rows, and the forged row must not exist.
	if !strings.Contains(text, evilWant1102) {
		t.Fatalf("sanitized role missing:\n%s", text)
	}
	if strings.Contains(text, strings.Repeat("x", 257)) || !strings.Contains(text, strings.Repeat("x", 255)+"…") {
		t.Fatalf("outcome not in SafeCell's cut form:\n%s", text)
	}
	rows := 0
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, "control") && strings.Contains(l, "APPLIED") {
			rows++
		}
		if strings.HasPrefix(strings.TrimSpace(l), "b") && strings.Contains(l, "[31mc") {
			t.Fatalf("a newline in the role forged a row: %q", l)
		}
	}
	if rows != 1 {
		t.Fatalf("control row seen %d times:\n%s", rows, text)
	}
	assertJSONUnchanged1102(t, js, true, true)
}

func transferEvil1102(blockers string) string {
	return `{"data":{"transferMemoryOwnership":{"memoryId":"` + evilJSON1102 + `","oldUrn":"hrn:mem:alice:notes","newUrn":"` + longJSON1102 + `","fromOwner":"` + evilJSON1102 + `","toOwner":"org-id","currentClass":"` + evilJSON1102 + `","targetClass":"knowledge",
		"dependents":[{"kind":"` + evilJSON1102 + `","count":2,"handling":"` + longJSON1102 + `"}],"blockers":[` + blockers + `],"canApply":false,"applied":false}}}`
}

func TestSafeCell1102MemoryTransfer(t *testing.T) {
	blockers := `{"code":"` + evilJSON1102 + `","message":"` + longJSON1102 + `"}`
	// A plan with blockers exits non-zero ("silent": the tables are the report),
	// which runBoth1102 treats as failure, so run it here and keep the output.
	gql, _ := captureGraphQL(t, map[string]string{"TransferMemoryOwnership": transferEvil1102(blockers)})
	var text, js string
	for _, asJSON := range []bool{false, true} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		args := []string{"memory", "transfer", "alice:notes", "--org", "acme.com", "--class", "knowledge", "--server", gql.URL}
		if asJSON {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		_ = root.Execute()
		if asJSON {
			js = out.String()
		} else {
			text = out.String()
		}
	}
	// key/value rows (4, no header), dependents (header + 1), blockers
	// (header + 1) = 8 lines; a forged row would add one.
	if n := len(strings.Split(strings.TrimRight(text, "\n"), "\n")); n != 8 {
		t.Fatalf("%d lines, want 8 (a value forged or hid a row):\n%s", n, text)
	}
	for i := 0; i < len(text); i++ {
		if c := text[i]; (c < 0x20 && c != '\n') || c == 0x7f {
			t.Fatalf("control byte %#x in text output:\n%q", c, text)
		}
	}
	if got := strings.Count(text, evilWant1102); got < 4 {
		t.Fatalf("sanitized label seen %d times, want in memory/owner/class/dependent/blocker cells:\n%s", got, text)
	}
	if strings.Contains(text, strings.Repeat("x", 257)) || strings.Count(text, strings.Repeat("x", 255)+"…") < 3 {
		t.Fatalf("long values not cut (newUrn, handling, blocker message):\n%s", text)
	}
	assertJSONUnchanged1102(t, js, true, true)
}

func TestSafeCell1102MemoryValidate(t *testing.T) {
	findings := `{"kind":"` + evilJSON1102 + `","nodeId":"n1","nodeLoc":"` + evilJSON1102 + `","nodeUrn":null,"detail":"` + evilJSON1102 + strings.Repeat("y", 300) + `"},
		{"kind":"BROKEN_REF","nodeId":"n2","nodeLoc":"c:d","nodeUrn":null,"detail":"control"}`
	text, js := runBoth1102(t, map[string]string{
		"GetMemory":      `{"data":{"memory":{"id":"mem1","urn":"hrn:mem:o:m","name":"M","shortDescription":null,"class":"shared","visibility":"private","organizationId":"org1","isEncrypted":false,"maxRevCount":null,"updatedAt":"2026-08-05T00:00:00Z"}}}`,
		"ValidateMemory": validateResp(2, false, false, findings, ""),
	}, "memory", "validate", "o::m")
	for i := 0; i < len(text); i++ {
		if c := text[i]; (c < 0x20 && c != '\n') || c == 0x7f {
			t.Fatalf("control byte %#x in text output:\n%q", c, text)
		}
	}
	// Counts table (header + 2 kinds) and findings table (header + 2 rows):
	// the evil kind/loc/detail must each stay on one line.
	if got := strings.Count(text, evilWant1102); got < 4 {
		t.Fatalf("sanitized label seen %d times, want kind (counts + findings), loc, detail:\n%s", got, text)
	}
	if strings.Contains(text, strings.Repeat("y", 97)) || !strings.Contains(text, "…") {
		t.Fatalf("detail not cut at 96 runes:\n%s", text)
	}
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "b") && strings.Contains(l, "[31mc") {
			t.Fatalf("a newline forged a row: %q", l)
		}
	}
	assertJSONUnchanged1102(t, js, true, false)
}
