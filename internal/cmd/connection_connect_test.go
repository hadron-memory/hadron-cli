package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmd/connection"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const (
	connectURL   = "https://hadron.example/connect/done?nonce=SECRETNONCE123456&x=1"
	connectStart = `{"data":{"startConnection":{"provider":"GMAIL","orgId":"org_1","authorizeUrl":"` + connectURL + `","expiresAt":"2099-01-01T00:00:00Z"}}}`
)

func connRows(rows ...string) string {
	return `{"data":{"exchangeConnections":[` + strings.Join(rows, ",") + `]}}`
}

func connRow(id, provider string) string {
	return `{"id":"` + id + `","organizationId":"org_1","userId":"u1","mailboxEmail":"` + id + `@acme.com","provider":"` + provider +
		`","displayName":null,"syncEnabled":true,"syncStatus":"IDLE","lastSyncAt":null,"createdAt":"2026-10-30T00:00:00Z","updatedAt":"2026-10-30T00:00:00Z"}`
}

type connectRun struct {
	out, errOut string
	err         error
	code        int
	ops         []string
	vars        map[string]json.RawMessage
	opened      []string
	slept       time.Duration
}

// runConnect drives `connection connect` against a scripted server. emailPolls
// answers each EmailConnections call in order (the first is the baseline); the
// last answer repeats. The clock only moves when the command sleeps.
func runConnect(t *testing.T, start string, emailPolls []string, openErr error, args ...string) *connectRun {
	t.Helper()
	r := &connectRun{}
	clock := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	prev := connection.Hooks
	t.Cleanup(func() { connection.Hooks = prev })
	connection.Hooks.Now = func() time.Time { return clock }
	connection.Hooks.Sleep = func(_ context.Context, d time.Duration) error { clock = clock.Add(d); r.slept += d; return nil }
	connection.Hooks.Open = func(u string) error { r.opened = append(r.opened, u); return openErr }

	emailCalls := 0
	srv, vars := captureGraphQLFunc(t, func(op string) string {
		r.ops = append(r.ops, op)
		switch op {
		case "StartConnection":
			return start
		case "EmailConnections":
			i := emailCalls
			emailCalls++
			if i >= len(emailPolls) {
				i = len(emailPolls) - 1
			}
			return emailPolls[i]
		}
		return ""
	})
	r.vars = vars
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL, "connection", "connect"}, args...))
	r.err = root.Execute()
	if r.err != nil {
		r.code = renderError(f, r.err)
	}
	r.out = out.String()
	r.errOut = f.IOStreams.ErrOut.(*strings.Builder).String()
	return r
}

func countOf(s, sub string) int { return strings.Count(s, sub) }

// Acceptance 1: exactly one start operation, with its provider and org.
func TestConnectionConnectCallsOneStartWithProviderAndOrg(t *testing.T) {
	r := runConnect(t, connectStart, []string{connRows(), connRows(connRow("c9", "gmail"))}, nil, "gmail", "--org", "hrn:org:acme.com")
	if r.err != nil {
		t.Fatal(r.err)
	}
	n := 0
	for _, op := range r.ops {
		if op == "StartConnection" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly one StartConnection, got ops %v", r.ops)
	}
	var v struct{ Provider, OrgRef string }
	_ = json.Unmarshal(r.vars["StartConnection"], &v)
	if v.Provider != "GMAIL" || v.OrgRef != "hrn:org:acme.com" {
		t.Fatalf("start variables %s", r.vars["StartConnection"])
	}
}

// Acceptance 3a: a connection appearing inside the wait prints and exits 0.
func TestConnectionConnectWaitSuccess(t *testing.T) {
	r := runConnect(t, connectStart, []string{connRows(), connRows(), connRows(connRow("c9", "gmail"))}, nil, "gmail", "--org", "org_1")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !strings.Contains(r.out, "✓ connected") || !strings.Contains(r.out, "c9@acme.com") {
		t.Fatalf("out %q", r.out)
	}
	// Baseline read happens before the browser is opened.
	if r.ops[0] != "StartConnection" || r.ops[1] != "EmailConnections" {
		t.Fatalf("ops %v", r.ops)
	}
}

// Acceptance 3a: nothing new inside the timeout exits 13, "not found yet".
func TestConnectionConnectWaitTimeoutExits13(t *testing.T) {
	r := runConnect(t, connectStart, []string{connRows()}, nil, "gmail", "--org", "org_1", "--wait", "10s")
	if r.code != exitcode.WaitTimeout {
		t.Fatalf("exit %d (%v), want %d", r.code, r.err, exitcode.WaitTimeout)
	}
	if !strings.Contains(r.err.Error(), "not found yet") {
		t.Fatalf("err %v", r.err)
	}
	if r.slept > 10*time.Second {
		t.Fatalf("waited %s, past --wait 10s", r.slept)
	}
}

