package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#898: an internal fault renders as one CLI-written sentence with the
// request id, exits 1 (the server answered), and never echoes the server text.
func faultServer(t *testing.T, status int, header string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if header != "" {
			w.Header().Set("X-Request-Id", header)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"errors":[{"message":"secret stack","extensions":{"code":"INTERNAL_SERVER_ERROR","requestId":"r-123"}}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestInternalFaultTextAndJSON(t *testing.T) {
	for _, status := range []int{200, 500} {
		srv := faultServer(t, status, "h-999")

		f, out := testFactory(t)
		errOut := captureErrOut(f)
		root := NewRootCmd(f)
		root.SetArgs([]string{"memory", "ls", "--server", srv.URL})
		err := root.Execute()
		if err == nil {
			t.Fatal("expected failure")
		}
		if got := exitCodeFor(err); got != exitcode.Error {
			t.Errorf("status %d: exit = %d, want 1", status, got)
		}
		renderError(f, err)
		all := out.String() + errOut()
		if !strings.Contains(all, "request id: r-123") || strings.Contains(all, "secret stack") || strings.Contains(all, "h-999") {
			t.Errorf("status %d text output wrong: %q", status, all)
		}

		f, out = testFactory(t)
		root = NewRootCmd(f)
		root.SetArgs([]string{"memory", "ls", "--json", "--server", srv.URL})
		err = root.Execute()
		if err == nil {
			t.Fatal("expected failure")
		}
		renderError(f, err)
		if strings.Contains(out.String(), "secret stack") {
			t.Errorf("--json echoed raw server text: %s", out.String())
		}
		var env struct {
			Error map[string]any `json:"error"`
		}
		if jerr := json.Unmarshal([]byte(out.String()), &env); jerr != nil {
			t.Fatalf("stdout not JSON: %v\n%s", jerr, out.String())
		}
		if env.Error["code"] != float64(1) || env.Error["requestId"] != "r-123" || env.Error["serverCode"] != "INTERNAL_SERVER_ERROR" {
			t.Errorf("envelope = %v", env.Error)
		}
		if !strings.Contains(env.Error["message"].(string), "r-123") {
			t.Errorf("message lacks id: %v", env.Error["message"])
		}
	}
}

func TestInternalFaultJSONOmitsRequestIDWhenAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"errors":[{"message":"secret","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`))
	}))
	t.Cleanup(srv.Close)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--json", "--server", srv.URL})
	err := root.Execute()
	renderError(f, err)
	if strings.Contains(out.String(), "requestId") || !strings.Contains(out.String(), `"serverCode": "INTERNAL_SERVER_ERROR"`) {
		t.Errorf("envelope = %s", out.String())
	}
}

// `hadron api` prints the server's body verbatim by design: no mapping.
func TestAPIRawStaysVerbatimOnInternalFault(t *testing.T) {
	srv := faultServer(t, 500, "h-999")
	f, out := testFactory(t)
	errOut := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"api", "query Q { __typename }", "--json", "--server", srv.URL})
	err := root.Execute()
	renderError(f, err)
	if !strings.Contains(out.String()+errOut()+errString(err), "secret stack") {
		t.Errorf("hadron api must stay verbatim; got out=%q err=%v", out.String(), err)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
