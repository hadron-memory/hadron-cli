package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const deleteOp1568 = "DeleteUserApiKey"

func deletion1568(revoked, minted string) string {
	return `{"data":{"deleteUserApiKey":{"id":"del_1","keyId":"uak_1","keyPreview":"hdrk_ab1","revokedKeyIds":[` + revoked +
		`],"mintedKeyIds":[` + minted + `],"deletedAt":"2026-11-03T07:00:00Z"}}}`
}

// countingServer answers every non-GET with respond and counts the delete calls.
func countingServer1568(t *testing.T, respond func(w http.ResponseWriter)) (*httptest.Server, *atomic.Int32, *json.RawMessage) {
	t.Helper()
	var calls atomic.Int32
	var vars json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.OperationName == deleteOp1568 {
			calls.Add(1)
			vars = body.Variables
		}
		respond(w)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, &vars
}

func TestAuthTokenDelete1568Wire(t *testing.T) {
	t.Run("--yes sends exactly one deleteUserApiKey with the id", func(t *testing.T) {
		srv, calls, vars := countingServer1568(t, func(w http.ResponseWriter) { _, _ = w.Write([]byte(deletion1568("", ""))) })
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"auth", "token", "delete", "uak_1", "--yes", "--server", srv.URL})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 1 || !strings.Contains(string(*vars), `"id":"uak_1"`) {
			t.Fatalf("want one call with id, got %d %s", calls.Load(), *vars)
		}
	})
	t.Run("no terminal and no --yes: usage exit, zero requests", func(t *testing.T) {
		srv, calls, _ := countingServer1568(t, func(w http.ResponseWriter) { _, _ = w.Write([]byte(deletion1568("", ""))) })
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"auth", "token", "delete", "uak_1", "--server", srv.URL})
		err := root.Execute()
		if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "--yes") {
			t.Fatalf("want exit 2 naming --yes, got %v", err)
		}
		if calls.Load() != 0 {
			t.Fatalf("nothing is sent before confirmation, sent %d", calls.Load())
		}
	})
	t.Run("terminal: the prompt names permanent removal and revocation; no sends zero", func(t *testing.T) {
		srv, calls, _ := countingServer1568(t, func(w http.ResponseWriter) { _, _ = w.Write([]byte(deletion1568("", ""))) })
		f, _, errOut := testFactoryTTY(t, "n\n")
		root := NewRootCmd(f)
		root.SetArgs([]string{"auth", "token", "delete", "uak_1", "--server", srv.URL})
		_ = root.Execute()
		if calls.Load() != 0 {
			t.Fatalf("a declined delete sends nothing, sent %d", calls.Load())
		}
		for _, want := range []string{"uak_1", "for good", "revoked first"} {
			if !strings.Contains(errOut.String(), want) {
				t.Errorf("prompt should contain %q, got %q", want, errOut.String())
			}
		}
	})
	t.Run("terminal: yes sends once", func(t *testing.T) {
		srv, calls, _ := countingServer1568(t, func(w http.ResponseWriter) { _, _ = w.Write([]byte(deletion1568("", ""))) })
		f, _, _ := testFactoryTTY(t, "y\n")
		root := NewRootCmd(f)
		root.SetArgs([]string{"auth", "token", "delete", "uak_1", "--server", srv.URL})
		if err := root.Execute(); err != nil || calls.Load() != 1 {
			t.Fatalf("err=%v calls=%d", err, calls.Load())
		}
	})
}

func TestAuthTokenDelete1568Output(t *testing.T) {
	run := func(t *testing.T, resp string, extra ...string) string {
		t.Helper()
		srv, _, _ := countingServer1568(t, func(w http.ResponseWriter) { _, _ = w.Write([]byte(resp)) })
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"auth", "token", "delete", "uak_1", "--yes", "--server", srv.URL}, extra...))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	t.Run("empty lists print the one success line", func(t *testing.T) {
		if got := run(t, deletion1568("", "")); got != "✓ Deleted API token uak_1 (hdrk_ab1)\n" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("both lists print one line each, distinct", func(t *testing.T) {
		got := run(t, deletion1568(`"uak_1","uak_9"`, `"uak_9"`))
		lines := strings.Split(strings.TrimSpace(got), "\n")
		if len(lines) != 3 || !strings.Contains(lines[1], "uak_1") || !strings.Contains(lines[1], "uak_9") ||
			!strings.Contains(lines[1], "revoked") || !strings.Contains(lines[2], "uak_9") ||
			!strings.Contains(lines[2], "no parent") {
			t.Fatalf("got %q", got)
		}
		if lines[1] == lines[2] {
			t.Fatal("revoked and minted summaries must stay distinct")
		}
	})
	t.Run("--json carries all six fields, empty lists as []", func(t *testing.T) {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(run(t, deletion1568("", ""), "--json")), &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"id", "keyId", "keyPreview", "revokedKeyIds", "mintedKeyIds", "deletedAt"} {
			if _, ok := m[k]; !ok {
				t.Errorf("missing %s in %v", k, m)
			}
		}
		if string(m["revokedKeyIds"]) != "[]" || string(m["mintedKeyIds"]) != "[]" {
			t.Errorf("empty lists must be [], got %s %s", m["revokedKeyIds"], m["mintedKeyIds"])
		}
	})
	t.Run("--json populated", func(t *testing.T) {
		var m struct {
			Revoked []string `json:"revokedKeyIds"`
			Minted  []string `json:"mintedKeyIds"`
		}
		_ = json.Unmarshal([]byte(run(t, deletion1568(`"a","b"`, `"b"`), "--json")), &m)
		if len(m.Revoked) != 2 || len(m.Minted) != 1 || m.Minted[0] != "b" {
			t.Fatalf("got %+v", m)
		}
	})
}

func TestAuthTokenDelete1568Refusals(t *testing.T) {
	for _, tc := range []struct {
		name, code, msg string
		exit            int
	}{
		{"not found", "NOT_FOUND", "UserApiKey uak_1 not found.", exitcode.NotFound},
		{"forbidden", "FORBIDDEN", "Only a signed-in person can delete an API key.", -1},
		{"unauthenticated", "UNAUTHENTICATED", "not signed in", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := `{"data":null,"errors":[{"message":"` + tc.msg + `","extensions":{"code":"` + tc.code + `"}}]}`
			srv, calls, _ := countingServer1568(t, func(w http.ResponseWriter) { _, _ = w.Write([]byte(resp)) })
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"auth", "token", "delete", "uak_1", "--yes", "--server", srv.URL})
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("want the server message %q byte-exact, got %v", tc.msg, err)
			}
			if tc.exit >= 0 && exitCodeFor(err) != tc.exit {
				t.Errorf("want exit %d, got %d", tc.exit, exitCodeFor(err))
			}
			if calls.Load() != 1 {
				t.Errorf("sent %d times", calls.Load())
			}
		})
	}
}

func TestAuthTokenDelete1568NeverRetried(t *testing.T) {
	srv, calls, _ := countingServer1568(t, func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) })
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "delete", "uak_1", "--yes", "--server", srv.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("want an error on 5xx")
	}
	if calls.Load() != 1 {
		t.Fatalf("a delete is never retried; sent %d times", calls.Load())
	}
}