// Acceptance 3a: a connection that existed before the start does not count, and
// neither does a new one for another provider.
func TestConnectionConnectPreexistingAndOtherProviderDoNotCount(t *testing.T) {
	old := connRow("old", "gmail")
	r := runConnect(t, connectStart, []string{connRows(old), connRows(old, connRow("cal", "google-cal"))}, nil, "gmail", "--org", "org_1", "--wait", "9s")
	if r.code != exitcode.WaitTimeout {
		t.Fatalf("exit %d (%v), want %d", r.code, r.err, exitcode.WaitTimeout)
	}
}

// Acceptance 3a: --no-wait sends no poll at all.
func TestConnectionConnectNoWaitSendsNoPoll(t *testing.T) {
	r := runConnect(t, connectStart, []string{connRows()}, nil, "gmail", "--org", "org_1", "--no-wait", "--no-browser")
	if r.err != nil {
		t.Fatal(r.err)
	}
	for _, op := range r.ops {
		if op == "EmailConnections" {
			t.Fatalf("--no-wait polled: %v", r.ops)
		}
	}
}

// Acceptance 3a: the wait never outlasts the link's expiry.
func TestConnectionConnectWaitCappedAtExpiry(t *testing.T) {
	soon := strings.Replace(connectStart, "2099-01-01T00:00:00Z", "2026-10-30T12:00:06Z", 1)
	r := runConnect(t, soon, []string{connRows()}, nil, "gmail", "--org", "org_1", "--wait", "1h")
	if r.code != exitcode.WaitTimeout {
		t.Fatalf("exit %d (%v)", r.code, r.err)
	}
	if r.slept > 6*time.Second || !strings.Contains(r.errOut, "link expires") {
		t.Fatalf("slept %s, stderr %q", r.slept, r.errOut)
	}
}

// Acceptance 3a: the poll is the existing metadata read, which selects no
// credential or lastError field (comment lines stripped: the file's own header
// names lastError to forbid it).
func TestConnectionConnectPollRequestsOnlyMetadata(t *testing.T) {
	raw, err := os.ReadFile("../api/queries/email_connections.graphql")
	if err != nil {
		t.Fatal(err)
	}
	var sel []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			sel = append(sel, line)
		}
	}
	for _, m := range []string{"lastError", "credential", "accessToken", "refreshToken", "secret"} {
		if strings.Contains(strings.ToLower(strings.Join(sel, "\n")), strings.ToLower(m)) {
			t.Errorf("EmailConnections selects %s", m)
		}
	}
}

// Acceptance 3: browser opened => only a notice; the URL appears nowhere in
// text output. --no-browser prints it exactly once; a failed open prints it
// exactly once; --json carries it once.
func TestConnectionConnectPrintsURLOnceInEveryMode(t *testing.T) {
	polls := []string{connRows(), connRows(connRow("c9", "gmail"))}
	cases := []struct {
		name       string
		args       []string
		openErr    error
		wantStdout int
		wantOpened int
	}{
		{"browser opens", []string{"gmail", "--org", "org_1"}, nil, 0, 1},
		{"no-browser", []string{"gmail", "--org", "org_1", "--no-browser"}, nil, 1, 0},
		{"open fails", []string{"gmail", "--org", "org_1"}, errors.New("no display for " + connectURL), 1, 1},
	}
	for _, tc := range cases {
		r := runConnect(t, connectStart, polls, tc.openErr, tc.args...)
		if r.err != nil {
			t.Fatalf("%s: %v", tc.name, r.err)
		}
		if got := countOf(r.out, connectURL); got != tc.wantStdout {
			t.Errorf("%s: URL on stdout %d times, want %d", tc.name, got, tc.wantStdout)
		}
		if strings.Contains(r.errOut, "SECRETNONCE") || (tc.wantStdout == 0 && strings.Contains(r.out, "SECRETNONCE")) {
			t.Errorf("%s: URL/nonce leaked on stderr: %q", tc.name, r.errOut)
		}
		if len(r.opened) != tc.wantOpened {
			t.Errorf("%s: opened %v", tc.name, r.opened)
		}
	}

	r := runConnect(t, connectStart, polls, nil, "gmail", "--org", "org_1", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if countOf(r.out, connectURL) != 1 || strings.Contains(r.errOut, "SECRETNONCE") {
		t.Fatalf("json: stdout %q stderr %q", r.out, r.errOut)
	}
	var dto struct {
		AuthorizeURL  string `json:"authorizeUrl"`
		BrowserOpened bool   `json:"browserOpened"`
		Status        string `json:"status"`
		Connection    struct {
			ID string `json:"id"`
		} `json:"connection"`
	}
	if err := json.Unmarshal([]byte(r.out), &dto); err != nil || dto.AuthorizeURL != connectURL || !dto.BrowserOpened || dto.Status != "connected" || dto.Connection.ID != "c9" {
		t.Fatalf("dto %+v err %v", dto, err)
	}
}

// A failed browser launch whose error text names the URL is redacted.
func TestConnectionConnectRedactsOpenFailure(t *testing.T) {
	r := runConnect(t, connectStart, []string{connRows(), connRows(connRow("c9", "gmail"))},
		errors.New("launcher rejected "+connectURL+" nonce SECRETNONCE123456"), "gmail", "--org", "org_1", "--no-wait")
	if r.err != nil {
		t.Fatal(r.err)
	}
	if strings.Contains(r.errOut, "SECRETNONCE") || strings.Contains(r.errOut, "hadron.example") || !strings.Contains(r.errOut, "<redacted>") {
		t.Fatalf("stderr %q", r.errOut)
	}
}

// --json --no-browser while waiting would hide the URL until the wait ends, so
// it is refused up front, before any single-use link is minted.
func TestConnectionConnectJSONNoBrowserNeedsNoWait(t *testing.T) {
	r := runConnect(t, connectStart, []string{connRows()}, nil, "gmail", "--org", "org_1", "--json", "--no-browser")
	if r.code != exitcode.Usage || len(r.ops) != 0 {
		t.Fatalf("exit %d ops %v err %v", r.code, r.ops, r.err)
	}
	r = runConnect(t, connectStart, []string{connRows()}, nil, "gmail", "--org", "org_1", "--json", "--no-browser", "--no-wait")
	if r.err != nil || countOf(r.out, connectURL) != 1 {
		t.Fatalf("err %v out %q", r.err, r.out)
	}
}

func TestConnectionConnectUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"yahoo", "--org", "o"},
		{"gmail", "--org", " "},
		{"gmail", "--org", "o", "--no-wait", "--wait", "1m"},
		{"gmail", "--org", "o", "--wait", "0s"},
	} {
		r := runConnect(t, connectStart, []string{connRows()}, nil, args...)
		if r.code != exitcode.Usage || len(r.ops) != 0 {
			t.Errorf("%v: exit %d ops %v", args, r.code, r.ops)
		}
	}
}

