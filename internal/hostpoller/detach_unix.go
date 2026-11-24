//go:build !windows

package hostpoller

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

func realPath(p string) (string, error) { return filepath.EvalSymlinks(p) }

// keyFileTooOpen: any group or other permission bit.
func keyFileTooOpen(info os.FileInfo) bool { return info.Mode().Perm()&0o077 != 0 }

// processAlive: signal 0 probes without delivering; EPERM still means a
// process holds the pid.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// occupied scans /proc/<pid>/cwd for a process working inside path and names
// it by pid and argv (#1474: an operator must be able to tell WHICH process
// holds the tree — the comm name alone cannot). Only processes this user may
// inspect are seen, which covers agents the holder's user runs. Without /proc
// (macOS) it reports none.
func occupied(path string) (string, error) {
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir("/proc")
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	self := os.Getpid()
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		cwd, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if err != nil {
			continue // gone, or not ours to inspect
		}
		if cwd == root || strings.HasPrefix(cwd, root+string(os.PathSeparator)) {
			return fmt.Sprintf("pid %d, %s", pid, processArgv(e.Name())), nil
		}
	}
	return "", nil
}

// processArgv names a process by its command line (/proc/<pid>/cmdline,
// NUL-separated), falling back to comm for a process with no command line
// (a kernel thread) or an unreadable one. The line is rendered by
// renderArgv — quoted, redacted and bounded — because it leaves the host in
// a refusal reason and the logs (#1474).
func processArgv(dir string) string {
	raw, err := os.ReadFile(filepath.Join("/proc", dir, "cmdline"))
	if fields, ok := argvFromCmdline(raw); err == nil && ok {
		return renderArgv(fields)
	}
	comm, _ := os.ReadFile(filepath.Join("/proc", dir, "comm"))
	return strings.TrimSpace(string(comm))
}

// argvFromCmdline splits a /proc/<pid>/cmdline read: NUL-separated, ending
// in ONE terminator NUL. A further trailing NUL is an empty LAST argument,
// which is kept — an empty argv slot is not nothing (#1474). An empty read
// — kernel threads, zombies — is not an argv at all: false, so comm answers
// instead (Split("") returns [""], which would render as a bare quoted
// empty argument and starve the comm fallback). A setproctitle-padded
// cmdline carries runs of trailing NULs beyond that, which read as empty
// arguments; a RUN of trailing empties is padding and collapses to one
// HERE — at the /proc boundary where padding happens — so the display bound
// is not spent on it and renderArgv renders the slots it is given (the
// collapse used to live in renderArgv and ate genuine slots). Residual,
// accepted: in /proc a genuine argv with two or more trailing empty slots
// is byte-identical to padding, so it collapses with it and one empty slot
// renders.
func argvFromCmdline(raw []byte) ([]string, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	fields := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	for len(fields) > 1 && fields[len(fields)-1] == "" && fields[len(fields)-2] == "" {
		fields = fields[:len(fields)-1]
	}
	return fields, true
}

// argvDisplayLimit bounds how much of an occupant's command line leaves the
// host, in bytes. The pid, the executable and a short prefix of the rest are
// what an operator needs to tell WHICH process holds the tree (#1474); the
// rest of an arbitrary command line is not this host's to send or log.
const argvDisplayLimit = 200

// credentialName matches the NAME of an assignment (NAME=value,
// --flag=value) whose value looks like a credential. Over-matching is safe —
// the name alone still identifies the process; the value never leaves.
var credentialName = regexp.MustCompile(`(?i)(token|secret|password|passwd|passphrase|credential|api[-_]?key|private[-_]?key|session|authorization|cookie)`)

// credentialHeader matches an argument that IS a header whose name says it
// carries a credential: any header name containing auth, token, key, secret
// or cookie ("Authorization: Bearer …", "X-API-Key: …", "Cookie: …").
// Invariant: a match means the VALUE never leaves; whether the name does is
// decided by renderHeaderShaped. Over-matching is safe — the name alone
// still identifies the header (#1474).
var credentialHeader = regexp.MustCompile(`(?i)^[\w-]*(auth|token|key|secret|cookie)[\w-]*\s*:`)

// safeHeaderNames is the allowlist of header names that are safe to SHOW
// (the name alone is not secret material): the part before ':' in a
// header-shaped argument survives rendering only when it is one of these
// names, case-insensitively. Anything else name-shaped that credentialHeader
// accepts is presumed to be credential material — the user half of a
// user:pass value, a token before a realm — and must not leave.
// Over-redaction is the safe direction: dropping a name loses only which
// header it was, never which process held the tree.
var safeHeaderNames = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
	"x-api-key":           true,
	"x-auth-token":        true,
	"x-session-token":     true,
}

