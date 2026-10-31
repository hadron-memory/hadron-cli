package healthsched

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// A config path with a space and a percent sign exercises every quoting rule.
var spec = Spec{Bin: "/usr/local/bin/hadron", Config: "/home/u/my hosts/host 100%.yaml", Interval: 5 * time.Minute, Path: "/usr/bin:/home/u/bin"}

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("%s differs:\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

func TestGoldens(t *testing.T) {
	golden(t, "hadron-team-health.service", SystemdService(spec))
	golden(t, "hadron-team-health.timer", SystemdTimer(spec))
	line, err := CronLine(spec)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "crontab.line", line+"\n")
	golden(t, "com.hadronmemory.team-health.plist", Plist(spec))
}

// The exact argv the entry runs is the one #1181 pins.
func TestArgvIsTheSweepWithConfig(t *testing.T) {
	want := []string{"/usr/local/bin/hadron", "team", "health", "sweep", "--config", spec.Config}
	if got := spec.Argv(); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q", got)
	}
}

func TestQuotingPercentIsEscapedEverywhere(t *testing.T) {
	if !strings.Contains(SystemdService(spec), "host 100%%.yaml") {
		t.Error("systemd must double a % in ExecStart")
	}
	line, _ := CronLine(spec)
	if strings.Contains(strings.ReplaceAll(line, `\%`, ""), "%") {
		t.Errorf("cron line has a bare %%, which cron reads as a newline: %s", line)
	}
}

type fake struct {
	calls   []string
	stdins  []string
	crontab string
	have    map[string]bool
	fail    map[string]bool // argv-prefix -> error
	out     map[string]string
}

func (f *fake) run(argv []string, stdin string) ([]byte, error) {
	k := strings.Join(argv, " ")
	f.calls = append(f.calls, k)
	for p := range f.fail {
		if strings.HasPrefix(k, p) {
			return []byte("boom"), errors.New("exit 1")
		}
	}
	if k == "crontab -l" {
		if f.crontab == "" {
			return nil, errors.New("no crontab for u")
		}
		return []byte(f.crontab), nil
	}
	if k == "crontab -" {
		f.crontab = stdin
		f.stdins = append(f.stdins, stdin)
	}
	return []byte(f.out[k]), nil
}

func (f *fake) env(t *testing.T, goos string, tools ...string) Env {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", "")
	f.have = map[string]bool{}
	for _, x := range tools {
		f.have[x] = true
	}
	return Env{Home: t.TempDir(), GOOS: goos, UID: 1000, Run: f.run, Which: func(n string) (string, error) {
		if f.have[n] {
			return "/usr/bin/" + n, nil
		}
		return "", exec.ErrNotFound
	}}
}

func tree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil {
			content, _ := os.ReadFile(p)
			b.WriteString(p + "|" + string(content) + "\n")
		}
		return nil
	})
	return b.String()
}