// Acceptance 2: each server refusal keeps its code in --json, with a typed exit,
// and the error output never carries a URL.
func TestConnectionConnectRefusalsKeepServerCode(t *testing.T) {
	cases := []struct {
		code string
		ext  string
	}{
		{"FORBIDDEN", `"code":"FORBIDDEN"`},                                                   // not org ADMIN / App key / impersonation / worker session
		{"FORBIDDEN", `"code":"FORBIDDEN","surface":"mcp-only"`},                              // MCP-only OAuth credential
		{"UNAUTHENTICATED", `"code":"UNAUTHENTICATED"`},                                       // user-less credential
		{"CONNECTION_PROVIDER_NOT_CONFIGURED", `"code":"CONNECTION_PROVIDER_NOT_CONFIGURED"`}, // unconfigured provider
	}
	for _, tc := range cases {
		resp := `{"errors":[{"message":"refused","extensions":{` + tc.ext + `}}],"data":null}`
		r := runConnect(t, resp, []string{connRows()}, nil, "gmail", "--org", "org_1", "--json")
		if r.err == nil || r.code == 0 {
			t.Fatalf("%s: want a refusal, got ok", tc.code)
		}
		if len(r.opened) != 0 {
			t.Errorf("%s: opened a browser on a refusal", tc.code)
		}
		combined := r.out + r.errOut
		if !strings.Contains(combined, `"serverCode": "`+tc.code+`"`) {
			t.Errorf("%s: serverCode missing from %q", tc.code, combined)
		}
		for _, op := range r.ops {
			if op == "EmailConnections" {
				t.Errorf("%s: polled after a refusal", tc.code)
			}
		}
	}
}

// The wait bounds the in-flight read too: a hung poll cannot outlast --wait.
func TestConnectionConnectHungPollDoesNotOutlastWait(t *testing.T) {
	prev := connection.Hooks
	t.Cleanup(func() { connection.Hooks = prev })
	connection.Hooks.Sleep = func(context.Context, time.Duration) error { return nil }
	connection.Hooks.Open = func(string) error { return nil }
	calls := 0
	srv, _ := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "StartConnection":
			return connectStart
		case "EmailConnections":
			calls++
			if calls > 1 {
				time.Sleep(1500 * time.Millisecond)
			}
			return connRows()
		}
		return ""
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "connection", "connect", "gmail", "--org", "org_1", "--wait", "300ms"})
	began := time.Now()
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.WaitTimeout {
		t.Fatalf("exit %d (%v), want %d", code, err, exitcode.WaitTimeout)
	}
	if took := time.Since(began); took > 1200*time.Millisecond {
		t.Fatalf("a hung poll held the command for %s, past --wait 300ms", took)
	}
}
