package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// cli#1084 (S10): the optional response metadata
// extensions.hadron.deliveryPending becomes ONE stderr line, from the one
// shared client path, for text and --json alike. stdout, the result, the exit
// status and the request count never change; anything absent, malformed,
// unsupported, incomplete or unsafe is omitted (never a fabricated zero); a
// failed operation reports only its own error.

var (
	footerBinOnce sync.Once
	footerBin     string
	footerBinErr  string
	footerBinDir  string
)

// TestMain-free cleanup: the compiled binary lives for the package run and is
// removed by the last test to finish via footerBinCleanup (see
// TestDeliveryFooterZZCleanup, which Go runs last in source order).
func footerBinCleanup() {
	if footerBinDir != "" {
		_ = os.RemoveAll(footerBinDir)
	}
}

func footerBinary(t *testing.T) string {
	t.Helper()
	footerBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "hadron-footer-bin-")
		if err != nil {
			footerBinErr = err.Error()
			return
		}
		footerBinDir = dir
		footerBin = filepath.Join(dir, "hadron")
		if out, err := exec.Command("go", "build", "-o", footerBin, "../../cmd/hadron").CombinedOutput(); err != nil {
			footerBinErr = string(out)
		}
	})
	if footerBinErr != "" {
		t.Fatalf("build: %s", footerBinErr)
	}
	return footerBin
}

