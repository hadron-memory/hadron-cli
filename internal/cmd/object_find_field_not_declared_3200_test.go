package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3200 (server#5196, cor:api:170:03): findObjects refuses a filter or sort
// on a field a strict collection does not declare. The path can only be a
// mistake in the caller's input, so it exits 2 like BAD_USER_INPUT, and the
// server's sentence is shown as sent.

// A made-up sentence: the CLI must pass through whatever the server wrote, so
// the test does not depend on the server's wording.
const fieldNotDeclaredMessage3200 = `SENTINEL-3200: field "fundingUSD" is not declared on collection "competitor"`

func TestObjectFindFieldNotDeclared3200(t *testing.T) {
	body := `{"errors":[{"message":` + mustJSON3200(fieldNotDeclaredMessage3200) +
		`,"path":["findObjects"],"extensions":{"code":"FIELD_NOT_DECLARED","field":"fundingUSD","objectType":"competitor"}}],"data":null}`
	for _, tc := range []struct {
		name   string
		status int
	}{{"200", http.StatusOK}, {"non200", http.StatusBadRequest}} {
		for _, asJSON := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/text", true: "/json"}[asJSON], func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(body))
				}))
				t.Cleanup(srv.Close)
				f, out := testFactory(t)
				errOut := &strings.Builder{}
				f.IOStreams.ErrOut = errOut
				args := []string{"object", "find", "-m", "acme.com::market", "--type", "competitor",
					"--match", `{"fundingUSD":1}`, "--server", srv.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root := NewRootCmd(f)
				root.SetArgs(args)
				err := root.Execute()
				if err == nil {
					t.Fatal("want a refusal")
				}
				if code := renderFailure(f, args, err); code != exitcode.Usage {
					t.Fatalf("exit %d, want %d (usage): %v", code, exitcode.Usage, err)
				}
				if !asJSON {
					if err.Error() != fieldNotDeclaredMessage3200 {
						t.Errorf("message = %q, want the server's %q", err.Error(), fieldNotDeclaredMessage3200)
					}
					if !strings.Contains(errOut.String(), fieldNotDeclaredMessage3200) || out.String() != "" {
						t.Errorf("stderr %q stdout %q", errOut.String(), out.String())
					}
					return
				}
				var envelope struct {
					Error struct {
						Code    int    `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if jerr := json.Unmarshal([]byte(out.String()), &envelope); jerr != nil {
					t.Fatalf("stdout is not the JSON envelope (%v): %q", jerr, out.String())
				}
				if envelope.Error.Code != exitcode.Usage || envelope.Error.Message != fieldNotDeclaredMessage3200 {
					t.Errorf("envelope %+v, want code %d and the server's message", envelope.Error, exitcode.Usage)
				}
			})
		}
	}
}

// In a non-strict collection, or a memory with no schema, an undeclared field
// stays queryable (cor:api:170:03): the CLI sends match, where and sort as
// given and reads no schema first.
func TestObjectFindSendsUndeclaredFieldUnchecked3200(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindObjects": `{"data":{"findObjects":{"objects":[{"id":"o1","type":"competitor"}],"total":1}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"object", "find", "-m", "acme.com::market", "--type", "competitor",
		"--match", `{"nobodyDeclaredMatch":1}`, "--where", `{"path":["nobodyDeclaredWhere"],"exists":true}`,
		"--sort", `{"nobodyDeclaredSort":"asc"}`, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(captured) != 1 {
		t.Errorf("want only FindObjects on the wire, got %d operations", len(captured))
	}
	sent := string(captured["FindObjects"])
	for _, field := range []string{"nobodyDeclaredMatch", "nobodyDeclaredWhere", "nobodyDeclaredSort"} {
		if !strings.Contains(sent, field) {
			t.Errorf("%s was not sent: %s", field, sent)
		}
	}
}

func mustJSON3200(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
