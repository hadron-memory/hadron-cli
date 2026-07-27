package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const serverInfoJSON = `{"data":{"serverInfo":{"version":"0.10.0","baseUrl":"https://srv.example"}}}`

func TestServerInfo(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"ServerInfo": serverInfoJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"server-info", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		URL           string `json:"url"`
		Version       string `json:"version"`
		BaseURL       string `json:"baseUrl"`
		Authenticated bool   `json:"authenticated"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.Version != "0.10.0" || dto.BaseURL != "https://srv.example" {
		t.Errorf("dto = %+v", dto)
	}
	// url is where the query was SENT — distinct from the server's own
	// reported baseUrl, which is the whole point of carrying both.
	if dto.URL != gql.URL {
		t.Errorf("url = %q, want the queried server %q", dto.URL, gql.URL)
	}
	if !dto.Authenticated {
		t.Error("authenticated should be true when the test factory has a token")
	}
}

// serverInfo is a PUBLIC field, so the command must work signed out — that is
// what makes it a reachability probe rather than just a version check. Every
// other command exits 3 here.
func TestServerInfoWorksUnauthenticated(t *testing.T) {
	var sawAuthHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawAuthHeader = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(serverInfoJSON))
	}))
	t.Cleanup(srv.Close)

	f, out := testFactory(t)
	// testFactory installs a credential via HADRON_TOKEN and an empty
	// token store; clearing the env var leaves nothing to resolve.
	t.Setenv("HADRON_TOKEN", "")

	root := NewRootCmd(f)
	root.SetArgs([]string{"server-info", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("server-info must work signed out, got: %v", err)
	}
	if sawAuthHeader {
		t.Error("no Authorization header should be sent when signed out")
	}
	var dto struct {
		Version       string `json:"version"`
		Authenticated bool   `json:"authenticated"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.Version != "0.10.0" {
		t.Errorf("version = %q", dto.Version)
	}
	if dto.Authenticated {
		t.Error("authenticated must be false when no credential was sent")
	}
}

// A server whose reported base URL disagrees with the one queried is behind a
// proxy without its public base URL configured — every absolute URL it emits
// then points somewhere wrong, so the human output must call it out.
func TestServerInfoFlagsBaseURLMismatch(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ServerInfo": `{"data":{"serverInfo":{"version":"0.10.0","baseUrl":"http://internal:4000"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"server-info", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "http://internal:4000") || !strings.Contains(got, "different base url") {
		t.Errorf("a base-url mismatch should be called out: %q", got)
	}
}

// The converse: when the two agree there must be no warning, or the useful
// signal drowns in noise on every healthy server.
func TestServerInfoNoWarningWhenBaseURLAgrees(t *testing.T) {
	// The fake echoes back its own URL as baseUrl, which is what a correctly
	// configured server does.
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"serverInfo":{"version":"0.10.0","baseUrl":"` + srv.URL + `"}}}`))
	}))
	t.Cleanup(srv.Close)

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"server-info", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out.String(), "different base url") {
		t.Errorf("no mismatch warning expected when the URLs agree: %q", out.String())
	}
}

// A trailing slash on --server must not trip the mismatch warning: the client
// trims it when building the endpoint, so both spellings reach the same
// deployment (review on #302).
func TestServerInfoNoWarningOnTrailingSlash(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"serverInfo":{"version":"0.10.0","baseUrl":"` + srv.URL + `"}}}`))
	}))
	t.Cleanup(srv.Close)

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"server-info", "--server", srv.URL + "/"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out.String(), "different base url") {
		t.Errorf("a trailing slash is not a mismatch: %q", out.String())
	}
}

// A misbehaving server returning null for the non-null field must error
// rather than panic, and the message must name what was queried.
func TestServerInfoNullResult(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"ServerInfo": `{"data":{"serverInfo":null}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"server-info", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error for a null serverInfo")
	}
	if !strings.Contains(err.Error(), gql.URL) {
		t.Errorf("error should name the server queried, got %v", err)
	}
}

func TestServerInfoRejectsArgs(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"server-info", "extra", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a usage error for an unexpected argument")
	}
	// Cobra's own arg errors are plain errors; Execute classifies them as
	// usage (exit 2) via isUsageError, so assert through that same path
	// rather than exitcode.FromError, which only sees a CodedError.
	if !isUsageError(err) {
		t.Errorf("error should classify as a usage error (exit %d), got %v", exitcode.Usage, err)
	}
}