// headerNameAllowed reports whether the pre-colon material of a
// header-shaped argument is an allowlisted header name (case-insensitive,
// surrounding whitespace ignored: "Authorization :" is the same name). Only
// curl's -H glues its value (-HAuthorization: …), so only a leading "-H" is
// stripped; any other flag letter is not a header flag, and its glued value
// (mysql -pcookie:rest, curl -uauthorization:pass) is credential material
// that must not survive as a "name". Everything else — token_X:pass on an
// unknown flag, in an env assignment, or as a bare argument — renders fully
// redacted.
func headerNameAllowed(name string) bool {
	if rest, ok := strings.CutPrefix(name, "-H"); ok {
		name = rest
	}
	return safeHeaderNames[strings.ToLower(strings.TrimSpace(name))]
}

// renderHeaderShaped renders an argument (prefix "") or the value of an
// assignment (prefix "NAME=") whose text credentialHeader matched: the
// pre-colon name survives only when headerNameAllowed accepts it, and
// otherwise the whole value is <redacted>.
func renderHeaderShaped(prefix, text string) string {
	name, _, _ := strings.Cut(text, ":")
	if !headerNameAllowed(name) {
		return prefix + "<redacted>"
	}
	return prefix + strings.TrimSpace(name) + ": <redacted>"
}

// credentialFlagValue says how a binary's short flag carries a credential:
// as the NEXT argument (curl -u user:pass), attached to the flag itself
// (mysql -pSECRET), or both (sshpass -p secret / sshpass -psecret).
type credentialFlagValue struct {
	next     bool
	attached bool
}

// credentialFlagBinaries is the per-binary table of flags that carry a
// credential, because the same letters mean different things in different
// tools: curl's -u/--user and -U/--proxy-user take user:pass as the next
// argument or attached (-uuser:pass, --user=u:p); curl's --pass,
// --proxy-pass, --oauth2-bearer and --proxy-oauth2-bearer carry a secret
// next or attached, and its -E/--cert and --proxy-cert carry
// keyfile:password the same two ways; header values (-H/--header,
// --proxy-header) need no table entry — the argument itself is a header
// line, matched by credentialHeader whatever flag carried it (the
// '='-attached long form --header=Authorization: … through the value-side
// match in renderArgv's default branch, which fires only when no armed
// table claims the flag — the regex cannot cross '=', and a table flag's
// header-shaped value is credential material that keeps its full
// redaction); sshpass's -p takes the password next or attached; mysql's -p
// takes the password ATTACHED only — `-p db` names a database and prompts
// instead. ssh's -p is a port and ssh takes no password on the command
// line, so it is not in any table; python -u (unbuffered) and find -print
// stay readable for the same reason (#1474). The tables ACCUMULATE at every
// argv entry whose basename is in the map, not only argv[0], because
// wrapper launchers run in front (sudo curl -u …, env curl -u …, timeout 30
// sshpass -p …) and argv[0]-gating leaked the credential for every wrapped
// form — and a later table must never DISARM an earlier one: `curl -o mysql
// -u u:p` would switch to mysql's table and leak curl's -u value, and the
// replacement is not knowable from the argv alone (an output FILE named
// mysql is not the mysql binary), so the union of every armed table
// applies, and where two tables spell the same flag differently the
// more-redacting spec wins (next|attached beats either alone: over-
// redaction is the safe direction under this contract) (#1474). Residuals,
// deliberate: a command line hidden inside ONE argument (sh -c "curl -u …")
// is not tokenised here — it renders as one quoted argument, and only its
// URL-shaped userinfo is stripped (stripURLUserinfo) — bundled short flags
// (curl -su user:pass) are not unclustered into their letters, and curl's
// unambiguous long- option abbreviations (--proxy-use for --proxy-user) are
// not expanded: only exact spellings and their '='-attached values are
// matched.
var credentialFlagBinaries = map[string]map[string]credentialFlagValue{
	"curl": {
		"-u": {next: true, attached: true}, "--user": {next: true, attached: true},
		"-U": {next: true, attached: true}, "--proxy-user": {next: true, attached: true},
		"--pass": {next: true, attached: true}, "--proxy-pass": {next: true, attached: true},
		"--oauth2-bearer":       {next: true, attached: true},
		"--proxy-oauth2-bearer": {next: true, attached: true},
		"-E":                    {next: true, attached: true}, "--cert": {next: true, attached: true},
		"--proxy-cert": {next: true, attached: true},
	},
	"sshpass": {"-p": {next: true, attached: true}},
	"mysql":   {"-p": {attached: true}},
}

