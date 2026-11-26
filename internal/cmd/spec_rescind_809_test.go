package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#809 (split A, Vera 2026-10-31): `spec rescind` retires a spec WITHOUT a
// successor. One guarded updateNode (immutable node id + expectedRevision) sets
// the `superseded` tag, the banner (body) and the abstract together; there is no
// edge, no allocation and no mint. Synthetic transport only.

const rescindBody = "# msg:010:02 — W2\n\nThe rule text, with {{name}}.\n"

// rescindServer answers each operation by name. cli#1736: the retirement is one
// retireSpec request (spec_retire_door_1736_test.go pins its behaviour); these
// tests keep the command's input, dry-run and output contract.
type rescindServer struct {
	t     *testing.T
	tags  string // JSON array of the node's tags
	abstr string // JSON value of the abstract
	calls map[string]int
	vars  map[string][]json.RawMessage
	// cli#1912: who the caller is and what the server says about their write
	// access. authUser "" = a credential with no user (the check is skipped).
	authUser string
	canWrite bool
}

func (s *rescindServer) serve() *httptest.Server {
	s.calls, s.vars = map[string]int{}, map[string][]json.RawMessage{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		s.calls[body.OperationName]++
		s.vars[body.OperationName] = append(s.vars[body.OperationName], body.Variables)
		w.Header().Set("Content-Type", "application/json")
		content, _ := json.Marshal(rescindBody)
		switch body.OperationName {
		case "ResolveUrn":
			_, _ = w.Write([]byte(resolveSpecJSON))
		case "GetSpecNodeForEdit":
			_, _ = w.Write([]byte(`{"data":{"node":{"id":"sp1","memoryId":"mem1","loc":"msg:010:02","name":"msg:010:02 — W2","description":null,"tags":` + s.tags +
				`,"role":null,"content":` + string(content) + `,"abstract":` + s.abstr + `,"abstractOriginHash":null,"revision":7}}}`))
		case "AuthContext":
			if s.authUser == "" {
				_, _ = w.Write([]byte(`{"data":{"authContext":null}}`))
			} else {
				_, _ = w.Write([]byte(`{"data":{"authContext":{"principalType":"USER","appId":null,"agentId":null,"user":{"id":"` + s.authUser + `","name":"U","email":null,"handle":"u","githubUsername":null,"roles":[]},"apiKey":null,"impersonation":null}}}`))
			}
		case "EffectiveAccess":
			_, _ = w.Write([]byte(effectiveAccessJSON(s.canWrite)))
		case "RetireSpec":
			_, _ = w.Write([]byte(retiredAnswer(body.Variables)))
		default:
			s.t.Errorf("unexpected operation %q (rescind sends one retireSpec and no node write, edge or create)", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected operation"}]}`))
		}
	}))
	s.t.Cleanup(srv.Close)
	return srv
}

var rescindArgs = []string{"--reason", "ruling 0099 A", "--summary", "A memory was once drafted at creation.", "--yes"}

func runRescind(t *testing.T, s *rescindServer, stdin string, args ...string) (string, *rescindServer, error) {
	t.Helper()
	if s.tags == "" {
		s.tags = `["spec"]`
	}
	if s.abstr == "" {
		s.abstr = "null"
	}
	s.t = t
	srv := s.serve()
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader(stdin)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"spec", "rescind", "msg:010:02", "-m", specMem, "--server", srv.URL}, args...))
	err := root.Execute()
	return out.String(), s, err
}

// --reason and a non-empty --summary are required; both refuse before ANY request.
func TestSpecRescindRefusesMissingReasonOrEmptySummaryBeforeAnyRequest(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"no reason":     {[]string{"--summary", "x", "--yes"}, "--reason"},
		"blank reason":  {[]string{"--reason", "  ", "--summary", "x", "--yes"}, "--reason"},
		"no summary":    {[]string{"--reason", "r", "--yes"}, "--summary"},
		"blank summary": {[]string{"--reason", "r", "--summary", "   ", "--yes"}, "--summary"},
	} {
		t.Run(name, func(t *testing.T) {
			_, s, err := runRescind(t, &rescindServer{}, "", c.args...)
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Fatalf("exit = %d, want 2 (%v)", got, err)
			}
			// Not cobra's unknown-command refusal, which is also exit 2: the verb's own message.
			if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "unknown command") {
				t.Errorf("want the verb's own refusal naming %s, got %v", c.want, err)
			}
			if len(s.calls) != 0 {
				t.Errorf("no request may be sent, got %v", s.calls)
			}
		})
	}
}