func count(calls []string, prefix string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func TestSystemdRoundTrip(t *testing.T) {
	f := &fake{}
	e := f.env(t, "linux", "systemctl")
	res, err := e.Install(spec)
	if err != nil || !res.Changed || res.Kind != Systemd {
		t.Fatalf("install: %+v %v", res, err)
	}
	for _, p := range []string{e.servicePath(), e.timerPath()} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
	if count(f.calls, "systemctl --user daemon-reload") != 1 || count(f.calls, "systemctl --user enable --now "+Name+".timer") != 1 {
		t.Fatalf("calls %q", f.calls)
	}

	// Same again: byte-identical, no scheduler call.
	before, n := tree(t, e.Home), len(f.calls)
	res, err = e.Install(spec)
	if err != nil || res.Changed || tree(t, e.Home) != before || len(f.calls) != n+countDetect(f.calls[n:]) {
		t.Fatalf("re-install changed something: %+v %v calls %q", res, err, f.calls[n:])
	}
	if count(f.calls[n:], "systemctl --user daemon-reload") != 0 {
		t.Error("an unchanged re-install must not reload")
	}

	// A new interval updates the timer with one reload.
	n = len(f.calls)
	s2 := spec
	s2.Interval = 10 * time.Minute
	res, err = e.Install(s2)
	if err != nil || !res.Changed || !strings.Contains(tree(t, e.Home), "OnUnitActiveSec=10min") || count(f.calls[n:], "systemctl --user daemon-reload") != 1 {
		t.Fatalf("interval update: %+v %v %q", res, err, f.calls[n:])
	}

	// Uninstall removes exactly what was written; a second one is a no-op.
	res, err = e.Uninstall()
	if err != nil || !res.Changed {
		t.Fatal(res, err)
	}
	for _, p := range []string{e.servicePath(), e.timerPath()} {
		if fileExists(p) {
			t.Errorf("%s survived uninstall", p)
		}
	}
	if res, err = e.Uninstall(); err != nil || res.Changed {
		t.Errorf("second uninstall: %+v %v", res, err)
	}
}

// Detection probes (show-environment, loginctl) are calls too; count only the
// ones that would change state.
func countDetect(calls []string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, "systemctl --user show-environment") {
			n++
		}
	}
	return n
}

func TestDifferentConfigIsRefusedUntilUninstall(t *testing.T) {
	f := &fake{}
	e := f.env(t, "linux", "systemctl")
	if _, err := e.Install(spec); err != nil {
		t.Fatal(err)
	}
	s2 := spec
	s2.Config = "/etc/other.yaml"
	before := tree(t, e.Home)
	_, err := e.Install(s2)
	var ue *UsageError
	if !errors.As(err, &ue) || tree(t, e.Home) != before {
		t.Fatalf("want a usage refusal that writes nothing, got %v", err)
	}
	if _, err := e.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Install(s2); err != nil {
		t.Fatalf("after uninstall: %v", err)
	}
}

func TestMidwayFailureLeavesAStateUninstallRemovesAndStatusReports(t *testing.T) {
	f := &fake{fail: map[string]bool{"systemctl --user enable": true, "systemctl --user is-enabled": true}}
	e := f.env(t, "linux", "systemctl")
	_, err := e.Install(spec)
	if err == nil || !strings.Contains(err.Error(), "uninstall") {
		t.Fatalf("want a failure that names uninstall, got %v", err)
	}
	st := e.ReadStatus()
	if !st.Installed || st.Active || len(st.Notes) == 0 {
		t.Fatalf("status must report the partial install: %+v", st)
	}
	f.fail = nil
	if _, err := e.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if fileExists(e.servicePath()) || fileExists(e.timerPath()) || e.ReadStatus().Installed {
		t.Error("uninstall left the partial install behind")
	}
}

func TestCronRoundTripKeepsOtherLines(t *testing.T) {
	f := &fake{crontab: "MAILTO=me\n0 3 * * * /usr/bin/backup\n"}
	e := f.env(t, "linux", "crontab")
	if res, err := e.Install(spec); err != nil || res.Kind != Cron || !res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.HasPrefix(f.crontab, "MAILTO=me\n0 3 * * * /usr/bin/backup\n*/5 * * * * ") {
		t.Fatalf("crontab = %q", f.crontab)
	}
	n := len(f.stdins)
	if res, err := e.Install(spec); err != nil || res.Changed || len(f.stdins) != n {
		t.Fatalf("re-install wrote the crontab again: %+v %v", res, err)
	}
	s2 := spec
	s2.Interval = 15 * time.Minute
	if _, err := e.Install(s2); err != nil || strings.Count(f.crontab, Name) != 1 || !strings.Contains(f.crontab, "*/15 * * * *") {
		t.Fatalf("interval update: %v %q", err, f.crontab)
	}
	st := e.ReadStatus()
	if !st.Installed || st.Kind != "cron" || st.Interval != "15m0s" || st.LastRun == nil || st.LastRun.At != nil || st.LastRun.ExitCode != nil {
		t.Fatalf("status %+v", st)
	}
	if _, err := e.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if f.crontab != "MAILTO=me\n0 3 * * * /usr/bin/backup\n" {
		t.Fatalf("uninstall must leave every other line: %q", f.crontab)
	}
}

