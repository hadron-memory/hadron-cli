package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const secretJSON = `{"id":"sec_1","ownerType":"user","ownerId":"u1","name":"github-token","kind":"generic",
	"metadata":{"env":"dev"},"createdAt":"2026-07-15T00:00:00Z","createdBy":"u1",
	"updatedAt":"2026-07-15T00:00:00Z","updatedBy":"u1"}`

func TestSecretCreateGenericReadsValueFromStdin(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateSecret": `{"data":{"createSecret":` + secretJSON + `}}`,
	})
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader("sk-secret-123\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"secret", "create", "--name", "github-token", "--scope", "user",
		"--kind", "generic", "--value-file", "-", "--meta", "env=dev", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var vars struct {
		OwnerType string         `json:"ownerType"`
		OwnerRef  *string        `json:"ownerRef"`
		Name      string         `json:"name"`
		Kind      string         `json:"kind"`
		Metadata  map[string]any `json:"metadata"`
		Value     string         `json:"value"`
	}
	_ = json.Unmarshal(captured["CreateSecret"], &vars)
	if vars.OwnerType != "user" || vars.OwnerRef != nil || vars.Name != "github-token" || vars.Kind != "generic" {
		t.Fatalf("unexpected create vars: %+v", vars)
	}
	if vars.Value != "sk-secret-123" {
		t.Fatalf("secret value should come from stdin, got %q", vars.Value)
	}
	if vars.Metadata["env"] != "dev" {
		t.Fatalf("metadata = %#v, want env=dev", vars.Metadata)
	}
	if strings.Contains(out.String(), "sk-secret-123") {
		t.Fatalf("create output leaked secret value:\n%s", out.String())
	}
}

func TestSecretCreateWebFetchBearer(t *testing.T) {
	resp := strings.Replace(secretJSON, `"kind":"generic","metadata":{"env":"dev"}`, `"kind":"webfetch-auth","metadata":{"urlPrefix":"https://api.example.test/","type":"bearer"}`, 1)
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateSecret": `{"data":{"createSecret":` + resp + `}}`,
	})
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader("bearer-token\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"secret", "create", "--name", "poll-auth", "--scope", "app", "--owner", "acme.com::monitor",
		"--kind", "webfetch-auth", "--type", "bearer", "--url-prefix", "https://api.example.test/",
		"--value-file", "-", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var vars struct {
		OwnerType string            `json:"ownerType"`
		OwnerRef  string            `json:"ownerRef"`
		Kind      string            `json:"kind"`
		Metadata  map[string]string `json:"metadata"`
		Value     map[string]string `json:"value"`
	}
	_ = json.Unmarshal(captured["CreateSecret"], &vars)
	if vars.OwnerType != "app" || vars.OwnerRef != "acme.com::monitor" || vars.Kind != "webfetch-auth" {
		t.Fatalf("unexpected webfetch vars: %+v", vars)
	}
	if vars.Metadata["urlPrefix"] != "https://api.example.test/" {
		t.Fatalf("metadata = %#v", vars.Metadata)
	}
	if vars.Value["type"] != "bearer" || vars.Value["token"] != "bearer-token" {
		t.Fatalf("value payload = %#v", vars.Value)
	}
	if strings.Contains(out.String(), "bearer-token") {
		t.Fatalf("create output leaked bearer token:\n%s", out.String())
	}
}

func TestSecretLsNeverContainsValue(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Secrets": `{"data":{"secrets":{"items":[` + secretJSON + `],"total":1}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"secret", "ls", "--scope", "user", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("--json invalid: %v\n%s", err, out.String())
	}
	if len(got) != 1 || got[0]["name"] != "github-token" {
		t.Fatalf("unexpected ls output: %s", out.String())
	}
	if _, ok := got[0]["value"]; ok {
		t.Fatalf("secret list output must not include value: %#v", got[0])
	}
	var vars struct {
		OwnerType string  `json:"ownerType"`
		OwnerRef  *string `json:"ownerRef"`
	}
	_ = json.Unmarshal(captured["Secrets"], &vars)
	if vars.OwnerType != "user" || vars.OwnerRef != nil {
		t.Fatalf("unexpected ls vars: %+v", vars)
	}
}

func TestSecretRm(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"DeleteSecret": `{"data":{"deleteSecret":true}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"secret", "rm", "sec_1", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), `"status": "deleted"`) {
		t.Fatalf("unexpected rm output: %s", out.String())
	}
}

func TestSecretCreateValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "bad name",
			args: []string{"secret", "create", "--name", "has_underscore", "--scope", "user", "--kind", "generic"},
			want: "lowercase",
		},
		{
			name: "owner required",
			args: []string{"secret", "create", "--name", "x", "--scope", "app", "--kind", "generic"},
			want: "--owner is required",
		},
		{
			name: "missing value",
			args: []string{"secret", "create", "--name", "x", "--scope", "user", "--kind", "generic"},
			want: "no secret value",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", "http://127.0.0.1:1"))
			err := root.Execute()
			if got := exitcode.FromError(err); got != exitcode.Usage {
				t.Fatalf("exit code = %d, err=%v; want Usage", got, err)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want contains %q", err, tc.want)
			}
		})
	}
}
