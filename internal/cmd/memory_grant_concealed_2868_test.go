package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#2868 (hadron-server#4734, PR #4741; cor:agt:010:01, ruling 0222 extended by
// Holger's "Yes, null both"): for a write grant whose target memory is concealed
// from the viewer the server returns memoryUrn, prefix AND registerNodeUrn as null.
// A null prefix is the concealed case, not an incomplete row: the CLI shows the row,
// never rejects it, and never infers the withheld value. Fake server only, so this
// does not wait on #4741.

const concealedGrant2868 = `{"id":"g1","appId":"a1","memoryUrn":null,"prefix":null,"registerNodeUrn":null,"grantedBy":"u1","createdAt":"2026-10-31T00:00:00Z","revokedAt":"2026-10-31T01:00:00Z","revokedBy":"u1"}`

type grantRun2868 struct {
	name, op, response string
	args               []string
	list               bool
}

func grantRuns2868() []grantRun2868 {
	return []grantRun2868{
		{"revoke", "RevokeAppMemoryWriteGrant", `{"revokeAppMemoryWriteGrant":` + concealedGrant2868 + `}`, []string{"app", "memory-grant", "rm", "g1", "--yes"}, false},
		{"add", "SetAppMemoryWriteGrant", `{"setAppMemoryWriteGrant":` + concealedGrant2868 + `}`, []string{"app", "memory-grant", "add", "acme.com:team", "--memory", "acme.com:notes", "--prefix", "reports", "--yes"}, false},
		{"list", "AppMemoryWriteGrants", `{"appMemoryWriteGrants":[` + concealedGrant2868 + `]}`, []string{"app", "memory-grant", "list", "acme.com:team", "--include-revoked"}, true},
	}
}

// Acceptance 3 (red first): a concealed row is shown on every grant surface, in
// JSON with prefix, memoryUrn and registerNodeUrn all null. Red on main: the null
// prefix decodes to "" and the command fails "incomplete memory write grant".
func TestConcealedMemoryGrantRowIsShownAsJSON2868(t *testing.T) {
	for _, run := range grantRuns2868() {
		t.Run(run.name, func(t *testing.T) {
			srv, _ := captureGraphQL(t, map[string]string{run.op: `{"data":` + run.response + `}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{}, run.args...), "--json", "--server", srv.URL))
			if err := root.Execute(); err != nil {
				t.Fatalf("a concealed row must be shown, not rejected: %v", err)
			}
			var row map[string]any
			if run.list {
				var rows []map[string]any
				if err := json.Unmarshal([]byte(out.String()), &rows); err != nil || len(rows) != 1 {
					t.Fatalf("list --json: %v %q", err, out.String())
				}
				row = rows[0]
			} else if err := json.Unmarshal([]byte(out.String()), &row); err != nil {
				t.Fatalf("--json: %v %q", err, out.String())
			}
			for _, key := range []string{"memoryUrn", "prefix", "registerNodeUrn"} {
				if v, has := row[key]; !has || v != nil {
					t.Errorf("%s must be present and null (the server's own answer, never inferred), got %v present=%v", key, v, has)
				}
			}
			if row["id"] != "g1" || row["appId"] != "a1" {
				t.Errorf("the row keeps its identity: %v", row)
			}
		})
	}
}

// Text: memory, prefix AND register read "?" on a concealed row (the FIT condition:
// "-" would read as "this grant has no register node", which the viewer cannot know).
func TestConcealedMemoryGrantRowIsShownAsText2868(t *testing.T) {
	for _, run := range grantRuns2868() {
		t.Run(run.name, func(t *testing.T) {
			srv, _ := captureGraphQL(t, map[string]string{run.op: `{"data":` + run.response + `}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{}, run.args...), "--server", srv.URL))
			if err := root.Execute(); err != nil {
				t.Fatalf("a concealed row must be shown, not rejected: %v", err)
			}
			text := out.String()
			if run.list {
				var row string
				for _, l := range strings.Split(text, "\n") {
					if strings.HasPrefix(l, "g1") {
						row = l
					}
				}
				if row == "" {
					t.Fatalf("the row is listed: %q", text)
				}
				cells := strings.Fields(row)
				// ID, MEMORY, PREFIX, REGISTER, REVOKED(at, 1 cell)
				if len(cells) < 5 || cells[1] != "?" || cells[2] != "?" || cells[3] != "?" {
					t.Errorf("memory, prefix and register are ? on a concealed row: %q", row)
				}
				return
			}
			for _, want := range []string{"memory: ?\n", "prefix: ?\n", "register: ?\n"} {
				if !strings.Contains(text, want) {
					t.Errorf("text lacks %q:\n%s", want, text)
				}
			}
			if strings.Contains(text, "register: -") || strings.Contains(text, "prefix: -") {
				t.Errorf("'-' reads as 'none' on a row whose target is concealed:\n%s", text)
			}
		})
	}
}

// A VISIBLE row keeps its null register as "-" (no register node), and a visible
// prefix is printed as is: the "?" belongs to the concealed case only.
func TestVisibleMemoryGrantKeepsDashForNoRegister2868(t *testing.T) {
	visible := `{"id":"g1","appId":"a1","memoryUrn":"hrn:mem:acme.com:notes","prefix":"reports","registerNodeUrn":null,"grantedBy":"u1","createdAt":"2026-10-31T00:00:00Z","revokedAt":"2026-10-31T01:00:00Z","revokedBy":"u1"}`
	srv, _ := captureGraphQL(t, map[string]string{"RevokeAppMemoryWriteGrant": `{"data":{"revokeAppMemoryWriteGrant":` + visible + `}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "memory-grant", "rm", "g1", "--yes", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"memory: hrn:mem:acme.com:notes\n", "prefix: reports\n", "register: -\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("visible row text lacks %q:\n%s", want, out.String())
		}
	}
}

// The incomplete-grant check keeps id and appId: a row with no id (or no app) is
// still refused, and a null prefix is never the reason.
func TestIncompleteMemoryGrantStillRefusedWithoutIDOrApp2868(t *testing.T) {
	for name, row := range map[string]string{
		"no id":  strings.Replace(concealedGrant2868, `"id":"g1"`, `"id":""`, 1),
		"no app": strings.Replace(concealedGrant2868, `"appId":"a1"`, `"appId":""`, 1),
	} {
		srv, _ := captureGraphQL(t, map[string]string{"RevokeAppMemoryWriteGrant": `{"data":{"revokeAppMemoryWriteGrant":` + row + `}}`})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "memory-grant", "rm", "g1", "--yes", "--server", srv.URL})
		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), "incomplete memory write grant") {
			t.Errorf("%s: must still be refused as incomplete: %v", name, err)
		}
	}
}
