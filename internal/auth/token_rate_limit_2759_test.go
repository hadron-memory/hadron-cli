package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-server#4659 refuses a token request at admission with HTTP 429,
// Retry-After (seconds), code=RATE_LIMITED, retryAfterSeconds and fieldClass,
// and the OAuth error=temporarily_unavailable. The refusal is before the code
// is redeemed, so nothing was issued: it is a "wait", never a rejected
// credential, never a half-done exchange, and never worth printing the body.
const typed429 = `{"error":"temporarily_unavailable","error_description":"SECRET-DESCRIPTION","code":"RATE_LIMITED","retryAfterSeconds":%s,"fieldClass":"CREDENTIALS"}`

func rateLimited(header, retryAfterJSON string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if header != "" {
			w.Header().Set("Retry-After", header)
		}
		w.WriteHeader(http.StatusTooManyRequests)
		body := strings.Replace(typed429, "%s", retryAfterJSON, 1)
		_, _ = w.Write([]byte(body))
	}
}

func loginAgainst(t *testing.T, as *fakeAS) (*Token, error) {
	t.Helper()
	io, _, _ := output.Test()
	return BrowserStrategy{}.Login(loginCtx(t), LoginOptions{
		ServerURL:   as.server.URL,
		IO:          io,
		HTTPClient:  as.server.Client(),
		OpenBrowser: consentingBrowser(as),
	})
}

func TestTokenExchangeTyped429GivesBackoffGuidanceAndNothingElse(t *testing.T) {
	as := newFakeAS(t)
	as.tokenOverride = rateLimited("30", "30")
	token, err := loginAgainst(t, as)
	if err == nil || token != nil {
		t.Fatalf("Login() = %v, %v; want a refusal and no token", token, err)
	}
	msg := err.Error()
	for _, want := range []string{"rate", "30 seconds", "hadron auth login"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
	for _, leak := range []string{"SECRET-DESCRIPTION", "RATE_LIMITED", "CREDENTIALS", "temporarily_unavailable", "HTTP 429", "code-456", as.seenVerifier} {
		if leak != "" && strings.Contains(msg, leak) {
			t.Errorf("error %q must not carry %q", msg, leak)
		}
	}
	if !strings.Contains(msg, "not a rejected credential") || strings.Contains(msg, "invalid_") {
		t.Errorf("the message must say a rate limit is not a rejected credential: %q", msg)
	}
	if exitcode.FromError(err) == exitcode.AuthRequired {
		t.Error("a rate limit must not exit as AuthRequired")
	}
	if as.tokenCalls != 1 {
		t.Errorf("token endpoint called %d times: the CLI must not retry on its own", as.tokenCalls)
	}
}

func TestTokenExchangeBackoffFollowsTheHeader(t *testing.T) {
	tests := []struct {
		name, header, body, want string
	}{
		{"header only", "45", "null", "45 seconds"},
		{"body only", "", "20", "20 seconds"},
		{"the header wins over the body", "90", "5", "90 seconds"},
		{"one second", "1", "1", "1 second"},
		{"minutes", "600", "600", "10 minutes"},
		{"a garbage header falls back to the body", "soon", "15", "15 seconds"},
		{"a negative header falls back to the body", "-4", "15", "15 seconds"},
		{"an absurd header is not trusted", "999999999", "15", "15 seconds"},
		{"nothing usable still says to wait", "soon", "null", "a little while"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			as := newFakeAS(t)
			as.tokenOverride = rateLimited(tt.header, tt.body)
			_, err := loginAgainst(t, as)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want %q in %v", tt.want, err)
			}
		})
	}
}

func TestTokenExchangeRecognisesEitherTypedMarker(t *testing.T) {
	for name, body := range map[string]string{
		"RATE_LIMITED only":            `{"code":"RATE_LIMITED","retryAfterSeconds":7}`,
		"temporarily_unavailable only": `{"error":"temporarily_unavailable"}`,
	} {
		t.Run(name, func(t *testing.T) {
			as := newFakeAS(t)
			as.tokenOverride = func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "12")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(body))
			}
			_, err := loginAgainst(t, as)
			if err == nil || !strings.Contains(err.Error(), "12 seconds") || strings.Contains(err.Error(), "temporarily_unavailable") {
				t.Fatalf("%v", err)
			}
		})
	}
}

// An untyped 429 (a gateway's, say) is not this refusal: it keeps the generic
// path, and a typed body on any other status is not honoured as a rate limit.
func TestTokenExchangeOnlyATyped429IsARateLimit(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"an untyped 429":                  {429, `gateway says slow down`},
		"a typed body on a 400":           {400, `{"code":"RATE_LIMITED","retryAfterSeconds":9}`},
		"a typed body on a 503":           {503, `{"error":"temporarily_unavailable"}`},
		"the wrong type of marker on 429": {429, `{"code":"OTHER","retryAfterSeconds":9}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			as := newFakeAS(t)
			as.tokenOverride = func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "9")
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}
			_, err := loginAgainst(t, as)
			if err == nil || !strings.Contains(err.Error(), "token exchange returned HTTP") {
				t.Fatalf("want the generic exchange failure, got %v", err)
			}
		})
	}
}

func TestTokenExchangeRateLimitIsNotAnInternalFaultOrAScopeRefusal(t *testing.T) {
	srv := httptest.NewServer(rateLimited("30", "30"))
	defer srv.Close()
	_, err := exchangeCode(loginCtx(t), srv.Client(), srv.URL, "cid", "code", "ver", "http://127.0.0.1:1/cb", "")
	if err == nil || strings.Contains(err.Error(), "refusing to store") || strings.Contains(err.Error(), "request id") {
		t.Fatalf("%v", err)
	}
}
