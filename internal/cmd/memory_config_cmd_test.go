package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#716 slice 1: `memory config get` and `memory config rule add|update|rm`
// against hadron-server#1325 part (b). The matrix is the one audited on the
// issue (A1–A15, with A7 dropped by H2 and A9/A11/A12 deferred to (c)/#1334).

const configMemoryRef = "hrn:mem:acme.com:kb"

// ruleJSON is one NodeRoleRule as the server returns it, with the given
// reference states. A reference whose state is not OK carries a null URN —
// the server never returns a URN beside BROKEN or UNREADABLE.
func ruleJSON(id, role string, revision int, authorState, validationState string) string {
	urn := func(state, loc string) string {
		if state == "OK" {
			return `"hrn:node:acme.com:kb:tasks:` + loc + `"`
		}
		return "null"
	}
	return `{"id":"` + id + `","role":"` + role + `","revision":` + itoa(revision) + `,"enabled":true,
		"strictSubRoles":false,"writers":"ALL","validateBy":"AGENT",
		"authorTask":` + urn(authorState, "write") + `,"authorTaskState":"` + authorState + `",
		"validationTask":` + urn(validationState, "check") + `,"validationTaskState":"` + validationState + `",
		"descriptionNode":null,"descriptionNodeState":"NONE","locked":false,
		"sourceTemplateId":null,"sourceTemplate":null,
		"createdAt":"2026-09-25T00:00:00Z","createdBy":"u1","updatedAt":null,"updatedBy":null}`
}

func configJSON(rules ...string) string {
	return `{"data":{"memoryConfig":{"id":"cfg1","memoryId":"mem1","createdAt":"2026-09-25T00:00:00Z",
		"createdBy":"u1","updatedAt":null,"updatedBy":null,"rules":[` + strings.Join(rules, ",") + `]}}}`
}

func gqlError(code string, ext string) string {
	if ext != "" {
		ext = "," + ext
	}
	return `{"errors":[{"message":"refused: ` + code + `","extensions":{"code":"` + code + `"` + ext + `}}]}`
}

// runConfig executes the command and returns stdout plus the exit code a USER
// gets (renderError, not exitcode.FromError — #537).
func runConfig(t *testing.T, serverURL string, args ...string) (string, int) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", serverURL))
	err := root.Execute()
	if err == nil {
		return out.String(), exitcode.OK
	}
	return out.String(), renderError(f, err)
}

func vars(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	if raw == nil {
		t.Fatalf("operation was not called")
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("captured vars not JSON: %v", err)
	}
	return v
}

