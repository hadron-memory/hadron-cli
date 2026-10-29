package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const faultBody = `{"errors":[{"message":"secret stack","extensions":{"code":"INTERNAL_SERVER_ERROR","requestId":"r-123"}}]}`

// run drives one request through the real client boundary and MapError.
func runFault(t *testing.T, status int, header, body string) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if header != "" {
			w.Header().Set("X-Request-Id", header)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	client, err := NewClient(srv.URL, "", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	var data struct{}
	err = client.MakeRequest(context.Background(), &graphql.Request{Query: "query Q { __typename }", OpName: "Q"}, &graphql.Response{Data: &data})
	if err == nil {
		t.Fatal("expected an error")
	}
	return MapError(err)
}

func TestInternalFaultMapping(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		header     string
		body       string
		wantCode   int
		wantSubstr string
		wantID     string
	}{
		{"200 with errors", 200, "", faultBody, exitcode.Error, "request id: r-123", "r-123"},
		{"500 envelope is not transport", 500, "", faultBody, exitcode.Error, "request id: r-123", "r-123"},
		{"extensions beat header", 500, "h-999", faultBody, exitcode.Error, "request id: r-123", "r-123"},
		{"header fallback", 500, "h-999", `{"errors":[{"message":"secret stack","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, exitcode.Error, "request id: h-999", "h-999"},
		{"no id at all", 500, "", `{"errors":[{"message":"secret stack","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, exitcode.Error, "no request id was returned", ""},
		{"hostile body id dropped, header used", 200, "h-1", `{"errors":[{"message":"x","extensions":{"code":"INTERNAL_SERVER_ERROR","requestId":"a\u001b[31mb"}}]}`, exitcode.Error, "request id: h-1", "h-1"},
		{"overlong id dropped", 200, "", `{"errors":[{"message":"x","extensions":{"code":"INTERNAL_SERVER_ERROR","requestId":"` + strings.Repeat("a", 65) + `"}}]}`, exitcode.Error, "no request id was returned", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runFault(t, tc.status, tc.header, tc.body)
			if got := exitcode.FromError(err); got != tc.wantCode {
				t.Fatalf("exit = %d, want %d (%v)", got, tc.wantCode, err)
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.wantSubstr) {
				t.Errorf("message %q missing %q", msg, tc.wantSubstr)
			}
			for _, bad := range []string{"secret stack", "\x1b", "()", "<id>"} {
				if strings.Contains(msg, bad) {
					t.Errorf("message %q must not contain %q", msg, bad)
				}
			}
			f, ok := InternalFaultOf(err)
			if !ok || f.ServerCode != InternalServerErrorCode || f.RequestID != tc.wantID {
				t.Errorf("InternalFaultOf = %+v, %v; want id %q", f, ok, tc.wantID)
			}
		})
	}
}

func TestInternalFaultDoesNotCaptureOtherFailures(t *testing.T) {
	// Typed refusals keep their exit code and text; an unrelated unknown code
	// does not get the internal-fault message; a gateway page is still 7.
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		wantCode int
		wantText string
	}{
		{"forbidden", 200, `{"errors":[{"message":"nope","extensions":{"code":"FORBIDDEN"}}]}`, exitcode.Forbidden, "nope"},
		{"unauthenticated 401", 401, `{"errors":[{"message":"sign in","extensions":{"code":"UNAUTHENTICATED"}}]}`, exitcode.AuthRequired, "sign in"},
		{"unknown code keeps text", 200, `{"errors":[{"message":"odd thing","extensions":{"code":"SOMETHING_NEW","requestId":"r-1"}}]}`, exitcode.Error, "odd thing"},
		{"500 typed refusal not internal", 500, `{"errors":[{"message":"no such node","extensions":{"code":"NOT_FOUND"}}]}`, exitcode.NotFound, "no such node"},
		{"gateway 502 no envelope", 502, `<html>bad gateway</html>`, exitcode.Unavailable, ""},
		{"text alone is not the code", 500, `{"errors":[{"message":"INTERNAL_SERVER_ERROR","extensions":{"code":"X"}}]}`, exitcode.Error, "INTERNAL_SERVER_ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runFault(t, tc.status, "h-1", tc.body)
			if got := exitcode.FromError(err); got != tc.wantCode {
				t.Fatalf("exit = %d, want %d (%v)", got, tc.wantCode, err)
			}
			if _, ok := InternalFaultOf(err); ok {
				t.Errorf("must not be classified as an internal fault: %v", err)
			}
			if tc.wantText != "" && !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("message %q lost server text %q", err.Error(), tc.wantText)
			}
		})
	}
}

func TestPlainHTTPInternalFault(t *testing.T) {
	h := http.Header{"X-Request-Id": {"h-7"}}
	for _, tc := range []struct {
		name, body string
		want       string // "" = not a fault
	}{
		{"plain envelope", `{"error":"secret","code":"INTERNAL_SERVER_ERROR","requestId":"r-9"}`, "request id: r-9"},
		{"header fallback", `{"error":"secret","code":"INTERNAL_SERVER_ERROR"}`, "request id: h-7"},
		{"oauth server_error", `{"error":"server_error","error_description":"secret","request_id":"o-5"}`, "request id: o-5"},
		{"typed body", `{"error":"forbidden","code":"FORBIDDEN"}`, ""},
		{"oauth without request_id", `{"error":"server_error"}`, ""},
		{"not json", `<html>`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := PlainHTTPInternalFault(h, []byte(tc.body))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected fault: %v", err)
				}
				return
			}
			if err == nil || exitcode.FromError(err) != exitcode.Error || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("got %v, want %q without raw text", err, tc.want)
			}
		})
	}
}

func TestSanitizeRequestID(t *testing.T) {
	for in, want := range map[string]string{
		"6f1c0e52-8a7d-4b3e-9d1a-0c2b3a4d5e6f": "6f1c0e52-8a7d-4b3e-9d1a-0c2b3a4d5e6f",
		"a\x1b[2Jb":                            "",
		"":                                     "",
		"has space":                            "",
		strings.Repeat("a", 64):                strings.Repeat("a", 64),
		strings.Repeat("a", 65):                "",
	} {
		if got := SanitizeRequestID(in); got != want {
			t.Errorf("SanitizeRequestID(%q) = %q, want %q", in, got, want)
		}
	}
}
