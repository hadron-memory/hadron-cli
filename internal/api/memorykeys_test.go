package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#1234: the --unlock header and its guards at the transport.

func keysCtx1234(t *testing.T, pass string) context.Context {
	t.Helper()
	ctx, err := WithMemoryKeys(context.Background(), []MemoryKey{{Memory: "hrn:mem:acme.com:diary", Passphrase: pass}})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestMemoryKeysHeaderOnlyWhenTheContextCarriesOne(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get(MemoryKeysHeader))
	}))
	defer srv.Close()
	d := &bearerDoer{token: "hdr_user_x", inner: srv.Client()}
	empty, err := WithMemoryKeys(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{context.Background(), keysCtx1234(t, "pw"), empty} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, nil)
		resp, err := d.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	want := []string{"", `[{"memory":"hrn:mem:acme.com:diary","passphrase":"pw"}]`, ""}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d: %q, want %q", i, got[i], want[i])
		}
	}
}

func TestWithMemoryKeysBounds(t *testing.T) {
	nine := make([]MemoryKey, 9)
	for i := range nine {
		nine[i] = MemoryKey{Memory: "m", Passphrase: "p"}
	}
	if _, err := WithMemoryKeys(context.Background(), nine); exitcode.FromError(err) != exitcode.Usage {
		t.Fatalf("9 entries: %v", err)
	}
	big := []MemoryKey{{Memory: "m", Passphrase: strings.Repeat("x", MaxMemoryKeysBytes)}}
	_, err := WithMemoryKeys(context.Background(), big)
	if exitcode.FromError(err) != exitcode.Usage || strings.Contains(err.Error(), "xxxx") {
		t.Fatalf("oversized: %v", err)
	}
}

// net/http forwards a custom header across a cross-host redirect; a request
// carrying the passphrase must be refused before the replay, under both
// redirect policies. A same-host redirect keeps it.
func TestMemoryKeysDoNotFollowACrossHostRedirect(t *testing.T) {
	for _, token := range []string{"hdr_user_x", ""} {
		for _, crossHost := range []bool{true, false} {
			var got []string
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = append(got, r.Header.Get(MemoryKeysHeader))
				_, _ = w.Write([]byte(`{"data":{}}`))
			}))
			var origin *httptest.Server
			origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/landed" {
					got = append(got, r.Header.Get(MemoryKeysHeader))
					_, _ = w.Write([]byte(`{"data":{}}`))
					return
				}
				target := origin.URL + "/landed"
				if crossHost {
					target = other.URL + "/landed"
				}
				http.Redirect(w, r, target, http.StatusTemporaryRedirect)
			}))
			c, err := NewClient(origin.URL, token, nil)
			if err != nil {
				t.Fatal(err)
			}
			var data map[string]any
			err = c.MakeRequest(keysCtx1234(t, "pw"), &graphql.Request{Query: "query X { x }", OpName: "X"}, &graphql.Response{Data: &data})
			if crossHost {
				if err == nil || len(got) != 0 {
					t.Errorf("token=%q cross-host: err=%v headers=%q", token, err, got)
				}
			} else if err != nil || len(got) != 1 || !strings.Contains(got[0], `"passphrase":"pw"`) {
				t.Errorf("token=%q same-host: err=%v headers=%q", token, err, got)
			}
			other.Close()
			origin.Close()
		}
	}
}

func TestMemoryKeysNotSentOverCleartextHTTP(t *testing.T) {
	t.Setenv(EnvAllowHTTP, "")
	sent := 0
	d := &bearerDoer{inner: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sent++
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}, Request: r}, nil
	})}}
	req, _ := http.NewRequestWithContext(keysCtx1234(t, "pw"), http.MethodPost, "http://srv.example/graphql", nil)
	if _, err := d.Do(req); err == nil || sent != 0 {
		t.Fatalf("cleartext: err=%v sent=%d", err, sent)
	}
}

// Error-bearing answers are scrubbed of every spelling; a successful answer's
// data — the plaintext asked for — is never rewritten, even when a short
// passphrase also occurs in it.
func TestMemoryKeysRedactionScope(t *testing.T) {
	const pass = `pa"ss`
	esc := `pa\"ss`
	cases := []struct {
		name, body string
		status     int
		mustKeep   string
		mustLose   []string
	}{
		{"200 data kept", `{"data":{"node":{"content":"my pa\"ss note"}}}`, 200, `my pa\"ss note`, nil},
		{"200 errors scrubbed, data kept", `{"data":{"x":"pa\"ss"},"errors":[{"message":"echo pa\"ss","extensions":{"code":"X"}}]}`, 200, `"x":"pa\"ss"`, []string{`echo pa\"ss`}},
		{"500 envelope scrubbed", `{"errors":[{"message":"bad pa\"ss","extensions":{"code":"WRONG_KEY"}}]}`, 500, `"WRONG_KEY"`, []string{esc}},
		{"502 page scrubbed", `<html>pa"ss [{"memory":"hrn:mem:acme.com:diary","passphrase":"pa\"ss"}]</html>`, 502, "<html>", []string{pass, esc}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var seen []byte
			d := &bearerDoer{token: "hdr_user_x", inner: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: c.status, Body: io.NopCloser(strings.NewReader(c.body)), Header: http.Header{}, Request: r}, nil
			})}}
			req, _ := http.NewRequestWithContext(keysCtx1234(t, pass), http.MethodPost, "https://srv.example/graphql", nil)
			resp, err := d.Do(req)
			if err != nil {
				var gw *gatewayError
				if !errors.As(err, &gw) {
					t.Fatal(err)
				}
				seen = gw.body
			} else {
				seen, _ = io.ReadAll(resp.Body)
			}
			if !strings.Contains(string(seen), c.mustKeep) {
				t.Fatalf("lost %q: %s", c.mustKeep, seen)
			}
			for _, s := range c.mustLose {
				if strings.Contains(string(seen), s) {
					t.Fatalf("kept %q: %s", s, seen)
				}
			}
		})
	}
}

// A path-less unlock code (the server's context-time refusal) is typed; the
// same code from a resolver (with a path), or without --unlock, keeps the
// ordinary mapping.
func TestUnlockRefusalOnlyForContextErrorsOnKeyedRequests(t *testing.T) {
	body := func(path string) string {
		return `{"data":null,"errors":[{"message":"Memory not found.",` + path + `"extensions":{"code":"NOT_FOUND"}}]}`
	}
	for _, c := range []struct {
		name  string
		keyed bool
		path  string
		typed bool
	}{
		{"keyed, no path", true, "", true},
		{"keyed, resolver path", true, `"path":["node"],`, false},
		{"no keys", false, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body(c.path)))
			}))
			defer srv.Close()
			cl, err := NewClient(srv.URL, "hdr_user_x", nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if c.keyed {
				ctx = keysCtx1234(t, "pw")
			}
			var data map[string]any
			err = cl.MakeRequest(ctx, &graphql.Request{Query: "query X { x }", OpName: "X"}, &graphql.Response{Data: &data})
			typed := strings.Contains(err.Error(), "--unlock refused (NOT_FOUND)")
			if typed != c.typed || exitcode.FromError(MapError(err)) != exitcode.NotFound {
				t.Fatalf("typed=%v err=%v", typed, err)
			}
		})
	}
}

// The raw `hadron api` path and resolver-path errors map NOT_ENCRYPTED through
// the shared code table.
func TestNotEncryptedIsUsage(t *testing.T) {
	if got := codeForExtension("NOT_ENCRYPTED"); got != exitcode.Usage {
		t.Fatalf("NOT_ENCRYPTED → %d", got)
	}
}