// A1: a manager's memory with no rules is the EMPTY config — a success whose
// raw JSON says `rules: []` and `id: null`, never a refusal or a null list.
func TestMemoryConfigGetEmptyConfig(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MemoryConfig": `{"data":{"memoryConfig":{"id":null,"memoryId":"mem1","createdAt":null,"createdBy":null,
			"updatedAt":null,"updatedBy":null,"rules":[]}}}`,
	})
	out, code := runConfig(t, gql.URL, "memory", "config", "get", "acme.com::kb", "--json")
	if code != exitcode.OK {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, `"rules": []`) || !strings.Contains(out, `"id": null`) {
		t.Errorf("empty config must render rules [] and id null on the raw output:\n%s", out)
	}
	// The memory ref is canonicalized, not pre-resolved: the server resolves it.
	if got := vars(t, captured["MemoryConfig"])["memoryRef"]; got != configMemoryRef {
		t.Errorf("memoryRef = %v, want the canonical %s", got, configMemoryRef)
	}

	out, code = runConfig(t, gql.URL, "memory", "config", "get", configMemoryRef)
	if code != exitcode.OK || !strings.Contains(out, "no rules") {
		t.Errorf("human output = %q (exit %d), want a 'no rules' line and exit 0", out, code)
	}
}

// A1 (non-manager): the server answers exactly as for a missing memory. That
// is exit 4 with nothing on stdout — never "no rules", which would be a claim
// the server did not make.
func TestMemoryConfigGetNonManagerIsConcealedNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"MemoryConfig": gqlError("MEMORY_NOT_FOUND", `"ref":"hrn:mem:acme.com:kb"`),
	})
	out, code := runConfig(t, gql.URL, "memory", "config", "get", configMemoryRef)
	if code != exitcode.NotFound {
		t.Errorf("exit = %d, want %d (concealed not-found)", code, exitcode.NotFound)
	}
	if strings.Contains(out, "no rules") {
		t.Errorf("a refusal must never render as the empty config:\n%s", out)
	}
}

// A2: the four reference states. BROKEN and UNREADABLE both withhold the URN,
// and they must not render the same way — they are different facts with
// different remedies.
func TestMemoryConfigGetRendersEachReferenceState(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"MemoryConfig": configJSON(
			ruleJSON("r1", "spec", 3, "OK", "BROKEN"),
			ruleJSON("r2", "spec.rule", 1, "UNREADABLE", "NONE"),
		),
	})
	out, code := runConfig(t, gql.URL, "memory", "config", "get", configMemoryRef, "--json")
	if code != exitcode.OK {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	var dto struct {
		Rules []struct {
			Role                string  `json:"role"`
			AuthorTask          *string `json:"authorTask"`
			AuthorTaskState     string  `json:"authorTaskState"`
			ValidationTask      *string `json:"validationTask"`
			ValidationTaskState string  `json:"validationTaskState"`
		} `json:"rules"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out)
	}
	if len(dto.Rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(dto.Rules))
	}
	if r := dto.Rules[0]; r.AuthorTask == nil || r.AuthorTaskState != "OK" || r.ValidationTask != nil || r.ValidationTaskState != "BROKEN" {
		t.Errorf("rule 0 = %+v, want OK author with a URN and BROKEN validation without one", r)
	}
	if r := dto.Rules[1]; r.AuthorTask != nil || r.AuthorTaskState != "UNREADABLE" || r.ValidationTaskState != "NONE" {
		t.Errorf("rule 1 = %+v, want UNREADABLE author without a URN", r)
	}

	text, _ := runConfig(t, gql.URL, "memory", "config", "get", configMemoryRef)
	for _, want := range []string{"hrn:node:acme.com:kb:tasks:write", "BROKEN — the node was deleted", "UNREADABLE — it exists but you may not read it"} {
		if !strings.Contains(text, want) {
			t.Errorf("human output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "tasks:check") {
		t.Errorf("a BROKEN reference must not render a URN:\n%s", text)
	}
}

// add sends only the fields given (omitempty), the role VERBATIM, and each
// reference canonicalized but not resolved — the server resolves it.
func TestMemoryConfigRuleAddSendsOnlyGivenFields(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNodeRoleRule": `{"data":{"createNodeRoleRule":{"rule":` + ruleJSON("r1", "spec", 1, "OK", "NONE") + `,"warnings":[]}}}`,
	})
	out, code := runConfig(t, gql.URL, "memory", "config", "rule", "add", "acme.com::kb", "spec",
		"--author-task", "acme.com::kb::tasks:write", "--writers", "Admin", "--json")
	if code != exitcode.OK {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	v := vars(t, captured["CreateNodeRoleRule"])
	if v["memoryRef"] != configMemoryRef {
		t.Errorf("memoryRef = %v", v["memoryRef"])
	}
	input, _ := v["input"].(map[string]any)
	want := map[string]any{"role": "spec", "authorTaskRef": "hrn:node:acme.com::kb::tasks:write", "writers": "ADMIN"}
	if len(input) != len(want) {
		t.Errorf("input = %v, want exactly %v (unset flags must be OMITTED, not null)", input, want)
	}
	for k, w := range want {
		if input[k] != w {
			t.Errorf("input.%s = %v, want %v", k, input[k], w)
		}
	}
	// A15: every array renders as [], never null.
	if !strings.Contains(out, `"warnings": []`) {
		t.Errorf("warnings must render as [] when empty:\n%s", out)
	}
}

