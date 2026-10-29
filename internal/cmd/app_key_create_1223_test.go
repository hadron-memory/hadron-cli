package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/secretfile"
)

// cli#1223 `app key create`: the raw key goes to a 0600 file, never to output,
// and the file is opened BEFORE the mutation so a refused path never mints a
// key nobody can recover.

const (
	rawKey1223 = "hdr_app_RAWSECRET0123456789abcdef"
	app1223    = "hrn:app:acme.com:ops"
)

const createdKey1223 = `{"data":{"createAppKey":{"rawKey":"` + rawKey1223 + `","key":{"id":"key_1","keyPreview":"hdr_app_RAWS…cdef","label":"orca1 poller"}}}}`

// keyServer counts createAppKey requests and answers with the given status/body.
func keyServer(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	srv, n, _ := keyServerCapture(t, status, body)
	return srv, n
}

// keyServerCapture also returns the decoded request bodies it received.
func keyServerCapture(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32, func() []map[string]any) {
	t.Helper()
	var n atomic.Int32
	var mu sync.Mutex
	var reqs []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		reqs = append(reqs, req)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &n, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), reqs...)
	}
}

type keyRun struct {
	out, errOut string
	err         error
}

func (r keyRun) all() string {
	e := ""
	if r.err != nil {
		e = r.err.Error()
	}
	return r.out + r.errOut + e
}

func runKeyCreate(t *testing.T, srv *httptest.Server, args ...string) keyRun {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"app", "key", "create"}, args...), "--server", srv.URL))
	err := root.Execute()
	return keyRun{out: out.String(), errOut: f.IOStreams.ErrOut.(*strings.Builder).String(), err: err}
}

func assertNoRawKey(t *testing.T, r keyRun) {
	t.Helper()
	if strings.Contains(r.all(), rawKey1223) {
		t.Fatalf("raw key leaked into output: out=%q err=%q error=%v", r.out, r.errOut, r.err)
	}
}

func readMode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode()
}

func TestAppKeyCreateWritesKeyAt0600AndNeverEchoesIt(t *testing.T) {
	srv, n := keyServer(t, 200, createdKey1223)
	out := filepath.Join(t.TempDir(), "host.key")
	r := runKeyCreate(t, srv, app1223, "--out", out, "--label", "orca1 poller")
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	got, err := os.ReadFile(out)
	if err != nil || strings.TrimSpace(string(got)) != rawKey1223 {
		t.Fatalf("key file = %q, %v", got, err)
	}
	if m := readMode(t, out); !m.IsRegular() || m.Perm() != 0o600 {
		t.Errorf("mode = %v, want regular 0600", m)
	}
	assertNoRawKey(t, r)
	for _, want := range []string{"key_1", "hdr_app_RAWS…cdef", "orca1 poller", out} {
		if !strings.Contains(r.out, want) {
			t.Errorf("stdout missing %q: %q", want, r.out)
		}
	}
	if n.Load() != 1 {
		t.Errorf("createAppKey requests = %d, want 1", n.Load())
	}
}

func TestAppKeyCreateJSONShapeCarriesNoKey(t *testing.T) {
	srv, _ := keyServer(t, 200, createdKey1223)
	out := filepath.Join(t.TempDir(), "host.key")
	r := runKeyCreate(t, srv, app1223, "--out", out, "--json")
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(r.out), &m); err != nil {
		t.Fatalf("json: %v: %q", err, r.out)
	}
	if len(m) != 4 || m["id"] != "key_1" || m["keyPreview"] != "hdr_app_RAWS…cdef" || m["label"] != "orca1 poller" || m["out"] != out {
		t.Errorf("shape = %v", m)
	}
	assertNoRawKey(t, r)
}

func TestAppKeyCreatePrintKeyPrintsItOnStdoutToo(t *testing.T) {
	srv, _ := keyServer(t, 200, createdKey1223)
	out := filepath.Join(t.TempDir(), "host.key")
	r := runKeyCreate(t, srv, app1223, "--out", out, "--print-key")
	if r.err != nil || !strings.Contains(r.out, rawKey1223) {
		t.Fatalf("--print-key must print the key on stdout: %v %q", r.err, r.out)
	}
	if strings.Contains(r.errOut, rawKey1223) {
		t.Errorf("stderr leaked the key: %q", r.errOut)
	}
	if got, _ := os.ReadFile(out); strings.TrimSpace(string(got)) != rawKey1223 {
		t.Errorf("--out must still be written: %q", got)
	}
}

