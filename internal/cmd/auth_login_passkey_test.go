package cmd

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmd/agentic"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#875 (#1897 companion). The passkey step happens on the server's own
// page; the CLI's job is to describe it truthfully in every release state.
// Every passkey/registration sentence is conditional on the server, so the
// help makes no promise while #1897's switch is OFF.
var passkeyHelpPhrases = []string{
	"If the server requires a passkey for email sign-in, its page asks for one",
	"Where the server offers passkey registration, it emails a second link: open it in the same browser.",
	"An agent cannot complete a browser passkey step",
	"15 minutes",
}

// unconditionalEnrolment matches enrolment/registration promises. Each match
// must sit inside the one conditional sentence, so a reworded unconditional
// promise ("you can register one there") fails.
var unconditionalEnrolment = regexp.MustCompile(`(?i)(you can register|register one|enrol)`)

func TestAuthLoginHelpDescribesPasskeyStepConditionally(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "login", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range passkeyHelpPhrases {
		if !strings.Contains(help, want) {
			t.Errorf("auth login help missing %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "using GitHub for identity") {
		t.Error("help still says the browser flow is GitHub-only")
	}
	if m := unconditionalEnrolment.FindString(help); m != "" {
		t.Errorf("help promises enrolment unconditionally (%q):\n%s", m, help)
	}
}

func TestAgenticUsageDescribesPasskeyStepConditionally(t *testing.T) {
	doc := strings.Join(strings.Fields(agentic.Doc()), " ")
	for _, want := range passkeyHelpPhrases {
		if !strings.Contains(doc, want) {
			t.Errorf("agentic-usage.md missing %q", want)
		}
	}
	if m := unconditionalEnrolment.FindString(doc); m != "" {
		t.Errorf("agentic-usage.md promises enrolment unconditionally (%q)", m)
	}
}

// A STORED credential the server refuses (a cancelled sign-in lineage under
// R66, a revoked key, anything else) exits 3 with a re-login hint. The hint is
// generic: a plain 401 is not proof of cancellation. Exactly one request, no
// retry, and the stored token is left in place, never deleted silently.
func TestStoredCredentialRefusalHintsReloginWithoutRetryOrDeletion(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`unauthorized`))
	}))
	defer srv.Close()

	f, _ := testFactory(t)
	t.Setenv(store.EnvToken, "")
	st := memStore{auth.Host(srv.URL): "hdr_user_stored"}
	f.TokenStoreFn = func() store.Store { return st }
	errOut := captureErrOut(f)

	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--server", srv.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a refused credential must fail")
	}
	code := renderError(f, err)

	if code != exitcode.AuthRequired {
		t.Fatalf("exit = %d, want AuthRequired %d", code, exitcode.AuthRequired)
	}
	got := errOut()
	if !strings.Contains(got, "your stored credential was refused; run `hadron auth login`") {
		t.Errorf("missing the stored-credential re-login hint: %q", got)
	}
	if strings.Contains(strings.ToLower(got), "cancel") {
		t.Errorf("a plain refusal must not be reported as a cancellation: %q", got)
	}
	if n := posts.Load(); n != 1 {
		t.Errorf("made %d requests, want exactly 1 (no retry)", n)
	}
	if st[auth.Host(srv.URL)] != "hdr_user_stored" {
		t.Error("the stored credential must not be deleted silently")
	}
}

// The hint names the STORED credential, so it must not appear when the
// refused key came from HADRON_TOKEN: re-login would not replace it.
func TestEnvCredentialRefusalGetsNoStoredHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	f, _ := testFactory(t) // HADRON_TOKEN is set
	errOut := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--server", srv.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a refused credential must fail")
	}
	if code := renderError(f, err); code != exitcode.AuthRequired {
		t.Fatalf("exit = %d, want AuthRequired %d", code, exitcode.AuthRequired)
	}
	if got := errOut(); strings.Contains(got, "stored credential") {
		t.Errorf("an env credential must not get the stored-credential hint: %q", got)
	}
}