func TestMemoryConfigRuleAddSendsRoleVerbatim(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNodeRoleRule": gqlError("INVALID_NODE_ROLE", `"role":" spec","reason":"BAD_SEGMENT","maxLength":64`),
	})
	_, code := runConfig(t, gql.URL, "memory", "config", "rule", "add", configMemoryRef, " spec")
	if code != exitcode.Usage {
		t.Errorf("INVALID_NODE_ROLE exit = %d, want %d", code, exitcode.Usage)
	}
	// Nothing is trimmed or lower-cased client-side (team chat #1826): the
	// server judges the role exactly as given.
	if role := vars(t, captured["CreateNodeRoleRule"])["input"].(map[string]any)["role"]; role != " spec" {
		t.Errorf("role = %q, want it sent verbatim", role)
	}
}

// Refused before any request: a malformed value is the caller's to fix, and a
// permissive parse would only fail later, further from the cause.
func TestMemoryConfigRuleAddRefusesBadFlagsLocally(t *testing.T) {
	for name, args := range map[string][]string{
		"empty author task":     {"--author-task", ""},
		"empty validate-by":     {"--validate-by", ""},
		"unknown writers":       {"--writers", "everyone"},
		"unknown validate-by":   {"--validate-by", "human"},
		"unqualified task ref":  {"--author-task", "tasks:write"},
		"single-colon task ref": {"--validation-task", "acme.com:kb:tasks:check"},
	} {
		t.Run(name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{})
			_, code := runConfig(t, gql.URL, append([]string{"memory", "config", "rule", "add", configMemoryRef, "spec"}, args...)...)
			if code != exitcode.Usage {
				t.Errorf("exit = %d, want %d", code, exitcode.Usage)
			}
			if len(captured) != 0 {
				t.Errorf("no request may be sent for a refused flag, got %v", keys(captured))
			}
		})
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A3, A6, and the validate-by precondition: each server refusal reaches the
// user as its documented exit code.
func TestMemoryConfigRuleAddServerRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		resp string
		want int
	}{
		"role exists":            {gqlError("NODE_ROLE_RULE_EXISTS", `"role":"spec"`), exitcode.Conflict},
		"not a task":             {gqlError("NOT_A_TASK", `"field":"authorTask"`), exitcode.Usage},
		"missing or unreadable":  {gqlError("NODE_NOT_FOUND", `"ref":"hrn:node:acme.com:kb:tasks:write"`), exitcode.NotFound},
		"not a manager":          {gqlError("MEMORY_NOT_FOUND", `"ref":"hrn:mem:acme.com:kb"`), exitcode.NotFound},
		"validate-by needs task": {gqlError("BAD_USER_INPUT", `"field":"validateBy","reason":"VALIDATION_TASK_REQUIRED"`), exitcode.Usage},
	} {
		t.Run(name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{"CreateNodeRoleRule": tc.resp})
			_, code := runConfig(t, gql.URL, "memory", "config", "rule", "add", configMemoryRef, "spec",
				"--author-task", "hrn:node:acme.com:kb:tasks:write")
			if code != tc.want {
				t.Errorf("exit = %d, want %d", code, tc.want)
			}
		})
	}
}

