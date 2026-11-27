package team

// hadron-cli#3301 LOCAL at 8bc735a4 (Joran): a host that cannot start joins the exit-code selection (F3, ruling
// Q4 (a)), and a start read the server did not refuse is retried rather than ending the host (F4).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostcred"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const hostNotFound3301 = `{"errors":[{"message":"no such host","extensions":{"code":"HOST_NOT_FOUND"}}]}`

// refusingServer3301 answers the platform read with a fixed refusal: NotFound (exit 4) or setup source HOST (5).
func refusingServer3301(t *testing.T, app, host, key string, notFound bool) hostcred.Entry {
	t.Helper()
	srv, _ := fakeServer3237(t, func(op string, _ json.RawMessage) string {
		if op != "HostRunPlatform" {
			return ""
		}
		if notFound {
			return hostNotFound3301
		}
		return platformAnswer3237(app, host, "HOST")
	})
	return hostcred.Entry{Server: srv.URL, AppID: app, HostID: host, Key: key}
}

// Q4 (a): with every host refused at start, --once exits with the worst code and a run without it with the
// first host's (credential order) — never a flat 1.
func TestHostRunPlatformStartRefusalsJoinTheExitCode3301(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"once takes the worst", []string{"--once"}, exitcode.Conflict},
		{"run takes the first host's", nil, exitcode.NotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withDeps3237(t, []hostcred.Entry{
				refusingServer3301(t, "app-1", "host-1", key1, true),  // exit 4, first in credential order
				refusingServer3301(t, "app-2", "host-2", key2, false), // exit 5, the worse code
			})
			out, errOut, err := run3237(t, false, tc.args...)
			if got := exitcode.FromError(err); got != tc.want {
				t.Fatalf("exit %d, want %d (%v)\n%s\n%s", got, tc.want, err, out, errOut)
			}
			if strings.Count(out+errOut, "host-not-started") != 2 {
				t.Errorf("each refused host is reported once:\n%s\n%s", out, errOut)
			}
		})
	}
}

