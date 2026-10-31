package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/secretfile"
)

// #1239 item 1: an explicit empty --label= is a usage error before the file is
// opened or anything is sent; an unset shell variable must not mint an
// unlabelled key.
func TestAppKeyCreateExplicitEmptyLabelRefusedBeforeFileAndRequest(t *testing.T) {
	for name, args := range map[string][]string{
		"equals form": {"--label="},
		"empty arg":   {"--label", ""},
	} {
		t.Run(name, func(t *testing.T) {
			srv, n := keyServer(t, 200, createdKey1223)
			dir := t.TempDir()
			r := runKeyCreate(t, srv, append([]string{app1223, "--out", filepath.Join(dir, "host.key")}, args...)...)
			if r.err == nil || exitCodeFor(r.err) != 2 {
				t.Fatalf("want usage error (2), got %v (%d)", r.err, exitCodeFor(r.err))
			}
			if !strings.Contains(r.all(), "--label") {
				t.Errorf("message should name --label: %q", r.all())
			}
			if n.Load() != 0 {
				t.Errorf("%d request(s) sent, want none", n.Load())
			}
			ents, rerr := os.ReadDir(dir)
			if rerr != nil {
				t.Fatalf("cannot read the output dir, so the no-file assertion would be vacuous: %v", rerr)
			}
			if len(ents) != 0 {
				t.Errorf("file created before the refusal: %v", ents)
			}
		})
	}
}

// Control: no --label at all still mints with the server's default label.
func TestAppKeyCreateUnsetLabelStillMints(t *testing.T) {
	srv, n, reqs := keyServerCapture(t, 200, createdKey1223)
	r := runKeyCreate(t, srv, app1223, "--out", filepath.Join(t.TempDir(), "host.key"))
	if r.err != nil || n.Load() != 1 {
		t.Fatalf("unset --label must mint once: err=%v n=%d", r.err, n.Load())
	}
	// The wire must carry no label at all: not "" and not null (the server reads
	// an omitted field as "use the default").
	got := reqs()
	if len(got) != 1 {
		t.Fatalf("requests = %d", len(got))
	}
	vars, _ := got[0]["variables"].(map[string]any)
	if v, present := vars["label"]; present {
		t.Errorf("label must be omitted from the mutation variables, got %#v", v)
	}
}

// #1239 item 2: when the cleanup fails the file remains, so the message must
// not claim "no file was kept".
func TestAppKeyCreateLostAnswerFailedCleanupDoesNotClaimNoFileKept(t *testing.T) {
	srv, _ := keyServer(t, 502, "<html>Bad Gateway</html>")
	old := secretfile.Remove
	secretfile.Remove = func(string) error { return errors.New("injected remove failure") }
	defer func() { secretfile.Remove = old }()
	r := runKeyCreate(t, srv, app1223, "--out", filepath.Join(t.TempDir(), "host.key"), "--label", "orca1 poller")
	if r.err == nil {
		t.Fatal("must fail")
	}
	if strings.Contains(r.all(), "no file was kept") {
		t.Errorf("claims no file was kept although cleanup failed: %q", r.all())
	}
	if !strings.Contains(r.all(), "delete it yourself") {
		t.Errorf("must still name the remaining file: %q", r.all())
	}
}

// Control: with a successful cleanup the claim stays (true).
func TestAppKeyCreateLostAnswerCleanCleanupSaysNoFileKept(t *testing.T) {
	srv, _ := keyServer(t, 502, "<html>Bad Gateway</html>")
	r := runKeyCreate(t, srv, app1223, "--out", filepath.Join(t.TempDir(), "host.key"), "--label", "orca1 poller")
	if r.err == nil || !strings.Contains(r.all(), "no file was kept") {
		t.Errorf("clean cleanup should say no file was kept: %v", r.err)
	}
}

// #1239 item 3 (pin): a refusal (exit 8) whose cleanup also fails keeps exit 8.
func TestAppKeyCreateRefusalPlusFailedCleanupKeepsExit8(t *testing.T) {
	srv, _ := keyServer(t, 200, `{"errors":[{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}],"data":null}`)
	old := secretfile.Remove
	secretfile.Remove = func(string) error { return errors.New("injected remove failure") }
	defer func() { secretfile.Remove = old }()
	r := runKeyCreate(t, srv, app1223, "--out", filepath.Join(t.TempDir(), "host.key"))
	if r.err == nil || exitCodeFor(r.err) != 8 {
		t.Fatalf("want exit 8, got %v (%d)", r.err, exitCodeFor(r.err))
	}
	if !strings.Contains(r.all(), "delete it yourself") {
		t.Errorf("cleanup failure not named: %q", r.all())
	}
}

// #1239 item 4 (pin): an internal error (exit 1) gets the lost-answer wording.
func TestAppKeyCreateInternalErrorGetsLostAnswerWording(t *testing.T) {
	srv, n := keyServer(t, 200, `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`)
	r := runKeyCreate(t, srv, app1223, "--out", filepath.Join(t.TempDir(), "host.key"), "--label", "orca1 poller")
	if r.err == nil {
		t.Fatal("must fail")
	}
	if c := exitCodeFor(r.err); c != 1 {
		t.Fatalf("want exit 1, got %d: %v", c, r.err)
	}
	if strings.Contains(r.all(), "boom") {
		t.Errorf("the server's message must not be echoed on the credential-minting path: %q", r.all())
	}
	for _, want := range []string{"answer was lost", "orca1 poller", "hadron app key list " + app1223, "revoke"} {
		if !strings.Contains(r.all(), want) {
			t.Errorf("message missing %q: %q", want, r.all())
		}
	}
	if n.Load() != 1 {
		t.Errorf("requests = %d, want 1 (never retried)", n.Load())
	}
	assertNoRawKey(t, r)
}

// The same non-echo holds for a transport-class lost answer, whose body can quote
// anything (a gateway page): mutant H (appending the server text) must die here
// too, in a key-create test and not only in the generic help contract.
func TestAppKeyCreateLostAnswerTransportDoesNotEchoServerBody(t *testing.T) {
	srv, _ := keyServer(t, 502, "<html>gateway-secret-marker</html>")
	r := runKeyCreate(t, srv, app1223, "--out", filepath.Join(t.TempDir(), "host.key"), "--label", "orca1 poller")
	if r.err == nil {
		t.Fatal("must fail")
	}
	if strings.Contains(r.all(), "gateway-secret-marker") {
		t.Errorf("server body echoed: %q", r.all())
	}
}
