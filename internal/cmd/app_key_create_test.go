package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/app"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const (
	rawKey1223 = "hdr_app_RAW-SECRET-0123456789abcdef"
	appRef1223 = "hrn:app:acme.com:support"
)

type keyCreate1223 struct {
	t        *testing.T
	srv      *httptest.Server
	requests atomic.Int32
}

// newKeyCreate1223 serves createAppKey with the given handler body; every
// request is counted so a test can assert "no mutation" or "exactly one".
func newKeyCreate1223(t *testing.T, respond func(w http.ResponseWriter)) *keyCreate1223 {
	k := &keyCreate1223{t: t}
	k.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		k.requests.Add(1)
		respond(w)
	}))
	t.Cleanup(k.srv.Close)
	return k
}

func okKey1223(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"data":{"createAppKey":{"key":{"id":"key_1","keyPreview":"hdr_app_...cdef","label":"orca1"},"rawKey":%q}}}`, rawKey1223)
}

// run returns exit code, stdout and stderr.
func (k *keyCreate1223) run(args ...string) (int, string, string) {
	k.t.Helper()
	f, out := testFactory(k.t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", k.srv.URL))
	err := root.Execute()
	errOut := f.IOStreams.ErrOut.(*strings.Builder).String()
	if err != nil {
		errOut += err.Error()
	}
	return exitCodeFor(err), out.String(), errOut
}

func noSecret1223(t *testing.T, outs ...string) {
	t.Helper()
	for _, o := range outs {
		if strings.Contains(o, rawKey1223) {
			t.Fatalf("raw key leaked into output: %q", o)
		}
	}
}

func readMode1223(t *testing.T, p string) (string, os.FileMode) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b)), info.Mode()
}

func privateDir1223(t *testing.T) string {
	d := t.TempDir()
	if err := os.Chmod(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestAppKeyCreate1223Success(t *testing.T) {
	k := newKeyCreate1223(t, okKey1223)
	out := filepath.Join(privateDir1223(t), "host.key")
	code, stdout, stderr := k.run("app", "key", "create", appRef1223, "--out", out, "--label", "orca1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	got, mode := readMode1223(t, out)
	if got != rawKey1223 || mode.Perm() != 0o600 || !mode.IsRegular() {
		t.Fatalf("file = %q mode %v", got, mode)
	}
	noSecret1223(t, stdout, stderr)
	for _, want := range []string{"key_1", "hdr_app_...cdef", "orca1", out, "app key scope grant key_1"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q: %s", want, stdout)
		}
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestAppKeyCreate1223JSON(t *testing.T) {
	k := newKeyCreate1223(t, okKey1223)
	out := filepath.Join(privateDir1223(t), "host.key")
	code, stdout, stderr := k.run("app", "key", "create", appRef1223, "--out", out, "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	noSecret1223(t, stdout, stderr)
	for _, want := range []string{`"id": "key_1"`, `"keyPreview": "hdr_app_...cdef"`, `"label": "orca1"`, `"out": "` + out + `"`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("json missing %s: %s", want, stdout)
		}
	}
}

func TestAppKeyCreate1223PrintKey(t *testing.T) {
	k := newKeyCreate1223(t, okKey1223)
	out := filepath.Join(privateDir1223(t), "host.key")
	code, stdout, _ := k.run("app", "key", "create", appRef1223, "--out", out, "--print-key")
	if code != 0 || !strings.Contains(stdout, rawKey1223) {
		t.Fatalf("exit %d, stdout %q", code, stdout)
	}
	if got, _ := readMode1223(t, out); got != rawKey1223 {
		t.Fatalf("file = %q", got)
	}
}

func TestAppKeyCreate1223UsageBeforeNetwork(t *testing.T) {
	d := privateDir1223(t)
	for name, args := range map[string][]string{
		"missing app-ref":    {"app", "key", "create", "--out", filepath.Join(d, "a")},
		"missing out":        {"app", "key", "create", appRef1223},
		"empty label":        {"app", "key", "create", appRef1223, "--out", filepath.Join(d, "c"), "--label="},
		"print-key and json": {"app", "key", "create", appRef1223, "--out", filepath.Join(d, "b"), "--print-key", "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			k := newKeyCreate1223(t, okKey1223)
			code, _, _ := k.run(args...)
			if code != exitcode.Usage || k.requests.Load() != 0 {
				t.Fatalf("exit %d, requests %d", code, k.requests.Load())
			}
		})
	}
}

func TestAppKeyCreate1223ExistingPathRefused(t *testing.T) {
	d := privateDir1223(t)
	existing := filepath.Join(d, "exists")
	if err := os.WriteFile(existing, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(d, "target")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	link, dangling := filepath.Join(d, "link"), filepath.Join(d, "dangling")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(d, "nowhere"), dangling); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{existing, link, dangling} {
		k := newKeyCreate1223(t, okKey1223)
		code, _, _ := k.run("app", "key", "create", appRef1223, "--out", p)
		if code != exitcode.Usage || k.requests.Load() != 0 {
			t.Fatalf("%s: exit %d, requests %d", p, code, k.requests.Load())
		}
	}
	if got, _ := readMode1223(t, target); got != "untouched" {
		t.Fatalf("symlink target was written: %q", got)
	}
}

func TestAppKeyCreate1223Force(t *testing.T) {
	d := privateDir1223(t)
	existing := filepath.Join(d, "exists")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(d, "target")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(d, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{existing, link} {
		k := newKeyCreate1223(t, okKey1223)
		code, stdout, stderr := k.run("app", "key", "create", appRef1223, "--out", p, "--force")
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", p, code, stderr)
		}
		noSecret1223(t, stdout, stderr)
		got, mode := readMode1223(t, p)
		if got != rawKey1223 || mode.Perm() != 0o600 || !mode.IsRegular() {
			t.Fatalf("%s = %q %v", p, got, mode)
		}
	}
	if got, _ := readMode1223(t, target); got != "untouched" {
		t.Fatalf("symlink target was written: %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(d, ".hadron-key-*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestAppKeyCreate1223WriteAfterMintFailure(t *testing.T) {
	restore := app.RenameFile
	app.RenameFile = func(string, string) error { return errors.New("rename failed near " + rawKey1223) }
	t.Cleanup(func() { app.RenameFile = restore })
	k := newKeyCreate1223(t, okKey1223)
	d := privateDir1223(t)
	out := filepath.Join(d, "host.key")
	if err := os.WriteFile(out, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := k.run("app", "key", "create", appRef1223, "--out", out, "--force")
	if code == 0 {
		t.Fatal("want non-zero")
	}
	noSecret1223(t, stdout, stderr)
	for _, want := range []string{"key_1", "hdr_app_...cdef", "revokeAppKey"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q: %s", want, stderr)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(d, ".hadron-key-*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
	if got, _ := readMode1223(t, out); got != "old" {
		t.Fatalf("existing --out changed: %q", got)
	}
}

func TestAppKeyCreate1223ServerErrorsLeaveNoFile(t *testing.T) {
	cases := map[string]func(http.ResponseWriter){
		"refused (error body embeds the key)": func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"errors":[{"message":"not allowed for %s","extensions":{"code":"FORBIDDEN"}}],"data":null}`, rawKey1223)
		},
		"lost answer": func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) },
	}
	for name, respond := range cases {
		for _, force := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/force=%v", name, force), func(t *testing.T) {
				k := newKeyCreate1223(t, respond)
				d := privateDir1223(t)
				out := filepath.Join(d, "host.key")
				args := []string{"app", "key", "create", appRef1223, "--out", out, "--label", "orca1 poller"}
				if force {
					if err := os.WriteFile(out, []byte("old"), 0o600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "--force")
				}
				code, stdout, stderr := k.run(args...)
				if code == 0 {
					t.Fatal("want non-zero")
				}
				noSecret1223(t, stdout, stderr)
				if k.requests.Load() != 1 {
					t.Fatalf("createAppKey sent %d times, want exactly 1", k.requests.Load())
				}
				if left, _ := filepath.Glob(filepath.Join(d, ".hadron-key-*")); len(left) != 0 {
					t.Fatalf("temp files left: %v", left)
				}
				_, err := os.Lstat(out)
				switch {
				case force && err != nil:
					t.Fatalf("existing --out should be untouched: %v", err)
				case !force && err == nil:
					t.Fatal("--out should be absent")
				}
				if name == "lost answer" {
					for _, want := range []string{"orca1 poller", "app key list " + appRef1223, "revoke"} {
						if !strings.Contains(stderr, want) {
							t.Errorf("stderr missing %q: %s", want, stderr)
						}
					}
				}
			})
		}
	}
}

func TestAppKeyCreate1223OpenParentWarnsOnStderr(t *testing.T) {
	k := newKeyCreate1223(t, okKey1223)
	d := t.TempDir()
	if err := os.Chmod(d, 0o755); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := k.run("app", "key", "create", appRef1223, "--out", filepath.Join(d, "host.key"))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "accessible to group or others") || strings.Contains(stdout, "accessible to group") {
		t.Fatalf("stderr %q stdout %q", stderr, stdout)
	}
}

func TestAppKeyCreate1223HelpNamesGrant(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "key", "create", "--help"})
	root.SetOut(out)
	_ = root.Execute()
	if !strings.Contains(out.String(), "app key scope grant") {
		t.Fatalf("help: %s", out.String())
	}
}
