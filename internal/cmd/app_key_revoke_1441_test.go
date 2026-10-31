package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#1441 (ruling: revoke matches create): an answer that is not a definite
// refusal — an internal-fault envelope, a reply that does not decode, an
// unrecognised error — leaves the revocation's outcome unknown, so it carries
// the same "may already be revoked" guidance as a lost answer. Exit code,
// server code and the internal-fault request id are unchanged; one request,
// never retried.
func TestAppKey1441RevokeUncertainAnswerHint(t *testing.T) {
	const fault = `{"errors":[{"message":"Internal server error","extensions":{"code":"INTERNAL_SERVER_ERROR","requestId":"req-1441"}}]}`
	for _, tc := range []struct {
		name, body, serverCode, requestID string
		status                            int
	}{
		{name: "internal-fault-500", status: 500, body: fault, serverCode: "INTERNAL_SERVER_ERROR", requestID: "req-1441"},
		{name: "internal-fault-200", status: 200, body: fault, serverCode: "INTERNAL_SERVER_ERROR", requestID: "req-1441"},
		{name: "undecodable", status: 200, body: `not json SENTINEL-1441`},
		{name: "wrong-typed-data", status: 200, body: `{"data":{"revokeAppKey":"SENTINEL-1441"}}`},
		{name: "unrecognised-code", status: 200, body: `{"errors":[{"message":"odd answer","extensions":{"code":"SOME_NEW_CODE"}}]}`, serverCode: "SOME_NEW_CODE"},
	} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/text", true: "/json"}[jsonMode], func(t *testing.T) {
				srv, n := keyServer(t, tc.status, tc.body)
				args := []string{"revoke", "key_9", "--yes"}
				if jsonMode {
					args = append(args, "--json")
				}
				r := runKeyArgs(t, srv, args...)
				if r.err == nil {
					t.Fatal("uncertain answer reported success")
				}
				if code := exitCodeFor(r.err); code != 1 {
					t.Fatalf("exit %d, want 1 (unchanged): %v", code, r.err)
				}
				if n.Load() != 1 {
					t.Fatalf("requests %d, want exactly one (never retried)", n.Load())
				}
				msg := r.err.Error()
				for _, want := range []string{"may already be revoked", "not retried", "hadron app key list <app-ref>"} {
					if !strings.Contains(msg, want) {
						t.Errorf("missing %q: %s", want, msg)
					}
				}
				if tc.requestID != "" && !strings.Contains(msg, tc.requestID) {
					t.Errorf("internal-fault request id lost: %s", msg)
				}
				if strings.Contains(msg, "SENTINEL-1441") {
					t.Errorf("raw reply echoed: %s", msg)
				}
				if !jsonMode {
					return
				}
				// The envelope a --json caller gets, through the production renderer.
				f, out := testFactory(t)
				f.JSON = true
				if code := renderError(f, r.err); code != 1 {
					t.Fatalf("rendered exit %d", code)
				}
				var env struct {
					Error struct {
						Code       int    `json:"code"`
						ServerCode string `json:"serverCode"`
						RequestID  string `json:"requestId"`
						Message    string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal([]byte(out.String()), &env); err != nil {
					t.Fatal(err, out.String())
				}
				if env.Error.Code != 1 || env.Error.ServerCode != tc.serverCode || env.Error.RequestID != tc.requestID || !strings.Contains(env.Error.Message, "may already be revoked") {
					t.Fatalf("envelope: %s", out.String())
				}
			})
		}
	}
}

func TestAppKey1441RevokeHelpNamesUncertainAnswers(t *testing.T) {
	for _, args := range [][]string{{"app", "key", "revoke", "--help"}, {"agentic-usage"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "internal server error") {
			t.Errorf("%v does not name an internal server error as an uncertain revoke answer", args)
		}
	}
}