// footerProxy forwards to the fake GraphQL server and, when ext is non-empty,
// adds it as the response's "extensions" member. It counts POSTs.
func footerProxy(t *testing.T, upstream string, exts ...string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	target, _ := url.Parse(upstream)
	rp := httputil.NewSingleHostReverseProxy(target)
	var posts atomic.Int32
	var seen atomic.Int32
	rp.ModifyResponse = func(resp *http.Response) error {
		if resp.Request.Method != http.MethodPost || len(exts) == 0 {
			return nil
		}
		// The i-th POST gets exts[i]; the last entry repeats. An empty entry
		// leaves that response without metadata.
		i := int(seen.Add(1)) - 1
		if i >= len(exts) {
			i = len(exts) - 1
		}
		ext := exts[i]
		if ext == "" {
			return nil
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var env map[string]json.RawMessage
		if json.Unmarshal(body, &env) == nil {
			env["extensions"] = json.RawMessage(ext)
			body, _ = json.Marshal(env)
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", "")
		resp.Header.Del("Content-Length")
		return nil
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		}
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &posts
}

type footerRun struct {
	stdout, stderr string
	code           int
	posts          int32
}

func runFooterCLI(t *testing.T, responses map[string]string, ext string, args ...string) footerRun {
	t.Helper()
	if ext == "" {
		return runFooterSeq(t, responses, nil, args...)
	}
	return runFooterSeq(t, responses, []string{ext}, args...)
}

// runFooterSeq is runFooterCLI with per-response metadata: the i-th POST
// carries exts[i] (the last repeats; "" means none on that response).
func runFooterSeq(t *testing.T, responses map[string]string, exts []string, args ...string) footerRun {
	t.Helper()
	bin := footerBinary(t)
	teamGitDir(t)
	testFactory(t) // isolated config/token environment for the subprocess
	gql, _ := captureGraphQL(t, responses)
	srv, posts := footerProxy(t, gql.URL, exts...)
	cmd := exec.Command(bin, append(args, "--server", srv.URL)...)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return footerRun{so.String(), se.String(), code, posts.Load()}
}

func footerPaths(t *testing.T) map[string]struct {
	responses map[string]string
	args      []string
} {
	return map[string]struct {
		responses map[string]string
		args      []string
	}{
		"non-team agent get": {
			map[string]string{"GetAgent": `{"data":{"agent":` + agentJSON + `}}`},
			[]string{"agent", "get", "agt1"},
		},
		"team worker list": {
			activityStubs(t),
			[]string{"team", "worker", "list", "--app", "acme.com:eng-team"},
		},
	}
}

const footerOK = `{"hadron":{"deliveryPending":{"v":1,"count":3,"complete":true,"asOf":"2026-11-02T00:00:00Z"}}}`

func TestDeliveryFooterRendersOnStderrOnly(t *testing.T) {
	for name, p := range footerPaths(t) {
		for _, mode := range [][]string{nil, {"--json"}} {
			t.Run(name+" "+strings.Join(mode, ""), func(t *testing.T) {
				args := append(append([]string{}, p.args...), mode...)
				base := runFooterCLI(t, p.responses, "", args...)
				got := runFooterCLI(t, p.responses, footerOK, args...)
				if got.stdout != base.stdout || got.code != base.code || got.posts != base.posts {
					t.Fatalf("stdout/exit/request count must not change:\nbase %q %d %d\ngot  %q %d %d", base.stdout, base.code, base.posts, got.stdout, got.code, got.posts)
				}
				if want := "3 messages waiting; pull with hadron team chat inbox\n"; !strings.HasSuffix(got.stderr, want) || strings.Count(got.stderr, "messages waiting") != 1 {
					t.Fatalf("stderr = %q, want one line ending %q", got.stderr, want)
				}
				if strings.Contains(base.stderr, "waiting") {
					t.Fatalf("no metadata, no footer: %q", base.stderr)
				}
			})
		}
	}
}

func TestDeliveryFooterOmittedUnlessPositiveExactCount(t *testing.T) {
	p := footerPaths(t)["non-team agent get"]
	for name, ext := range map[string]string{
		"zero":             `{"hadron":{"deliveryPending":{"v":1,"count":0,"complete":true}}}`,
		"absent":           `{}`,
		"no hadron":        `{"warnings":[]}`,
		"unsupported v":    `{"hadron":{"deliveryPending":{"v":2,"count":3,"complete":true}}}`,
		"missing v":        `{"hadron":{"deliveryPending":{"count":3,"complete":true}}}`,
		"string v":         `{"hadron":{"deliveryPending":{"v":"1","count":3,"complete":true}}}`,
		"string count":     `{"hadron":{"deliveryPending":{"v":1,"count":"3","complete":true}}}`,
		"fractional count": `{"hadron":{"deliveryPending":{"v":1,"count":2.5,"complete":true}}}`,
		"negative count":   `{"hadron":{"deliveryPending":{"v":1,"count":-1,"complete":true}}}`,
		"unsafe count":     `{"hadron":{"deliveryPending":{"v":1,"count":9007199254740993,"complete":true}}}`,
		"huge count":       `{"hadron":{"deliveryPending":{"v":1,"count":1e300,"complete":true}}}`,
		"null count":       `{"hadron":{"deliveryPending":{"v":1,"count":null,"complete":true}}}`,
		"incomplete":       `{"hadron":{"deliveryPending":{"v":1,"count":3,"complete":false}}}`,
		"complete missing": `{"hadron":{"deliveryPending":{"v":1,"count":3}}}`,
		"not an object":    `{"hadron":{"deliveryPending":3}}`,
		"array":            `{"hadron":{"deliveryPending":[1,3]}}`,
		"hadron a string":  `{"hadron":"deliveryPending"}`,
	} {
		t.Run(name, func(t *testing.T) {
			base := runFooterCLI(t, p.responses, "", p.args...)
			got := runFooterCLI(t, p.responses, ext, p.args...)
			if strings.Contains(got.stderr, "waiting") || got.stderr != base.stderr || got.stdout != base.stdout || got.code != 0 {
				t.Fatalf("must be omitted without a trace: stderr=%q stdout-equal=%v code=%d", got.stderr, got.stdout == base.stdout, got.code)
			}
		})
	}
}

func TestDeliveryFooterNeverAccompaniesAFailure(t *testing.T) {
	failing := map[string]string{"GetAgent": `{"errors":[{"message":"nope","extensions":{"code":"FORBIDDEN"}}]}`}
	args := []string{"agent", "get", "agt1"}
	base := runFooterCLI(t, failing, "", args...)
	got := runFooterCLI(t, failing, footerOK, args...)
	if base.code == 0 || got.code != base.code || got.stdout != base.stdout || got.stderr != base.stderr {
		t.Fatalf("a failed operation keeps its own error and exit:\nbase %q %d\ngot  %q %d", base.stderr, base.code, got.stderr, got.code)
	}
}

func TestDeliveryFooterSingular(t *testing.T) {
	p := footerPaths(t)["non-team agent get"]
	got := runFooterCLI(t, p.responses, `{"hadron":{"deliveryPending":{"v":1,"count":1,"complete":true}}}`, p.args...)
	if !strings.HasSuffix(got.stderr, "1 message waiting; pull with hadron team chat inbox\n") {
		t.Fatalf("stderr = %q", got.stderr)
	}
}

func footerCount(n int, complete bool) string {
	c := "false"
	if complete {
		c = "true"
	}
	return `{"hadron":{"deliveryPending":{"v":1,"count":` + strconv.Itoa(n) + `,"complete":` + c + `}}}`
}

// A command that really makes several requests (node rm: resolve, read,
// delete = 3 POSTs): the footer is printed once per invocation, from the
// LATEST valid snapshot, never per response, and never changes stdout, exit or
// the request count.
func TestDeliveryFooterAcrossSeveralRequests(t *testing.T) {
	responses := map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
		"DeleteNode": `{"data":{"deleteNode":true}}`,
	}
	args := []string{"node", "rm", nodeURN, "--yes"}
	base := runFooterCLI(t, responses, "", args...)
	if base.posts < 3 {
		t.Fatalf("fixture must make several requests, made %d", base.posts)
	}
	const tail = " waiting; pull with hadron team chat inbox\n"
	for name, tc := range map[string]struct {
		exts []string
		want string // "" = no footer
	}{
		"positive then different positive":  {[]string{footerCount(5, true), footerCount(2, true), footerCount(7, true)}, "7 messages" + tail},
		"positive then exact zero":          {[]string{footerCount(5, true), footerCount(5, true), footerCount(0, true)}, ""},
		"zero then positive":                {[]string{footerCount(0, true), "", footerCount(4, true)}, "4 messages" + tail},
		"valid then invalid keeps valid":    {[]string{footerCount(3, true), `{"hadron":{"deliveryPending":{"v":2,"count":9,"complete":true}}}`, ""}, "3 messages" + tail},
		"valid then incomplete keeps valid": {[]string{footerCount(3, true), footerCount(9, false), footerCount(9, false)}, "3 messages" + tail},
		"valid then single":                 {[]string{footerCount(5, true), footerCount(1, true), footerCount(1, true)}, "1 message" + tail},
	} {
		t.Run(name, func(t *testing.T) {
			got := runFooterSeq(t, responses, tc.exts, args...)
			if got.stdout != base.stdout || got.code != base.code || got.posts != base.posts {
				t.Fatalf("stdout/exit/request count must not change:\nbase %q %d %d\ngot  %q %d %d", base.stdout, base.code, base.posts, got.stdout, got.code, got.posts)
			}
			if n := strings.Count(got.stderr, "waiting"); (tc.want == "") != (n == 0) || n > 1 {
				t.Fatalf("want at most one footer line, stderr = %q", got.stderr)
			}
			if tc.want != "" && !strings.HasSuffix(got.stderr, tc.want) {
				t.Fatalf("stderr = %q, want suffix %q", got.stderr, tc.want)
			}
		})
	}
}

// A success-then-failure invocation: an earlier response recorded a count, a
// later request fails; only the failure's own error is reported.
func TestDeliveryFooterSuppressedWhenALaterRequestFails(t *testing.T) {
	responses := map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
		"DeleteNode": `{"errors":[{"message":"nope","extensions":{"code":"FORBIDDEN"}}]}`,
	}
	args := []string{"node", "rm", nodeURN, "--yes"}
	base := runFooterCLI(t, responses, "", args...)
	got := runFooterSeq(t, responses, []string{footerCount(5, true)}, args...)
	if base.code == 0 || got.code != base.code || got.stdout != base.stdout || got.stderr != base.stderr || got.posts != base.posts {
		t.Fatalf("a later failure keeps its own error and exit:\nbase %q %d\ngot  %q %d", base.stderr, base.code, got.stderr, got.code)
	}
}

// Removes the compiled binary; Go runs tests in source order, so this is last.
func TestDeliveryFooterZZCleanup(t *testing.T) { t.Cleanup(footerBinCleanup) }
