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

// An agent that already exited — its group gone — is a confirmed stop that
// SIGNALS nothing. On Linux the waiter proved the group empty at its reap
// (emptyAtReap) and the stop confirms from that proof alone; on the other
// Unix platforms the leader was reaped at its exit and the stop's ESRCH
// probe — the documented residual position — is the confirmation. Either
// way the contract every Unix platform keeps is stopped=true, silently,
// with zero signals (the probe counts are asserted on
// Linux in reapgroup_linux_test.go — this file also builds on darwin/BSD).
//
// #1970: on Linux a churning /proc can void the exit-time scan, so the
// zombie is still held when this stop runs and the journey walks the
// whole ladder truthfully — one TERM, one KILL, or records kept. The
// Linux branch asserts that ladder (stormscan_linux_test.go); every other
// Unix platform keeps the settled strict baseline below verbatim.
func TestExecHostTerminateAfterExitSignalsNothing(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no true")
	}
	if journeyLadderApplies() {
		runPostExitJourneyLadder(t)
		return
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
// must still stop the whole group: the exited leader is held as an unreaped
// zombie (Linux), so the group's number is still the launch's and the stop
// reaches the member through it.
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
// caller keeps the agent's records (#1474 review P2-2). terminateGroup is a
// method now (it consults the agent's hold state); the agents here model a
// LIVE leader (not exited), whose drain oracle is the number probe, so the
// wrapped probe and signal decide the outcome.
func TestTerminateGroupReportsWhatItCouldNotConfirm(t *testing.T) {
	t.Run("signal fails", func(t *testing.T) {
		realSignal, realProbe := signalGroup, probeGroup
		signalGroup = func(int, syscall.Signal) error { return syscall.EPERM }
		probeGroup = func(int) error { return nil } // the group is there
		defer func() { signalGroup, probeGroup = realSignal, realProbe }()
		a := &execAgent{pid: 999998, grace: 50 * time.Millisecond}
		stopped, detail := a.Terminate()
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
		a := &execAgent{pid: 999998, grace: 50 * time.Millisecond}
		stopped, detail := a.Terminate()
		if stopped || !strings.Contains(detail, "did not exit within") {
			t.Errorf("a group that outlives SIGKILL is not a stop: stopped=%v %q", stopped, detail)
		}
	})
}

// The occupied-tree refusal names the occupant without carrying its
// credentials or an unbounded tail (#1474 review P2-4; delta review D2/P3:
// credential-shaped headers, URL userinfo, -u/-p, invalid UTF-8).
func TestRenderArgvBoundsAndRedacts(t *testing.T) {
	const tok36 = "abcdefghijklmnopqrstuvwxyz0123456789"
	for name, tc := range map[string]struct {
		argv []string
		want string
	}{
		"plain":             {[]string{"sleep", "30"}, "sleep 30"},
		"assignment":        {[]string{"agent", "--token=hdr_secret_1"}, "agent --token=<redacted>"},
		"env style":         {[]string{"agent", "HADRON_TOKEN=hdr_x"}, "agent HADRON_TOKEN=<redacted>"},
		"bare flag":         {[]string{"agent", "--token", "hdr_secret_1", "--verbose"}, "agent --token <redacted> --verbose"},
		"header":            {[]string{"curl", "-H", "Authorization: Bearer hdr_x"}, "curl -H Authorization: <redacted>"},
		"cookie":            {[]string{"curl", "-H", "Cookie: session=hdr_x"}, "curl -H Cookie: <redacted>"},
		"api-key header":    {[]string{"curl", "-H", "X-API-Key: SYNTHETIC_SECRET_1817"}, "curl -H X-API-Key: <redacted>"},
		"auth-token header": {[]string{"curl", "-H", "X-Auth-Token: SYNTHETIC_SECRET_1817"}, "curl -H X-Auth-Token: <redacted>"},
		// Client-Secret is not an allowlisted header name, so not even the
		// name leaves — only <redacted> does; over-redaction is the safe
		// direction.
		"secret-named header":     {[]string{"curl", "-H", "Client-Secret: SYNTHETIC_1817"}, "curl -H <redacted>"},
		"plain header kept":       {[]string{"curl", "-H", "Accept: application/json"}, `curl -H "Accept: application/json"`},
		"url userinfo":            {[]string{"curl", "https://user:pass@host/path"}, "curl https://host/path"},
		"url userinfo no pass":    {[]string{"curl", "https://user@host/path"}, "curl https://host/path"},
		"git ssh form kept":       {[]string{"git", "clone", "git@host:repo.git"}, "git clone git@host:repo.git"},
		"curl -u next arg":        {[]string{"curl", "-u", "user:pass", "https://h"}, "curl -u <redacted> https://h"},
		"mysql -p attached":       {[]string{"mysql", "-pSECRET_1817"}, "mysql -p<redacted>"},
		"invalid utf8 quoted":     {[]string{"a", "b\xff", "c"}, `a "b\xff" c`},
		"spaces kept unambiguous": {[]string{"sh", "-c", "sleep 30 & wait"}, `sh -c "sleep 30 & wait"`},
		"empty argument survives": {[]string{"a", "", "b"}, `a "" b`},
		// An unencoded @ in the password must not leak the tail after the
		// first @.
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
		// The rules arm from the first argument.
		// naming a credential-flag binary, so WRAPPERS no longer leak the
		// value; and the flags are per-binary, so curl's -p (--proxytunnel,
		// no value) does not swallow -u's value and mysql's -p (attached
		// only) leaves a following database name readable.
		"sudo curl -u":            {[]string{"sudo", "curl", "-u", "user:pass", "https://h"}, "sudo curl -u <redacted> https://h"},
		"env curl -u":             {[]string{"env", "curl", "-u", "user:pass"}, "env curl -u <redacted>"},
		"timeout sshpass -p":      {[]string{"timeout", "30", "sshpass", "-p", "secret", "ssh", "h"}, "timeout 30 sshpass -p <redacted> ssh h"},
		"nice mysql -p attached":  {[]string{"nice", "mysql", "-pX"}, "nice mysql -p<redacted>"},
		"curl -p takes no value":  {[]string{"curl", "-p", "-u", "user:pass", "https://h"}, "curl -p -u <redacted> https://h"},
		"curl -U next":            {[]string{"curl", "-U", "proxy:pass", "https://h"}, "curl -U <redacted> https://h"},
		"curl -U attached":        {[]string{"curl", "-Uproxy:pass"}, "curl -U<redacted>"},
		"mysql -p next is a name": {[]string{"mysql", "-p", "db"}, "mysql -p db"},
		// A URL-shaped prefix running into prose ends
		// its authority at the whitespace; the prose is never cut at a
		// later @.
		"url into prose kept": {[]string{"sh", "-c", "curl https://h1 mail user@example.com"}, `sh -c "curl https://h1 mail user@example.com"`},
		// F6 moved to the /proc boundary (argvFromCmdline): renderArgv
		// renders the slots it is given, real empties included.
		"two real trailing empties": {[]string{"agent", "", ""}, `agent "" ""`},
		// P3-2 residual: an argument appended raw for its name is still
		// quoted when it carries invalid UTF-8.
		"raw append quoted too": {[]string{"a", "--token\xff"}, `a "--token\xff"`},
		// curl's LONG credential spellings: next-argument, and attached
		// with '='. A long flag matches exactly or '='-delimited —
		// --user-agent is a different flag and stays readable.
		"curl --user next":       {[]string{"curl", "--user", "user:pass", "https://h"}, "curl --user <redacted> https://h"},
		"curl --user attached":   {[]string{"curl", "--user=user:pass"}, "curl --user=<redacted>"},
		"curl --proxy-user next": {[]string{"curl", "--proxy-user", "p:q"}, "curl --proxy-user <redacted>"},
		"curl --proxy-user att":  {[]string{"curl", "--proxy-user=p:q"}, "curl --proxy-user=<redacted>"},
		"curl -u attached short": {[]string{"curl", "-uuser:pass"}, "curl -u<redacted>"},
		"curl --user-agent kept": {[]string{"curl", "--user-agent", "hdr_agent_1", "https://h"}, "curl --user-agent hdr_agent_1 https://h"},
		"curl --username kept":   {[]string{"curl", "--username", "bob"}, "curl --username bob"},
		// A long credential flag's space-separated value is its value in the
		// generic credential-name rules too (mysql --password pass): the
		// value never leaves; -p's attached-only distinction is the readable
		// bare form.
		"mysql --password next": {[]string{"mysql", "--password", "db"}, "mysql --password <redacted>"},
		"second binary re-arms": {[]string{"sshpass", "-p", "secret", "curl", "-u", "user:pass"}, "sshpass -p <redacted> curl -u <redacted>"},
		"re-arm proxy-user att": {[]string{"sshpass", "-p", "s", "curl", "--proxy-user=p:q"}, "sshpass -p <redacted> curl --proxy-user=<redacted>"},
		// A later table-binary spelling never
		// DISARMS an earlier one — whether the spelling is the binary or
		// just an argument (an output file named mysql) is not knowable
		// from argv, so the UNION of the armed tables applies.
		"curl -o mysql -u":  {[]string{"curl", "-o", "mysql", "-u", "user:pass", "https://h"}, "curl -o mysql -u <redacted> https://h"},
		"mysql -u curl -p":  {[]string{"mysql", "-u", "curl", "-phunter2"}, "mysql -u curl -p<redacted>"},
		"re-arm on a value": {[]string{"sshpass", "-p", "curl", "curl", "-u", "u:p"}, "sshpass -p <redacted> curl -u <redacted>"},
		// curl's other secret-carrying flags.
		"curl --pass next":         {[]string{"curl", "--pass", "secret", "https://h"}, "curl --pass <redacted> https://h"},
		"curl --pass attached":     {[]string{"curl", "--pass=secret"}, "curl --pass=<redacted>"},
		"curl --proxy-pass next":   {[]string{"curl", "--proxy-pass", "secret"}, "curl --proxy-pass <redacted>"},
		"curl --oauth2-bearer att": {[]string{"curl", "--oauth2-bearer=hdr_x"}, "curl --oauth2-bearer=<redacted>"},
		"curl -E next":             {[]string{"curl", "-E", "k.pem:secret"}, "curl -E <redacted>"},
		"curl --cert attached":     {[]string{"curl", "--cert=k.pem:secret"}, "curl --cert=<redacted>"},
		// '='-attached header forms: credentialHeader cannot cross '=', so
		// --header=Authorization: … never matches it as a whole and
		// --header carries no table entry — the value-side match in
		// renderArgv's default branch is what redacts these. The glued
		// short form (-H) matches the header regex as a whole argument;
		// its allowlisted name must keep rendering.
		"curl --header attached":        {[]string{"curl", "--header=Authorization: Bearer hdr_x", "https://h"}, `curl --header=Authorization: <redacted> https://h`},
		"curl --proxy-header att":       {[]string{"curl", "--proxy-header=X-Auth-Token: SYNTHETIC_1817"}, `curl --proxy-header=X-Auth-Token: <redacted>`},
		"glued -H allowlisted name":     {[]string{"curl", "-HAuthorization: Bearer hdr_x", "https://h"}, `curl -HAuthorization: <redacted> https://h`},
		"curl --header attached cookie": {[]string{"curl", "--header=Cookie: session=hdr_x"}, `curl --header=Cookie: <redacted>`},
		// A header-SHAPED value on a flag the credential table knows keeps
		// its FULL redaction: the value of --user, --cert or --proxy-user
		// is credential material (user:pass, keyfile:password), so its
		// prefix is not a "header name" and must not survive.
		"curl --user header-shaped":   {[]string{"curl", "--user=token_SYNTHETIC_1817:ignored"}, `curl --user=<redacted>`},
		"curl --cert header-shaped":   {[]string{"curl", "--cert=key_SYNTHETIC_1817:ignored"}, `curl --cert=<redacted>`},
		"curl --proxy-user hdr-shape": {[]string{"curl", "--proxy-user=auth_SYNTHETIC_1817:ignored"}, `curl --proxy-user=<redacted>`},
		// An assignment argument whose VALUE is a header line (env HDR=…,
		// docker -e AUTH_HDR=…) is not dash-prefixed, so only the
		// non-dash value-side check catches it.
		"env assignment header": {[]string{"env", "HDR=Authorization: Bearer tok_1817", "curl", "https://h"}, `env HDR=Authorization: <redacted> curl https://h`},
		"docker -e assignment":  {[]string{"docker", "run", "-e", "AUTH_HDR=Authorization: Bearer tok_1817", "img"}, `docker run -e AUTH_HDR=Authorization: <redacted> img`},
		// A header-SHAPED value keeps its pre-colon material only when that
		// material is an ALLOWLISTED header name. credentialHeader accepts
		// any name-shaped prefix containing auth/token/key/secret/cookie,
		// so without the allowlist the token half of a non-header
		// user:pass value (UPSTREAM=token_X:…, --foo=token_X:…, a bare
		// token_X:…) would render as a "name". The lowercase row pins the
		// other side: an allowlisted name renders as written,
		// case-insensitively.
		"env non-header token:pass":  {[]string{"env", "UPSTREAM=token_SYNTHETIC_1817:x-oauth-basic", "curl", "https://h"}, `env UPSTREAM=<redacted> curl https://h`},
		"unknown flag token:pass":    {[]string{"curl", "--foo=token_SYNTHETIC_1817:pass", "https://h"}, `curl --foo=<redacted> https://h`},
		"lowercase allowlisted name": {[]string{"env", "hdr=authorization: bearer tok_1817", "curl"}, `env hdr=authorization: <redacted> curl`},
		"bare non-allowlisted":       {[]string{"curl", "-H", "token_SYNTHETIC_1817:y", "https://h"}, `curl -H <redacted> https://h`},
		"glued non-allowlisted":      {[]string{"curl", "-Htoken_SYNTHETIC_1817:y", "https://h"}, `curl <redacted> https://h`},
		// Only -H glues a header (-HAuthorization: …); any other short
		// flag's glued value is credential material even when its tail
		// spells an allowlisted name (mysql -pcookie:rest leaked "cookie",
		// curl -uauthorization:pass leaked the user). The next-argument
		// forms are claimed by the table, not the header allowlist.
		"mysql -p glued cookie-shaped":     {[]string{"mysql", "-pcookie:rest"}, `mysql <redacted>`},
		"curl -u glued allowlisted-shaped": {[]string{"curl", "-uauthorization:pass", "https://h"}, `curl <redacted> https://h`},
		"curl -u glued token-shaped":       {[]string{"curl", "-utoken_X:pass", "https://h"}, `curl <redacted> https://h`},
		"curl -u next token-shaped":        {[]string{"curl", "-u", "token_X:pass", "https://h"}, `curl -u <redacted> https://h`},
		"glued -H uppercase name":          {[]string{"curl", "-HAUTHORIZATION: Bearer x"}, `curl -HAUTHORIZATION: <redacted>`},
		// A trailing space before ':' is the same header name (N-18).
		"header name before spaced colon": {[]string{"curl", "-H", "Authorization : x"}, `curl -H Authorization: <redacted>`},
		// The detector runs on the rendered pieces, after classification: a
		// known token inside a header-shaped name or a credential-named
		// assignment must not break the keyword rules and let the value
		// through (§10 seat, cli#1982).
		"token-named header with prefix token": {[]string{"tool", "token_ghp_" + tok36 + ":hunter2"}, `tool <redacted>`},
		"x-auth header with prefix token":      {[]string{"curl", "X-Auth-ghp_" + tok36 + ": hunter2"}, `curl <redacted>`},
		"env header with prefix token":         {[]string{"env", "HDR=X-Auth-ghp_" + tok36 + ": hunter2"}, `env HDR=<redacted>`},
		"flag header with prefix token":        {[]string{"tool", "--x=X-Token-ghp_" + tok36 + ":hunter2"}, `tool --x=<redacted>`},
		"keyword inside token body":            {[]string{"tool", "--opt-ghp_" + tok36 + "TOKEN", "hunter2"}, `tool --opt-<redacted> <redacted>`},
		"aws temporary key id":                 {[]string{"tool", "ASIAABCDEFGHIJKLMNOP"}, `tool <redacted>`},
		"github oauth token letter":            {[]string{"tool", "gho_" + tok36}, `tool <redacted>`},
		// Known credential prefixes (cli#1972 N-19): the token renders
		// <redacted> wherever it sits; the rest of the argument stays.
		"env prefix token colon":    {[]string{"env", "UPSTREAM=ghp_" + tok36 + ":x-oauth-basic", "curl"}, `env UPSTREAM=<redacted>:x-oauth-basic curl`},
		"unknown flag prefix token": {[]string{"curl", "--foo=ghp_" + tok36 + ":pass"}, `curl --foo=<redacted>:pass`},
		"bare hadron user token":    {[]string{"agent", "hdr_user_" + tok36}, `agent <redacted>`},
		"hadron app token after =":  {[]string{"agent", "X=hdr_app_" + tok36}, `agent X=<redacted>`},
		"url-embedded prefix token": {[]string{"git", "clone", "https://host/x?k=glpat-" + tok36}, `git clone https://host/x?k=<redacted>`},
		"slack token":               {[]string{"tool", "xoxb-1234567890-abcdef"}, `tool <redacted>`},
		"stripe live key":           {[]string{"tool", "sk_live_" + tok36}, `tool <redacted>`},
		"openai style key":          {[]string{"tool", "--k", "sk-" + tok36}, `tool --k <redacted>`},
		"aws access key id":         {[]string{"tool", "AKIAABCDEFGHIJKLMNOP"}, `tool <redacted>`},
		"github fine-grained pat":   {[]string{"tool", "github_pat_" + tok36}, `tool <redacted>`},
		"hpt query value":           {[]string{"curl", "https://h/hooks/abc/name?x=1&hpt=eyJhbGciOi.payload.sig"}, `curl https://h/hooks/<redacted>/name?x=1&hpt=<redacted>`},
		"webhook path secret":       {[]string{"curl", "https://h/hooks/s3cr3tpath/deploy"}, `curl https://h/hooks/<redacted>/deploy`},
		// A token directly after a control character is quoted as \n / \t,
		// whose last letter would hide the boundary; the detector runs
		// BEFORE quoting for the verbatim pieces (P3-D1).
		"token after newline in sh -c": {[]string{"sh", "-c", "echo hi\nghp_" + tok36}, `sh -c "echo hi\n<redacted>"`},
		"token after tab in sh -c":     {[]string{"sh", "-c", "echo hi\tghp_" + tok36}, `sh -c "echo hi\t<redacted>"`},
		// Minimum body lengths: the shortest body fires, one less does not.
		"ghp min body fires": {[]string{"tool", "ghp_" + strings.Repeat("a", 20)}, `tool <redacted>`},
		"ghp below min kept": {[]string{"tool", "ghp_" + strings.Repeat("a", 19)}, `tool ghp_` + strings.Repeat("a", 19)},
		"hdr_user min body":  {[]string{"tool", "hdr_user_" + strings.Repeat("a", 16)}, `tool <redacted>`},
		"hdr_user below min": {[]string{"tool", "hdr_user_" + strings.Repeat("a", 15)}, `tool hdr_user_` + strings.Repeat("a", 15)},
		"xoxb min body":      {[]string{"tool", "xoxb-" + strings.Repeat("1", 10)}, `tool <redacted>`},
		"xoxb below min":     {[]string{"tool", "xoxb-" + strings.Repeat("1", 9)}, `tool xoxb-` + strings.Repeat("1", 9)},
		"akia exact length":  {[]string{"tool", "AKIA" + strings.Repeat("A", 16)}, `tool <redacted>`},
		"akia one short":     {[]string{"tool", "AKIA" + strings.Repeat("A", 15)}, `tool AKIA` + strings.Repeat("A", 15)},
		// A long hyphenated word that merely ENDS in a prefix-alike is kept.
		"hyphenated prefix-alike kept": {[]string{"echo", "a-very-long-desk-" + tok36}, `echo a-very-long-desk-` + tok36},
		// Negatives: short prefix-alikes and prose stay readable.
		"short ghp prefix kept":       {[]string{"echo", "ghp_short"}, `echo ghp_short`},
		"path containing sk- kept":    {[]string{"ls", "/opt/sk-learn/x"}, `ls /opt/sk-learn/x`},
		"word ending in prefix kept":  {[]string{"echo", "desk-" + tok36}, `echo desk-` + tok36},
		"hooks path without url kept": {[]string{"ls", "/hooks/abc/name"}, `ls /hooks/abc/name`},
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

// argvFromCmdline keeps an empty LAST argument, collapses a RUN of
// trailing empties — setproctitle padding (F6), now at this /proc boundary
// rather than in renderArgv — and treats an empty read as
// no argv at all, so comm answers for kernel threads and zombies (#1474).
func TestArgvFromCmdline(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  []byte
		want []string
		ok   bool
	}{
		"ordinary":             {[]byte("sh\x00-c\x00echo hi\x00"), []string{"sh", "-c", "echo hi"}, true},
		"one terminator only":  {[]byte("a\x00"), []string{"a"}, true},
		"empty last argument":  {[]byte("sh\x00-c\x00sleep 30 & wait\x00label\x00\x00"), []string{"sh", "-c", "sleep 30 & wait", "label", ""}, true},
		"setproctitle padding": {[]byte("titled\x00x\x00\x00\x00\x00"), []string{"titled", "x", ""}, true},
		"empty is no argv":     {nil, nil, false},
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
