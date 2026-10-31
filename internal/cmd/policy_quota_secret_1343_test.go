package cmd

// cli#1343: policy explain/set, quota counters/limits, single grant reads and
// secret read + rotation. Each test fails on main because the command (or the
// org update --policy flag) does not exist, so the operation is never sent.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// run1343 executes the root command against a capturing fake and returns the
// stdout text and the error.
func run1343(t *testing.T, responses map[string]string, stdin string, args ...string) (string, map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, out := testFactory(t)
	if stdin != "" {
		f.IOStreams.In = strings.NewReader(stdin)
	}
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", gql.URL))
	err := root.Execute()
	return out.String(), captured, err
}

// vars1343 decodes one operation's captured variables into a generic map, so
// a test can tell an omitted key from an explicit null.
func vars1343(t *testing.T, captured map[string]json.RawMessage, op string) map[string]json.RawMessage {
	t.Helper()
	raw, ok := captured[op]
	if !ok {
		t.Fatalf("operation %s was not sent (captured: %v)", op, keys1343(captured))
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode %s vars: %v", op, err)
	}
	return m
}

func keys1343(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ---- secret get ----

func TestSecretGet1343SendsRefAndNeverAValue(t *testing.T) {
	out, captured, err := run1343(t, map[string]string{
		"Secret": `{"data":{"secret":` + secretJSON + `}}`,
	}, "", "secret", "get", "hrn:secret:alice:github-token", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	v := vars1343(t, captured, "Secret")
	if string(v["ref"]) != `"hrn:secret:alice:github-token"` {
		t.Fatalf("ref = %s", v["ref"])
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json is not an object: %v\n%s", err, out)
	}
	if got["name"] != "github-token" || got["id"] != "sec_1" {
		t.Fatalf("unexpected DTO: %v", got)
	}
	if _, has := got["value"]; has {
		t.Fatalf("secret get must never carry a value field: %v", got)
	}
}

func TestSecretGet1343NullIsNotFound(t *testing.T) {
	_, _, err := run1343(t, map[string]string{
		"Secret": `{"data":{"secret":null}}`,
	}, "", "secret", "get", "sec_missing")
	wantExit(t, err, exitcode.NotFound)
}

// ---- secret update ----

func TestSecretUpdate1343GenericValueOnlyOmitsMetadata(t *testing.T) {
	out, captured, err := run1343(t, map[string]string{
		"UpdateSecret": `{"data":{"updateSecret":` + secretJSON + `}}`,
	}, "rotated-value\n", "secret", "update", "sec_1", "--value-file", "-")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	v := vars1343(t, captured, "UpdateSecret")
	if string(v["ref"]) != `"sec_1"` || string(v["value"]) != `"rotated-value"` {
		t.Fatalf("vars = %v", v)
	}
	if _, has := v["metadata"]; has {
		t.Fatalf("metadata must be omitted (preserve) when not passed: %v", v)
	}
	if strings.Contains(out, "rotated-value") {
		t.Fatalf("update output leaked the value:\n%s", out)
	}
}

func TestSecretUpdate1343MetadataOnlyOmitsValue(t *testing.T) {
	_, captured, err := run1343(t, map[string]string{
		"UpdateSecret": `{"data":{"updateSecret":` + secretJSON + `}}`,
	}, "", "secret", "update", "sec_1", "--meta", "env=prod")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	v := vars1343(t, captured, "UpdateSecret")
	if _, has := v["value"]; has {
		t.Fatalf("value must be omitted (preserve) when --value-file is not passed: %v", v)
	}
	if string(v["metadata"]) != `{"env":"prod"}` {
		t.Fatalf("metadata = %s", v["metadata"])
	}
}

func TestSecretUpdate1343WebFetchBearerValue(t *testing.T) {
	_, captured, err := run1343(t, map[string]string{
		"UpdateSecret": `{"data":{"updateSecret":` + secretJSON + `}}`,
	}, "new-token\n", "secret", "update", "sec_1", "--type", "bearer", "--value-file", "-",
		"--url-prefix", "https://api.example.test/v2/")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	v := vars1343(t, captured, "UpdateSecret")
	var value map[string]string
	_ = json.Unmarshal(v["value"], &value)
	if value["type"] != "bearer" || value["token"] != "new-token" {
		t.Fatalf("value = %s", v["value"])
	}
	if string(v["metadata"]) != `{"urlPrefix":"https://api.example.test/v2/"}` {
		t.Fatalf("metadata = %s", v["metadata"])
	}
}

func TestSecretUpdate1343Refusals(t *testing.T) {
	cases := map[string][]string{
		"nothing to change":      {"secret", "update", "sec_1"},
		"name is immutable":      {"secret", "update", "sec_1", "--name", "other"},
		"kind is immutable":      {"secret", "update", "sec_1", "--kind", "generic"},
		"meta with webfetch":     {"secret", "update", "sec_1", "--meta", "a=b", "--url-prefix", "https://x.test/"},
		"type without value":     {"secret", "update", "sec_1", "--type", "bearer"},
		"meta and meta-json":     {"secret", "update", "sec_1", "--meta", "a=b", "--meta-json", `{"c":"d"}`},
		"meta-json not object":   {"secret", "update", "sec_1", "--meta-json", `["x"]`},
		"basic without username": {"secret", "update", "sec_1", "--type", "basic", "--value-file", "-"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, captured, err := run1343(t, map[string]string{
				"UpdateSecret": `{"data":{"updateSecret":` + secretJSON + `}}`,
			}, "x\n", args...)
			wantExit(t, err, exitcode.Usage)
			if _, sent := captured["UpdateSecret"]; sent {
				t.Fatalf("a refused update must not reach the server")
			}
		})
	}
}

// ---- grant get / connection grant get ----

func TestGrantGet1343(t *testing.T) {
	out, captured, err := run1343(t, map[string]string{
		"PrincipalGrant": `{"data":{"principalGrant":{"id":"g1","principalType":"USER","principalId":"u1",
			"principalHandle":"alice","organizationId":"o1","organizationUrn":"hrn:org:acme.com",
			"actions":["memory.*"],"expiresAt":null,"createdAt":"2026-10-01T00:00:00Z"}}}`,
	}, "", "grant", "get", "g1", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if string(vars1343(t, captured, "PrincipalGrant")["ref"]) != `"g1"` {
		t.Fatalf("ref not sent")
	}
	if !strings.Contains(out, `"actions": [`) || !strings.Contains(out, `"memory.*"`) {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestGrantGet1343NullIsNotFound(t *testing.T) {
	_, _, err := run1343(t, map[string]string{
		"PrincipalGrant": `{"data":{"principalGrant":null}}`,
	}, "", "grant", "get", "g_missing")
	wantExit(t, err, exitcode.NotFound)
}

func TestConnectionGrantGet1343(t *testing.T) {
	out, captured, err := run1343(t, map[string]string{
		"ConnectionGrant": `{"data":{"connectionGrant":{"id":"cg1","connectionId":"c1","granteeAppId":"a1",
			"granteeAppName":"Assistant","granteeAppUrn":"hrn:app:acme.com:assistant","scopes":["mail.read"],
			"expiresAt":null,"createdAt":"2026-10-01T00:00:00Z"}}}`,
	}, "", "connection", "grant", "get", "cg1", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if string(vars1343(t, captured, "ConnectionGrant")["ref"]) != `"cg1"` {
		t.Fatalf("ref not sent")
	}
	if !strings.Contains(out, `"granteeAppUrn": "hrn:app:acme.com:assistant"`) {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestConnectionGrantGet1343NullIsNotFound(t *testing.T) {
	_, _, err := run1343(t, map[string]string{
		"ConnectionGrant": `{"data":{"connectionGrant":null}}`,
	}, "", "connection", "grant", "get", "cg_missing")
	wantExit(t, err, exitcode.NotFound)
}

// ---- policy explain / policy set ----

const policyExplainJSON = `{"data":{"effectiveActionPolicy":{"allowed":false,"layers":[
	{"layer":"org","present":true,"allowed":false,"allow":["run.execute"]},
	{"layer":"user","present":false,"allowed":true,"allow":null},
	{"layer":"app","present":true,"allowed":true,"allow":[]}]}}}`

func TestPolicyExplain1343(t *testing.T) {
	out, captured, err := run1343(t, map[string]string{"EffectiveActionPolicy": policyExplainJSON},
		"", "policy", "explain", "--action", "comm.outbound", "--app", "hrn:app:acme.com:bot", "--user", "alice", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	v := vars1343(t, captured, "EffectiveActionPolicy")
	if string(v["action"]) != `"comm.outbound"` || string(v["appRef"]) != `"hrn:app:acme.com:bot"` || string(v["userRef"]) != `"alice"` {
		t.Fatalf("vars = %v", v)
	}
	var got struct {
		Allowed bool `json:"allowed"`
		Layers  []struct {
			Layer   string    `json:"layer"`
			Present bool      `json:"present"`
			Allowed bool      `json:"allowed"`
			Allow   *[]string `json:"allow"`
		} `json:"layers"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	if got.Allowed || len(got.Layers) != 3 || got.Layers[0].Layer != "org" || got.Layers[0].Allowed {
		t.Fatalf("unexpected DTO: %+v", got)
	}
	// An absent link and an empty allow-list differ: null vs [].
	if got.Layers[1].Allow != nil || got.Layers[2].Allow == nil || len(*got.Layers[2].Allow) != 0 {
		t.Fatalf("allow must keep null (no link) apart from [] (allows nothing):\n%s", out)
	}
}

func TestPolicyExplain1343OmitsUserWhenNotGiven(t *testing.T) {
	_, captured, err := run1343(t, map[string]string{"EffectiveActionPolicy": policyExplainJSON},
		"", "policy", "explain", "--action", "run.execute", "--app", "bot")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, has := vars1343(t, captured, "EffectiveActionPolicy")["userRef"]; has {
		t.Fatalf("userRef must be omitted when --user is not given")
	}
}

const updateMyPolicyJSON = `{"data":{"updateMyPolicy":{"id":"u1","handle":"alice","policy":{"allow":["run.execute"]}}}}`

func TestPolicySetMine1343SendsTheLink(t *testing.T) {
	_, captured, err := run1343(t, map[string]string{"UpdateMyPolicy": updateMyPolicyJSON},
		"", "policy", "set", "--mine", `{"allow":["run.execute"]}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := string(vars1343(t, captured, "UpdateMyPolicy")["policy"]); got != `{"allow":["run.execute"]}` {
		t.Fatalf("policy = %s", got)
	}
}

func TestPolicySetMine1343FromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"allow":["comm.outbound"]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, captured, err := run1343(t, map[string]string{"UpdateMyPolicy": updateMyPolicyJSON},
		"", "policy", "set", "--mine", "@"+path)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := string(vars1343(t, captured, "UpdateMyPolicy")["policy"]); got != `{"allow":["comm.outbound"]}` {
		t.Fatalf("policy = %s", got)
	}
}

func TestPolicySetMine1343ClearSendsExplicitNull(t *testing.T) {
	_, captured, err := run1343(t, map[string]string{"UpdateMyPolicy": updateMyPolicyJSON},
		"", "policy", "set", "--mine", "--clear")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	v := vars1343(t, captured, "UpdateMyPolicy")
	got, has := v["policy"]
	if !has || string(got) != "null" {
		t.Fatalf("--clear must send policy: null explicitly, got %v", v)
	}
}

func TestPolicySet1343Refusals(t *testing.T) {
	cases := map[string][]string{
		"no layer":          {"policy", "set", `{"allow":[]}`},
		"neither nor clear": {"policy", "set", "--mine"},
		"both":              {"policy", "set", "--mine", "--clear", `{"allow":[]}`},
		"not json":          {"policy", "set", "--mine", "{allow"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, captured, err := run1343(t, map[string]string{"UpdateMyPolicy": updateMyPolicyJSON}, "", args...)
			wantExit(t, err, exitcode.Usage)
			if _, sent := captured["UpdateMyPolicy"]; sent {
				t.Fatalf("a refused set must not reach the server")
			}
		})
	}
}

// ---- org update --policy ----

const orgUpdateJSON = `{"data":{"updateOrganization":{"id":"o1","urn":"hrn:org:acme.com","name":"Acme",
	"listedOnMarketplace":false,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-10-01T00:00:00Z"}}}`

func TestOrgUpdatePolicy1343(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string // "" = key omitted
	}{
		{"set", []string{"--policy", `{"allow":["run.execute"]}`}, `{"allow":["run.execute"]}`},
		{"empty clears", []string{"--policy", ""}, "null"},
		{"untouched by --name", []string{"--name", "Acme"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, captured, err := run1343(t, map[string]string{"UpdateOrganization": orgUpdateJSON}, "",
				append([]string{"org", "update", "acme.com"}, tc.args...)...)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			got, has := vars1343(t, captured, "UpdateOrganization")["policy"]
			if tc.want == "" {
				if has {
					t.Fatalf("policy must be omitted (preserve), got %s", got)
				}
				return
			}
			if !has || string(got) != tc.want {
				t.Fatalf("policy = %s (present %v), want %s", got, has, tc.want)
			}
		})
	}
}

func TestOrgUpdatePolicy1343RefusesBadJSON(t *testing.T) {
	_, captured, err := run1343(t, map[string]string{"UpdateOrganization": orgUpdateJSON}, "",
		"org", "update", "acme.com", "--policy", "{nope")
	wantExit(t, err, exitcode.Usage)
	if _, sent := captured["UpdateOrganization"]; sent {
		t.Fatalf("bad JSON must not reach the server")
	}
}

// ---- quota list / quota set ----

const quotaRowJSON = `{"id":"q1","scopeKind":"org","scopeId":"o1","action":"run.execute","window":"day",
	"used":3,"limit":100,"resetAt":"2026-11-01T00:00:00Z"}`

func TestQuotaList1343(t *testing.T) {
	out, captured, err := run1343(t, map[string]string{
		"QuotaCounters": `{"data":{"quotaCounters":[` + quotaRowJSON + `]}}`,
	}, "", "quota", "list", "--org", "acme.com", "--app", "bot", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	v := vars1343(t, captured, "QuotaCounters")
	if string(v["orgRef"]) != `"acme.com"` || string(v["appRef"]) != `"bot"` {
		t.Fatalf("vars = %v", v)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("--json rows: %v\n%s", err, out)
	}
	if rows[0]["used"] != float64(3) || rows[0]["limit"] != float64(100) || rows[0]["action"] != "run.execute" {
		t.Fatalf("row = %v", rows[0])
	}
}

func TestQuotaList1343EmptyIsArrayAndAppOmitted(t *testing.T) {
	out, captured, err := run1343(t, map[string]string{
		"QuotaCounters": `{"data":{"quotaCounters":[]}}`,
	}, "", "quota", "list", "--org", "acme.com", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, has := vars1343(t, captured, "QuotaCounters")["appRef"]; has {
		t.Fatalf("appRef must be omitted without --app")
	}
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty list must render [], got %q", out)
	}
}

func TestQuotaSet1343(t *testing.T) {
	_, captured, err := run1343(t, map[string]string{
		"SetQuotaLimit": `{"data":{"setQuotaLimit":` + quotaRowJSON + `}}`,
	}, "", "quota", "set", "--org", "acme.com", "--action", "run.execute", "--limit", "0")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	_ = json.Unmarshal(captured["SetQuotaLimit"], &vars)
	if string(vars.Input["orgRef"]) != `"acme.com"` || string(vars.Input["action"]) != `"run.execute"` || string(vars.Input["limit"]) != "0" {
		t.Fatalf("input = %v", vars.Input)
	}
	if _, has := vars.Input["appRef"]; has {
		t.Fatalf("appRef must be omitted for the org-level limit")
	}
}

func TestQuotaSet1343RequiresLimit(t *testing.T) {
	_, captured, err := run1343(t, map[string]string{
		"SetQuotaLimit": `{"data":{"setQuotaLimit":` + quotaRowJSON + `}}`,
	}, "", "quota", "set", "--org", "acme.com", "--action", "run.execute")
	wantExit(t, err, exitcode.Usage)
	if _, sent := captured["SetQuotaLimit"]; sent {
		t.Fatalf("a refused set must not reach the server")
	}
}
