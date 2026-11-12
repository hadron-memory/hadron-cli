//go:build !windows

package hostpoller

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

// An agent that already exited — its group gone, its number still reserved
// by the launch (pinPid) — is a confirmed stop that SIGNALS nothing: the
// probe is safe to run (a reserved number cannot be someone else's group),
// and its ESRCH answer is the confirmation. The probes count is asserted on
// Linux in pinpid_linux_test.go; here the contract every Unix platform
// keeps is stopped=true, silently, with zero signals (#1474 delta round 2:
// Jonas's P2 — this file also builds on darwin/BSD, where no pin exists).
func TestExecHostTerminateAfterExitSignalsNothing(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	signals := 0
	realSignal, realProbe := signalGroup, probeGroup
	signalGroup = func(pgid int, sig syscall.Signal) error {
		signals++
		return realSignal(pgid, sig)
	}
	defer func() { signalGroup, probeGroup = realSignal, realProbe }()
	a, err := ExecHost{}.Launch(context.Background(), LaunchSpec{Argv: []string{"true"}, Dir: t.TempDir(), Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true exits at once")
	}
	stopped, detail := a.Terminate()
	if !stopped || detail != "" {
		t.Errorf("an agent whose group is gone is a silent confirmed stop: stopped=%v %q", stopped, detail)
	}
	if signals != 0 {
		t.Errorf("an agent whose group is gone is never signalled: %d signals", signals)
	}
}

// #1474 review P2-1: the leader exiting first must not end the group stop.
// A launcher that backgrounds a TERM-ignoring agent and returns is the
// documented failure shape; Terminate — called AFTER the leader is gone —
// must still stop the whole group, because it probes the group, not the
// leader.
func TestExecHostTerminateStopsTheGroupAfterTheLeaderExits(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := os.Stat("/proc/self/cwd"); err != nil {
		t.Skip("no /proc")
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "child-armed")
	// The child traps TERM and stays; the leader forks it and exits at once.
	// (argv[1] of a `sh -c` script is $0 — the sibling tests' convention.)
	a, err := ExecHost{TerminateGrace: 300 * time.Millisecond}.Launch(context.Background(), LaunchSpec{
		Argv: []string{"sh", "-c", `sh -c 'trap "" TERM; touch "$0"; sleep 30' "$0" & exit 0`, ready},
		Dir:  dir, Env: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = a.Terminate() }()
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the leader must exit at once")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the child never armed its trap")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stopped, detail := a.Terminate()
	if !stopped || !strings.Contains(detail, "killed the launched process group") {
		t.Errorf("a group whose leader exited first is still killed: stopped=%v %q", stopped, detail)
	}
	// Nothing is left working in the tree (the review's requested
	// assertion).
	sweep := time.Now().Add(5 * time.Second)
	for {
		who, _ := ExecHost{}.Occupied(context.Background(), dir)
		if who == "" {
			return
		}
		if time.Now().After(sweep) {
			t.Fatalf("the TERM-ignoring child survived its leader's group: %q", who)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The paths a healthy host never takes: a group signal that fails outright,
// and a group that outlives SIGKILL — both must report stopped=false, so the
// caller keeps the agent's records (#1474 review P2-2).
func TestTerminateGroupReportsWhatItCouldNotConfirm(t *testing.T) {
	t.Run("signal fails", func(t *testing.T) {
		realSignal, realProbe := signalGroup, probeGroup
		signalGroup = func(int, syscall.Signal) error { return syscall.EPERM }
		probeGroup = func(int) error { return nil } // the group is there
		defer func() { signalGroup, probeGroup = realSignal, realProbe }()
		stopped, detail := terminateGroup(999998, 50*time.Millisecond)
		if stopped || !strings.Contains(detail, "could not stop") {
			t.Errorf("a failed signal is not a stop: stopped=%v %q", stopped, detail)
		}
	})
	t.Run("group outlives the kill", func(t *testing.T) {
		realSignal, realProbe, realWait := signalGroup, probeGroup, postKillWait
		signalGroup = func(int, syscall.Signal) error { return nil }
		probeGroup = func(int) error { return nil } // never gone
		postKillWait = 80 * time.Millisecond
		defer func() {
			signalGroup, probeGroup, postKillWait = realSignal, realProbe, realWait
		}()
		stopped, detail := terminateGroup(999998, 50*time.Millisecond)
		if stopped || !strings.Contains(detail, "did not exit within") {
			t.Errorf("a group that outlives SIGKILL is not a stop: stopped=%v %q", stopped, detail)
		}
	})
}

// The occupied-tree refusal names the occupant without carrying its
// credentials or an unbounded tail (#1474 review P2-4; delta review D2/P3:
// credential-shaped headers, URL userinfo, -u/-p, invalid UTF-8).
func TestRenderArgvBoundsAndRedacts(t *testing.T) {
	for name, tc := range map[string]struct {
		argv []string
		want string
	}{
		"plain":                   {[]string{"sleep", "30"}, "sleep 30"},
		"assignment":              {[]string{"agent", "--token=hdr_secret_1"}, "agent --token=<redacted>"},
		"env style":               {[]string{"agent", "HADRON_TOKEN=hdr_x"}, "agent HADRON_TOKEN=<redacted>"},
		"bare flag":               {[]string{"agent", "--token", "hdr_secret_1", "--verbose"}, "agent --token <redacted> --verbose"},
		"header":                  {[]string{"curl", "-H", "Authorization: Bearer hdr_x"}, "curl -H Authorization: <redacted>"},
		"cookie":                  {[]string{"curl", "-H", "Cookie: session=hdr_x"}, "curl -H Cookie: <redacted>"},
		"api-key header":          {[]string{"curl", "-H", "X-API-Key: SYNTHETIC_SECRET_1817"}, "curl -H X-API-Key: <redacted>"},
		"auth-token header":       {[]string{"curl", "-H", "X-Auth-Token: SYNTHETIC_SECRET_1817"}, "curl -H X-Auth-Token: <redacted>"},
		"secret-named header":     {[]string{"curl", "-H", "Client-Secret: SYNTHETIC_1817"}, "curl -H Client-Secret: <redacted>"},
		"plain header kept":       {[]string{"curl", "-H", "Accept: application/json"}, `curl -H "Accept: application/json"`},
		"url userinfo":            {[]string{"curl", "https://user:pass@host/path"}, "curl https://host/path"},
		"url userinfo no pass":    {[]string{"curl", "https://user@host/path"}, "curl https://host/path"},
		"git ssh form kept":       {[]string{"git", "clone", "git@host:repo.git"}, "git clone git@host:repo.git"},
		"curl -u next arg":        {[]string{"curl", "-u", "user:pass", "https://h"}, "curl -u <redacted> https://h"},
		"mysql -p attached":       {[]string{"mysql", "-pSECRET_1817"}, "mysql -p<redacted>"},
		"invalid utf8 quoted":     {[]string{"a", "b\xff", "c"}, `a "b\xff" c`},
		"spaces kept unambiguous": {[]string{"sh", "-c", "sleep 30 & wait"}, `sh -c "sleep 30 & wait"`},
		"empty argument survives": {[]string{"a", "", "b"}, `a "" b`},
		// #1474 delta round 2 (Jonas F5a): an unencoded @ in the password
		// must not leak the tail after the first @.
		"unencoded @ in password": {[]string{"curl", "https://u:p@ss@host/path"}, "curl https://host/path"},
		// F5c: every URL-shaped token in one argument is stripped, not only
		// the first.
		"two urls in one string": {[]string{"sh", "-c", "curl https://a:b@h1/x https://c:d@h2/x"}, `sh -c "curl https://h1/x https://h2/x"`},
		// F5d: -u/-p redaction is gated on binaries that take a credential
		// there, so other tools' flags stay readable.
		"python -u is unbuffered": {[]string{"python", "-u", "agent.py"}, "python -u agent.py"},
		"find -print kept":        {[]string{"find", ".", "-print"}, "find . -print"},
		"curl -u by name":         {[]string{"curl", "-u", "user:pass", "https://h"}, "curl -u <redacted> https://h"},
		"curl -u by path":         {[]string{"/usr/bin/curl", "-u", "user:pass"}, "/usr/bin/curl -u <redacted>"},
		"sshpass -p by name":      {[]string{"sshpass", "-p", "secret", "ssh", "h"}, "sshpass -p <redacted> ssh h"},
		// F6: a setproctitle-padded argv renders one trailing empty, not a
		// run of them.
		"padded trailing empties": {[]string{"titled", "x", "", ""}, `titled x ""`},
		// P3-2 residual: an argument appended raw for its name is still
		// quoted when it carries invalid UTF-8.
		"raw append quoted too": {[]string{"a", "--token\xff"}, `a "--token\xff"`},
	} {
		t.Run(name, func(t *testing.T) {
			if got := renderArgv(tc.argv); got != tc.want {
				t.Errorf("renderArgv(%q) = %q, want %q", tc.argv, got, tc.want)
			}
		})
	}
	long := make([]string, 50)
	for i := range long {
		long[i] = "0123456789"
	}
	if got := renderArgv(long); len(got) > argvDisplayLimit+len("…") {
		t.Errorf("the rendered line is bounded: %d bytes", len(got))
	}
	// Truncation cuts on a rune boundary, never mid-rune.
	multibyte := strings.Repeat("文", 150)
	if got := renderArgv([]string{multibyte}); !utf8.ValidString(strings.TrimSuffix(got, "…")) {
		t.Errorf("a truncated line is valid UTF-8 up to the ellipsis: %q", got)
	}
	long[len(long)-1] = "HADRON_TOKEN=hdr_secret_tail"
	if rendered := renderArgv(long); strings.Contains(rendered, "hdr_secret_tail") {
		t.Error("redaction applies inside the bounded tail too")
	}
}

// argvFromCmdline keeps an empty LAST argument (Jane D3) and treats an empty
// read as no argv at all, so comm answers for kernel threads and zombies
// (#1474 delta review P3-1).
func TestArgvFromCmdline(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  []byte
		want []string
		ok   bool
	}{
		"ordinary":            {[]byte("sh\x00-c\x00echo hi\x00"), []string{"sh", "-c", "echo hi"}, true},
		"one terminator only": {[]byte("a\x00"), []string{"a"}, true},
		"empty last argument": {[]byte("sh\x00-c\x00sleep 30 & wait\x00label\x00\x00"), []string{"sh", "-c", "sleep 30 & wait", "label", ""}, true},
		"empty is no argv":    {nil, nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := argvFromCmdline(tc.raw)
			if ok != tc.ok || len(got) != len(tc.want) {
				t.Fatalf("argvFromCmdline(%q) = %q, %v; want %q, %v", tc.raw, got, ok, tc.want, tc.ok)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("argv[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
