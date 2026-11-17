package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const (
	newTok = "hdr_user_new_secret"
	envTok = "hdr_user_env_secret"
)

type acctEnv struct {
	f      *cmdutil.Factory
	out    *strings.Builder
	errOut *strings.Builder
	st     store.Store
	host   string
	srv    *httptest.Server
}

// newAcctEnv serves AuthContext by Authorization header: newTok is dara, any
// other credential (including HADRON_TOKEN) is holger. status != 200 makes the
// server fail every request. appKey answers newTok as an App principal.
func newAcctEnv(t *testing.T, status int, appKey bool, jsonMode bool) *acctEnv {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_TOKEN", envTok)
	keyring.MockInit()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 200 {
			http.Error(w, "boom", status)
			return
		}
		user := `{"id":"u-holger","name":"Holger","email":"holger@example.com","handle":"holger","githubUsername":null,"roles":[]}`
		pt, app := "USER", "null"
		if r.Header.Get("Authorization") == "Bearer "+newTok {
			user = `{"id":"u-dara","name":"Dara Doe","email":"dara@example.com","handle":"dara","githubUsername":null,"roles":[]}`
			if appKey {
				user, pt, app = "null", "APP", `"app-123"`
			}
		}
		fmt.Fprintf(w, `{"data":{"authContext":{"principalType":%q,"appId":%s,"agentId":null,"user":%s,"apiKey":null,"impersonation":null}}}`, pt, app, user)
	}))
	t.Cleanup(srv.Close)
	streams, _, _ := output.Test()
	out, errOut := &strings.Builder{}, &strings.Builder{}
	streams.Out, streams.ErrOut = output.Tracked(out), errOut
	f := cmdutil.NewFactory()
	f.IOStreams = streams
	f.JSON = jsonMode
	f.ServerFlag = srv.URL
	f.HTTPClient = srv.Client()
	f.TokenStoreFn = func() store.Store { return store.Keyring{} }
	return &acctEnv{f: f, out: out, errOut: errOut, st: store.Keyring{}, host: auth.Host(srv.URL), srv: srv}
}

func (e *acctEnv) login(t *testing.T, args ...string) error {
	t.Helper()
	e.f.IOStreams.In = strings.NewReader(newTok + "\n")
	cmd := newCmdLogin(e.f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args)
	return cmd.Execute()
}

func (e *acctEnv) noSecretLeak(t *testing.T) {
	t.Helper()
	for _, s := range []string{newTok, envTok, "hdr_user_new"} {
		if strings.Contains(e.out.String()+e.errOut.String(), s) {
			t.Errorf("credential %q disclosed in output", s)
		}
	}
}

// P1 + P2: the line names the account of the token just stored, not HADRON_TOKEN's.
func TestLoginWithTokenNamesStoredTokenAccount(t *testing.T) {
	e := newAcctEnv(t, 200, false, false)
	if err := e.login(t, "--with-token"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(e.out.String(), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "✓ Signed in to "+e.srv.URL+" (token stored in keychain)") {
		t.Fatalf("want unchanged first line + account line, got %q", e.out.String())
	}
	if !strings.Contains(lines[1], "@dara") || !strings.Contains(lines[1], "dara@example.com") {
		t.Errorf("account line = %q", lines[1])
	}
	if strings.Contains(e.out.String(), "holger") {
		t.Errorf("named the environment's account: %q", e.out.String())
	}
	e.noSecretLeak(t)
}

// P3: additive nested account; null (key present) when unread.
func TestLoginJSONAccount(t *testing.T) {
	e := newAcctEnv(t, 200, false, true)
	if err := e.login(t, "--with-token"); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(e.out.String()), &got); err != nil {
		t.Fatal(err)
	}
	acct, _ := got["account"].(map[string]any)
	if got["server"] != e.srv.URL || got["tokenStorage"] != "keychain" || acct["handle"] != "dara" || acct["email"] != "dara@example.com" {
		t.Errorf("unexpected JSON: %s", e.out.String())
	}

	f := newAcctEnv(t, 500, false, true)
	if err := f.login(t, "--with-token"); err != nil {
		t.Fatal(err)
	}
	var failed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(f.out.String()), &failed); err != nil {
		t.Fatalf("stdout is not one JSON document: %v: %q", err, f.out.String())
	}
	if string(failed["account"]) != "null" {
		t.Errorf("account = %s, want null present", failed["account"])
	}
}

// P4: a failed read never fails the login or loses the token.
func TestLoginAccountReadFails(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		e := newAcctEnv(t, 500, false, jsonMode)
		if err := e.login(t, "--with-token"); err != nil {
			t.Fatalf("json=%v: login must exit 0, got %v", jsonMode, err)
		}
		if got, err := e.st.Get(e.host); err != nil || got != newTok {
			t.Errorf("token not kept: %v", err)
		}
		if !strings.Contains(e.errOut.String(), "hadron auth whoami") {
			t.Errorf("stderr lacks whoami pointer: %q", e.errOut.String())
		}
		if jsonMode && !json.Valid([]byte(e.out.String())) {
			t.Errorf("stdout not one JSON document: %q", e.out.String())
		}
		e.noSecretLeak(t)
	}
}

// P5: the browser path names the account the same way.
func TestLoginBrowserNamesAccount(t *testing.T) {
	old := browserLogin
	t.Cleanup(func() { browserLogin = old })
	browserLogin = func(context.Context, auth.LoginOptions) (*auth.Token, error) {
		return &auth.Token{AccessToken: newTok}, nil
	}
	e := newAcctEnv(t, 200, false, false)
	if err := e.login(t); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.out.String(), "@dara") {
		t.Errorf("browser login output = %q", e.out.String())
	}
}

// P6: an App key has no user; the line names the principal and login succeeds.
func TestLoginAppKeyAccount(t *testing.T) {
	e := newAcctEnv(t, 200, true, false)
	if err := e.login(t, "--with-token"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.out.String(), "App app-123") {
		t.Errorf("output = %q", e.out.String())
	}
}

// P7: logout help says the browser session stays signed in.
func TestLogoutHelpBrowserSession(t *testing.T) {
	long := newCmdLogout(cmdutil.NewFactory()).Long
	if !strings.Contains(long, "browser") || !strings.Contains(long, "not signed out") {
		t.Errorf("logout help lacks the browser-session sentence:\n%s", long)
	}
}
