package healthsched

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Kind is the scheduler an entry lives in.
type Kind string

const (
	Systemd Kind = "systemd"
	Cron    Kind = "cron"
	Launchd Kind = "launchd"
)

// Runner executes a scheduler command with optional stdin. Tests inject a
// recorder; nothing in this package execs anything itself.
type Runner func(argv []string, stdin string) ([]byte, error)

// Env is everything the package reads from the machine.
type Env struct {
	Home   string
	GOOS   string
	UID    int
	Which  func(name string) (string, error) // exec.LookPath
	Run    Runner
	Stderr func(format string, a ...any) // advisory notes (never on stdout)
}

// Result reports what an install or uninstall did.
type Result struct {
	Kind    Kind
	Changed bool
	Files   []string
	Notes   []string
}

// Detect picks the scheduler: systemd user units where `systemctl --user`
// works, else cron, on macOS launchd; otherwise a usage refusal.
func (e Env) Detect() (Kind, error) {
	if e.GOOS == "darwin" {
		if _, err := e.Which("launchctl"); err == nil {
			return Launchd, nil
		}
		return "", noScheduler()
	}
	if _, err := e.Which("systemctl"); err == nil {
		if _, err := e.Run([]string{"systemctl", "--user", "show-environment"}, ""); err == nil {
			return Systemd, nil
		}
	}
	if _, err := e.Which("crontab"); err == nil {
		return Cron, nil
	}
	return "", noScheduler()
}

func noScheduler() error {
	return usagef("no supported scheduler here (need systemd user units, crontab, or launchd on macOS); nothing was written")
}

func (e Env) unitDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && strings.HasPrefix(x, "/") && e.Home == os.Getenv("HOME") {
		return filepath.Join(x, "systemd", "user")
	}
	return filepath.Join(e.Home, ".config", "systemd", "user")
}

func (e Env) servicePath() string { return filepath.Join(e.unitDir(), Name+".service") }
func (e Env) timerPath() string   { return filepath.Join(e.unitDir(), Name+".timer") }
func (e Env) plistPath() string {
	return filepath.Join(e.Home, "Library", "LaunchAgents", LaunchdLabel+".plist")
}

func writeFile(path, content string) (changed bool, err error) {
	if old, rerr := os.ReadFile(path); rerr == nil && string(old) == content {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(content), 0o644)
}

var configRE = regexp.MustCompile(`config=("(?:[^"\\]|\\.)*")`)

// existingConfig is the config path an installed entry was written for ("" if
// none), read from the entry itself.
func (e Env) existingConfig(k Kind) string {
	var text string
	switch k {
	case Systemd:
		b, _ := os.ReadFile(e.servicePath())
		text = string(b)
	case Launchd:
		b, _ := os.ReadFile(e.plistPath())
		m := plistConfigRE.FindSubmatch(b)
		if m != nil {
			return xmlUnescape(string(m[1]))
		}
		return ""
	case Cron:
		out, _ := e.Run([]string{"crontab", "-l"}, "")
		for _, l := range strings.Split(string(out), "\n") {
			if strings.Contains(l, cronMarker) {
				text = strings.ReplaceAll(l, `\%`, "%")
			}
		}
	}
	if m := configRE.FindStringSubmatch(text); m != nil {
		if s, err := strconv.Unquote(m[1]); err == nil {
			return s
		}
	}
	return ""
}

var plistConfigRE = regexp.MustCompile(`<string>--config</string>\s*<string>([^<]*)</string>`)

func xmlUnescape(s string) string {
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&amp;", "&").Replace(s)
}

// Install writes the entry and starts it. Re-installing the same entry
// changes nothing and calls no scheduler; a changed interval updates it; a
// different config is refused until Uninstall.
func (e Env) Install(s Spec) (Result, error) {
	if err := ValidatePath("the hadron binary path", s.Bin); err != nil {
		return Result{}, err
	}
	if err := ValidatePath("--config", s.Config); err != nil {
		return Result{}, err
	}
	if err := ValidateInterval(s.Interval); err != nil {
		return Result{}, err
	}
	k, err := e.Detect()
	if err != nil {
		return Result{}, err
	}
	if k == Cron {
		if _, err := cronSchedule(s.Interval); err != nil {
			return Result{}, err
		}
	}
	if old := e.existingConfig(k); old != "" && old != s.Config {
		return Result{}, usagef("an entry for %s is already installed; run `hadron team health uninstall` first to switch configs", old)
	}
	res := Result{Kind: k}
	switch k {
	case Systemd:
		return e.installSystemd(s, res)
	case Cron:
		return e.installCron(s, res)
	default:
		return e.installLaunchd(s, res)
	}
}

