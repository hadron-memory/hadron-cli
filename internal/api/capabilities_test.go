package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
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

// The P2-2 pins (Andi #12479, Ada #12500): a per-process cache that honours
// the endpoint's max-age and revalidates by ETag — a 304 means unchanged.

func TestCapabilitiesHonoursMaxAgeWithinTheProcess(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("ETag", `"abc"`)
		_, _ = w.Write([]byte(`{"capabilities":["node-signing"]}`))
	}))
	defer srv.Close()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		caps, err := Capabilities(ctx, srv.URL)
		if err != nil || !caps["node-signing"] {
			t.Fatalf("call %d: caps=%v err=%v", i, caps, err)
		}
	}
	if hits != 1 {
		t.Fatalf("wire requests = %d, want 1 (max-age=60 holds within the process)", hits)
	}
}

func TestCapabilitiesRevalidatesWithIfNoneMatchPastMaxAge(t *testing.T) {
	var hits int32
	var sawINM atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if n := atomic.LoadInt32(&hits); n >= 2 {
			sawINM.Store(r.Header.Get("If-None-Match"))
			if r.Header.Get("If-None-Match") == `"v1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte(`{"capabilities":["node-signing"]}`))
	}))
	defer srv.Close()
	ctx := context.Background()
	if _, err := Capabilities(ctx, srv.URL); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Age the entry past its window: the next call revalidates.
	capabilitiesNow = func() time.Time { return time.Now().Add(2 * time.Minute) }
	t.Cleanup(func() { capabilitiesNow = time.Now })
	caps, err := Capabilities(ctx, srv.URL)
	if err != nil || !caps["node-signing"] {
		t.Fatalf("revalidated: caps=%v err=%v", caps, err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("wire requests = %d, want 2 (one fill + one revalidation)", got)
	}
	if sawINM.Load() != `"v1"` {
		t.Fatalf("If-None-Match = %v, want the held ETag", sawINM.Load())
	}
	// A renewed 304 window holds again: no further wire traffic.
	caps, err = Capabilities(ctx, srv.URL)
	if err != nil || !caps["node-signing"] {
		t.Fatalf("post-304: caps=%v err=%v", caps, err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("wire requests after 304 = %d, want 2 (the 304 renews the window)", got)
	}
}

func TestCapabilitiesRevalidation200ReplacesTheBody(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) >= 2 {
			w.Header().Set("Cache-Control", "public, max-age=60")
			w.Header().Set("ETag", `"v2"`)
			_, _ = w.Write([]byte(`{"capabilities":[]}`)) // the switch turned OFF
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte(`{"capabilities":["node-signing"]}`))
	}))
	defer srv.Close()
	ctx := context.Background()
	if caps, _ := Capabilities(ctx, srv.URL); !caps["node-signing"] {
		t.Fatal("fill must read on")
	}
	capabilitiesNow = func() time.Time { return time.Now().Add(2 * time.Minute) }
	t.Cleanup(func() { capabilitiesNow = time.Now })
	caps, err := Capabilities(ctx, srv.URL)
	if err != nil {
		t.Fatalf("revalidated: %v", err)
	}
	if caps["node-signing"] {
		t.Fatal("a changed 200 must replace the held body — the switch turned off")
	}
}
