package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// hadron-cli#1759 rows 3 and 4 (follow-ups to #1632): what the sent-credential
// ledger counts. It records a credential-bearing HTTP ATTEMPT, made after every
// local refusal and immediately before http.Client.Do. It is not proof that
// the request reached the network or the server. None of this changes which
// credential is sent, its headers, retries or authority.

func doerWithLedger1759(token string, rt http.RoundTripper) (*bearerDoer, *CredentialLedger) {
	l := &CredentialLedger{}
	d := &bearerDoer{token: token, inner: &http.Client{Transport: rt}}
	d.onSent = func() { l.Sent(CredentialStoredUser) }
	return d, l
}

// Row 3: a request the CLI refuses locally (a session over cleartext http) is
// never attempted, so it records nothing. The control: the same request to a
// secure URL is attempted and recorded.
func TestCredentialLedger1759LocalRefusalRecordsNoAttempt(t *testing.T) {
	t.Setenv(EnvAllowHTTP, "")
	var calls atomic.Int32
	ok := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}, Request: r}, nil
	})
	ctx := WithSession(context.Background(), "s-1759")

	d, l := doerWithLedger1759("hdr_user_x", ok)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://srv.example/graphql", nil)
	if _, err := d.Do(req); !errors.Is(err, ErrRedirectPolicy) {
		t.Fatalf("a session over cleartext must be refused locally, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("a locally refused request reached the transport %d times", calls.Load())
	}
	if l.Only(CredentialStoredUser) {
		t.Error("a locally refused request was recorded as a credential attempt")
	}

	// Control: allowed, attempted, recorded.
	d, l = doerWithLedger1759("hdr_user_x", ok)
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, "https://srv.example/graphql", nil)
	resp, err := d.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if calls.Load() != 1 || !l.Only(CredentialStoredUser) {
		t.Fatalf("the allowed request: calls=%d recorded=%v", calls.Load(), l.Only(CredentialStoredUser))
	}
}

// Row 4: the typed client records the attempt even when the transport rejects
// it or the context was already cancelled, so nothing was transmitted. The
// header handed to the transport is the one the client always sends.
func TestCredentialLedger1759RecordsAttemptNotTransmission(t *testing.T) {
	t.Run("rejecting-transport", func(t *testing.T) {
		var sawBearer atomic.Bool
		reject := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			sawBearer.Store(r.Header.Get("Authorization") == "Bearer hdr_user_x")
			return nil, errors.New("transport refused the request")
		})
		d, l := doerWithLedger1759("hdr_user_x", reject)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://srv.example/graphql", nil)
		if _, err := d.Do(req); err == nil {
			t.Fatal("a rejecting transport must fail the request")
		}
		if !sawBearer.Load() {
			t.Error("the transport did not get the client's usual Authorization header")
		}
		if !l.Only(CredentialStoredUser) {
			t.Error("a credential-bearing attempt must be recorded even though the transport rejected it")
		}
	})
	t.Run("cancelled-context", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
		defer srv.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		d, l := doerWithLedger1759("hdr_user_x", srv.Client().Transport)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/graphql", nil)
		if _, err := d.Do(req); !errors.Is(err, context.Canceled) {
			t.Fatalf("want the cancellation, got %v", err)
		}
		if hits.Load() != 0 {
			t.Fatalf("a cancelled request reached the server %d times", hits.Load())
		}
		if !l.Only(CredentialStoredUser) {
			t.Error("the attempt must be recorded although nothing reached the server")
		}
	})
}

// Row 4, `hadron api`: RawGraphQL records the same attempt at the same point.
func TestCredentialLedger1759RawRecordsAttemptNotTransmission(t *testing.T) {
	t.Run("rejecting-transport", func(t *testing.T) {
		reject := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("transport refused the request")
		})
		l := &CredentialLedger{}
		ctx := WithRawCredentialSent(context.Background(), l, CredentialEnv)
		if _, err := RawGraphQL(ctx, "https://srv.example", "hdr_env_x", "query { __typename }", nil, &http.Client{Transport: reject}); err == nil {
			t.Fatal("a rejecting transport must fail the request")
		}
		if !l.Only(CredentialEnv) {
			t.Error("the raw attempt must be recorded even though the transport rejected it")
		}
	})
	t.Run("cancelled-context", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
		defer srv.Close()
		l := &CredentialLedger{}
		ctx, cancel := context.WithCancel(WithRawCredentialSent(context.Background(), l, CredentialEnv))
		cancel()
		if _, err := RawGraphQL(ctx, srv.URL, "hdr_env_x", "query { __typename }", nil, srv.Client()); err == nil {
			t.Fatal("a cancelled request must fail")
		}
		if hits.Load() != 0 {
			t.Fatalf("a cancelled raw request reached the server %d times", hits.Load())
		}
		if !l.Only(CredentialEnv) {
			t.Error("the raw attempt must be recorded although nothing reached the server")
		}
	})
	// A tokenless raw request records nothing, attempted or not.
	t.Run("no-token", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{}}`))
		}))
		defer srv.Close()
		l := &CredentialLedger{}
		ctx := WithRawCredentialSent(context.Background(), l, CredentialEnv)
		if _, err := RawGraphQL(ctx, srv.URL, "", "query { __typename }", nil, srv.Client()); err != nil {
			t.Fatal(err)
		}
		if l.Only(CredentialEnv) {
			t.Error("a request without a token must not record a credential")
		}
	})
}
