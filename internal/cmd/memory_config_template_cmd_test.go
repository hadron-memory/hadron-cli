package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#716 slice 2: `memory config template list|get|create|update|rm` against
// hadron-server#1325 part (c) (#1369, candidate).

func templateRuleJSON(role, authorState string) string {
	author, authorID := "null", "null"
	if authorState == "OK" {
		author, authorID = `"hrn:node:acme.com:kb:tasks:write"`, `"0123456789abcdef0123456789abcdef"`
	}
	return `{"role":"` + role + `","enabled":true,"strictSubRoles":false,"writers":"ALL","validateBy":null,
		"authorTask":` + author + `,"authorTaskId":` + authorID + `,"authorTaskState":"` + authorState + `",
		"validationTask":null,"validationTaskId":null,"validationTaskState":"NONE",
		"descriptionNode":null,"descriptionNodeId":null,"descriptionNodeState":"NONE"}`
}

func templateJSON(id, name string, revision int, rules ...string) string {
	return `{"id":"` + id + `","name":"` + name + `","description":"spec rules","ownerType":"ORGANIZATION","ownerId":"org1",
		"required":false,"revision":` + strconv.Itoa(revision) + `,"createdAt":"2026-09-25T00:00:00Z","createdBy":"u1",
		"updatedAt":null,"updatedBy":null,"rules":[` + strings.Join(rules, ",") + `]}`
}

func templatePayload(op, tmpl string) string {
	return `{"data":{"` + op + `":{"template":` + tmpl + `,"warnings":[]}}}`
}