func TestCronUninstallOfOnlyEntryEmptiesCrontab(t *testing.T) {
	f := &fake{}
	e := f.env(t, "linux", "crontab")
	if _, err := e.Install(spec); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Uninstall(); err != nil || strings.TrimSpace(f.crontab) != "" {
		t.Fatalf("%v %q", err, f.crontab)
	}
}

func TestLaunchdRoundTrip(t *testing.T) {
	f := &fake{}
	e := f.env(t, "darwin", "launchctl")
	if res, err := e.Install(spec); err != nil || res.Kind != Launchd || !fileExists(e.plistPath()) {
		t.Fatalf("%+v %v", res, err)
	}
	if count(f.calls, "launchctl bootstrap gui/1000 ") != 1 {
		t.Fatalf("calls %q", f.calls)
	}
	n := len(f.calls)
	if res, err := e.Install(spec); err != nil || res.Changed || len(f.calls) != n {
		t.Fatalf("re-install: %+v %v", res, err)
	}
	if st := e.ReadStatus(); !st.Installed || st.Config != spec.Config || st.Interval != "5m0s" || st.Bin != spec.Bin {
		t.Fatalf("status %+v", st)
	}
	if _, err := e.Uninstall(); err != nil || fileExists(e.plistPath()) {
		t.Fatal(err)
	}
}

func TestNoSupportedSchedulerWritesNothing(t *testing.T) {
	f := &fake{}
	e := f.env(t, "linux")
	before := tree(t, e.Home)
	_, err := e.Install(spec)
	var ue *UsageError
	if !errors.As(err, &ue) || tree(t, e.Home) != before {
		t.Fatalf("want a usage refusal and an unchanged HOME, got %v", err)
	}
	// systemctl present but no user session behaves the same.
	f2 := &fake{fail: map[string]bool{"systemctl --user show-environment": true}}
	e2 := f2.env(t, "linux", "systemctl")
	if _, err := e2.Install(spec); !errors.As(err, &ue) {
		t.Fatalf("got %v", err)
	}
}

