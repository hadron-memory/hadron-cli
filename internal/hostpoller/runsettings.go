package hostpoller

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Run settings (hadron-cli#2199 C8, companion to hadron-server#4295 S14): a
// worker.start names the family to launch and, optionally, a model or a tier
// and an effort. They override the worker's launcher profile for this run
// only; nothing here rewrites the profile.

// Effort values a start carries and a bind reports (hadron-server EffortLevel).
const (
	EffortUnset         = "UNSET"
	EffortNotApplicable = "NOT_APPLICABLE"
)

// RunSettings is what a worker.start's args say about the run.
type RunSettings struct {
	Family string
	Model  string
	Tier   string
	Effort string
}

// RunSettingsFrom reads the run settings off a worker.start's args.
func RunSettingsFrom(op Operation) RunSettings {
	return RunSettings{
		Family: argString(op, "family"),
		Model:  argString(op, "model"),
		Tier:   argString(op, "tier"),
		Effort: argString(op, "effort"),
	}
}

// requested is whether the start asks for anything beyond the worker's own
// profile: a model, a tier or an effort.
func (r RunSettings) requested() bool { return r.Model != "" || r.Tier != "" || r.Effort != "" }

// Validate refuses the shapes the server never sends: a model with a tier, a
// tier outside high|mid|low, an effort of NOT_APPLICABLE (a report, never a
// request), and a model or effort that is not a plain token.
func (r RunSettings) Validate() error {
	switch {
	case r.Model != "" && r.Tier != "":
		return fmt.Errorf("malformed start: it names both a model (%q) and a tier (%q)", r.Model, r.Tier)
	case r.Tier != "" && tierModels["claude"][r.Tier] == "":
		return fmt.Errorf("malformed start: tier %q is not high, mid or low", r.Tier)
	case r.Effort == EffortNotApplicable:
		return fmt.Errorf("malformed start: effort %s is a report, not a request", EffortNotApplicable)
	}
	for name, v := range map[string]string{"model": r.Model, "effort": r.Effort} {
		if v != "" && !profileTokenRE.MatchString(v) {
			return fmt.Errorf("malformed start: %s %q is not a plain token", name, v)
		}
	}
	return nil
}

// tierModels is the tier -> model table per launcher family (lane default,
// hadron-cli#2199 Ruling): Claude Code's own aliases, which follow the newest
// model of each tier, so the table needs no edit per release. A family absent
// here has no table: a tier launches with the account's default model.
var tierModels = map[string]map[string]string{
	"claude": {"high": "opus", "mid": "sonnet", "low": "haiku"},
}

// codexEfforts maps the generic scale to the values the Codex CLI accepts for
// model_reasoning_effort (confirmed in codex-cli 0.169.0, which has xhigh).
var codexEfforts = map[string]string{"LOW": "low", "MEDIUM": "medium", "HIGH": "high", "XHIGH": "xhigh", "MAX": "xhigh"}

// genericEfforts are the generic scale's levels a bind can report.
var genericEfforts = map[string]bool{"LOW": true, "MEDIUM": true, "HIGH": true, "XHIGH": true, "MAX": true, EffortUnset: true}

// Bind is what the launched agent reports when it binds its worker session.
// The zero Bind adds nothing to the first prompt.
type Bind struct {
	Family string
	Model  string
	Effort string
}

// Resolved is a start's launch settings: the profile to build the argv from,
// what the bind reports, and a note for the operation's log when something
// asked for was not mapped.
type Resolved struct {
	Profile LauncherProfile
	Bind    Bind
	Note    string
}

// accountLaunches reports whether the worker's account has a launch template
// for fam. Capability is explicit, never inferred from a launcher's name or
// home: the account's own families list, else just the worker's profile
// family. A start naming anything else is refused.
func accountLaunches(fam, profileFam string, a AccountProfile) bool {
	if !claudeFamily[fam] && fam != "codex" {
		return false // no launch template for this family at all
	}
	if len(a.Families) == 0 {
		// Unlisted: the worker's own profile family, and nothing that
		// contradicts it: codex_home is optional for a Codex account, so its
		// absence refuses nothing, but a Claude-family profile on an account
		// that looks like a Codex one (a Codex home, or a codex* launcher) is
		// a misconfiguration and is refused, never launched.
		codexLooking := a.CodexHome != "" || strings.HasPrefix(filepath.Base(a.Bin), "codex")
		// A Codex profile needs a Codex-looking account too (a Claude launcher
		// script cannot run it); the home is just optional.
		return fam == CanonicalFamily(profileFam) && (fam == "codex") == codexLooking
	}
	for _, f := range a.Families {
		if CanonicalFamily(f) == fam {
			return true
		}
	}
	return false
}

// StartSettingsAppliedEnvVar is the launched agent's environment variable
// that states a start's run settings were NOT applied: the exec path runs the
// worker's own fixed command, so the model, tier and effort a start names
// cannot be applied (Wolfgang, #110836). It is set to "false" only then, and
// is absent otherwise. The agent's own `session start` bind is where the
// server would otherwise inherit those settings from the start's args, so the
// statement rides the environment beside the launch grant (hadron-server#4306,
// carrier (a)); the report has no field for a SUCCEEDED statement.
const StartSettingsAppliedEnvVar = "HADRON_START_SETTINGS_APPLIED"

// ResolveRun applies a start's run settings over the worker's launcher
// profile. A start that asks for nothing returns the profile unchanged and a
// zero Bind, so its argv and first prompt are exactly what they were.
func ResolveRun(prof LauncherProfile, acct AccountProfile, rs RunSettings, accountName string) (Resolved, error) {
	if err := rs.Validate(); err != nil {
		return Resolved{}, err
	}
	out := Resolved{Profile: prof}
	fam := CanonicalFamily(prof.Family)
	switchedFamily := false
	if rs.Family != "" {
		// Checked for every start that names a family, switched or not: the
		// account must be able to launch exactly what the start asks for.
		fam = CanonicalFamily(rs.Family)
		if !familyRE.MatchString(fam) {
			return Resolved{}, fmt.Errorf("malformed start: family %q is not a family token", rs.Family)
		}
		if (!claudeFamily[fam] && fam != "codex") || !accountLaunches(fam, prof.Family, acct) {
			return Resolved{}, fmt.Errorf("this host cannot launch family %q on account %q (no launch template for it there); nothing was launched", fam, accountName)
		}
		if !SameFamily(rs.Family, prof.Family) {
			// The profile's model and effort belong to its own family.
			out.Profile.Family, out.Profile.Model, out.Profile.Effort = fam, "", ""
			switchedFamily = true
		}
	}
	if !switchedFamily && !rs.requested() {
		return out, nil
	}
	switch {
	case rs.Model != "":
		out.Profile.Model = rs.Model
	case rs.Tier != "":
		if m := tierModels[fam][rs.Tier]; m != "" {
			out.Profile.Model = m
		} else {
			out.Profile.Model = ""
			out.Note = fmt.Sprintf("tier %s is not mapped for family %s; the account's default model runs", rs.Tier, fam)
		}
	}
	applied := EffortNotApplicable
	switch {
	case claudeFamily[fam]:
		out.Profile.Effort = "" // no fixed flag carries an effort: launched without, reported NOT_APPLICABLE
	case rs.Effort != "":
		if v, ok := codexEfforts[rs.Effort]; ok {
			out.Profile.Effort, applied = v, rs.Effort
		} else {
			// UNSET: no flag. Anything else is the profile's free token, passed as today.
			out.Profile.Effort, applied = "", ""
			if rs.Effort == EffortUnset {
				applied = EffortUnset
			} else {
				out.Profile.Effort = rs.Effort
			}
		}
	default:
		applied = ""
		if up := strings.ToUpper(out.Profile.Effort); codexEfforts[up] != "" {
			out.Profile.Effort, applied = codexEfforts[up], up
		}
	}
	if applied != EffortNotApplicable && !genericEfforts[applied] {
		applied = ""
	}
	out.Bind = Bind{Family: fam, Model: out.Profile.Model, Effort: applied}
	return out, nil
}
