package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCapabilitiesParsesOnSwitches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/capabilities" {
			t.Errorf("path = %s, want /capabilities", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("the discovery endpoint must not receive credentials, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"capabilities":["node-signing","passkey-registration"]}`))
	}))
	defer srv.Close()

	caps, err := Capabilities(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps["node-signing"] || !caps["passkey-registration"] {
		t.Fatalf("caps = %v, want both switches on", caps)
	}
}

func TestCapabilitiesOlderServer404IsAnError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := Capabilities(context.Background(), srv.URL); err == nil {
		t.Fatal("an older server's 404 must read as an error, so callers treat it as unadvertised")
	}
}

func TestCapabilitiesOffSwitchOmittedNeverFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"capabilities":["passkey-registration"]}`))
	}))
	defer srv.Close()
	caps, err := Capabilities(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if caps["node-signing"] {
		t.Fatal("an off switch is omitted — it can never read as on")
	}
}

func TestCapabilitiesUnparsableBodyIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	if _, err := Capabilities(context.Background(), srv.URL); err == nil {
		t.Fatal("an unparsable body must read as an error")
	}
}
