// Package healthsched renders and manages the user-level scheduler entry that
// runs `hadron team health sweep` on a host: a systemd user timer, a crontab
// line or a launchd agent. It has no command wiring and no server access; the
// scheduler calls go through an injected Runner so tests never exec systemctl,
// crontab or launchctl.
package healthsched

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Name is the one fixed entry per user (one sweep per host).
const (
	Name         = "hadron-team-health"
	LaunchdLabel = "com.hadronmemory.team-health"
	cronMarker   = "# " + Name
)

// UsageError is a refusal that maps to exit 2: bad input or no supported
// scheduler. Anything else from this package is an operational failure.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

func usagef(format string, a ...any) error { return &UsageError{fmt.Sprintf(format, a...)} }

// Spec is what an entry runs: exactly `<Bin> team health sweep --config <Config>`.
type Spec struct {
	Bin      string        // absolute path of hadron
	Config   string        // absolute path of the host config
	Interval time.Duration // whole minutes, at least one
	Path     string        // PATH the entry runs with (the installing shell's)
}

// Argv is the command the entry runs.
func (s Spec) Argv() []string {
	return []string{s.Bin, "team", "health", "sweep", "--config", s.Config}
}

// ValidatePath refuses a path that cannot be quoted safely in every scheduler.
func ValidatePath(what, p string) error {
	if p == "" || !strings.HasPrefix(p, "/") {
		return usagef("%s must be an absolute path, got %q", what, p)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return usagef("%s contains a control character (%q); refusing to write it into a scheduler entry", what, p)
		}
	}
	return nil
}

// ValidateInterval requires whole minutes, at least one.
func ValidateInterval(d time.Duration) error {
	if d < time.Minute || d%time.Minute != 0 {
		return usagef("--interval must be a whole number of minutes, at least 1m (for example 5m or 1h), got %s", d)
	}
	return nil
}

// cronSchedule renders the five-field schedule for d, or refuses an interval
// cron cannot express evenly (it has no "every 7 minutes across the hour").
func cronSchedule(d time.Duration) (string, error) {
	m := int(d / time.Minute)
	switch {
	case m < 60 && 60%m == 0:
		return fmt.Sprintf("*/%d * * * *", m), nil
	case m%60 == 0 && 24%(m/60) == 0:
		if h := m / 60; h == 1 {
			return "0 * * * *", nil
		} else if h == 24 {
			return "0 0 * * *", nil
		} else {
			return fmt.Sprintf("0 */%d * * *", h), nil
		}
	}
	return "", usagef("cron cannot run every %s evenly: use a divisor of 60 minutes (1, 2, 3, 4, 5, 6, 10, 12, 15, 20, 30), or whole hours that divide 24", d)
}

// systemdQuote quotes one ExecStart word: backslash and quote escaped, and the
// specifier and variable introducers (% and $) doubled.
func systemdQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$")
	return `"` + r.Replace(s) + `"`
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func headerComment(s Spec) string { return "# " + Name + ": config=" + strconv.Quote(s.Config) }

// SystemdService is the unit the timer starts.
func SystemdService(s Spec) string {
	words := make([]string, 0, 6)
	for _, w := range s.Argv() {
		words = append(words, systemdQuote(w))
	}
	return headerComment(s) + `
[Unit]
Description=Hadron team health sweep

[Service]
Type=oneshot
Environment="PATH=` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s.Path) + `"
ExecStart=` + strings.Join(words, " ") + "\n"
}

// SystemdTimer fires the service every interval.
func SystemdTimer(s Spec) string {
	return headerComment(s) + `
[Unit]
Description=Run the Hadron team health sweep every ` + strconv.Itoa(int(s.Interval/time.Minute)) + ` minutes

[Timer]
OnBootSec=1min
OnUnitActiveSec=` + strconv.Itoa(int(s.Interval/time.Minute)) + `min
Unit=` + Name + `.service

[Install]
WantedBy=timers.target
`
}

// CronLine is the one crontab line; the trailing marker is how it is found
// again, and a bare % (a newline to cron) is escaped.
var ownedCronRE = regexp.MustCompile(`^\S+ \S+ \S+ \S+ \S+ PATH='[^']*' .*'team' 'health' 'sweep' .* ` + cronMarker + ` config="(?:[^"\\]|\\.)*"\s*$`)

// IsOwnedCronLine is true only for the line CronLine writes: the schedule,
// the sweep command and the trailing marker with its config. A comment that
// merely mentions the name is somebody else's.
func IsOwnedCronLine(l string) bool { return ownedCronRE.MatchString(l) }

func CronLine(s Spec) (string, error) {
	sched, err := cronSchedule(s.Interval)
	if err != nil {
		return "", err
	}
	words := make([]string, 0, 6)
	for _, w := range s.Argv() {
		words = append(words, shellQuote(w))
	}
	cmd := "PATH=" + shellQuote(s.Path) + " " + strings.Join(words, " ")
	return sched + " " + strings.ReplaceAll(cmd+" "+cronMarker+" config="+strconv.Quote(s.Config), "%", `\%`), nil
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// Plist is the launchd agent.
func Plist(s Spec) string {
	var args strings.Builder
	for _, w := range s.Argv() {
		args.WriteString("\t\t<string>" + xmlEscape(w) + "</string>\n")
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + LaunchdLabel + `</string>
	<key>ProgramArguments</key>
	<array>
` + args.String() + `	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>` + xmlEscape(s.Path) + `</string>
	</dict>
	<key>StartInterval</key>
	<integer>` + strconv.Itoa(int(s.Interval/time.Second)) + `</integer>
</dict>
</plist>
`
}