// A4 + A10's precondition: update reads the rule, then sends ONLY the given
// field, under the rule's id and the revision it just read.
func TestMemoryConfigRuleUpdateSendsOnlyGivenFieldsUnderReadRevision(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MemoryConfig":       configJSON(ruleJSON("r1", "spec", 7, "OK", "OK"), ruleJSON("r2", "spec.rule", 2, "NONE", "NONE")),
		"UpdateNodeRoleRule": `{"data":{"updateNodeRoleRule":{"rule":` + ruleJSON("r1", "spec", 8, "OK", "OK") + `,"warnings":[]}}}`,
	})
	out, code := runConfig(t, gql.URL, "memory", "config", "rule", "update", configMemoryRef, "spec", "--writers", "admin", "--json")
	if code != exitcode.OK {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	v := vars(t, captured["UpdateNodeRoleRule"])
	if v["ref"] != "r1" {
		t.Errorf("ref = %v, want the id of the rule for role spec", v["ref"])
	}
	if v["expectedRevision"] != float64(7) {
		t.Errorf("expectedRevision = %v, want the revision just read (7)", v["expectedRevision"])
	}
	input, _ := v["input"].(map[string]any)
	if len(input) != 1 || input["writers"] != "ADMIN" {
		t.Errorf("input = %v, want exactly {writers: ADMIN}", input)
	}
	if _, called := captured["UpdateNodeRoleRuleClearingValidateBy"]; called {
		t.Errorf("no clear was asked for, so the literal-null operation must not be used")
	}
}

// A5: an EMPTY reference flag is an explicit clear — sent as "", not omitted.
// A false boolean is sent too: it is a value, not an absence.
func TestMemoryConfigRuleUpdateEmptyRefClears(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MemoryConfig":       configJSON(ruleJSON("r1", "spec", 3, "OK", "OK")),
		"UpdateNodeRoleRule": `{"data":{"updateNodeRoleRule":{"rule":` + ruleJSON("r1", "spec", 4, "NONE", "OK") + `,"warnings":[]}}}`,
	})
	_, code := runConfig(t, gql.URL, "memory", "config", "rule", "update", configMemoryRef, "spec",
		"--author-task", "", "--strict-sub-roles=false")
	if code != exitcode.OK {
		t.Fatalf("exit = %d", code)
	}
	input, _ := vars(t, captured["UpdateNodeRoleRule"])["input"].(map[string]any)
	if got, present := input["authorTaskRef"]; !present || got != "" {
		t.Errorf("authorTaskRef = %v (present %v), want an explicit \"\" clear", got, present)
	}
	if got, present := input["strictSubRoles"]; !present || got != false {
		t.Errorf("strictSubRoles = %v (present %v), want an explicit false", got, present)
	}
	if len(input) != 2 {
		t.Errorf("input = %v, want exactly the two given fields", input)
	}
}

// Clearing validateBy needs an explicit null, which only the literal-null
// operation sends. Every other given field must ride in that SAME operation
// under the same revision — never a clear followed by a second save (#1888).
func TestMemoryConfigRuleUpdateClearValidateByIsOneUpdate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MemoryConfig": configJSON(ruleJSON("r1", "spec", 5, "OK", "OK")),
		"UpdateNodeRoleRuleClearingValidateBy": `{"data":{"updateNodeRoleRule":{"rule":` + ruleJSON("r1", "spec", 6, "OK", "NONE") +
			`,"warnings":[]}}}`,
	})
	_, code := runConfig(t, gql.URL, "memory", "config", "rule", "update", configMemoryRef, "spec",
		"--validate-by", "", "--validation-task", "", "--writers", "owner")
	if code != exitcode.OK {
		t.Fatalf("exit = %d", code)
	}
	if _, called := captured["UpdateNodeRoleRule"]; called {
		t.Fatalf("the ordinary update must not be sent beside the clearing one: that would be two saves")
	}
	v := vars(t, captured["UpdateNodeRoleRuleClearingValidateBy"])
	want := map[string]any{"ref": "r1", "expectedRevision": float64(5), "validationTaskRef": "", "writers": "OWNER"}
	if len(v) != len(want) {
		t.Errorf("vars = %v, want exactly %v (unset fields must be MISSING so the server reads them as absent)", v, want)
	}
	for k, w := range want {
		if v[k] != w {
			t.Errorf("%s = %v, want %v", k, v[k], w)
		}
	}
}

