package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// pushStub answers every GraphQL request with body (status), recording each
// operation name and the variables of the PushMemoryToGit request. It counts
// requests so "one request, no retry" is asserted, not inferred.
type pushStub struct {
	srv  *httptest.Server
	mu   sync.Mutex
	ops  []string
	vars json.RawMessage
}

func newPushStub(t *testing.T, status int, body string) *pushStub {
	t.Helper()
	s := &pushStub{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"capabilities":[]}`)
			return
		}
		var req struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.ops = append(s.ops, req.OperationName)
		if req.OperationName == "PushMemoryToGit" {
			s.vars = req.Variables
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *pushStub) opsSeen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ops...)
}

func runPush(t *testing.T, s *pushStub, extra ...string) (stdout, stderr string, err error) {
	t.Helper()
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"memory", "sync", "push", "acme.com:kb", "--server", s.srv.URL}, extra...))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func TestMemorySyncPush2513Success(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		s := newPushStub(t, 200, `{"data":{"pushMemoryToGit":true}}`)
		args := []string{"--yes"}
		if asJSON {
			args = append(args, "--json")
		}
		out, _, err := runPush(t, s, args...)
		if err != nil {
			t.Fatal(err)
		}
		if ops := s.opsSeen(); len(ops) != 1 || ops[0] != "PushMemoryToGit" {
			t.Fatalf("ops %v", ops)
		}
		var vars map[string]any
		if e := json.Unmarshal(s.vars, &vars); e != nil || len(vars) != 1 || vars["ref"] != "hrn:mem:acme.com:kb" {
			t.Fatalf("variables %s", s.vars)
		}
		if asJSON {
			var dto map[string]any
			if e := json.Unmarshal([]byte(out), &dto); e != nil {
				t.Fatal(e, out)
			}
			if len(dto) != 3 || dto["memory"] != "hrn:mem:acme.com:kb" || dto["operation"] != "push" || dto["pushed"] != true {
				t.Fatalf("dto %v", dto)
			}
		} else if strings.TrimSpace(out) != "pushed memory hrn:mem:acme.com:kb" {
			t.Fatalf("out %q", out)
		}
	}
}

func TestMemorySyncPush2513Confirm(t *testing.T) {
	// No --yes and no terminal: refused before any request, exit 2.
	s := newPushStub(t, 200, `{"data":{"pushMemoryToGit":true}}`)
	out, _, err := runPush(t, s)
	if exitCodeFor(err) != 2 || len(s.opsSeen()) != 0 || strings.Contains(out, "pushed") {
		t.Fatalf("err %v ops %v out %s", err, s.opsSeen(), out)
	}
	// Declined on a terminal: nothing sent, cancelled exit 6; the prompt names
	// the remote write and the read-branch warning.
	s = newPushStub(t, 200, `{"data":{"pushMemoryToGit":true}}`)
	f, out2, errOut := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "sync", "push", "acme.com:kb", "--server", s.srv.URL})
	err = root.Execute()
	if exitCodeFor(err) != 6 || len(s.opsSeen()) != 0 || strings.Contains(out2.String(), "pushed") {
		t.Fatalf("err %v ops %v out %s", err, s.opsSeen(), out2)
	}
	prompt := errOut.String() + out2.String()
	for _, want := range []string{"write branch", "remote", "data nodes"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q: %s", want, prompt)
		}
	}
	// Accepted on a terminal: one push.
	s = newPushStub(t, 200, `{"data":{"pushMemoryToGit":true}}`)
	f, out3, _ := testFactoryTTY(t, "y\n")
	root = NewRootCmd(f)
	root.SetArgs([]string{"memory", "sync", "push", "acme.com:kb", "--server", s.srv.URL})
	if err := root.Execute(); err != nil || len(s.opsSeen()) != 1 {
		t.Fatalf("err %v ops %v", err, s.opsSeen())
	}
	if !strings.Contains(out3.String(), "pushed memory hrn:mem:acme.com:kb") {
		t.Fatalf("no success line: %s", out3)
	}
}

func TestMemorySyncPush2513NeverSendsOtherOps(t *testing.T) {
	s := newPushStub(t, 200, `{"data":{"pushMemoryToGit":true}}`)
	if _, _, err := runPush(t, s, "--yes"); err != nil {
		t.Fatal(err)
	}
	for _, op := range s.opsSeen() {
		if op != "PushMemoryToGit" {
			t.Fatalf("unexpected operation %s", op)
		}
	}
}

func TestMemorySyncPush2513NotPushedAnswers(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"false", `{"data":{"pushMemoryToGit":false}}`},
		{"empty", `{"data":{}}`},
		{"null", `{"data":{"pushMemoryToGit":null}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, asJSON := range []bool{false, true} {
				s := newPushStub(t, 200, tc.body)
				args := []string{"--yes"}
				if asJSON {
					args = append(args, "--json")
				}
				out, _, err := runPush(t, s, args...)
				if exitCodeFor(err) != 7 || len(s.opsSeen()) != 1 {
					t.Fatalf("err %v ops %v", err, s.opsSeen())
				}
				if strings.Contains(out, "pushed") || strings.Contains(err.Error(), "pushed memory") {
					t.Fatalf("claimed a push: %s / %v", out, err)
				}
				if !strings.Contains(err.Error(), "may have happened") {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestMemorySyncPush2513Refusals(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		exit       int
	}{
		{"notfound", "NOT_FOUND", 4},
		{"forbidden", "FORBIDDEN", 8},
		{"encrypted", "ENCRYPTED_GIT_SYNC_UNAVAILABLE", 2},
		{"nosource", "BAD_USER_INPUT", 2},
		{"pushfailed", "INTERNAL_SERVER_ERROR", 1},
		{"nocode", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newPushStub(t, 200, `{"errors":[{"message":"push failed for https://user:SENTINEL@host/repo.git","extensions":{"code":"`+tc.code+`"}}]}`)
			out, errOut, err := runPush(t, s, "--yes")
			if exitCodeFor(err) != tc.exit || len(s.opsSeen()) != 1 {
				t.Fatalf("err %v ops %v", err, s.opsSeen())
			}
			if strings.Contains(out, "pushed") {
				t.Fatal(out)
			}
			if tc.code == "ENCRYPTED_GIT_SYNC_UNAVAILABLE" && !strings.Contains(err.Error(), "encrypted") {
				t.Fatal(err)
			}
			for _, got := range []string{out, errOut, err.Error()} {
				if strings.Contains(got, "SENTINEL") {
					t.Fatalf("credential leak: %q", got)
				}
			}
		})
	}
}

func TestMemorySyncPush2513Unanswered(t *testing.T) {
	s := newPushStub(t, 503, "upstream unavailable")
	out, _, err := runPush(t, s, "--yes")
	if exitCodeFor(err) != 7 || len(s.opsSeen()) != 1 || strings.Contains(out, "pushed") {
		t.Fatalf("err %v ops %v out %s", err, s.opsSeen(), out)
	}
	if !strings.Contains(err.Error(), "may have happened") || !strings.Contains(err.Error(), "before retrying") {
		t.Fatal(err)
	}
}

func TestMemorySyncPush2513Sentinels(t *testing.T) {
	// A sentinel URL and token inside server text reach none of stdout,
	// stderr, --json or the error text, on every refusal shape.
	const msg = "git push to https://user:SENTINEL@host/repo.git failed; token ghp_SENTINELTOKEN https://x:ghp_SENTINELTOKEN@h/r"
	for _, asJSON := range []bool{false, true} {
		for _, code := range []string{"", "INTERNAL_SERVER_ERROR", "BAD_USER_INPUT", "FORBIDDEN"} {
			s := newPushStub(t, 200, `{"errors":[{"message":"`+msg+`","extensions":{"code":"`+code+`"}}]}`)
			args := []string{"--yes"}
			if asJSON {
				args = append(args, "--json")
			}
			out, errOut, err := runPush(t, s, args...)
			if err == nil {
				t.Fatal("want error")
			}
			rendered := err.Error()
			for _, got := range []string{out, errOut, rendered} {
				if strings.Contains(got, "SENTINEL@") || strings.Contains(got, "x:ghp_") || strings.Contains(got, "ghp_SENTINELTOKEN") {
					t.Fatalf("credential leak (%s, json=%v): %q", code, asJSON, got)
				}
			}
		}
	}
}

func TestMemorySyncPush2513BareTokenShapes(t *testing.T) {
	// The well-known prefixed token shapes are masked wherever they sit in
	// server text; the redaction marker replaces them.
	for _, tok := range []string{"ghp_abcdefgh12345678", "gho_abcdefgh12345678", "ghs_abcdefgh12345678", "github_pat_11AAAAAAA_bbbbbbbbbbbb", "glpat-abcdefgh_12345678"} {
		s := newPushStub(t, 200, `{"errors":[{"message":"push rejected, credential `+tok+` was refused","extensions":{"code":"FORBIDDEN"}}]}`)
		out, errOut, err := runPush(t, s, "--yes", "--json")
		if err == nil || exitCodeFor(err) != 8 {
			t.Fatalf("err %v", err)
		}
		for _, got := range []string{out, errOut, err.Error()} {
			if strings.Contains(got, tok) {
				t.Fatalf("%s leaked: %q", tok, got)
			}
		}
		if !strings.Contains(err.Error(), "[redacted]") {
			t.Fatalf("no marker: %v", err)
		}
	}
}

func TestMemorySyncPush2513InternalErrorWording(t *testing.T) {
	s := newPushStub(t, 200, `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`)
	_, _, err := runPush(t, s, "--yes")
	if err == nil || !strings.Contains(err.Error(), "internal error") || strings.Contains(err.Error(), "no refusal code") {
		t.Fatalf("err %v", err)
	}
	s = newPushStub(t, 200, `{"errors":[{"message":"boom"}]}`)
	_, _, err = runPush(t, s, "--yes")
	if err == nil || !strings.Contains(err.Error(), "no refusal code") {
		t.Fatalf("err %v", err)
	}
}

func TestMemorySyncPush2513RefForms(t *testing.T) {
	// An invalid ref is refused locally with no request; every accepted
	// spelling is sent canonicalized.
	s := newPushStub(t, 200, `{"data":{"pushMemoryToGit":true}}`)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "sync", "push", "not a ref", "--yes", "--server", s.srv.URL})
	if err := root.Execute(); exitCodeFor(err) != 2 || len(s.opsSeen()) != 0 {
		t.Fatalf("err %v ops %v", err, s.opsSeen())
	}
	for _, in := range []string{"hrn:mem:acme.com:kb", "acme.com::kb", "hrn:memory:acme.com::kb"} {
		s := newPushStub(t, 200, `{"data":{"pushMemoryToGit":true}}`)
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"memory", "sync", "push", in, "--yes", "--server", s.srv.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		var vars map[string]any
		if e := json.Unmarshal(s.vars, &vars); e != nil || vars["ref"] != "hrn:mem:acme.com:kb" {
			t.Fatalf("%s: variables %s", in, s.vars)
		}
	}
}

func TestMemorySyncPush2513JSONErrorShape(t *testing.T) {
	s := newPushStub(t, 200, `{"errors":[{"message":"nope","extensions":{"code":"FORBIDDEN"}}]}`)
	out, _, err := runPush(t, s, "--yes", "--json")
	if err == nil || strings.Contains(out, `"pushed":true`) || strings.Contains(out, `"pushed": true`) {
		t.Fatalf("err %v out %s", err, out)
	}
}