func (e Env) installSystemd(s Spec, res Result) (Result, error) {
	var changed bool
	for _, f := range []struct{ path, body string }{{e.servicePath(), SystemdService(s)}, {e.timerPath(), SystemdTimer(s)}} {
		c, err := writeFile(f.path, f.body)
		if err != nil {
			return res, err
		}
		changed = changed || c
		res.Files = append(res.Files, f.path)
	}
	res.Changed = changed
	if !changed {
		return res, nil
	}
	for _, argv := range [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "--now", Name + ".timer"}} {
		if out, err := e.Run(argv, ""); err != nil {
			return res, fmt.Errorf("%s failed (%s); files written: %s; `hadron team health uninstall` removes them", strings.Join(argv, " "), oneLine(out, err), strings.Join(res.Files, ", "))
		}
	}
	if e.lingerOff() {
		res.Notes = append(res.Notes, "systemd linger is off for this user: the timer stops when you log out. `loginctl enable-linger` (needs privileges) keeps it running.")
	}
	return res, nil
}

func (e Env) lingerOff() bool {
	out, err := e.Run([]string{"loginctl", "show-user", strconv.Itoa(e.UID), "-p", "Linger"}, "")
	return err == nil && strings.TrimSpace(string(out)) == "Linger=no"
}

func oneLine(out []byte, err error) string {
	m := strings.TrimSpace(string(out))
	if m == "" {
		return err.Error()
	}
	return strings.Join(strings.Fields(m), " ")
}

func (e Env) installCron(s Spec, res Result) (Result, error) {
	line, err := CronLine(s)
	if err != nil {
		return res, err
	}
	cur, _ := e.Run([]string{"crontab", "-l"}, "")
	lines := splitCron(string(cur))
	next := append(dropMarked(lines), line)
	if equalLines(lines, next) {
		return res, nil
	}
	res.Changed = true
	if out, err := e.Run([]string{"crontab", "-"}, strings.Join(next, "\n")+"\n"); err != nil {
		return res, fmt.Errorf("crontab - failed (%s); nothing was changed", oneLine(out, err))
	}
	res.Notes = append(res.Notes, "cron cannot report a last run: `status` shows it as unknown.")
	return res, nil
}

func splitCron(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func dropMarked(lines []string) []string {
	var keep []string
	for _, l := range lines {
		if !strings.Contains(l, cronMarker) {
			keep = append(keep, l)
		}
	}
	return keep
}

func equalLines(a, b []string) bool { return strings.Join(a, "\n") == strings.Join(b, "\n") }

func (e Env) installLaunchd(s Spec, res Result) (Result, error) {
	existed := fileExists(e.plistPath())
	c, err := writeFile(e.plistPath(), Plist(s))
	if err != nil {
		return res, err
	}
	res.Files = []string{e.plistPath()}
	res.Changed = c
	if !c {
		return res, nil
	}
	domain := "gui/" + strconv.Itoa(e.UID)
	if existed {
		_, _ = e.Run([]string{"launchctl", "bootout", domain + "/" + LaunchdLabel}, "")
	}
	if out, err := e.Run([]string{"launchctl", "bootstrap", domain, e.plistPath()}, ""); err != nil {
		return res, fmt.Errorf("launchctl bootstrap failed (%s); file written: %s; `hadron team health uninstall` removes it", oneLine(out, err), e.plistPath())
	}
	return res, nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// Uninstall removes exactly what Install wrote, whatever state it stopped in,
// and nothing else (other crontab lines are untouched). Nothing installed is
// success with Changed false.
func (e Env) Uninstall() (Result, error) {
	var res Result
	var errs []error
	// Cron first: it needs no files and is detected by its marker line.
	if _, err := e.Which("crontab"); err == nil && e.GOOS != "darwin" {
		if cur, cerr := e.Run([]string{"crontab", "-l"}, ""); cerr == nil {
			lines := splitCron(string(cur))
			if next := dropMarked(lines); !equalLines(lines, next) {
				res.Kind, res.Changed = Cron, true
				in := ""
				if len(next) > 0 {
					in = strings.Join(next, "\n") + "\n"
				}
				if out, err := e.Run([]string{"crontab", "-"}, in); err != nil {
					errs = append(errs, fmt.Errorf("crontab - failed (%s)", oneLine(out, err)))
				}
			}
		}
	}
	if fileExists(e.timerPath()) || fileExists(e.servicePath()) {
		res.Kind, res.Changed = Systemd, true
		_, _ = e.Run([]string{"systemctl", "--user", "disable", "--now", Name + ".timer"}, "")
		for _, p := range []string{e.timerPath(), e.servicePath()} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			} else {
				res.Files = append(res.Files, p)
			}
		}
		_, _ = e.Run([]string{"systemctl", "--user", "daemon-reload"}, "")
	}
	if fileExists(e.plistPath()) {
		res.Kind, res.Changed = Launchd, true
		_, _ = e.Run([]string{"launchctl", "bootout", "gui/" + strconv.Itoa(e.UID) + "/" + LaunchdLabel}, "")
		if err := os.Remove(e.plistPath()); err != nil {
			errs = append(errs, err)
		} else {
			res.Files = append(res.Files, e.plistPath())
		}
	}
	return res, errors.Join(errs...)
}