// The clear itself lives in the OPERATION TEXT, not the variables, so the
// variable assertions above cannot see it: captureGraphQL records only the
// variables (review:assert-the-query-not-the-capture). Assert the generated
// document — field-exact — carries the literal null, and that the ordinary
// update does not (there, an unset validateBy must stay ABSENT).
func TestMemoryConfigClearingOperationCarriesLiteralNull(t *testing.T) {
	literalNull := func(op string) bool {
		for _, line := range strings.Split(op, "\n") {
			for _, field := range strings.FieldsFunc(line, func(r rune) bool { return r == '{' || r == '}' || r == ',' || r == '(' || r == ')' }) {
				if strings.ReplaceAll(field, " ", "") == "validateBy:null" {
					return true
				}
			}
		}
		return false
	}
	if !literalNull(gen.UpdateNodeRoleRuleClearingValidateBy_Operation) {
		t.Errorf("the clearing operation must send validateBy as a literal null:\n%s", gen.UpdateNodeRoleRuleClearingValidateBy_Operation)
	}
	if literalNull(gen.UpdateNodeRoleRule_Operation) {
		t.Errorf("the ordinary update must never send validateBy: null — it would clear it on every update:\n%s", gen.UpdateNodeRoleRule_Operation)
	}
}

func TestMemoryConfigRuleUpdateClearValidateByAlone(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MemoryConfig": configJSON(ruleJSON("r1", "spec", 5, "OK", "OK")),
		"UpdateNodeRoleRuleClearingValidateBy": `{"data":{"updateNodeRoleRule":{"rule":` + ruleJSON("r1", "spec", 6, "OK", "OK") +
			`,"warnings":[]}}}`,
	})
	if _, code := runConfig(t, gql.URL, "memory", "config", "rule", "update", configMemoryRef, "spec", "--validate-by", ""); code != exitcode.OK {
		t.Fatalf("exit = %d", code)
	}
	v := vars(t, captured["UpdateNodeRoleRuleClearingValidateBy"])
	if len(v) != 2 || v["ref"] != "r1" || v["expectedRevision"] != float64(5) {
		t.Errorf("vars = %v, want only ref and expectedRevision", v)
	}
}

func TestMemoryConfigRuleUpdateRefusals(t *testing.T) {
	t.Run("no flags", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{})
		_, code := runConfig(t, gql.URL, "memory", "config", "rule", "update", configMemoryRef, "spec")
		if code != exitcode.Usage || len(captured) != 0 {
			t.Errorf("exit = %d, requests %v; want %d and none", code, keys(captured), exitcode.Usage)
		}
	})
	t.Run("writers cannot be cleared", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{})
		_, code := runConfig(t, gql.URL, "memory", "config", "rule", "update", configMemoryRef, "spec", "--writers", "")
		if code != exitcode.Usage || len(captured) != 0 {
			t.Errorf("exit = %d, requests %v; want %d and none", code, keys(captured), exitcode.Usage)
		}
	})
	t.Run("no rule for the role", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{"MemoryConfig": configJSON(ruleJSON("r1", "spec", 1, "NONE", "NONE"))})
		_, code := runConfig(t, gql.URL, "memory", "config", "rule", "update", configMemoryRef, "spec.draft", "--enabled=false")
		if code != exitcode.NotFound {
			t.Errorf("exit = %d, want %d", code, exitcode.NotFound)
		}
		if _, sent := captured["UpdateNodeRoleRule"]; sent {
			t.Errorf("no update may be sent for a role with no rule")
		}
	})
	// A10: someone changed the rule after it was read.
	t.Run("stale revision", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"MemoryConfig":       configJSON(ruleJSON("r1", "spec", 1, "NONE", "NONE")),
			"UpdateNodeRoleRule": gqlError("CONFLICT", `"currentRevision":2`),
		})
		_, code := runConfig(t, gql.URL, "memory", "config", "rule", "update", configMemoryRef, "spec", "--enabled=false")
		if code != exitcode.Conflict {
			t.Errorf("exit = %d, want %d", code, exitcode.Conflict)
		}
	})
}