func TestSpecRescindReadsTheSummaryFromStdin(t *testing.T) {
	_, s, err := runRescind(t, &rescindServer{}, "Text from stdin.\n", "--reason", "r", "--summary", "-", "--yes")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(s.vars["RetireSpec"]) != 1 || !strings.Contains(string(s.vars["RetireSpec"][0]), `"summary":"Text from stdin."`) {
		t.Errorf("retireSpec = %s, want the stdin summary", s.vars["RetireSpec"])
	}
}

// Without --yes a non-interactive run refuses before writing.
func TestSpecRescindNeedsYesWhenNonInteractive(t *testing.T) {
	_, s, err := runRescind(t, &rescindServer{}, "", "--reason", "r", "--summary", "s")
	if got := exitCodeFor(err); got != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("exit = %d, want 2 naming --yes (%v)", got, err)
	}
	if s.calls["RetireSpec"] != 0 {
		t.Error("an unconfirmed rescind must not write")
	}
}

// --json is a stable DTO with the documented keys.
func TestSpecRescindJSONContract(t *testing.T) {
	out, _, err := runRescind(t, &rescindServer{}, "", append([]string{"--json"}, rescindArgs...)...)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	var dto map[string]any
	if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil {
		t.Fatalf("%v\n%s", jerr, out)
	}
	for _, k := range []string{"loc", "memoryId", "nodeId", "revision", "retired", "wording", "abstract", "withdrawn", "dryRun", "alreadyRescinded", "tags"} {
		if _, ok := dto[k]; !ok {
			t.Errorf("--json is missing %q: %v", k, dto)
		}
	}
	if dto["retired"] != true || dto["withdrawn"] != false {
		t.Errorf("dto = %v, want retired true, withdrawn false", dto)
	}
	if tags, ok := dto["tags"].([]any); !ok || len(tags) == 0 {
		t.Errorf("tags must be a non-empty array, got %v", dto["tags"])
	}
}

type rescindFailingWriter struct{}

func (rescindFailingWriter) Write([]byte) (int, error) { return 0, io.ErrShortWrite }

func runRescindFailingOut(t *testing.T, s *rescindServer, args ...string) (*rescindServer, error) {
	t.Helper()
	if s.tags == "" {
		s.tags = `["spec"]`
	}
	if s.abstr == "" {
		s.abstr = "null"
	}
	s.t = t
	srv := s.serve()
	f, _ := testFactory(t)
	f.IOStreams.Out = rescindFailingWriter{}
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"spec", "rescind", "msg:010:02", "-m", specMem, "--server", srv.URL}, args...))
	return s, root.Execute()
}

// A result that cannot be printed is never a clean exit, and when the write landed the error says so
// rather than reading as a failed rescind (Andi's local P2: renderRescind dropped writer errors).
func TestSpecRescindUnwritableOutputIsNeverACleanExit(t *testing.T) {
	for name, extra := range map[string][]string{"text": nil, "json": {"--json"}} {
		t.Run("applied/"+name, func(t *testing.T) {
			s, err := runRescindFailingOut(t, &rescindServer{}, append(extra, rescindArgs...)...)
			if err == nil {
				t.Fatal("a failed stdout after the write must not exit 0")
			}
			if s.calls["RetireSpec"] != 1 {
				t.Errorf("want the one retirement, got %d", s.calls["RetireSpec"])
			}
			if !strings.Contains(err.Error(), "was rescinded") || !strings.Contains(err.Error(), "hadron spec get") {
				t.Errorf("the error must say the retirement succeeded and name the check, got %v", err)
			}
		})
		t.Run("dry-run/"+name, func(t *testing.T) {
			s, err := runRescindFailingOut(t, &rescindServer{}, append(append([]string{"--dry-run"}, extra...), rescindArgs...)...)
			if err == nil {
				t.Fatal("a failed stdout on a dry run must not exit 0")
			}
			if s.calls["RetireSpec"] != 0 {
				t.Error("a dry run never writes")
			}
		})
	}
}