// LastRun is what the scheduler recorded; nil fields mean unknown.
type LastRun struct {
	At       *time.Time `json:"at"`
	ExitCode *int       `json:"exitCode"`
}

// Status describes the installed entry.
type Status struct {
	Installed bool     `json:"installed"`
	Kind      string   `json:"scheduler"`
	Active    bool     `json:"active"`
	Interval  string   `json:"interval"`
	Bin       string   `json:"binary"`
	Config    string   `json:"config"`
	Orca      string   `json:"orca"`
	Files     []string `json:"files"`
	LastRun   *LastRun `json:"lastRun"`
	Notes     []string `json:"notes"`
}

var (
	execStartRE = regexp.MustCompile(`(?m)^ExecStart="((?:[^"\\]|\\.)*)"`)
	timerRE     = regexp.MustCompile(`OnUnitActiveSec=(\d+)min`)
	pathRE      = regexp.MustCompile(`(?m)^Environment="PATH=((?:[^"\\]|\\.)*)"`)
)

// ReadStatus inspects what is installed. A partial install (files present, the
// scheduler not running them) is reported as installed, not active.
func (e Env) ReadStatus() Status {
	st := Status{Files: []string{}, Notes: []string{}}
	switch {
	case fileExists(e.timerPath()) || fileExists(e.servicePath()):
		st.Installed, st.Kind = true, string(Systemd)
		st.Files = []string{e.timerPath(), e.servicePath()}
		svc, _ := os.ReadFile(e.servicePath())
		tmr, _ := os.ReadFile(e.timerPath())
		if m := execStartRE.FindSubmatch(svc); m != nil {
			st.Bin = unSystemd(string(m[1]))
		}
		if m := timerRE.FindSubmatch(tmr); m != nil {
			n, _ := strconv.Atoi(string(m[1]))
			st.Interval = (time.Duration(n) * time.Minute).String()
		}
		st.Config = e.existingConfig(Systemd)
		if m := pathRE.FindSubmatch(svc); m != nil {
			st.Orca = e.orcaIn(unSystemd(string(m[1])))
		}
		_, err := e.Run([]string{"systemctl", "--user", "is-enabled", Name + ".timer"}, "")
		st.Active = err == nil
		if !st.Active {
			st.Notes = append(st.Notes, "unit files are present but the timer is not enabled; run install again or `uninstall`")
		}
		st.LastRun = e.systemdLastRun()
		if e.lingerOff() {
			st.Notes = append(st.Notes, "systemd linger is off: the timer stops when you log out")
		}
	case fileExists(e.plistPath()):
		st.Installed, st.Kind = true, string(Launchd)
		st.Files = []string{e.plistPath()}
		st.Config = e.existingConfig(Launchd)
		b, _ := os.ReadFile(e.plistPath())
		if m := regexp.MustCompile(`<string>([^<]*)</string>\s*<string>team</string>`).FindSubmatch(b); m != nil {
			st.Bin = xmlUnescape(string(m[1]))
		}
		if m := regexp.MustCompile(`<key>StartInterval</key>\s*<integer>(\d+)</integer>`).FindSubmatch(b); m != nil {
			n, _ := strconv.Atoi(string(m[1]))
			st.Interval = (time.Duration(n) * time.Second).String()
		}
		if m := regexp.MustCompile(`<key>PATH</key>\s*<string>([^<]*)</string>`).FindSubmatch(b); m != nil {
			st.Orca = e.orcaIn(xmlUnescape(string(m[1])))
		}
		out, err := e.Run([]string{"launchctl", "print", "gui/" + strconv.Itoa(e.UID) + "/" + LaunchdLabel}, "")
		st.Active = err == nil
		if err == nil {
			st.LastRun = launchdLastRun(string(out))
		}
	default:
		if out, err := e.Run([]string{"crontab", "-l"}, ""); err == nil {
			for _, l := range strings.Split(string(out), "\n") {
				if strings.Contains(l, cronMarker) {
					st.Installed, st.Active, st.Kind = true, true, string(Cron)
					st.Config = e.existingConfig(Cron)
					st.Interval = cronInterval(l)
					if m := regexp.MustCompile(`PATH='([^']*)'`).FindStringSubmatch(l); m != nil {
						st.Orca = e.orcaIn(m[1])
					}
					if m := regexp.MustCompile(`PATH='[^']*' '([^']*)'`).FindStringSubmatch(l); m != nil {
						st.Bin = m[1]
					}
					st.LastRun = &LastRun{}
					st.Notes = append(st.Notes, "last run: unknown (cron does not record it)")
				}
			}
		}
	}
	return st
}

