package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
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
		_, _ = w.Write([]byte(translateFindNodes(body.OperationName, resp)))
	}))
	t.Cleanup(server.Close)
	return server
}

// translateFindNodes lets the many spec/node fakes keep their readable legacy
// `{"data":{"nodes":[...]}}` (and `{"data":{"nodeSearch":{…}}}`) response
// literals now that the CLI queries the unified `findNodes` field: it rewraps a
// legacy body into the findNodes hit envelope (each node under `hits[].node`,
// plus total/degraded/reason) that the client decodes. A body already in
// findNodes shape, an error body, or any non-FindNodes op passes through
// untouched.
func translateFindNodes(op, resp string) string {
	if op != "FindNodes" {
		return resp
	}
	// Decide by JSON structure, not a substring probe: a node field could
	// legitimately contain the text "findNodes", which would fool a naive
	// contains-check into skipping translation.
	var parsed struct {
		Errors json.RawMessage `json:"errors"`
		Data   struct {
			FindNodes  json.RawMessage   `json:"findNodes"`
			Nodes      []json.RawMessage `json:"nodes"`
			NodeSearch *struct {
				Degraded json.RawMessage   `json:"degraded"`
				Reason   json.RawMessage   `json:"reason"`
				Nodes    []json.RawMessage `json:"nodes"`
			} `json:"nodeSearch"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		return resp
	}
	// Already a findNodes envelope, or an error body — pass through untouched.
	if len(parsed.Errors) > 0 || len(parsed.Data.FindNodes) > 0 {
		return resp
	}
	nodes := parsed.Data.Nodes
	degraded, reason := "null", "null"
	if ns := parsed.Data.NodeSearch; ns != nil {
		nodes = ns.Nodes
		if len(ns.Degraded) > 0 {
			degraded = string(ns.Degraded)
		}
		if len(ns.Reason) > 0 {
			reason = string(ns.Reason)
		}
	}
	hits := make([]string, len(nodes))
	for i, n := range nodes {
		hits[i] = `{"score":null,"node":` + string(n) + `}`
	}
	return fmt.Sprintf(`{"data":{"findNodes":{"total":%d,"degraded":%s,"reason":%s,"hits":[%s]}}}`,
		len(hits), degraded, reason, strings.Join(hits, ","))
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
		"Memories": `{"data":{"memories":{"total":1,"items":[
			{"id":"m1","urn":"acme.com::kb","name":"KB","shortDescription":null,
			 "class":"knowledge","visibility":"ORGANIZATION","organizationId":"o1",
			 "isEncrypted":false,"updatedAt":"2026-06-11T00:00:00Z"}]}}}`,
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
		"Memories": `{"data":{"memories":{"total":1,"items":[
			{"id":"m1","urn":"acme.com::kb","name":"KB","shortDescription":null,
			 "class":"knowledge","visibility":null,"organizationId":"o1",
			 "isEncrypted":false,"updatedAt":"2026-06-11T00:00:00Z"}]}}}`,
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
		"AuthContext": `{"data":{"authContext":{"principalType":"USER","appId":null,"agentId":null,
			"user":{"id":"u1","name":"Holger","email":"h@example.com","handle":null,"githubUsername":null,"roles":[]},
			"apiKey":null}}}`,
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

// status resolves the active token via authContext and surfaces the principal
// type plus the authenticating key.
func TestAuthStatus(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"AuthContext": `{"data":{"authContext":{"principalType":"USER","appId":null,"agentId":null,
			"user":{"id":"u1","name":"Holger","email":"h@example.com","handle":null,"githubUsername":null,"roles":[]},
			"apiKey":{"id":"uak_1","label":"ci-deploy","keyPreview":"hdrk_ab1","issuedVia":"cli","createdAt":"2026-06-19T00:00:00Z","lastUsedAt":null,"revokedAt":null}}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "status", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Authenticated bool   `json:"authenticated"`
		PrincipalType string `json:"principalType"`
		Key           *struct {
			KeyPreview string `json:"keyPreview"`
		} `json:"key"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if !dto.Authenticated || dto.PrincipalType != "USER" || dto.Key == nil || dto.Key.KeyPreview != "hdrk_ab1" {
		t.Errorf("unexpected status: %s", out.String())
	}
}

// Against an older/self-hosted server without authContext, status must surface
// the schema-skew error (exit 2), not masquerade it as a rejected token (Codex).
func TestAuthStatusSurfacesSchemaSkew(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"AuthContext": `{"errors":[{"message":"Cannot query field \"authContext\" on type \"Query\"","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "status", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Fatalf("schema skew should exit %d (usage), got %d (err=%v)", exitcode.Usage, code, err)
	}
	if strings.Contains(out.String(), "rejected") {
		t.Errorf("skew must not be reported as a rejected token: %s", out.String())
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
