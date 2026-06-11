package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// fakeGraphQL routes by operationName and asserts the Bearer header.
func fakeGraphQL(t *testing.T, responses map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("missing Bearer auth header, got %q", got)
		}
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		resp, ok := responses[body.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(server.Close)
	return server
}

type memStore map[string]string

func (memStore) Name() string { return "memory" }
func (m memStore) Get(host string) (string, error) {
	token, ok := m[host]
	if !ok {
		return "", store.ErrNotFound
	}
	return token, nil
}
func (m memStore) Set(host, token string) error { m[host] = token; return nil }
func (m memStore) Delete(host string) error     { delete(m, host); return nil }

func testFactory(t *testing.T) (*cmdutil.Factory, *strings.Builder) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_TOKEN", "hdr_user_test")
	t.Setenv("HADRON_SERVER", "")
	io, _, _ := output.Test()
	out := &strings.Builder{}
	errOut := &strings.Builder{}
	io.Out = out
	io.ErrOut = errOut
	f := &cmdutil.Factory{
		IOStreams:    io,
		HTTPClient:   http.DefaultClient,
		ConfigFn:     config.Load,
		TokenStoreFn: func() store.Store { return memStore{} },
	}
	// The server URL must be passed as --server in test args: cobra's
	// flag binding resets f.ServerFlag to its default at parse time.
	return f, out
}

func TestMemoryLsJSON(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"MyMemories": `{"data":{"myMemories":[
			{"id":"m1","urn":"acme.com::kb","name":"KB","shortDescription":null,
			 "class":"knowledge","visibility":"ORGANIZATION","organizationId":"o1",
			 "isEncrypted":false,"updatedAt":"2026-06-11T00:00:00Z"}]}}`,
	})
	f, out := testFactory(t)

	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var got []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(got) != 1 || got[0]["urn"] != "acme.com::kb" || got[0]["class"] != "knowledge" {
		t.Errorf("unexpected output: %s", out.String())
	}
}

func TestMemoryLsTable(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"MyMemories": `{"data":{"myMemories":[
			{"id":"m1","urn":"acme.com::kb","name":"KB","shortDescription":null,
			 "class":"knowledge","visibility":null,"organizationId":"o1",
			 "isEncrypted":false,"updatedAt":"2026-06-11T00:00:00Z"}]}}`,
	})
	f, out := testFactory(t)

	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "URN") || !strings.Contains(text, "acme.com::kb") {
		t.Errorf("unexpected table output:\n%s", text)
	}
}

func TestWhoami(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"Me": `{"data":{"me":{"id":"u1","name":"Holger","email":"h@example.com",
			"handle":null,"githubUsername":null,"roles":[]}}}`,
	})
	f, out := testFactory(t)

	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "whoami", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Holger") {
		t.Errorf("unexpected output: %s", out.String())
	}
}

func TestStubExitsUsage(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "ls"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("expected not-implemented error, got %v", err)
	}
}

func TestAgenticUsagePrintsDoc(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Exit codes") {
		t.Error("agentic-usage doc missing exit codes section")
	}
}
