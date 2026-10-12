package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

// testOperationKey is canonical base64 for exactly 32 bytes — the header shape
// hadron-server's requestOperationKey parser accepts.
var testOperationKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))

// The operation key rides ONLY a call whose context asked for it (#846): like
// the worker-session header, it changes what the server does with one request
// (consumes the key once), so it must never become a client default stamped on
// every request a bound worktree makes.
func TestOperationKeyHeaderOnlyWhenTheCallCarriesOne(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get(OperationKeyHeader))
	}))
	defer srv.Close()
	d := &bearerDoer{token: "hdr_user_x", inner: srv.Client()}

	for _, ctx := range []context.Context{
		context.Background(),
		WithOperationKey(context.Background(), testOperationKey),
		WithOperationKey(context.Background(), ""), // empty is no key
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := d.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	want := []string{"", testOperationKey, ""}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d: %s = %q, want %q", i, OperationKeyHeader, got[i], want[i])
		}
	}
}

// The redirect hazard #846 exists for: net/http strips Authorization and
// cookies on a cross-host redirect but FORWARDS custom headers. A key-bearing
// request redirected to another host must be refused before the replay, under
// BOTH of NewClient's redirect policies (with and without a bearer token).
// A same-host redirect keeps the header.
func TestOperationKeyHeaderDoesNotFollowACrossHostRedirect(t *testing.T) {
	for _, token := range []string{"hdr_user_x", ""} {
		for _, crossHost := range []bool{true, false} {
			var got []string
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = append(got, r.Header.Get(OperationKeyHeader))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{}}`))
			}))
			var origin *httptest.Server
			origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/landed" { // same-host landing: answer, don't loop
					got = append(got, r.Header.Get(OperationKeyHeader))
					w.Header().Set("Content-Type", "application/json")
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
			ctx := WithOperationKey(context.Background(), testOperationKey)
			var data map[string]any
			err = c.MakeRequest(ctx, &graphql.Request{Query: "mutation X { x }", OpName: "X"}, &graphql.Response{Data: &data})
			if crossHost {
				if err == nil || len(got) != 0 {
					t.Errorf("token=%q cross-host redirect must refuse before replaying the key: err=%v headers=%q", token, err, got)
				}
			} else if err != nil || len(got) != 1 || got[0] != testOperationKey {
				t.Errorf("token=%q same-host redirect: err=%v headers=%q", token, err, got)
			}
			other.Close()
			origin.Close()
		}
	}
}

// The policy-level table, as client_test exercises for the scheme guard: a
// request carrying the header (or the context that would set it) refuses an
// insecure or cross-host hop under both redirect policies.
func TestOperationKeyRedirectPolicy(t *testing.T) {
	t.Setenv(EnvAllowHTTP, "")
	originHeader, _ := http.NewRequest(http.MethodPost, "https://srv.example/graphql", nil)
	originHeader.Header.Set(OperationKeyHeader, testOperationKey)
	originCtx, _ := http.NewRequestWithContext(
		WithOperationKey(context.Background(), testOperationKey), http.MethodPost, "https://srv.example/graphql", nil)
	for name, c := range map[string]*http.Client{
		"with token":    withSecureRedirects(&http.Client{}),
		"without token": withSessionRedirects(&http.Client{}),
	} {
		for _, origin := range []*http.Request{originHeader, originCtx} {
			for _, tc := range []struct {
				url  string
				keep bool
			}{
				{"http://srv.example/graphql", false},    // same host, cleartext
				{"https://srv.example/graphql", true},    // same host, secure
				{"https://other.example/graphql", false}, // another host
			} {
				req, _ := http.NewRequest(http.MethodPost, tc.url, nil)
				if err := c.CheckRedirect(req, []*http.Request{origin}); (err == nil) != tc.keep {
					t.Errorf("%s (header=%v) → %s: allowed = %v, want %v", name, origin.Header.Get(OperationKeyHeader) != "", tc.url, err == nil, tc.keep)
				}
			}
		}
	}
}

// The INITIAL request never passes through the redirect policy, and link-user
// runs with a token that would otherwise clear RequireSecureURL only for
// loopback — so the doer itself must refuse to put the key on cleartext http,
// on the no-token client path too (mirrors the session guard).
func TestOperationKeyRequestIsNotSentOverCleartextHTTP(t *testing.T) {
	t.Setenv(EnvAllowHTTP, "")
	var got []string
	record := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = append(got, r.Header.Get(OperationKeyHeader))
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}, Request: r}, nil
	})
	d := &bearerDoer{inner: &http.Client{Transport: record}}
	ctx := WithOperationKey(context.Background(), testOperationKey)
	for _, u := range []string{"http://srv.example/graphql", "https://srv.example/graphql", "http://127.0.0.1:8080/graphql"} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
		resp, err := d.Do(req)
		if u == "http://srv.example/graphql" {
			if err == nil {
				t.Fatal("cleartext key-bearing request must be refused")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	want := []string{testOperationKey, testOperationKey}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d: operation key = %q, want %q", i, got[i], want[i])
		}
	}
}

// A gateway that echoes the request (headers included) in its 502 page must
// not turn the error path into the leak path: the bounded snippet gatewayError
// keeps for diagnostics is redacted (#846, §10 pre-read).
//
// #847 cycle 0 (Diego P2, Ralion's amended P2, Xavier P2-1) widened this from
// 5xx-only: genqlient embeds the WHOLE raw body of any non-200 non-envelope
// response in its printed error, and a 200 errors envelope renders through
// MapError's message join — so a body that echoes the key reaches the user at
// EVERY status unless the doer scrubs each response a key-bearing request
// gets. Status, GraphQL envelope and codes survive; only the key's spellings
// (URL-encoded included — Xavier P3-7) are replaced.
func TestOperationKeyEchoRedactedAtEveryStatus(t *testing.T) {
	// A key whose base64 contains '+' and '/', so the exact value, its
	// query-escaped form (%2B/%2F/%3D) and its path-escaped form (%2F only)
	// are three distinct spellings. The assertion below DEMANDS that: a key
	// of letters and '=' alone would make the path-escaped row the exact-key
	// row in disguise and pin nothing.
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xfb, 0xff, 0x00, 0xef}, 8))
	escaped := url.QueryEscape(key)
	pathEscaped := url.PathEscape(key)
	if escaped == key || pathEscaped == key || pathEscaped == escaped {
		t.Fatalf("the test key's spellings must be pairwise distinct: key=%q query=%q path=%q", key, escaped, pathEscaped)
	}
	// Lower-hex spellings built HERE by hand, not via the production
	// lowercasePercentHex — a mutant that breaks that function must not break
	// this test's expectation along with it. Base64 only ever escapes to
	// %2B/%2F/%3D triplets, so swapping each triplet's case is exactly the
	// transformation.
	lowerQueryHex := strings.NewReplacer("%2B", "%2b", "%2F", "%2f", "%3D", "%3d").Replace(escaped)
	lowerPathHex := strings.ReplaceAll(pathEscaped, "%2F", "%2f")
	spellings := []string{key, escaped, pathEscaped, lowerQueryHex, lowerPathHex}
	// Ralion's cycle-1 mutants R6 (lower-hex) and R7 (path-escape) survived
	// the first table: the spellings were redacted by the code but pinned by
	// no test. Every row checks every spelling, so any survivor leaks.
	for _, tc := range []struct {
		name   string
		status int
		echo   string
	}{
		{"200 errors envelope echoing the key", 200, key},
		{"400 non-envelope JSON echoing the key", 400, key},
		{"404 proxy page echoing the key", 404, key},
		{"502 page echoing the key query-escaped", 502, escaped},
		{"502 page echoing the key path-escaped (R7)", 502, pathEscaped},
		{"502 page echoing the key lower-hex query-escaped (R6)", 502, lowerQueryHex},
		{"502 page echoing the key lower-hex path-escaped", 502, lowerPathHex},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A 200 must carry a GraphQL errors envelope to reach the message
			// join; the other statuses echo a page the way a proxy would.
			body := `<html>502: saw key ` + tc.echo + ` in the request</html>`
			if tc.status == 200 {
				body = `{"errors":[{"message":"bad key ` + tc.echo + ` supplied"}]}`
			}
			c, err := NewClient("https://srv.example", "", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tc.status,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Request:    r,
				}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			ctx := WithOperationKey(context.Background(), key)
			var data map[string]any
			err = c.MakeRequest(ctx, &graphql.Request{Query: "mutation X { x }", OpName: "X"}, &graphql.Response{Data: &data})
			if err == nil {
				t.Fatalf("the %d response must surface as an error", tc.status)
			}
			for _, leaked := range spellings {
				if strings.Contains(err.Error(), leaked) {
					t.Errorf("the error leaked the operation key (spelling %q): %v", leaked, err)
				}
			}
		})
	}
}

func TestGatewaySnippetRedactsOperationKey(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	page := "<html>502 Bad Gateway: saw key " + key + " from upstream</html>"
	d := &bearerDoer{inner: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 502,
			Body:       io.NopCloser(strings.NewReader(page)),
			Header:     http.Header{},
			Request:    r,
		}, nil
	})}}
	req, _ := http.NewRequestWithContext(
		WithOperationKey(context.Background(), key), http.MethodPost, "https://srv.example/graphql", nil)
	_, err := d.Do(req)
	if err == nil {
		t.Fatal("the 502 must surface as an error")
	}
	if strings.Contains(err.Error(), key) {
		t.Errorf("the gateway error leaked the operation key: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("the echoed key should be visibly redacted, got: %v", err)
	}
}

func TestValidateOperationKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		ok   bool
	}{
		{"canonical 32 bytes", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), true},
		{"canonical, high bytes", base64.StdEncoding.EncodeToString([]byte{
			0x00, 0xff, 0x10, 0xf1, 0x02, 0x9a, 0x7c, 0x3d, 0x55, 0x01, 0xfe, 0x33, 0x2b, 0x88, 0x19, 0xc0,
			0x00, 0xff, 0x10, 0xf1, 0x02, 0x9a, 0x7c, 0x3d, 0x55, 0x01, 0xfe, 0x33, 0x2b, 0x88, 0x19, 0xc0,
		}), true},
		{"empty", "", false},
		{"not base64", "not a key!", false},
		{"too short", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 8)), false},
		{"too long", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 33)), false},
		{"missing padding", strings.Repeat("A", 43), false},
		{"double padding", strings.Repeat("A", 42) + "==", false},
		{"non-canonical spelling", strings.Repeat("A", 42) + "B=", false},
		{"url-safe alphabet", base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{0xfb}, 32)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOperationKey(tc.key)
			if tc.ok && err != nil {
				t.Fatalf("a valid key was refused: %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatalf("key %q must be refused", tc.key)
				}
				// Contains(x, "") is trivially true, so the echo check needs a
				// value to look for.
				if tc.key != "" && strings.Contains(err.Error(), tc.key) {
					t.Errorf("the refusal must not echo the key value: %v", err)
				}
			}
		})
	}
}