// F4: without --once a transient platform read at start (the server did not refuse it) is retried with
// backoff in that host's goroutine, said once for the episode, and the host then runs.
func TestHostRunPlatformRetriesATransientStartRead3301(t *testing.T) {
	fastBackoff3301(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var reads atomic.Int32
	srv, _ := fakeServer3237(t, func(op string, _ json.RawMessage) string {
		switch op {
		case "HostRunPlatform":
			if reads.Add(1) <= 3 {
				return `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
			}
			return platformAnswer3237("app-1", "host-1", "SERVER")
		case "RegisterHost", "RegisterHostWithRefused":
			cancel() // the host ran: stop the process
			return registerAnswer3237("host-1")
		case "CollectHostOperations":
			return `{"data":{"collectHostOperations":[]}}`
		}
		return ""
	})
	withDeps3237(t, []hostcred.Entry{{Server: srv.URL, AppID: "app-1", HostID: "host-1", Key: key1}})
	out, err := runCtx3301(t, ctx)
	if err != nil {
		t.Fatalf("a transient start read is retried and the host runs: %v\n%s", err, out)
	}
	if n := reads.Load(); n < 4 {
		t.Fatalf("the start read was not retried: %d read(s)", n)
	}
	if got := strings.Count(out, `"host-start-retrying"`); got != 1 {
		t.Errorf("the retry episode is said once, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, `"host-started"`) {
		t.Errorf("the host's start after retrying is said:\n%s", out)
	}
	if strings.Contains(out, key1) {
		t.Error("a key reached a stream")
	}
}

// runCtx3301 runs `host run` bounded: a host that retries when it should not ends at the deadline, never hangs.
func runCtx3301(t *testing.T, ctx context.Context, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HADRON_TOKEN", "hdr_person_must_not_be_used")
	io, _, _ := output.Test()
	out, errOut := &strings.Builder{}, &strings.Builder{}
	io.Out, io.ErrOut = output.Tracked(out), errOut
	c := newCmdTeamHostRun(&cmdutil.Factory{IOStreams: io, JSON: true})
	c.SilenceUsage = true
	c.SetOut(out)
	c.SetErr(errOut)
	c.SetArgs(args)
	err := c.ExecuteContext(ctx)
	return out.String() + errOut.String(), err
}

func fastBackoff3301(t *testing.T) {
	old := platformStartBackoff
	platformStartBackoff = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(func() { platformStartBackoff = old })
}

func transientServer3301(t *testing.T, reads *atomic.Int32) string {
	srv, _ := fakeServer3237(t, func(op string, _ json.RawMessage) string {
		if op == "HostRunPlatform" {
			reads.Add(1)
			return `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
		}
		return ""
	})
	return srv.URL
}

// F4: --once does not retry, with one host or several: one pass, the transient start failure is its exit.
func TestHostRunPlatformOnceDoesNotRetryAStartRead3301(t *testing.T) {
	fastBackoff3301(t)
	for _, n := range []int{1, 2} {
		var reads atomic.Int32
		entries := []hostcred.Entry{{Server: transientServer3301(t, &reads), AppID: "app-1", HostID: "host-1", Key: key1}}
		if n == 2 {
			srv2, _ := goodServer3237(t, "app-2", "host-2")
			entries = append(entries, hostcred.Entry{Server: srv2.URL, AppID: "app-2", HostID: "host-2", Key: key2})
		}
		withDeps3237(t, entries)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := runCtx3301(t, ctx, "--once")
		cancel()
		if err == nil || reads.Load() != 1 {
			t.Fatalf("%d host(s): --once makes one read and fails: err %v, %d read(s)", n, err, reads.Load())
		}
	}
}

// F4 and LOCAL delta P3-2 (a): every fixed refusal is never retried, with or without --once (not only a removed
// host: a revoked key, a lost permission, a refused input and a conflict end the start too).
func TestHostRunPlatformDoesNotRetryAFixedRefusal3301(t *testing.T) {
	fastBackoff3301(t)
	for _, c := range []struct {
		code string
		want int
	}{
		{"HOST_NOT_FOUND", exitcode.NotFound},
		{"UNAUTHENTICATED", exitcode.AuthRequired},
		{"FORBIDDEN", exitcode.Forbidden},
		{"BAD_USER_INPUT", exitcode.Usage},
		{"CONFLICT", exitcode.Conflict},
	} {
		t.Run(c.code, func(t *testing.T) {
			var reads atomic.Int32
			srv, _ := fakeServer3237(t, func(op string, _ json.RawMessage) string {
				if op == "HostRunPlatform" {
					reads.Add(1)
					return `{"errors":[{"message":"refused","extensions":{"code":"` + c.code + `"}}]}`
				}
				return ""
			})
			withDeps3237(t, []hostcred.Entry{{Server: srv.URL, AppID: "app-1", HostID: "host-1", Key: key1}})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := runCtx3301(t, ctx)
			if exitcode.FromError(err) != c.want || reads.Load() != 1 {
				t.Fatalf("%s ends the start at once: exit %d (want %d), %d read(s)", c.code, exitcode.FromError(err), c.want, reads.Load())
			}
		})
	}
}

// LOCAL delta P3-2 (b): a start that failed transiently and is then refused on a retry stops retrying; with a
// second host keeping the run alive, the refused one makes no further reads.
func TestHostRunPlatformARetryEndsOnARefusal3301(t *testing.T) {
	fastBackoff3301(t)
	var reads atomic.Int32
	srv, _ := fakeServer3237(t, func(op string, _ json.RawMessage) string {
		if op == "HostRunPlatform" {
			if reads.Add(1) == 1 {
				return `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
			}
			return hostNotFound3301
		}
		return ""
	})
	srv2, _ := goodServer3237(t, "app-2", "host-2")
	withDeps3237(t, []hostcred.Entry{
		{Server: srv.URL, AppID: "app-1", HostID: "host-1", Key: key1},
		{Server: srv2.URL, AppID: "app-2", HostID: "host-2", Key: key2},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, _ = runCtx3301(t, ctx)
	if n := reads.Load(); n != 2 {
		t.Fatalf("one transient read, one refused retry, then no more: %d read(s)", n)
	}
}

// F6: an assignment the platform answers for another host id is not this host's, so it is not registered.
func TestHostRunPlatformRegistersOnlyThisHostsAssignments3301(t *testing.T) {
	answer := strings.Replace(platformAnswer3237("app-1", "host-1", "SERVER"), `{"hostId":"host-1","workerId":"wkr-antonio"`, `{"hostId":"host-9","workerId":"wkr-antonio"`, 1)
	if !strings.Contains(answer, `"host-9"`) {
		t.Fatal("the fixture did not move Antonio to another host")
	}
	srv, calls := fakeServer3237(t, func(op string, _ json.RawMessage) string {
		switch op {
		case "HostRunPlatform":
			return answer
		case "RegisterHost", "RegisterHostWithRefused":
			return registerAnswer3237("host-1")
		case "CollectHostOperations":
			return `{"data":{"collectHostOperations":[]}}`
		}
		return ""
	})
	withDeps3237(t, []hostcred.Entry{{Server: srv.URL, AppID: "app-1", HostID: "host-1", Key: key1}})
	if out, errOut, err := run3237(t, false, "--once"); err != nil {
		t.Fatalf("run --once: %v\n%s\n%s", err, out, errOut)
	}
	for _, c := range calls() {
		if strings.HasPrefix(c.op, "RegisterHost") {
			if !strings.Contains(string(c.vars), "wkr-judy") || strings.Contains(string(c.vars), "wkr-antonio") {
				t.Fatalf("only this host's assignments register: %s", c.vars)
			}
			return
		}
	}
	t.Fatal("nothing registered")
}

// F6: the runner a platform worktree.create goes through gets the scrubbed environment: no person's token, no
// host key, nothing but the short allowlist.
func TestTheWorktreeCreateRunnerHasAScrubbedEnv3301(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake orca is a shell script; host run with no --config is Linux-only")
	}
	bin := filepath.Join(t.TempDir(), "orca")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nenv\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HADRON_TOKEN", "hdr_person_must_not_be_used")
	t.Setenv("HADRON_HOST_KEY_PIN_3301", key1)
	res := platformDepsFor().worktreeRun(bin)(context.Background(), []string{"worktree", "create"})
	if res.Code != 0 {
		t.Fatalf("the fake orca ran: %+v", res)
	}
	if strings.Contains(res.Stdout, "HADRON_TOKEN") || strings.Contains(res.Stdout, key1) {
		t.Fatalf("the create's env is not scrubbed:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "PATH=") {
		t.Errorf("the allowlist (PATH) is kept:\n%s", res.Stdout)
	}
}