func unSystemd(s string) string {
	return strings.NewReplacer(`\"`, `"`, `\\`, `\`, "%%", "%", "$$", "$").Replace(s)
}

// orcaIn finds orca on the PATH the entry carries, "" when it is not there.
func (e Env) orcaIn(path string) string {
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			continue
		}
		p := filepath.Join(d, "orca")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

func cronInterval(line string) string {
	f := strings.Fields(line)
	if len(f) < 5 {
		return ""
	}
	switch {
	case strings.HasPrefix(f[0], "*/"):
		n, _ := strconv.Atoi(strings.TrimPrefix(f[0], "*/"))
		return (time.Duration(n) * time.Minute).String()
	case f[0] == "0" && f[1] == "*":
		return time.Hour.String()
	case f[0] == "0" && strings.HasPrefix(f[1], "*/"):
		n, _ := strconv.Atoi(strings.TrimPrefix(f[1], "*/"))
		return (time.Duration(n) * time.Hour).String()
	case f[0] == "0" && f[1] == "0":
		return (24 * time.Hour).String()
	}
	return ""
}

func (e Env) systemdLastRun() *LastRun {
	out, err := e.Run([]string{"systemctl", "--user", "show", Name + ".service", "-p", "ExecMainExitTimestamp", "-p", "ExecMainStatus"}, "")
	lr := &LastRun{}
	if err != nil {
		return lr
	}
	for _, l := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok || v == "" {
			continue
		}
		switch k {
		case "ExecMainExitTimestamp":
			if t, err := time.Parse("Mon 2006-01-02 15:04:05 MST", v); err == nil && !t.IsZero() {
				lr.At = &t
			}
		case "ExecMainStatus":
			if n, err := strconv.Atoi(v); err == nil && lr.At != nil {
				lr.ExitCode = &n
			}
		}
	}
	return lr
}

var launchdExitRE = regexp.MustCompile(`last exit code = (\d+)`)

func launchdLastRun(out string) *LastRun {
	lr := &LastRun{}
	if m := launchdExitRE.FindStringSubmatch(out); m != nil {
		n, _ := strconv.Atoi(m[1])
		lr.ExitCode = &n
	}
	return lr
}

// ResolveOrca returns the orca the sweep will exec, resolved as the host does:
// delivery.orca_bin when set (it must exist), else orca on path. Neither is a
// usage refusal, so an entry that could only fail unattended is never written.
func ResolveOrca(orcaBin, path string) (string, error) {
	if orcaBin != "" {
		if fi, err := os.Stat(orcaBin); err != nil || fi.IsDir() {
			return "", usagef("delivery.orca_bin %q does not exist; fix the host config or unset it", orcaBin)
		}
		return orcaBin, nil
	}
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			continue
		}
		p := filepath.Join(d, "orca")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", usagef("orca was not found: set delivery.orca_bin in the host config or put orca on PATH before installing; the unattended sweep could not run without it")
}
