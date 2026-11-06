package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1716: the CLI companion of hadron-server#3426 (merged 6c9e93b9).
// `orgDefault` rides the template file like `required` (a key the file has is
// applied, an omitted key is unchanged, false clears), and `memory config get`
// shows the manager-only `defaultSkips`.

func orgDefaultTemplateJSON(orgDefault string) string {
	return strings.Replace(templateJSON("t9", "spec-rules", 7), `"required":false,`, `"required":false,"orgDefault":`+orgDefault+`,`, 1)
}

func TestTemplateGetShowsOrgDefault(t *testing.T) {
	for _, want := range []string{"true", "false"} {
		gql, _ := captureGraphQL(t, map[string]string{
			"MemoryConfigTemplate": `{"data":{"memoryConfigTemplate":` + orgDefaultTemplateJSON(want) + `}}`,
		})
		out, code := runConfig(t, gql.URL, "memory", "config", "template", "get", "t9", "--json")
		if code != exitcode.OK || !strings.Contains(out, `"orgDefault": `+want) {
			t.Fatalf("--json (exit %d) must print orgDefault %s:\n%s", code, want, out)
		}
		out, code = runConfig(t, gql.URL, "memory", "config", "template", "get", "t9")
		yes := map[string]string{"true": "yes", "false": "no"}[want]
		if code != exitcode.OK || !strings.Contains(out, "org default") || !strings.Contains(out, yes) {
			t.Fatalf("text (exit %d) must show an org default row of %q:\n%s", code, yes, out)
		}
	}
}

func TestTemplateListShowsOrgDefault(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"MemoryConfigTemplates": `{"data":{"memoryConfigTemplates":{"total":1,"items":[` + orgDefaultTemplateJSON("true") + `]}}}`,
	})
	out, code := runConfig(t, gql.URL, "memory", "config", "template", "list", "--json")
	if code != exitcode.OK || !strings.Contains(out, `"orgDefault": true`) {
		t.Fatalf("list --json (exit %d) must print orgDefault:\n%s", code, out)
	}
}

func TestTemplateOrgDefaultOnTheWire(t *testing.T) {
	sentInput := func(op, payloadOp, file string, args ...string) map[string]any {
		t.Helper()
		gql, captured := captureGraphQL(t, map[string]string{op: templatePayload(payloadOp, templateJSON("t9", "spec-rules", 8))})
		a := append([]string{"memory", "config", "template"}, args...)
		a = append(a, "--file", writeFile(t, file))
		if out, code := runConfig(t, gql.URL, a...); code != exitcode.OK {
			t.Fatalf("%s: exit %d\n%s", op, code, out)
		}
		in, _ := vars(t, captured[op])["input"].(map[string]any)
		return in
	}
	// Create: present true is sent, an omitted key is absent.
	in := sentInput("CreateMemoryConfigTemplate", "createMemoryConfigTemplate", `{"name":"a","orgDefault":true}`, "create", "--owner-org", "acme.com")
	if in["orgDefault"] != true {
		t.Errorf("create orgDefault = %v, want true", in["orgDefault"])
	}
	in = sentInput("CreateMemoryConfigTemplate", "createMemoryConfigTemplate", `{"name":"a"}`, "create", "--owner-org", "acme.com")
	if _, sent := in["orgDefault"]; sent {
		t.Errorf("create with the key omitted must not send orgDefault, got %v", in["orgDefault"])
	}
	// Update: false is sent (never dropped by omitempty), omitted is absent.
	in = sentInput("UpdateMemoryConfigTemplate", "updateMemoryConfigTemplate", `{"revision":7,"orgDefault":false}`, "update", "t9")
	if v, sent := in["orgDefault"]; !sent || v != false {
		t.Errorf("update orgDefault = %v (sent %v), want an explicit false", v, sent)
	}
	in = sentInput("UpdateMemoryConfigTemplate", "updateMemoryConfigTemplate", `{"revision":7,"name":"renamed"}`, "update", "t9")
	if _, sent := in["orgDefault"]; sent {
		t.Errorf("update with the key omitted must not send orgDefault, got %v", in["orgDefault"])
	}
}

// The clearing-description operation builds its input as a literal, so it must
// carry orgDefault too, or a file with both would silently drop the flag.
func TestTemplateOrgDefaultRidesTheClearingDescriptionUpdate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateMemoryConfigTemplateClearingDescription": templatePayload("updateMemoryConfigTemplate", templateJSON("t9", "spec-rules", 8)),
	})
	file := writeFile(t, `{"revision":7,"description":null,"orgDefault":false}`)
	if out, code := runConfig(t, gql.URL, "memory", "config", "template", "update", "t9", "--file", file); code != exitcode.OK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if v := vars(t, captured["UpdateMemoryConfigTemplateClearingDescription"]); v["orgDefault"] != false {
		t.Errorf("vars = %v, want orgDefault false in the same update", v)
	}
	// Omitted stays absent on the same operation.
	gql, captured = captureGraphQL(t, map[string]string{
		"UpdateMemoryConfigTemplateClearingDescription": templatePayload("updateMemoryConfigTemplate", templateJSON("t9", "spec-rules", 8)),
	})
	file = writeFile(t, `{"revision":7,"description":null}`)
	if _, code := runConfig(t, gql.URL, "memory", "config", "template", "update", "t9", "--file", file); code != exitcode.OK {
		t.Fatalf("exit %d", code)
	}
	if _, sent := vars(t, captured["UpdateMemoryConfigTemplateClearingDescription"])["orgDefault"]; sent {
		t.Errorf("an omitted orgDefault must not be sent on the clearing operation")
	}
}

