package cmd

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#2492: `app key scope grant` and its revoke twin dereferenced the
// mutation result without a nil check, so a reply with no result (an empty
// data object or an explicit null, as a fixture, proxy or misbehaving server
// can send) panicked. Each must instead exit 1 naming what is missing, and
// print nothing on stdout.
//
// cli#2507 (Belinda's BLIND P3s on #2503): B1 the grant and revoke refusals
// use the same wording shape ("no grant result" / "no revocation result": the request was sent, so the wording describes an empty reply, never a business outcome, cli#2523); B2 the message
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
	return keyRun{out: out.String(), errOut: errOutString2523(t, f.IOStreams), err: err}
}

// errOutString2523 reads the stderr buffer, failing with a message (not a
// panic) if testFactory ever changes the buffer type (cli#2523 item 2).
func errOutString2523(t *testing.T, ios *output.IOStreams) string {
	t.Helper()
	sb, ok := ios.ErrOut.(*strings.Builder)
	if !ok {
		t.Fatalf("ErrOut is %T, want *strings.Builder", ios.ErrOut)
	}
	return sb.String()
}

func TestAppKeyScope2492NoResultExitsOneNamingIt(t *testing.T) {
	cases := []struct {
		verb, field, want string
		args              []string
	}{
		{"grant", "grantAppKeyScope", "server returned no grant result", []string{"grant", "key_1", "attention:deliver"}},
		{"grant host", "grantAppKeyScope", "server returned no grant result", []string{"grant", "key_1", "host:operate", "--host-id", "orca1"}},
		{"revoke", "revokeAppKeyScope", "server returned no revocation result", []string{"revoke", "key_1", "attention:deliver"}},
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
					} else {
						// Item 3: pin the whole "hadron: <message>" line the
						// comment promises, and that stdout stays empty.
						if stderr := errOutString2523(t, rf.IOStreams); !strings.HasPrefix(stderr, "hadron: ") || !strings.Contains(stderr, "hadron: "+c.want) {
							t.Errorf("stderr = %q, want it to begin \"hadron: \" and carry %q", stderr, "hadron: "+c.want)
						}
						if rout.String() != "" {
							t.Errorf("rendered stdout must be empty in text mode, got %q", rout.String())
						}
					}
					if n.Load() != 1 {
						t.Errorf("mutation sent %d times, want once", n.Load())
					}
				})
			}
		}
	}
}

// A conforming reply still renders as before, in BOTH output modes: B3 pins
// the human ✓ line and the scopes table (only --json was covered), B4 decodes
// the JSON and compares fields instead of matching the encoder's indentation.
// cli#2523: the revoke fixtures hold what a revoke leaves (a different scope,
// or none), so the pinned output is not self-contradictory; the table's
// column order and the null/non-null host cell are pinned; a host-scoped
// grant and an empty scopes list are covered on the success path.
func TestAppKeyScope2492ConformingReplyUnchanged(t *testing.T) {
	const at = "2026-11-18T00:00:00Z"
	type scopeRow struct{ scope, host string }
	cases := []struct {
		name, field string
		args        []string
		fields      string
		wantLine    string
		rows        []scopeRow // host "" = null
	}{
		{"grant", "grantAppKeyScope", []string{"grant", "key_1", "attention:deliver"},
			`{"id":"key_1","scopes":[{"scope":"attention:deliver","hostId":null,"grantedAt":"` + at + `"}]}`,
			"✓ key key_1 holds attention:deliver", []scopeRow{{"attention:deliver", ""}}},
		{"grant host", "grantAppKeyScope", []string{"grant", "key_1", "host:operate", "--host-id", "orca1"},
			`{"id":"key_1","scopes":[{"scope":"host:operate","hostId":"orca1","grantedAt":"` + at + `"}]}`,
			"✓ key key_1 holds host:operate for host orca1", []scopeRow{{"host:operate", "orca1"}}},
		{"revoke leaves another scope", "revokeAppKeyScope", []string{"revoke", "key_1", "attention:deliver"},
			`{"id":"key_1","scopes":[{"scope":"host:operate","hostId":"orca1","grantedAt":"` + at + `"}]}`,
			"✓ key key_1 no longer holds attention:deliver", []scopeRow{{"host:operate", "orca1"}}},
		{"revoke the last scope", "revokeAppKeyScope", []string{"revoke", "key_1", "attention:deliver"},
			`{"id":"key_1","scopes":[]}`,
			"✓ key key_1 no longer holds attention:deliver", nil},
	}
	for _, c := range cases {
		for _, jsonMode := range []bool{false, true} {
			mode := "text"
			if jsonMode {
				mode = "json"
			}
			t.Run(c.name+"/"+mode, func(t *testing.T) {
				srv, _ := keyServer(t, 200, `{"data":{"`+c.field+`":`+c.fields+`}}`)
				args := c.args
				if jsonMode {
					args = append(append([]string{}, args...), "--json")
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
					if dto.ID != "key_1" || len(dto.Scopes) != len(c.rows) {
						t.Fatalf("json = %q", r.out)
					}
					for i, row := range c.rows {
						got := dto.Scopes[i]
						if got.Scope != row.scope || got.GrantedAt != at || (row.host == "") != (got.HostID == nil) || (row.host != "" && *got.HostID != row.host) {
							t.Errorf("json scope %d = %+v, want %+v", i, got, row)
						}
					}
					return
				}
				lines := strings.Split(strings.TrimRight(r.out, "\n"), "\n")
				if lines[0] != c.wantLine {
					t.Errorf("first line = %q, want %q", lines[0], c.wantLine)
				}
				if len(c.rows) == 0 {
					if len(lines) != 2 || lines[1] != "scopes: none" {
						t.Errorf("an empty scopes list renders %q, want the line %q", r.out, "scopes: none")
					}
					return
				}
				if len(lines) != 2+len(c.rows) {
					t.Fatalf("output = %q, want the ✓ line, a header and %d row(s)", r.out, len(c.rows))
				}
				if h := strings.Fields(lines[1]); strings.Join(h, " ") != "SCOPE HOST GRANTED" {
					t.Errorf("header = %q, want columns SCOPE HOST GRANTED in that order", lines[1])
				}
				for i, row := range c.rows {
					host := row.host
					if host == "" {
						host = "—"
					}
					if got := strings.Join(strings.Fields(lines[2+i]), " "); got != row.scope+" "+host+" "+at {
						t.Errorf("row %d = %q, want %q", i, got, row.scope+" "+host+" "+at)
					}
				}
			})
		}
	}
}