func TestIntervalAndPathValidation(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Minute, 30 * time.Second, 90 * time.Second} {
		if err := ValidateInterval(d); err == nil {
			t.Errorf("%s accepted", d)
		}
	}
	for _, p := range []string{"relative.yaml", "", "/a\nb.yaml", "/a\x1b[0m.yaml"} {
		if err := ValidatePath("--config", p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
	// Cron cannot express 7m or 7h evenly: refused with nothing written.
	for _, d := range []time.Duration{7 * time.Minute, 90 * time.Minute, 7 * time.Hour} {
		f := &fake{}
		e := f.env(t, "linux", "crontab")
		s := spec
		s.Interval = d
		_, err := e.Install(s)
		var ue *UsageError
		if !errors.As(err, &ue) || len(f.stdins) != 0 {
			t.Errorf("%s: got %v", d, err)
		}
	}
	// The same 7m is fine under systemd.
	f := &fake{}
	e := f.env(t, "linux", "systemctl")
	s := spec
	s.Interval = 7 * time.Minute
	if _, err := e.Install(s); err != nil {
		t.Fatal(err)
	}
}

func TestCronScheduleTable(t *testing.T) {
	for d, want := range map[time.Duration]string{time.Minute: "*/1 * * * *", 15 * time.Minute: "*/15 * * * *", time.Hour: "0 * * * *", 6 * time.Hour: "0 */6 * * *", 24 * time.Hour: "0 0 * * *"} {
		if got, err := cronSchedule(d); err != nil || got != want {
			t.Errorf("%s = %q %v, want %q", d, got, err, want)
		}
	}
}

// The host key lives in a 0600 file the sweep reads; nothing here reads it, and
// nothing it could be in reaches a generated file or a scheduler argv.
func TestHostKeyNeverReachesAnythingWritten(t *testing.T) {
	const sentinel = "hadron-host-key-SENTINEL-9f3"
	f := &fake{}
	e := f.env(t, "linux", "systemctl")
	keyFile := filepath.Join(e.Home, "host.key")
	if err := os.WriteFile(keyFile, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	s := spec
	s.Config = filepath.Join(e.Home, "host.yaml")
	if _, err := e.Install(s); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(f.calls, "\n") + strings.Join(f.stdins, "\n")
	for _, p := range []string{e.servicePath(), e.timerPath()} {
		b, _ := os.ReadFile(p)
		all += string(b)
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
			t.Errorf("%s mode %v", p, fi.Mode())
		}
	}
	if strings.Contains(all, sentinel) || strings.Contains(all, keyFile) {
		t.Error("the host key or its path reached a generated file or argv")
	}
}

func TestResolveOrca(t *testing.T) {
	dir := t.TempDir()
	orca := filepath.Join(dir, "orca")
	if err := os.WriteFile(orca, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveOrca("", dir+":/nonexistent"); err != nil || got != orca {
		t.Fatalf("PATH: %q %v", got, err)
	}
	// orca_bin wins even when the PATH has no orca.
	if got, err := ResolveOrca(orca, "/nonexistent"); err != nil || got != orca {
		t.Fatalf("orca_bin: %q %v", got, err)
	}
	var ue *UsageError
	if _, err := ResolveOrca("", "/nonexistent"); !errors.As(err, &ue) {
		t.Fatalf("neither resolves: %v", err)
	}
	if _, err := ResolveOrca("/nonexistent/orca", dir); !errors.As(err, &ue) {
		t.Fatalf("a missing orca_bin must be refused, not fall back: %v", err)
	}
}

func TestStatusShowsTheOrcaTheEntryResolves(t *testing.T) {
	dir := t.TempDir()
	orca := filepath.Join(dir, "orca")
	_ = os.WriteFile(orca, []byte("#!/bin/sh\n"), 0o755)
	f := &fake{}
	e := f.env(t, "linux", "systemctl")
	s := spec
	s.Path = dir + ":/usr/bin"
	if _, err := e.Install(s); err != nil {
		t.Fatal(err)
	}
	if st := e.ReadStatus(); st.Orca != orca || st.Bin != s.Bin || st.Config != s.Config || st.Interval != "5m0s" {
		t.Fatalf("%+v", st)
	}
}

func TestSystemdLastRunFromTheScheduler(t *testing.T) {
	f := &fake{out: map[string]string{"systemctl --user show " + Name + ".service -p ExecMainExitTimestamp -p ExecMainStatus": "ExecMainExitTimestamp=Sat 2026-10-31 02:00:00 UTC\nExecMainStatus=3\n"}}
	e := f.env(t, "linux", "systemctl")
	if _, err := e.Install(spec); err != nil {
		t.Fatal(err)
	}
	lr := e.ReadStatus().LastRun
	if lr == nil || lr.At == nil || lr.ExitCode == nil || *lr.ExitCode != 3 || lr.At.Year() != 2026 {
		t.Fatalf("%+v", lr)
	}
}

// The marker comment carries the config path too, and a bare % in it would be
// a newline to cron: the config must survive a round trip through the line.
func TestCronConfigWithPercentSurvivesTheRoundTrip(t *testing.T) {
	f := &fake{}
	e := f.env(t, "linux", "crontab")
	if _, err := e.Install(spec); err != nil {
		t.Fatal(err)
	}
	if got := e.existingConfig(Cron); got != spec.Config {
		t.Fatalf("config = %q", got)
	}
	s2 := spec
	s2.Config = "/etc/other.yaml"
	var ue *UsageError
	if _, err := e.Install(s2); !errors.As(err, &ue) {
		t.Fatalf("a different config must be refused: %v", err)
	}
}