func TestTemplateOrgDefaultFileEdges(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateMemoryConfigTemplate": templatePayload("updateMemoryConfigTemplate", templateJSON("t9", "spec-rules", 8)),
	})
	// A file whose only change is orgDefault is a change.
	if out, code := runConfig(t, gql.URL, "memory", "config", "template", "update", "t9", "--file", writeFile(t, `{"revision":7,"orgDefault":true}`)); code != exitcode.OK {
		t.Fatalf("orgDefault alone must be accepted: exit %d\n%s", code, out)
	}
	if _, sent := captured["UpdateMemoryConfigTemplate"]; !sent {
		t.Fatalf("the update was not sent")
	}
	// A null is a hand edit (get --json never prints it): refused before any call.
	gql2, captured2 := captureGraphQL(t, map[string]string{})
	out, code := runConfig(t, gql2.URL, "memory", "config", "template", "update", "t9", "--file", writeFile(t, `{"revision":7,"orgDefault":null}`))
	if code != exitcode.Usage || len(captured2) != 0 {
		t.Errorf("null orgDefault: exit %d, calls %v, want usage and no call\n%s", code, captured2, out)
	}
}

// The server's refusals are shown verbatim through the existing mapping.
func TestTemplateOrgDefaultRefusalsAreVerbatim(t *testing.T) {
	for reason, msg := range map[string]string{
		"ORG_DEFAULT_ORG_ONLY": "Only an organization-owned template can be an org default.",
		"ORG_DEFAULT_LIMIT":    "An organization may have at most 10 org default templates.",
	} {
		body := `{"errors":[{"message":"` + msg + `","extensions":{"code":"BAD_USER_INPUT","reason":"` + reason + `"}}]}`
		gql, _ := captureGraphQL(t, map[string]string{"UpdateMemoryConfigTemplate": body})
		out, code := runConfig(t, gql.URL, "memory", "config", "template", "update", "t9", "--file", writeFile(t, `{"revision":7,"orgDefault":true}`))
		if code != exitcode.Usage || !strings.Contains(out, msg) {
			t.Errorf("%s: exit %d, want %d with the server message verbatim:\n%s", reason, code, exitcode.Usage, out)
		}
	}
}

func skipsConfigJSON(skips string) string {
	return `{"data":{"memoryConfig":{"id":"cfg1","memoryId":"mem1","createdAt":null,"createdBy":null,"updatedAt":null,
		"updatedBy":null,"rules":[],"defaultSkips":` + skips + `}}}`
}

func TestMemoryConfigGetShowsDefaultSkips(t *testing.T) {
	// None: [] and never null, and no section in the text.
	gql, _ := captureGraphQL(t, map[string]string{"MemoryConfig": skipsConfigJSON(`[]`)})
	out, code := runConfig(t, gql.URL, "memory", "config", "get", configMemoryRef, "--json")
	if code != exitcode.OK || !strings.Contains(out, `"defaultSkips": []`) {
		t.Fatalf("exit %d, want defaultSkips [] on the raw output:\n%s", code, out)
	}
	out, _ = runConfig(t, gql.URL, "memory", "config", "get", configMemoryRef)
	if strings.Contains(out, "Skipped org defaults") {
		t.Errorf("no skips must print no section:\n%s", out)
	}

	skip := `{"role":"spec","field":"authorTask","reason":"REFERENCE_UNAVAILABLE","ref":null,"refState":"BROKEN",
		"createdAt":"2026-11-05T00:00:00Z","sourceTemplate":{"id":"t9","name":"org-default","deleted":false}}`
	gql, _ = captureGraphQL(t, map[string]string{"MemoryConfig": skipsConfigJSON(`[` + skip + `]`)})
	out, code = runConfig(t, gql.URL, "memory", "config", "get", configMemoryRef, "--json")
	for _, want := range []string{`"defaultSkips": [`, `"role": "spec"`, `"field": "authorTask"`, `"reason": "REFERENCE_UNAVAILABLE"`, `"refState": "BROKEN"`, `"ref": null`, `"org-default"`} {
		if code != exitcode.OK || !strings.Contains(out, want) {
			t.Fatalf("exit %d: json missing %s:\n%s", code, want, out)
		}
	}
	out, code = runConfig(t, gql.URL, "memory", "config", "get", configMemoryRef)
	for _, want := range []string{"Skipped org defaults", "spec", "authorTask", "REFERENCE_UNAVAILABLE", "BROKEN", "org-default"} {
		if code != exitcode.OK || !strings.Contains(out, want) {
			t.Fatalf("exit %d: text missing %q:\n%s", code, want, out)
		}
	}
}

// A non-manager is refused before the server returns any skip: exit 4, and the
// CLI adds no disclosure of its own.
func TestMemoryConfigGetDefaultSkipsNonManagerPrintsNothing(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"MemoryConfig": gqlError("MEMORY_NOT_FOUND", `"ref":"hrn:mem:acme.com:kb"`)})
	out, code := runConfig(t, gql.URL, "memory", "config", "get", configMemoryRef, "--json")
	if code != exitcode.NotFound || strings.Contains(out, "defaultSkips") || strings.Contains(out, "Skipped") {
		t.Errorf("exit %d (want %d) and no skip output:\n%s", code, exitcode.NotFound, out)
	}
}