func TestAppKeyCreateUsageErrorsSendNothing(t *testing.T) {
	srv, n := keyServer(t, 200, createdKey1223)
	dir := t.TempDir()
	for name, args := range map[string][]string{
		"missing app-ref":     {"--out", filepath.Join(dir, "a")},
		"missing --out":       {app1223},
		"print-key with json": {app1223, "--out", filepath.Join(dir, "b"), "--print-key", "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			r := runKeyCreate(t, srv, args...)
			if r.err == nil || exitCodeFor(r.err) != 2 {
				t.Fatalf("want usage error (2), got %v (%d)", r.err, exitCodeFor(r.err))
			}
		})
	}
	if n.Load() != 0 {
		t.Errorf("%d request(s) sent, want none", n.Load())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("usage error left files behind: %v", ents)
	}
}

func TestAppKeyCreateExistingOutRefusedBeforeAnyMutation(t *testing.T) {
	srv, n := keyServer(t, 200, createdKey1223)
	dir := t.TempDir()
	existing := filepath.Join(dir, "host.key")
	if err := os.WriteFile(existing, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	live, dangling := filepath.Join(dir, "live-link"), filepath.Join(dir, "dangling-link")
	if err := os.Symlink(target, live); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "nowhere"), dangling); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{"file": existing, "live symlink": live, "dangling symlink": dangling} {
		t.Run(name, func(t *testing.T) {
			r := runKeyCreate(t, srv, app1223, "--out", p)
			if r.err == nil || exitCodeFor(r.err) != 2 {
				t.Fatalf("want exit 2, got %v (%d)", r.err, exitCodeFor(r.err))
			}
		})
	}
	if n.Load() != 0 {
		t.Errorf("createAppKey sent %d time(s) for a refused path", n.Load())
	}
	if b, _ := os.ReadFile(existing); string(b) != "old" {
		t.Errorf("existing file modified: %q", b)
	}
	if b, _ := os.ReadFile(target); string(b) != "untouched" {
		t.Errorf("symlink target modified: %q", b)
	}
}

