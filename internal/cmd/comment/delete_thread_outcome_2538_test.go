package comment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// A stub that answers only the DeleteCommentThread mutation, with a canned
// body, and counts requests so a test can pin sent-once-never-retried (#2538).
func threadDeleteStub(t *testing.T, body string, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.OperationName != "DeleteCommentThread" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected operation ` + req.OperationName + `"}]}`))
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runDeleteThread runs `comment delete --thread` against the stub once, the
// way the binary would: a factory on the stub server, auth from the env. It
// returns stdout, stderr and the command's error.
func runDeleteThread(t *testing.T, srvURL string, jsonMode bool) (string, string, error) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_TOKEN", "hdr_user_test")
	t.Setenv("HADRON_SERVER", srvURL)
	return runDeleteThreadWith(t, jsonMode, func(f *cmdutil.Factory) Connect { return ConnectGraphQL(f) })
}

// runDeleteThreadWith is runDeleteThread with the service connection chosen by
// the caller, so a test can inject a Service whose answer the server cannot
// produce.
func runDeleteThreadWith(t *testing.T, jsonMode bool, connect func(*cmdutil.Factory) Connect) (string, string, error) {
	t.Helper()
	ios, _, errOut := output.Test()
	out := &strings.Builder{}
	ios.Out = output.Tracked(out)
	f := &cmdutil.Factory{IOStreams: ios, JSON: jsonMode, ConfigFn: config.Load, TokenStoreFn: store.Resolve}
	cmd := NewCmd(f, connect(f))
	cmd.SetContext(t.Context())
	cmd.SetArgs([]string{"delete", "hrn:node:acme.com:kb:review:1", "--thread", "--reason", "cleanup", "--yes"})
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// cli#2538: comment delete --thread must not exit 0 when the mutation's answer
// is absent, null or false. The write was sent, so an absent/null answer means
// the outcome is UNKNOWN: exit 1 with read-the-thread advice, never a blind
// retry (#2493 pattern — the envelope arrived, so exit 7's retry-blind class is
// wrong here). An explicit false is client-side defence (the server only ever
// returns true or throws): exit 5 with the deleted:false detail kept. The true
// answer keeps today's success receipt.
func TestCommentDeleteThreadOutcome2538(t *testing.T) {
	shapes := []struct {
		name string
		body string
		want int // the exit code; 0 is success
	}{
		{"absent", `{"data":{}}`, exitcode.Error},
		{"null", `{"data":{"deleteCommentThread":null}}`, exitcode.Error},
		{"false", `{"data":{"deleteCommentThread":false}}`, exitcode.Conflict},
		{"true", `{"data":{"deleteCommentThread":true}}`, 0},
	}
	for _, shape := range shapes {
		for _, jsonMode := range []bool{false, true} {
			t.Run(shape.name+"/json="+strconv.FormatBool(jsonMode), func(t *testing.T) {
				var calls atomic.Int32
				srv := threadDeleteStub(t, shape.body, &calls)
				out, errOut, err := runDeleteThread(t, srv.URL, jsonMode)
				if n := calls.Load(); n != 1 {
					t.Fatalf("the mutation must be sent once and never retried: %d requests", n)
				}
				if got := exitcode.FromError(err); got != shape.want {
					t.Fatalf("exit %d, want %d: %v", got, shape.want, err)
				}
				// In --json mode the error is the caller's to render once; nothing
				// extra may reach stderr from the command itself, on success too (cli#2547).
				if jsonMode && errOut != "" {
					t.Fatalf("--json writes nothing to stderr: %q", errOut)
				}
				if shape.want == 0 {
					return
				}
				if out != "" {
					t.Fatalf("a delete that did not report success writes no receipt: stdout=%q", out)
				}
				wants := []string{"no delete-thread result", "outcome unknown", "read the thread"}
				if shape.name == "false" {
					wants = []string{"was not deleted", "deleted:false", "read the thread"}
				}
				for _, want := range wants {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("message lacks %q: %q", want, err.Error())
					}
				}
			})
		}
	}
}