// matchCredentialFlag finds the table's flag that f carries. A SHORT flag
// carries its value attached with no separator (curl -uuser:pass), so a
// bare prefix matches it; a LONG flag carries one only after '=' (--user=u:p),
// so it matches exactly or '='-delimited — a bare prefix (--user-agent) is
// a different flag and must stay readable. The longest matching flag wins,
// so a table holding a flag and its longer spelling cannot mis-split.
func matchCredentialFlag(table map[string]credentialFlagValue, f string) (credentialFlagValue, string) {
	var best credentialFlagValue
	var bestFlag string
	for flag, spec := range table {
		var matched bool
		switch {
		case f == flag:
			matched = true
		case strings.HasPrefix(flag, "--"):
			matched = strings.HasPrefix(f, flag+"=")
		default:
			matched = len(f) > len(flag) && strings.HasPrefix(f, flag)
		}
		if matched && len(flag) > len(bestFlag) {
			best, bestFlag = spec, flag
		}
	}
	return best, bestFlag
}

// stripURLUserinfo removes user:password@ (or user@) from every URL-shaped
// occurrence in an argument: https://u:p@host/path renders as
// https://host/path, and a shell string carrying two URLs loses both
// userinfos (#1474). The cut for each URL is the LAST @ before the
// authority's end — the first /, ? or # after the scheme, or any whitespace
// or quote character, none of which a URL's authority can carry raw — so an
// unencoded @ inside a password leaves none of its tail behind, and a
// URL-shaped prefix running into prose (`https://h1 mail user@example.com`)
// ends its authority at the space instead of cutting the prose at a later
// @. Residual, deliberate: a password containing an unencoded /, ? or # is
// indistinguishable from the path boundary and is not stripped.
// git@host:path and bare addresses carry no scheme separator and pass
// through.
func stripURLUserinfo(arg string) string {
	const authorityEnd = "/?# \t\r\n\v\f\"'`"
	var out strings.Builder
	rest := arg
	for {
		i := strings.Index(rest, "://")
		if i < 0 {
			out.WriteString(rest)
			return out.String()
		}
		out.WriteString(rest[:i+3])
		after := rest[i+3:]
		end := len(after)
		if j := strings.IndexAny(after, authorityEnd); j >= 0 {
			end = j
		}
		if at := strings.LastIndexByte(after[:end], '@'); at >= 0 {
			after = after[at+1:]
			// after now starts at the host; its authority ends at the same
			// boundary — the first character of the same set in the
			// remainder.
			if j := strings.IndexAny(after, authorityEnd); j >= 0 {
				end = j
			} else {
				end = len(after)
			}
		}
		out.WriteString(after[:end])
		rest = after[end:]
	}
}