func TestAppKeyCreateForceReplacesFileWith0600(t *testing.T) {
	srv, _ := keyServer(t, 200, createdKey1223)
	dir := t.TempDir()
	out := filepath.Join(dir, "host.key")
	if err := os.WriteFile(out, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runKeyCreate(t, srv, app1223, "--out", out, "--force")
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	if b, _ := os.ReadFile(out); strings.TrimSpace(string(b)) != rawKey1223 {
		t.Errorf("file = %q", b)
	}
	if m := readMode(t, out); !m.IsRegular() || m.Perm() != 0o600 {
		t.Errorf("mode = %v, want regular 0600", m)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("temp file left behind: %v", ents)
	}
	assertNoRawKey(t, r)
}

func TestAppKeyCreateForceReplacesSymlinkNotItsTarget(t *testing.T) {
	srv, _ := keyServer(t, 200, createdKey1223)
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if r := runKeyCreate(t, srv, app1223, "--out", link, "--force"); r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	if m := readMode(t, link); !m.IsRegular() || m.Perm() != 0o600 {
		t.Errorf("link must now be a regular 0600 file, got %v", m)
	}
	if b, _ := os.ReadFile(link); strings.TrimSpace(string(b)) != rawKey1223 {
		t.Errorf("link content = %q", b)
	}
	if b, _ := os.ReadFile(target); string(b) != "untouched" {
		t.Errorf("symlink target written through: %q", b)
	}
}

func TestAppKeyCreateRefusedMutationLeavesNoFile(t *testing.T) {
	// The refusal body embeds the exact key: it must not be echoed.
	refusal := `{"errors":[{"message":"not a manager ` + rawKey1223 + `","extensions":{"code":"FORBIDDEN"}}],"data":null}`
	srv, n := keyServer(t, 200, refusal)
	dir := t.TempDir()
	out := filepath.Join(dir, "host.key")
	r := runKeyCreate(t, srv, app1223, "--out", out)
	if r.err == nil || exitCodeFor(r.err) == 0 {
		t.Fatalf("refused mutation must fail, got %v", r.err)
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("--out must be absent after a refusal: %v", err)
	}
	assertNoRawKey(t, r)
	if n.Load() != 1 {
		t.Errorf("requests = %d, want 1", n.Load())
	}
	// A rerun with the same --out is not blocked by a leftover file.
	srv2, _ := keyServer(t, 200, createdKey1223)
	if r := runKeyCreate(t, srv2, app1223, "--out", out); r.err != nil {
		t.Fatalf("rerun after refusal: %v", r.err)
	}
}

func TestAppKeyCreateRefusedWithForceKeepsExistingOut(t *testing.T) {
	refusal := `{"errors":[{"message":"nope","extensions":{"code":"FORBIDDEN"}}],"data":null}`
	srv, _ := keyServer(t, 200, refusal)
	dir := t.TempDir()
	out := filepath.Join(dir, "host.key")
	if err := os.WriteFile(out, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := runKeyCreate(t, srv, app1223, "--out", out, "--force")
	if r.err == nil {
		t.Fatal("refused mutation must fail")
	}
	if b, _ := os.ReadFile(out); string(b) != "old" {
		t.Errorf("existing --out must be untouched, got %q", b)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("temp file left behind: %v", ents)
	}
}

func TestAppKeyCreateLostAnswerSendsOnceAndPointsAtListAndRevoke(t *testing.T) {
	srv, n := keyServer(t, 502, "<html>Bad Gateway "+rawKey1223+"</html>")
	dir := t.TempDir()
	out := filepath.Join(dir, "host.key")
	r := runKeyCreate(t, srv, app1223, "--out", out, "--label", "orca1 poller")
	if r.err == nil || exitCodeFor(r.err) == 0 {
		t.Fatalf("lost answer must fail, got %v", r.err)
	}
	if n.Load() != 1 {
		t.Fatalf("createAppKey requests = %d, want exactly 1 (never retried)", n.Load())
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("--out must be absent: %v", err)
	}
	msg := r.all()
	for _, want := range []string{"orca1 poller", "hadron app key list " + app1223, "revoke"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %q", want, msg)
		}
	}
	assertNoRawKey(t, r)
}

func TestAppKeyCreateWriteAfterMintNamesTheKeyAndRevokeRoute(t *testing.T) {
	srv, _ := keyServer(t, 200, createdKey1223)
	for name, hook := range map[string]func() func(){
		"rename failure (--force)": func() func() {
			old := secretfile.Rename
			secretfile.Rename = func(string, string) error { return errors.New("injected rename failure " + rawKey1223) }
			return func() { secretfile.Rename = old }
		},
		"write failure": func() func() {
			old := secretfile.Write
			secretfile.Write = func(*os.File, []byte) error { return errors.New("injected write failure " + rawKey1223) }
			return func() { secretfile.Write = old }
		},
	} {
		t.Run(name, func(t *testing.T) {
			defer hook()()
			dir := t.TempDir()
			out := filepath.Join(dir, "host.key")
			args := []string{app1223, "--out", out}
			if strings.Contains(name, "--force") {
				if err := os.WriteFile(out, []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--force")
			}
			r := runKeyCreate(t, srv, args...)
			if r.err == nil || exitCodeFor(r.err) == 0 {
				t.Fatalf("must fail, got %v", r.err)
			}
			for _, want := range []string{"key_1", "hdr_app_RAWS…cdef", "revoke", "revokeAppKey"} {
				if !strings.Contains(r.all(), want) {
					t.Errorf("message missing %q: %q", want, r.all())
				}
			}
			assertNoRawKey(t, r)
			ents, _ := os.ReadDir(dir)
			for _, e := range ents {
				if e.Name() != "host.key" {
					t.Errorf("temp file %q left behind", e.Name())
				}
			}
			if b, _ := os.ReadFile(out); strings.Contains(string(b), rawKey1223) {
				t.Errorf("a failed write left the key in the file")
			}
		})
	}
}

func TestAppKeyCreateWarnsOnStderrWhenParentIsOpen(t *testing.T) {
	srv, _ := keyServer(t, 200, createdKey1223)
	open := t.TempDir()
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	r := runKeyCreate(t, srv, app1223, "--out", filepath.Join(open, "host.key"))
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	if !strings.Contains(r.errOut, "warning") || strings.Contains(r.out, "warning") {
		t.Errorf("warning must be on stderr only: out=%q err=%q", r.out, r.errOut)
	}
	closed := t.TempDir()
	if err := os.Chmod(closed, 0o700); err != nil {
		t.Fatal(err)
	}
	r = runKeyCreate(t, srv, app1223, "--out", filepath.Join(closed, "host.key"))
	if r.err != nil || strings.Contains(r.errOut, "warning") {
		t.Errorf("a 0700 parent must not warn: %v %q", r.err, r.errOut)
	}
}

func TestAppKeyCreateHelpNamesScopeGrantFollowOn(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "key", "create", "--help"})
	_ = root.Execute()
	if !strings.Contains(out.String(), "app key scope grant") {
		t.Errorf("help must name the scope grant follow-on:\n%s", out.String())
	}
}

