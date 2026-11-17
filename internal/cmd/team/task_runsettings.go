package team

import (
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// A task's run settings (hadron-cli#2206; cor:agt:050:01, cor:agt:060:04): the
// family, the model or tier, and the effort a launch should use. They say what
// to launch, not whom to match, and are set once at creation (no edit takes
// them, a clone copies none), so there is no clear and nothing is ever sent as
// null: a flag not given is absent, and no flag omits runSettings entirely.
//
// The CLI refuses, before any write, only the shapes the server ALWAYS
// refuses (a model with a tier, NOT_APPLICABLE as a request, a value that is
// not its token). What depends on the task's requirements or on server data
// (MODEL_OUTSIDE_FAMILY, FAMILY_EXCLUDED, TIER_BELOW_MINIMUM, a model the CLI
// does not recognise) is the server's, and surfaces as
// TASK_RUN_SETTINGS_INVALID with its reason.

// The contract (AgentTaskRunSettingsInput): a model is letters, digits, '.',
// '_' or '-', at most 128 characters, any of them first; the CLI refuses
// nothing the server's own rule allows. A family is a lowercase-or-mixed
// token of the same characters: the server normalises and judges it.
var (
	runSettingModelRE  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	runSettingFamilyRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

// taskEfforts are the generic scale a task may REQUEST: NOT_APPLICABLE is a
// report (a session says it has no effort control), never a request.
var taskEfforts = map[gen.EffortLevel]bool{
	gen.EffortLevelLow: true, gen.EffortLevelMedium: true, gen.EffortLevelHigh: true,
	gen.EffortLevelXhigh: true, gen.EffortLevelMax: true, gen.EffortLevelUnset: true,
}

// whereLead is the prefix for a message about the pair, not one field: a
// flag's prefix "--" would read as a flag name, so it is dropped there.
func whereLead(where string) string {
	if where == "--" {
		return ""
	}
	return where + ": "
}

// validateTaskRunSettings checks the values a task's run settings carry; nil
// means not given. It is shared by the flags and by a batch item's file.
func validateTaskRunSettings(where string, family, model, tier, effort *string) error {
	if family != nil && !runSettingFamilyRE.MatchString(*family) {
		return exitcode.Newf(exitcode.Usage, "%sfamily %q must be 1-64 letters, digits, '.', '_' or '-'", where, *family)
	}
	if model != nil && !runSettingModelRE.MatchString(*model) {
		return exitcode.Newf(exitcode.Usage, "%smodel %q must be 1-128 letters, digits, '.', '_' or '-'", where, *model)
	}
	// A flag value that could be read as another flag never reaches the wire.
	for name, v := range map[string]*string{"family": family, "model": model} {
		if v != nil && strings.HasPrefix(*v, "--") {
			return exitcode.Newf(exitcode.Usage, "%s%s %q looks like a flag", where, name, *v)
		}
	}
	if model != nil && tier != nil {
		return exitcode.Newf(exitcode.Usage, "%sgive a model or a tier, not both (MODEL_AND_TIER)", whereLead(where))
	}
	if tier != nil {
		switch *tier {
		case "high", "mid", "low":
		default:
			return exitcode.Newf(exitcode.Usage, "%stier %q must be high, mid or low", where, *tier)
		}
	}
	if effort != nil {
		e := gen.EffortLevel(*effort)
		switch {
		case e == gen.EffortLevelNotApplicable:
			return exitcode.Newf(exitcode.Usage, "%seffort NOT_APPLICABLE is a report, not a request; use UNSET for the family's default", where)
		case !taskEfforts[e]:
			return exitcode.Newf(exitcode.Usage, "%seffort %q must be one of LOW, MEDIUM, HIGH, XHIGH, MAX or UNSET", where, *effort)
		}
	}
	return nil
}

type taskRunSettingsFlags struct{ family, model, tier, effort string }

func (v *taskRunSettingsFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&v.family, "family", "", "run the task on this model family (e.g. claude, codex); what to launch, not whom to match (that is --family-in)")
	cmd.Flags().StringVar(&v.model, "model", "", "run the task on this exact model; not with --tier")
	cmd.Flags().StringVar(&v.tier, "tier", "", "run the task on a model tier: high, mid or low; not with --model")
	cmd.Flags().StringVar(&v.effort, "effort", "", "run the task at this effort: LOW, MEDIUM, HIGH, XHIGH, MAX or UNSET (the family's default)")
}

// build returns the runSettings to send, or nil when no flag was given.
func (v *taskRunSettingsFlags) build(cmd *cobra.Command) (*gen.AgentTaskRunSettingsInput, error) {
	var out gen.AgentTaskRunSettingsInput
	set := false
	pick := func(flag, val string) (*string, error) {
		if !cmd.Flags().Changed(flag) {
			return nil, nil
		}
		val = strings.TrimSpace(val)
		if val == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--%s needs a value", flag)
		}
		set = true
		return &val, nil
	}
	var err error
	var fam, model, tier, effort *string
	if fam, err = pick("family", v.family); err != nil {
		return nil, err
	}
	if model, err = pick("model", v.model); err != nil {
		return nil, err
	}
	if tier, err = pick("tier", v.tier); err != nil {
		return nil, err
	}
	if effort, err = pick("effort", v.effort); err != nil {
		return nil, err
	}
	if !set {
		return nil, nil
	}
	if err := validateTaskRunSettings("--", fam, model, tier, effort); err != nil {
		return nil, err
	}
	out.Family, out.Model, out.Tier = fam, model, tier
	if effort != nil {
		e := gen.EffortLevel(*effort)
		out.Effort = &e
	}
	return &out, nil
}
