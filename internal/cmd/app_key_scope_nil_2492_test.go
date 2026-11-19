package cmd

import (
	"encoding/json"
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
//
// cli#2507 (Belinda's BLIND P3s on #2503): B1 the grant and revoke refusals
// use the same wording shape ("no grant" / "no revocation"); B2 the message
// reaches stderr, asserted through the production renderer — SilenceErrors
// means nothing prints inside Execute itself; B3 the human path (the ✓ line
// and the scopes table) is pinned, not just --json; B4 the JSON check
// unmarshals fields instead of matching the encoder's indentation.

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
		{"revoke", "revokeAppKeyScope", "server returned no revocation", []string{"revoke", "key_1", "attention:deliver"}},
	}
	for _, c := range cases {
		for _, body := range []string{`{"data":{}}`, `{"data":{"` + c.field + `":null}}`} {
			for _, jsonMode := range []bool{false, true} {
				name := fmt.Sprintf("%s/%s/json=%v", c.verb, body, jsonMode)
				t.Run(name, func(t *testing.T) {
					srv, n := keyServer(t, 200, body)
					args := c.args
					if jsonMode {
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
					// B2: the message reaches the caller's error channel.
					// SilenceErrors means nothing prints inside Execute
					// itself, so route the returned error through the
					// production renderer, the way cmd.Execute does: text
					// mode gets "hadron: <message>" on stderr; --json keeps
					// stdout a single document, so the envelope — message
					// included — IS the channel there.
					rf, rout := testFactory(t)
					rf.JSON = jsonMode
					if code := renderError(rf, r.err); code != exitcode.Error {
						t.Fatalf("rendered exit = %d, want %d", code, exitcode.Error)
					}
					if jsonMode {
						var env struct {
							Error struct {
								Code    int    `json:"code"`
								Message string `json:"message"`
							} `json:"error"`
						}
						if err := json.Unmarshal([]byte(rout.String()), &env); err != nil {
							t.Fatalf("envelope = %q (%v)", rout.String(), err)
						}
						if env.Error.Code != exitcode.Error || !strings.Contains(env.Error.Message, c.want) {
							t.Errorf("envelope = %q, want %q in it", rout.String(), c.want)
						}
					} else if stderr := rf.IOStreams.ErrOut.(*strings.Builder).String(); !strings.Contains(stderr, c.want) {
						t.Errorf("stderr = %q, want the message %q on it", stderr, c.want)
					}
					if n.Load() != 1 {
						t.Errorf("mutation sent %d times, want once", n.Load())
					}
				})
			}
		}
	}
}

// A conforming reply still renders as before — in BOTH output modes: B3 pins
// the human ✓ line and the scopes table (only --json was covered), B4 decodes
// the JSON and compares fields instead of matching the encoder's indentation.
func TestAppKeyScope2492ConformingReplyUnchanged(t *testing.T) {
	fields := `{"id":"key_1","scopes":[{"scope":"attention:deliver","hostId":null,"grantedAt":"2026-11-18T00:00:00Z"}]}`
	for verb, field := range map[string]string{"grant": "grantAppKeyScope", "revoke": "revokeAppKeyScope"} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(verb+map[bool]string{false: "/text", true: "/json"}[jsonMode], func(t *testing.T) {
				srv, _ := keyServer(t, 200, `{"data":{"`+field+`":`+fields+`}}`)
				args := []string{verb, "key_1", "attention:deliver"}
				if jsonMode {
					args = append(args, "--json")
				}
				r := runKeyScope2492(t, srv, args...)
				if r.err != nil {
					t.Fatalf("execute: %v", r.err)
				}
				if jsonMode {
					var dto struct {
						ID     string `json:"id"`
						Scopes []struct {
							Scope     string  `json:"scope"`
							HostID    *string `json:"hostId"`
							GrantedAt string  `json:"grantedAt"`
						} `json:"scopes"`
					}
					if err := json.Unmarshal([]byte(r.out), &dto); err != nil {
						t.Fatalf("json = %q (%v)", r.out, err)
					}
					if dto.ID != "key_1" || len(dto.Scopes) != 1 || dto.Scopes[0].Scope != "attention:deliver" || dto.Scopes[0].HostID != nil || dto.Scopes[0].GrantedAt != "2026-11-18T00:00:00Z" {
						t.Errorf("json = %q", r.out)
					}
					return
				}
				wantLine := "✓ key key_1 holds attention:deliver"
				if verb == "revoke" {
					wantLine = "✓ key key_1 no longer holds attention:deliver"
				}
				for _, want := range []string{wantLine, "SCOPE", "GRANTED", "attention:deliver", "2026-11-18T00:00:00Z"} {
					if !strings.Contains(r.out, want) {
						t.Errorf("human output missing %q: %q", want, r.out)
					}
				}
			})
		}
	}
}
