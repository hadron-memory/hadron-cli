package comment

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// A stub that answers only the DeleteCommentThread mutation, with a canned
// body, and counts requests so a test can pin sent-once-never-retried (#2538).
func threadDeleteStub(t *testing.T, body string, calls *int) *httptest.Server {
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
		*calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runDeleteThread runs `comment delete --thread` against the stub once, the
// way the binary would: a factory on the stub server, auth from the env.
func runDeleteThread(t *testing.T, srvURL string, jsonMode bool) (string, error) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_TOKEN", "hdr_user_test")
	t.Setenv("HADRON_SERVER", srvURL)
	io, _, _ := output.Test()
	out := &strings.Builder{}
	io.Out = output.Tracked(out)
	f := &cmdutil.Factory{IOStreams: io, JSON: jsonMode, ConfigFn: config.Load, TokenStoreFn: store.Resolve}
	cmd := NewCmd(f, ConnectGraphQL(f))
	cmd.SetContext(t.Context())
	cmd.SetArgs([]string{"delete", "hrn:node:acme.com:kb:review:1", "--thread", "--reason", "cleanup", "--yes"})
	err := cmd.Execute()
	return out.String(), err
}

// cli#2538: comment delete --thread must not exit 0 when the mutation's answer
// is absent, null or false. The write was sent, so an absent/null answer means
// the outcome is UNKNOWN: exit 1 with read-the-thread advice, never a blind
// retry (#2493 pattern — the envelope arrived, so exit 7's retry-blind class is
// wrong here). An explicit false is client-side defence (the server only ever
// returns true or throws): exit 5 with the deleted:false detail kept. The true
// answer keeps today's success receipt.
func TestCommentDeleteThreadOutcome2538(t *testing.T) {
	shapes := map[string]string{
		"absent": `{"data":{}}`,
		"null":   `{"data":{"deleteCommentThread":null}}`,
		"false":  `{"data":{"deleteCommentThread":false}}`,
		"true":   `{"data":{"deleteCommentThread":true}}`,
	}
	for _, shape := range []string{"absent", "null", "false", "true"} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(shape+"/json="+jsonBool(jsonMode), func(t *testing.T) {
				var calls int
				srv := threadDeleteStub(t, shapes[shape], &calls)
				out, err := runDeleteThread(t, srv.URL, jsonMode)
				if calls != 1 {
					t.Fatalf("the mutation must be sent once and never retried: %d requests", calls)
				}
				switch shape {
				case "true":
					if err != nil {
						t.Fatalf("a true answer is a success: %v", err)
					}
				case "absent", "null":
					if out != "" {
						t.Fatalf("an unanswered delete writes no receipt: stdout=%q", out)
					}
					if code := exitcode.FromError(err); code != exitcode.Error {
						t.Fatalf("exit %d, want %d (outcome unknown): %v", code, exitcode.Error, err)
					}
					for _, want := range []string{"no delete-thread result", "outcome unknown", "read the thread"} {
						if !strings.Contains(err.Error(), want) {
							t.Fatalf("message lacks %q: %q", want, err.Error())
						}
					}
				case "false":
					if out != "" {
						t.Fatalf("a refused delete writes no receipt: stdout=%q", out)
					}
					if code := exitcode.FromError(err); code != exitcode.Conflict {
						t.Fatalf("exit %d, want %d (conflict, deleted:false): %v", code, exitcode.Conflict, err)
					}
					for _, want := range []string{"was not deleted", "deleted:false", "read the thread"} {
						if !strings.Contains(err.Error(), want) {
							t.Fatalf("message lacks %q: %q", want, err.Error())
						}
					}
				}
			})
		}
	}
}

// The success receipt keeps its shape in both modes: the ✓ line in text, the
// explicit DTO (thread, deleted:true) in --json.
func TestCommentDeleteThreadSuccessReceipt2538(t *testing.T) {
	body := `{"data":{"deleteCommentThread":true}}`

	out, err := runDeleteThread(t, threadDeleteStub(t, body, new(int)).URL, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "✓ deleted the thread of hrn:node:acme.com:kb:review:1") {
		t.Fatalf("text receipt: %q", out)
	}

	out, err = runDeleteThread(t, threadDeleteStub(t, body, new(int)).URL, true)
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

func jsonBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