// writeFile writes a --file fixture and returns its path.
func writeFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "template.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The contract is "the templates you manage", not the first page of them.
func TestTemplateListPagesToExhaustion(t *testing.T) {
	page := func(n, from int) string {
		items := make([]string, 0, n)
		for i := 0; i < n; i++ {
			items = append(items, templateJSON("t"+strconv.Itoa(from+i), "tmpl-"+strconv.Itoa(from+i), 1))
		}
		return `{"data":{"memoryConfigTemplates":{"total":201,"items":[` + strings.Join(items, ",") + `]}}}`
	}
	calls := 0
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		calls++
		if calls == 1 {
			return page(200, 0)
		}
		return page(1, 200)
	})
	out, code := runConfig(t, gql.URL, "memory", "config", "template", "list", "--json")
	if code != exitcode.OK {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	var dto struct {
		Items []struct{ ID string } `json:"items"`
		Total int                   `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	if calls != 2 || len(dto.Items) != 201 || dto.Total != 201 {
		t.Errorf("calls = %d, items = %d, total = %d; want 2 pages and all 201", calls, len(dto.Items), dto.Total)
	}
	// The second page asked from where the first ended; no filter was sent.
	v := vars(t, captured["MemoryConfigTemplates"])
	if v["offset"] != float64(200) || v["limit"] != float64(200) {
		t.Errorf("last page vars = %v, want offset 200 limit 200", v)
	}
	if _, present := v["filter"]; present {
		t.Errorf("no owner flag: the filter must be OMITTED, got %v", v["filter"])
	}
}

// A set that shrinks while paging must END the loop, not spin it.
func TestTemplateListStopsOnAnEmptyPage(t *testing.T) {
	calls := 0
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		calls++
		return `{"data":{"memoryConfigTemplates":{"total":5,"items":[]}}}`
	})
	out, code := runConfig(t, gql.URL, "memory", "config", "template", "list", "--json")
	if code != exitcode.OK || calls != 1 {
		t.Fatalf("exit = %d, calls = %d; want 0 and one call", code, calls)
	}
	if !strings.Contains(out, `"items": []`) {
		t.Errorf("an empty list must render items: [] on the raw output:\n%s", out)
	}
}

func TestTemplateListOwnerFilter(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MemoryConfigTemplates": `{"data":{"memoryConfigTemplates":{"total":0,"items":[]}}}`,
	})
	if _, code := runConfig(t, gql.URL, "memory", "config", "template", "list", "--owner-org", "acme.com"); code != exitcode.OK {
		t.Fatalf("exit = %d", code)
	}
	filter, _ := vars(t, captured["MemoryConfigTemplates"])["filter"].(map[string]any)
	if filter["ownerType"] != "ORGANIZATION" || filter["ownerRef"] != "acme.com" {
		t.Errorf("filter = %v, want ORGANIZATION acme.com", filter)
	}

	for name, args := range map[string][]string{
		"two owners":        {"--owner-org", "acme.com", "--owner-me"},
		"empty --owner-org": {"--owner-org", ""},
		"empty --owner-app": {"--owner-app", ""},
	} {
		t.Run(name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{})
			_, code := runConfig(t, gql.URL, append([]string{"memory", "config", "template", "list"}, args...)...)
			if code != exitcode.Usage || len(captured) != 0 {
				t.Errorf("exit = %d, requests %v; want %d and none", code, keys(captured), exitcode.Usage)
			}
		})
	}
}

// A template you do not manage does not exist — exit 4, the id named.
func TestTemplateGetUnmanagedIsNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"MemoryConfigTemplate": `{"data":{"memoryConfigTemplate":null}}`})
	if _, code := runConfig(t, gql.URL, "memory", "config", "template", "get", "tmpl1"); code != exitcode.NotFound {
		t.Errorf("exit = %d, want %d", code, exitcode.NotFound)
	}
}

// create: exactly one owner, refused before any request otherwise. Server- and
// user-owned templates send NO ownerRef.
func TestTemplateCreateOwner(t *testing.T) {
	file := writeFile(t, `{"name":"spec-rules","rules":[]}`)
	for name, tc := range map[string]struct {
		args    []string
		want    string
		wantRef any
	}{
		"me":     {[]string{"--owner-me"}, "USER", nil},
		"server": {[]string{"--owner-server"}, "HADRON_SERVER", nil},
		"org":    {[]string{"--owner-org", "acme.com"}, "ORGANIZATION", "acme.com"},
		"app":    {[]string{"--owner-app", "acme.com:dev-team"}, "APP", "hrn:app:acme.com:dev-team"},
	} {
		t.Run(name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"CreateMemoryConfigTemplate": templatePayload("createMemoryConfigTemplate", templateJSON("t1", "spec-rules", 1)),
			})
			args := append([]string{"memory", "config", "template", "create", "--file", file}, tc.args...)
			if _, code := runConfig(t, gql.URL, args...); code != exitcode.OK {
				t.Fatalf("exit = %d", code)
			}
			v := vars(t, captured["CreateMemoryConfigTemplate"])
			ref, present := v["ownerRef"]
			if v["ownerType"] != tc.want || (tc.wantRef == nil && present) || (tc.wantRef != nil && ref != tc.wantRef) {
				t.Errorf("ownerType = %v, ownerRef = %v (present %v); want %s, %v", v["ownerType"], ref, present, tc.want, tc.wantRef)
			}
		})
	}
	for name, args := range map[string][]string{
		"no owner":   {},
		"two owners": {"--owner-me", "--owner-server"},
	} {
		t.Run(name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{})
			_, code := runConfig(t, gql.URL, append([]string{"memory", "config", "template", "create", "--file", file}, args...)...)
			if code != exitcode.Usage || len(captured) != 0 {
				t.Errorf("exit = %d, requests %v; want %d and none", code, keys(captured), exitcode.Usage)
			}
		})
	}
}

// `template get --json` is the file format: its server-owned keys are accepted
// and NOT sent, a reference is taken from its URN or else its id, and a rule
// the server wrote as ALL/AGENT is read back case-insensitively.
func TestTemplateCreateFromGetOutputRoundTrips(t *testing.T) {
	legacy := strings.Replace(templateRuleJSON("spec.rule", "OK"), `"authorTask":"hrn:node:acme.com:kb:tasks:write"`, `"authorTask":null`, 1)
	file := writeFile(t, templateJSON("t9", "spec-rules", 7, templateRuleJSON("spec", "OK"), legacy))
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMemoryConfigTemplate": templatePayload("createMemoryConfigTemplate", templateJSON("t1", "spec-rules", 1)),
	})
	if out, code := runConfig(t, gql.URL, "memory", "config", "template", "create", "--owner-me", "--file", file); code != exitcode.OK {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	input, _ := vars(t, captured["CreateMemoryConfigTemplate"])["input"].(map[string]any)
	if input["name"] != "spec-rules" || input["description"] != "spec rules" {
		t.Errorf("name/description = %v/%v, want them copied verbatim", input["name"], input["description"])
	}
	for _, k := range []string{"id", "revision", "ownerType", "ownerId", "createdAt"} {
		if _, sent := input[k]; sent {
			t.Errorf("server-owned key %q must not be sent", k)
		}
	}
	rules, _ := input["rules"].([]any)
	if len(rules) != 2 {
		t.Fatalf("rules = %v, want 2", input["rules"])
	}
	first, second := rules[0].(map[string]any), rules[1].(map[string]any)
	if first["authorTaskRef"] != "hrn:node:acme.com:kb:tasks:write" || first["writers"] != "ALL" {
		t.Errorf("rule 0 = %v, want the author URN and writers ALL", first)
	}
	if second["authorTaskRef"] != "0123456789abcdef0123456789abcdef" {
		t.Errorf("rule 1 = %v, want the author ID when its URN is null", second)
	}
	if _, sent := first["validationTaskRef"]; sent {
		t.Errorf("a NONE reference must be omitted, got %v", first["validationTaskRef"])
	}
}

// Refused before any request: a typo in a key, a reference the file cannot
// name (the server withheld it — sending nothing would DROP it), a missing name.
func TestTemplateCreateRefusesBadFilesLocally(t *testing.T) {
	for name, body := range map[string]string{
		"unknown key":          `{"name":"x","requird":true}`,
		"unknown rule key":     `{"name":"x","rules":[{"role":"spec","writer":"ALL"}]}`,
		"withheld reference":   `{"name":"x","rules":[{"role":"spec","authorTask":null,"authorTaskId":null,"authorTaskState":"UNREADABLE"}]}`,
		"broken reference":     `{"name":"x","rules":[{"role":"spec","validationTaskState":"BROKEN"}]}`,
		"no name":              `{"rules":[]}`,
		"rule without role":    `{"name":"x","rules":[{"writers":"ALL"}]}`,
		"bad writers":          `{"name":"x","rules":[{"role":"spec","writers":"everyone"}]}`,
		"bare loc as ref":      `{"name":"x","rules":[{"role":"spec","authorTask":"write-spec"}]}`,
		"description not text": `{"name":"x","description":7}`,
		// #735 review round 1 (Copilot, Codex):
		"OK without URN or id": `{"name":"x","rules":[{"role":"spec","authorTask":null,"authorTaskId":null,"authorTaskState":"OK"}]}`,
		"misspelled state":     `{"name":"x","rules":[{"role":"spec","authorTaskState":"UNREADBLE"}]}`,
		"lower-case state":     `{"name":"x","rules":[{"role":"spec","validationTaskState":"ok","validationTask":"hrn:node:acme.com:kb:tasks:check"}]}`,
		"trailing object":      `{"name":"good"}{"requird":true}`,
		"trailing garbage":     `{"name":"good"} trailing`,
	} {
		t.Run(name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{})
			_, code := runConfig(t, gql.URL, "memory", "config", "template", "create", "--owner-me", "--file", writeFile(t, body))
			if code != exitcode.Usage || len(captured) != 0 {
				t.Errorf("exit = %d, requests %v; want %d and none", code, keys(captured), exitcode.Usage)
			}
		})
	}
}

func TestTemplateCreateNameTakenIsConflict(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateMemoryConfigTemplate": gqlError("MEMORY_CONFIG_TEMPLATE_EXISTS", `"name":"spec-rules"`),
	})
	_, code := runConfig(t, gql.URL, "memory", "config", "template", "create", "--owner-me", "--file", writeFile(t, `{"name":"spec-rules"}`))
	if code != exitcode.Conflict {
		t.Errorf("exit = %d, want %d", code, exitcode.Conflict)
	}
}

// The update is guarded by the revision the FILE was read at — never a fresh
// read, which would certify a stale file as current.
func TestTemplateUpdateUsesTheFilesRevision(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateMemoryConfigTemplate": templatePayload("updateMemoryConfigTemplate", templateJSON("t9", "spec-rules", 8)),
	})
	file := writeFile(t, templateJSON("t9", "spec-rules", 7, templateRuleJSON("spec", "NONE")))
	if out, code := runConfig(t, gql.URL, "memory", "config", "template", "update", "t9", "--file", file); code != exitcode.OK {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if _, read := captured["MemoryConfigTemplate"]; read {
		t.Errorf("update must not re-read the template: the file's revision is the guard")
	}
	v := vars(t, captured["UpdateMemoryConfigTemplate"])
	if v["ref"] != "t9" || v["expectedRevision"] != float64(7) {
		t.Errorf("vars = %v, want ref t9 under the file's revision 7", v)
	}
}

func TestTemplateUpdateRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		args []string
	}{
		"no revision anywhere":       {`{"name":"x"}`, nil},
		"flag disagrees with file":   {`{"name":"x","revision":3}`, []string{"--expected-revision", "4"}},
		"file of another template":   {templateJSON("t-other", "x", 3), nil},
		"changes nothing":            {`{"revision":3}`, nil},
		"withheld reference in file": {`{"revision":3,"rules":[{"role":"spec","authorTaskState":"UNREADABLE"}]}`, nil},
	} {
		t.Run(name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{})
			args := append([]string{"memory", "config", "template", "update", "t9", "--file", writeFile(t, tc.body)}, tc.args...)
			if _, code := runConfig(t, gql.URL, args...); code != exitcode.Usage || len(captured) != 0 {
				t.Errorf("exit = %d, requests %v; want %d and none", code, keys(captured), exitcode.Usage)
			}
		})
	}
	t.Run("stale file", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{"UpdateMemoryConfigTemplate": gqlError("CONFLICT", `"currentRevision":8`)})
		if _, code := runConfig(t, gql.URL, "memory", "config", "template", "update", "t9", "--file", writeFile(t, `{"name":"x","revision":7}`)); code != exitcode.Conflict {
			t.Errorf("exit = %d, want %d", code, exitcode.Conflict)
		}
	})
}

// "rules": [] removes every rule, so the EMPTY list must reach the wire —
// omitempty would drop it with nil. A file without "rules" sends null, which
// the server reads as unchanged.
func TestTemplateUpdateEmptyRulesReachTheWire(t *testing.T) {
	for name, tc := range map[string]struct {
		body     string
		wantNull bool
	}{
		"rules: [] replaces with none": {`{"revision":3,"rules":[]}`, false},
		"no rules key leaves them":     {`{"revision":3,"name":"renamed"}`, true},
	} {
		t.Run(name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"UpdateMemoryConfigTemplate": templatePayload("updateMemoryConfigTemplate", templateJSON("t9", "x", 4)),
			})
			if _, code := runConfig(t, gql.URL, "memory", "config", "template", "update", "t9", "--file", writeFile(t, tc.body)); code != exitcode.OK {
				t.Fatalf("exit = %d", code)
			}
			input, _ := vars(t, captured["UpdateMemoryConfigTemplate"])["input"].(map[string]any)
			rules, present := input["rules"]
			switch {
			case !present:
				t.Errorf("rules must be on the wire (as [] or null), got it omitted: %v", input)
			case tc.wantNull && rules != nil:
				t.Errorf("rules = %v, want null (unchanged)", rules)
			case !tc.wantNull:
				if list, ok := rules.([]any); !ok || len(list) != 0 {
					t.Errorf("rules = %v, want an empty list", rules)
				}
			}
		})
	}
}

// "description": null clears it — through the literal-null operation, carrying
// every other key, in ONE update under the file's revision.
func TestTemplateUpdateClearingDescriptionIsOneUpdate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateMemoryConfigTemplateClearingDescription": templatePayload("updateMemoryConfigTemplate", templateJSON("t9", "renamed", 4)),
	})
	file := writeFile(t, `{"revision":3,"description":null,"name":"renamed","required":true}`)
	if _, code := runConfig(t, gql.URL, "memory", "config", "template", "update", "t9", "--file", file); code != exitcode.OK {
		t.Fatalf("exit = %d", code)
	}
	if _, sent := captured["UpdateMemoryConfigTemplate"]; sent {
		t.Fatalf("the ordinary update must not be sent beside the clearing one")
	}
	v := vars(t, captured["UpdateMemoryConfigTemplateClearingDescription"])
	if v["ref"] != "t9" || v["expectedRevision"] != float64(3) || v["name"] != "renamed" || v["required"] != true {
		t.Errorf("vars = %v, want ref, revision 3, name and required in the same update", v)
	}
}

func TestTemplateRm(t *testing.T) {
	t.Run("without --yes", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{"MemoryConfigTemplate": `{"data":{"memoryConfigTemplate":` + templateJSON("t9", "x", 5) + `}}`})
		if _, code := runConfig(t, gql.URL, "memory", "config", "template", "rm", "t9"); code != exitcode.Usage {
			t.Errorf("exit = %d, want %d", code, exitcode.Usage)
		}
		if _, sent := captured["DeleteMemoryConfigTemplate"]; sent {
			t.Errorf("the delete must not be sent without confirmation")
		}
	})
	t.Run("with --yes", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{
			"MemoryConfigTemplate":       `{"data":{"memoryConfigTemplate":` + templateJSON("t9", "x", 5) + `}}`,
			"DeleteMemoryConfigTemplate": `{"data":{"deleteMemoryConfigTemplate":true}}`,
		})
		if _, code := runConfig(t, gql.URL, "memory", "config", "template", "rm", "t9", "--yes"); code != exitcode.OK {
			t.Fatalf("exit = %d", code)
		}
		if v := vars(t, captured["DeleteMemoryConfigTemplate"]); v["ref"] != "t9" || v["expectedRevision"] != float64(5) {
			t.Errorf("vars = %v, want t9 under revision 5", v)
		}
	})
}

// The documented remedy for a withheld reference — set a new one — must still
// work with the old state key left in place, and a NONE reference may gain one.
func TestTemplateFileNewReferenceBesideAStaleState(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateMemoryConfigTemplate": templatePayload("createMemoryConfigTemplate", templateJSON("t1", "x", 1)),
	})
	body := `{"name":"x","rules":[{"role":"spec","authorTask":"hrn:node:acme.com:kb:tasks:new","authorTaskState":"UNREADABLE",
		"validationTaskId":"0123456789abcdef0123456789abcdef","validationTaskState":"NONE"}]}`
	if _, code := runConfig(t, gql.URL, "memory", "config", "template", "create", "--owner-me", "--file", writeFile(t, body)); code != exitcode.OK {
		t.Fatalf("exit = %d, want 0", code)
	}
	rule := vars(t, captured["CreateMemoryConfigTemplate"])["input"].(map[string]any)["rules"].([]any)[0].(map[string]any)
	if rule["authorTaskRef"] != "hrn:node:acme.com:kb:tasks:new" || rule["validationTaskRef"] != "0123456789abcdef0123456789abcdef" {
		t.Errorf("rule = %v, want both new references sent", rule)
	}
}
