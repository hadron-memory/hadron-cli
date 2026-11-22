// Package hostyaml is TEST SUPPORT for host configuration fixtures (cli#2914). Every worker launches
// through the supervised (Orca) path, which needs a launcher_profile, an absolute disk_tmp, an account
// profile and orca_launch per worker; the older fixtures were written for the removed exec path as
// `launch: [program, ...]`. That line is kept in them as shorthand for "the worker whose launcher
// program is <program>", and Supervised renders it into what the supervised launch needs. Text without
// the shorthand comes back unchanged, so it is safe to apply to any file a test writes.
//
// Only tests import it; it is not part of the CLI.
package hostyaml

import (
	"fmt"
	"regexp"
	"strings"
)

// Supervised renders the `launch: [program, ...]` worker shorthand into the profile, disk_tmp, account
// profile and orca_launch the supervised launch needs. Everything else in the text is untouched.
var (
	fixtureWorkerLine2914 = regexp.MustCompile(`^  (\S+):\s*$`)
	fixtureFamilyLine2914 = regexp.MustCompile(`^    family:\s*(\S+)\s*$`)
	fixtureLaunchLine2914 = regexp.MustCompile(`^    launch:\s*\[(.*)\]\s*$`)
)

func Supervised(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	type account struct{ name, bin string }
	var accounts []account
	var ref, family string
	// blockHas reports whether the worker block that CONTAINS line i (before and after it) has a line
	// starting with prefix.
	blockHas := func(i int, prefix string) bool {
		start := i
		for start > 0 && !fixtureWorkerLine2914.MatchString(lines[start]) {
			start--
		}
		for j := start + 1; j < len(lines); j++ {
			l := lines[j]
			if fixtureWorkerLine2914.MatchString(l) || (len(l) > 0 && l[0] != ' ' && l[0] != '#') {
				return false
			}
			if strings.HasPrefix(l, prefix) {
				return true
			}
		}
		return false
	}
	inWorkers := false
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "workers:"):
			inWorkers = true
		case len(l) > 0 && l[0] != ' ' && l[0] != '#':
			inWorkers = false
		}
		if inWorkers {
			if m := fixtureWorkerLine2914.FindStringSubmatch(l); m != nil {
				ref, family = m[1], ""
			}
			if m := fixtureFamilyLine2914.FindStringSubmatch(l); m != nil {
				family = m[1]
			}
			if m := fixtureLaunchLine2914.FindStringSubmatch(l); m != nil && ref != "" {
				if blockHas(i, "    launcher_profile:") || blockHas(i, "    launch_via:") {
					continue // the worker states its own launch: the shorthand is just dropped
				}
				bin := strings.TrimSpace(strings.Split(m[1], ",")[0])
				name := "acct-" + ref
				accounts = append(accounts, account{name, bin})
				out = append(out, fmt.Sprintf("    launcher_profile: {family: %s, mode: long, account: %s}", family, name))
				if !blockHas(i, "    disk_tmp:") {
					out = append(out, "    disk_tmp: /tmp/supervised2914/"+ref)
				}
				continue
			}
		}
		out = append(out, l)
	}
	res := strings.Join(out, "\n")
	if len(accounts) == 0 {
		return res
	}
	var block strings.Builder
	for _, a := range accounts {
		fmt.Fprintf(&block, "  %s: {bin: %s}\n", a.name, a.bin)
	}
	if i := strings.Index(res, "\naccount_profiles:\n"); i >= 0 {
		at := i + len("\naccount_profiles:\n")
		res = res[:at] + block.String() + res[at:]
	} else {
		res = strings.TrimRight(res, "\n") + "\naccount_profiles:\n" + block.String()
	}
	if !strings.Contains(res, "\norca_launch:") && !strings.HasPrefix(res, "orca_launch:") {
		res += "orca_launch:\n  orca_bin: /usr/bin/orca\n"
	}
	return res
}