// renderArgv renders a command line for the occupied-tree refusal: an
// argument that would not survive a re-split on spaces is %q-quoted, a
// credential-shaped value keeps only its name, and the whole line is
// bounded (#1474: name WHICH process holds the tree, never its secrets, in
// the reason and in the logs).
func renderArgv(fields []string) string {
	out := make([]string, 0, len(fields))
	credTable := map[string]credentialFlagValue{}
	credFrom := -1
	redactNext := false
	for i, f := range fields {
		f = stripURLUserinfo(f)
		// Arm at EVERY credential-flag binary on the line, ACCUMULATING:
		// the union of the armed tables applies to everything after the
		// first one, and a later binary never disarms an earlier one's
		// flags — whether a later table-binary spelling is the binary or
		// an argument of the last one (`curl -o mysql -u u:p`) is not
		// knowable from argv alone, and the union redacts both readings.
		if table, ok := credentialFlagBinaries[filepath.Base(f)]; ok {
			if credFrom < 0 {
				credFrom = i
			}
			for flag, spec := range table {
				cur := credTable[flag]
				// More-redacting wins: next|attached beats either alone.
				cur.next = cur.next || spec.next
				cur.attached = cur.attached || spec.attached
				credTable[flag] = cur
			}
		}
		switch {
		case redactNext:
			// The previous argument was a bare credential flag (--token,
			// -u, --user); this one is its value.
			out = append(out, "<redacted>")
			redactNext = false
		case credentialHeader.MatchString(f):
			out = append(out, renderHeaderShaped("", f))
		default:
			name, value, hasValue := strings.Cut(f, "=")
			if hasValue && credentialName.MatchString(name) {
				out = append(out, name+"=<redacted>")
				continue
			}
			if strings.HasPrefix(f, "-") {
				// Only arguments AFTER the first credential-flag binary
				// carry its flags; a wrapper's own arguments never do.
				// The table match runs for '='-attached long spellings too
				// (--user=u:p) — the '='-cut above only governed the
				// credential-NAME assignment form.
				if credFrom >= 0 && i > credFrom {
					if spec, flag := matchCredentialFlag(credTable, f); flag != "" {
						switch {
						case spec.next && len(f) == len(flag):
							// The bare flag: its value is the next argument.
							out = append(out, quoteArg(f))
							redactNext = true
							continue
						case spec.attached && len(f) > len(flag):
							// An attached value (mysql -pSECRET, curl
							// -uuser:pass, curl --user=u:p): the flag stays,
							// the value never leaves. Checked BEFORE the
							// credential-name branch — the value attached to
							// a credential flag can itself contain "secret".
							// A long flag's separator '=' stays with the flag.
							rest := "<redacted>"
							if strings.HasPrefix(f[len(flag):], "=") {
								rest = "=<redacted>"
							}
							out = append(out, flag+rest)
							continue
						}
						// A bare attached-only flag (mysql -p) carries no
						// value here and none next: readable as-is.
						out = append(out, quoteArg(f))
						continue
					}
				}
				// A '='-attached header value (--header=Authorization: …,
				// --proxy-header=X-Auth-Token: …) on a flag no credential-flag
				// table claims: the header regex cannot cross '=', so the whole
				// argument never matched it above, and header flags
				// deliberately carry no table entry — the VALUE is the
				// header line, and only its ALLOWLISTED name leaves
				// (renderHeaderShaped). This runs AFTER
				// the table: a header-SHAPED value on a known credential
				// flag (--user=token:…, --cert=key:…) is credential
				// material, not a header line, and keeps the table's FULL
				// redaction — its prefix is not a header name and must
				// not survive.
				if hasValue && credentialHeader.MatchString(value) {
					out = append(out, renderHeaderShaped(name+"=", value))
					continue
				}
				if !hasValue && credentialName.MatchString(f) {
					out = append(out, quoteArg(f))
					redactNext = true
					continue
				}
			} else if hasValue && credentialHeader.MatchString(value) {
				// The same value-side header match, for a NON-dash
				// argument: an assignment whose value is a header line
				// (env HDR=Authorization: …, docker -e AUTH_HDR=…). The
				// whole-argument header case above cannot match it (the
				// NAME= prefix is not \w), and the dash-branch copy does
				// not run — without this, the assignment renders verbatim
				// (it must run for a non-dash argument as well as the
				// dash branch above). Only the header's ALLOWLISTED name
				// leaves (renderHeaderShaped).
				out = append(out, renderHeaderShaped(name+"=", value))
				continue
			}
			out = append(out, quoteArg(f))
		}
	}
	// Known tokens are also redacted on the RENDERED pieces (name pieces
	// that bypass quoteArg), never before the
	// classification above: a "<redacted>" inside a header-shaped name would
	// break the anchored credentialHeader/credentialName matches and let the
	// value fall through verbatim. Applied last, the output can only lose
	// characters relative to what the rules above rendered.
	for i := range out {
		out[i] = redactKnownTokens(out[i])
	}
	line := strings.Join(out, " ")
	if len(line) > argvDisplayLimit {
		// Never slice a rune in half: back the cut off to a rune boundary.
		// Every argument is valid UTF-8 by now (quoteArg escapes invalid
		// bytes), so this back-off is bounded by one rune's length.
		cut := argvDisplayLimit
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		line = line[:cut] + "…"
	}
	return line
}

// quoteArg keeps an argument unambiguous in the joined line: only arguments
// that would not survive being re-split on spaces — or that carry invalid
// UTF-8, which would otherwise both render raw and make the byte-bound
// truncation back off past them — are quoted, so a plain `sleep 30` stays
// readable while `sh -c "sleep 30 & wait"` keeps its shape and an invalid
// byte arrives as an escape, never raw.
func quoteArg(arg string) string {
	// Known tokens go BEFORE quoting: strconv.Quote turns a raw newline or
	// tab into \n / \t, whose letter would otherwise hide the token's boundary.
	arg = redactKnownTokens(arg)
	if arg == "" || !utf8.ValidString(arg) || strings.IndexFunc(arg, func(r rune) bool {
		return unicode.IsSpace(r) || r == '"' || r == '\\' || !unicode.IsPrint(r)
	}) >= 0 {
		return strconv.Quote(arg)
	}
	return arg
}

// groupPollInterval is how often the worker.end stop probes a group it is
// waiting on (endstop_linux.go). It is far below any sane grace, so the
// probe cost bounds the latency of noticing the group emptied.
var groupPollInterval = 20 * time.Millisecond

// postKillWait bounds the wait after SIGKILL. It is deliberately NOT
// TerminateGrace again: SIGKILL cannot be caught or ignored, so only an
// unkillable (D-state) member can outlive it, and that is reported rather
// than waited on twice.
var postKillWait = 2 * time.Second