func TestAppKeyCreateSendsTheMutationWithAppRefAndLabelOnly(t *testing.T) {
	for name, tc := range map[string]struct {
		args      []string
		wantLabel any
	}{
		"label":    {[]string{"--label", "orca1 poller"}, "orca1 poller"},
		"no label": {nil, nil},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _, reqs := keyServerCapture(t, 200, createdKey1223)
			out := filepath.Join(t.TempDir(), "host.key")
			r := runKeyCreate(t, srv, append([]string{app1223, "--out", out}, tc.args...)...)
			if r.err != nil {
				t.Fatalf("execute: %v", r.err)
			}
			got := reqs()
			if len(got) != 1 {
				t.Fatalf("requests = %d, want 1", len(got))
			}
			if op, _ := got[0]["operationName"].(string); op != "CreateAppKey" {
				t.Errorf("operationName = %q", op)
			}
			vars, _ := got[0]["variables"].(map[string]any)
			if vars["appRef"] != app1223 {
				t.Errorf("appRef = %v, want %s", vars["appRef"], app1223)
			}
			label, hasLabel := vars["label"]
			if (tc.wantLabel == nil) == hasLabel || (hasLabel && label != tc.wantLabel) {
				t.Errorf("label = %v (present %v), want %v", label, hasLabel, tc.wantLabel)
			}
			for k := range vars {
				if k != "appRef" && k != "label" {
					t.Errorf("unexpected variable %q", k)
				}
			}
			if strings.Contains(strings.ToLower(string(mustJSON(t, got[0]))), "session") {
				t.Errorf("request carries a session field: %v", got[0])
			}
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAppKeyCreateReportsAFileItCouldNotRemove(t *testing.T) {
	srv, _ := keyServer(t, 200, createdKey1223)
	oldW, oldR := secretfile.Write, secretfile.Remove
	secretfile.Write = func(f *os.File, b []byte) error { _, _ = f.Write(b[:4]); return errors.New("injected partial write") }
	secretfile.Remove = func(string) error { return errors.New("injected remove failure") }
	defer func() { secretfile.Write, secretfile.Remove = oldW, oldR }()
	dir := t.TempDir()
	out := filepath.Join(dir, "host.key")
	r := runKeyCreate(t, srv, app1223, "--out", out)
	if r.err == nil || exitCodeFor(r.err) == 0 {
		t.Fatalf("must fail, got %v", r.err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("expected the undeleted file to remain, got %v", ents)
	}
	left := filepath.Join(dir, ents[0].Name())
	for _, want := range []string{left, "delete it yourself", "key_1", "revoke"} {
		if !strings.Contains(r.all(), want) {
			t.Errorf("message missing %q: %q", want, r.all())
		}
	}
	assertNoRawKey(t, r)
}

func TestAppKeyCreateNeverEchoesAKeyOutsideTheDefaultPrefix(t *testing.T) {
	const custom = "customkey_RAWSECRET0123456789abcdef"
	for name, body := range map[string]string{
		"refusal":  `{"errors":[{"message":"nope ` + custom + `","extensions":{"code":"FORBIDDEN"}}],"data":null}`,
		"internal": `{"errors":[{"message":"boom ` + custom + `","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := keyServer(t, 200, body)
			r := runKeyCreate(t, srv, app1223, "--out", filepath.Join(t.TempDir(), "host.key"))
			if r.err == nil || exitCodeFor(r.err) == 0 {
				t.Fatalf("must fail, got %v", r.err)
			}
			if strings.Contains(r.all(), custom) || strings.Contains(r.all(), "RAWSECRET") {
				t.Errorf("custom-prefix key echoed: %q", r.all())
			}
		})
	}
}

func TestAppKeyCreateEmptyKeyAnswerLeavesNoFile(t *testing.T) {
	srv, _ := keyServer(t, 200, `{"data":{"createAppKey":{"rawKey":"","key":{"id":"key_1","keyPreview":"x","label":null}}}}`)
	dir := t.TempDir()
	out := filepath.Join(dir, "host.key")
	r := runKeyCreate(t, srv, app1223, "--out", out)
	if r.err == nil || exitCodeFor(r.err) == 0 {
		t.Fatalf("must fail, got %v", r.err)
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("--out must be absent: %v", err)
	}
	if err := os.WriteFile(out, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	r = runKeyCreate(t, srv, app1223, "--out", out, "--force")
	if r.err == nil {
		t.Fatal("--force must fail too")
	}
	if b, _ := os.ReadFile(out); string(b) != "old" {
		t.Errorf("--force kept %q, want the prior file", b)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("temp file left behind: %v", ents)
	}
}
