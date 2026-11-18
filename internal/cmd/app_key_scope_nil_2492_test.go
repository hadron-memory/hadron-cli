package cmd

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2492: `app key scope grant` and its revoke twin dereferenced the
// mutation result without a nil check, so a reply with no result (an empty
// data object or an explicit null, as a fixture, proxy or misbehaving server
// can send) panicked. Each must instead exit 1 naming what is missing, and
// print nothing on stdout.

func runKeyScope2492(t *testing.T, srv *httptest.Server, args ...string) (r keyRun) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"app", "key", "scope"}, args...), "--server", srv.URL))
	defer func() {
		if p := recover(); p != nil {
			r = keyRun{err: fmt.Errorf("panicked: %v", p)}
		}
	}()
	err := root.Execute()
	return keyRun{out: out.String(), errOut: f.IOStreams.ErrOut.(*strings.Builder).String(), err: err}
}

func TestAppKeyScope2492NoResultExitsOneNamingIt(t *testing.T) {
	cases := []struct {
		verb, field, want string
		args              []string
	}{
		{"grant", "grantAppKeyScope", "server returned no grant", []string{"grant", "key_1", "attention:deliver"}},
		{"grant host", "grantAppKeyScope", "server returned no grant", []string{"grant", "key_1", "host:operate", "--host-id", "orca1"}},
		{"revoke", "revokeAppKeyScope", "server returned no revoke result", []string{"revoke", "key_1", "attention:deliver"}},
	}
	for _, c := range cases {
		for _, body := range []string{`{"data":{}}`, `{"data":{"` + c.field + `":null}}`} {
			for _, json := range []bool{false, true} {
				name := fmt.Sprintf("%s/%s/json=%v", c.verb, body, json)
				t.Run(name, func(t *testing.T) {
					srv, n := keyServer(t, 200, body)
					args := c.args
					if json {
						args = append(append([]string{}, args...), "--json")
					}
					r := runKeyScope2492(t, srv, args...)
					if r.err == nil {
						t.Fatalf("must fail, got out=%q", r.out)
					}
					if strings.HasPrefix(r.err.Error(), "panicked:") {
						t.Fatalf("%v", r.err)
					}
					if got := exitCodeFor(r.err); got != exitcode.Error {
						t.Errorf("exit = %d, want %d (%v)", got, exitcode.Error, r.err)
					}
					if !strings.Contains(r.err.Error(), c.want) {
						t.Errorf("error = %q, want it to name %q", r.err, c.want)
					}
					if r.out != "" {
						t.Errorf("stdout must be empty, got %q", r.out)
					}
					if n.Load() != 1 {
						t.Errorf("mutation sent %d times, want once", n.Load())
					}
				})
			}
		}
	}
}

// A conforming reply still renders as before.
func TestAppKeyScope2492ConformingReplyUnchanged(t *testing.T) {
	fields := `{"id":"key_1","scopes":[{"scope":"attention:deliver","hostId":null,"grantedAt":"2026-11-18T00:00:00Z"}]}`
	for verb, field := range map[string]string{"grant": "grantAppKeyScope", "revoke": "revokeAppKeyScope"} {
		t.Run(verb, func(t *testing.T) {
			srv, _ := keyServer(t, 200, `{"data":{"`+field+`":`+fields+`}}`)
			r := runKeyScope2492(t, srv, verb, "key_1", "attention:deliver", "--json")
			if r.err != nil {
				t.Fatalf("execute: %v", r.err)
			}
			if !strings.Contains(r.out, `"id": "key_1"`) || !strings.Contains(r.out, "attention:deliver") {
				t.Errorf("json = %q", r.out)
			}
		})
	}
}