// nilService is a Service whose DeleteThread breaks the contract with (nil,
// nil). The server cannot produce that today; a future implementer could, and
// the destructive path must not panic on it (cli#2547).
type nilService struct{ Service }

func (nilService) DeleteThread(context.Context, string, string) (*bool, error) { return nil, nil }

func TestCommentDeleteThreadNilServiceAnswer2547(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run("json="+strconv.FormatBool(jsonMode), func(t *testing.T) {
			out, errOut, err := runDeleteThreadWith(t, jsonMode, func(*cmdutil.Factory) Connect {
				return func(context.Context) (Service, error) { return nilService{}, nil }
			})
			if got := exitcode.FromError(err); got != exitcode.Error {
				t.Fatalf("exit %d, want %d (outcome unknown): %v", got, exitcode.Error, err)
			}
			if out != "" {
				t.Fatalf("an unanswered delete writes no receipt: stdout=%q", out)
			}
			if jsonMode && errOut != "" {
				t.Fatalf("--json writes nothing to stderr: %q", errOut)
			}
			for _, want := range []string{"no delete-thread result", "outcome unknown", "read the thread"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("message lacks %q: %q", want, err.Error())
				}
			}
		})
	}
}

// A GraphQL errors response and a dropped connection are both answers that say
// nothing about whether the delete applied: neither may print a receipt, and
// the mutation is sent once (cli#2547).
func TestCommentDeleteThreadErrorPaths2547(t *testing.T) {
	cases := []struct {
		name   string
		want   int // the exact exit code, so a regression to another class fails
		handle func(http.ResponseWriter, *http.Request)
	}{
		{"graphql-errors", exitcode.Error, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errors":[{"message":"boom"}],"data":null}`))
		}},
		// The connection dropped before an answer: exit 7 is the repo-wide
		// transport class (exitcode.Unavailable), and api.transportError appends
		// "This was a MUTATION: it may or may not have been applied — verify the
		// current state before retrying" for a write. Pinned so a regression to
		// another class fails; the nil-answer case above is exit 1 because the
		// server DID answer (#2538), not a transport failure.
		{"transport", exitcode.Unavailable, func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}},
	}
	for _, tc := range cases {
		for _, jsonMode := range []bool{false, true} {
			t.Run(tc.name+"/json="+strconv.FormatBool(jsonMode), func(t *testing.T) {
				var calls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					tc.handle(w, r)
				}))
				t.Cleanup(srv.Close)
				out, errOut, err := runDeleteThread(t, srv.URL, jsonMode)
				if err == nil {
					t.Fatal("an error answer must fail the command")
				}
				if got := exitcode.FromError(err); got != tc.want {
					t.Fatalf("exit %d, want %d: %v", got, tc.want, err)
				}
				if out != "" {
					t.Fatalf("no receipt on an error answer: stdout=%q", out)
				}
				if jsonMode && errOut != "" {
					t.Fatalf("--json writes nothing to stderr: %q", errOut)
				}
				if n := calls.Load(); n != 1 {
					t.Fatalf("the mutation must be sent once and never retried: %d requests", n)
				}
			})
		}
	}
}

// The success receipt keeps its shape in both modes: the ✓ line in text, the
// explicit DTO (thread, deleted:true) in --json.
func TestCommentDeleteThreadSuccessReceipt2538(t *testing.T) {
	body := `{"data":{"deleteCommentThread":true}}`

	out, _, err := runDeleteThread(t, threadDeleteStub(t, body, new(atomic.Int32)).URL, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "✓ deleted the thread of hrn:node:acme.com:kb:review:1") {
		t.Fatalf("text receipt: %q", out)
	}

	out, _, err = runDeleteThread(t, threadDeleteStub(t, body, new(atomic.Int32)).URL, true)
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Thread  string `json:"thread"`
		Deleted bool   `json:"deleted"`
	}
	if e := json.Unmarshal([]byte(out), &dto); e != nil || !dto.Deleted || dto.Thread != "hrn:node:acme.com:kb:review:1" {
		t.Fatalf("json receipt: %v %q", e, out)
	}
}