// A13: save warnings are part of the payload and never change the exit code.
func TestMemoryConfigRuleWarningsAreReportedNotFatal(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateNodeRoleRule": `{"data":{"createNodeRoleRule":{"rule":` + ruleJSON("r1", "spec", 1, "UNREADABLE", "NONE") +
			`,"warnings":[{"code":"TASK_LESS_VISIBLE","role":"spec","field":"authorTask","taskUrn":null,"taskState":"UNREADABLE"}]}}}`,
	})
	out, code := runConfig(t, gql.URL, "memory", "config", "rule", "add", configMemoryRef, "spec",
		"--author-task", "hrn:node:acme.com:kb:tasks:write", "--json")
	if code != exitcode.OK {
		t.Fatalf("a warning must not change the exit code, got %d", code)
	}
	var dto struct {
		Warnings []struct {
			Code      string  `json:"code"`
			Field     *string `json:"field"`
			TaskURN   *string `json:"taskUrn"`
			TaskState *string `json:"taskState"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out)
	}
	if len(dto.Warnings) != 1 || dto.Warnings[0].Code != "TASK_LESS_VISIBLE" || dto.Warnings[0].TaskURN != nil ||
		dto.Warnings[0].TaskState == nil || *dto.Warnings[0].TaskState != "UNREADABLE" {
		t.Errorf("warnings = %+v, want the one TASK_LESS_VISIBLE warning with no URN", dto.Warnings)
	}
}

// A8: rm is destructive, so non-interactively it needs --yes and is refused
// BEFORE the delete is sent. With --yes it deletes under the revision read.
func TestMemoryConfigRuleRm(t *testing.T) {
	t.Run("without --yes", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{"MemoryConfig": configJSON(ruleJSON("r1", "spec", 4, "NONE", "NONE"))})
		_, code := runConfig(t, gql.URL, "memory", "config", "rule", "rm", configMemoryRef, "spec")
		if code != exitcode.Usage {
			t.Errorf("exit = %d, want %d", code, exitcode.Usage)
		}
		if _, sent := captured["DeleteNodeRoleRule"]; sent {
			t.Errorf("the delete must not be sent without confirmation")
		}
	})
	t.Run("with --yes", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{
			"MemoryConfig":       configJSON(ruleJSON("r1", "spec", 4, "NONE", "NONE")),
			"DeleteNodeRoleRule": `{"data":{"deleteNodeRoleRule":true}}`,
		})
		out, code := runConfig(t, gql.URL, "memory", "config", "rule", "rm", configMemoryRef, "spec", "--yes", "--json")
		if code != exitcode.OK {
			t.Fatalf("exit = %d\n%s", code, out)
		}
		v := vars(t, captured["DeleteNodeRoleRule"])
		if v["ref"] != "r1" || v["expectedRevision"] != float64(4) {
			t.Errorf("vars = %v, want ref r1 under revision 4", v)
		}
		var dto struct {
			MemoryID string `json:"memoryId"`
			Role     string `json:"role"`
			Deleted  bool   `json:"deleted"`
		}
		if err := json.Unmarshal([]byte(out), &dto); err != nil || !dto.Deleted || dto.Role != "spec" || dto.MemoryID != "mem1" {
			t.Errorf("output = %s (err %v)", out, err)
		}
	})
	t.Run("stale revision", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"MemoryConfig":       configJSON(ruleJSON("r1", "spec", 4, "NONE", "NONE")),
			"DeleteNodeRoleRule": gqlError("CONFLICT", `"currentRevision":5`),
		})
		if _, code := runConfig(t, gql.URL, "memory", "config", "rule", "rm", configMemoryRef, "spec", "--yes"); code != exitcode.Conflict {
			t.Errorf("exit = %d, want %d", code, exitcode.Conflict)
		}
	})
}
