package spec

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdRegister(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var check bool
	cmd := &cobra.Command{
		Use:     "register",
		Aliases: []string{"reg"},
		Short:   "Print the citation ledger computed from live nodes",
		Long: `Print the citation ledger (modules, features, rules, and next-free
numbers) derived from the live spec nodes.

The register node is treated as advisory and is never modified. With
--check, the live nodes are diffed against the register node's
hand-written ledger and any drift is reported (exit 5 if drift is found).`,
		Example: `  hadron spec register -m micromentor.org::platform-specs
  hadron spec register -m micromentor.org::platform-specs --check`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			memURN, err := memoryURNFromFlag(memory)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			resp, err := gen.Nodes(cmd.Context(), client, &memURN, nil, nil, []string{"spec"}, nil, nil, nil)
			if err != nil {
				return api.MapError(err)
			}
			var locs []string
			for _, n := range resp.Nodes {
				if n == nil {
					continue
				}
				if _, err := ParseCitation(n.Loc); err == nil {
					locs = append(locs, n.Loc)
				}
			}

			ledger := registerLedger{modules: map[string]bool{}, retired: map[string][]int{}}
			if reg, regErr := fetchRegister(cmd, client, memURN); regErr == nil && reg != nil && reg.Content != nil {
				ledger = parseLedger(*reg.Content)
			}

			dto := buildLedgerDTO(memURN, locs, ledger)
			if check {
				dto.Drift = computeDrift(locs, ledger)
			}

			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "Citation ledger — %s (derived from live nodes)\n", dto.Memory)
				for _, m := range dto.Modules {
					fmt.Fprintf(w, "\n%s  (next feature: %s)\n", m.Module, dashIfEmpty(m.NextFeature))
					for _, fe := range m.Features {
						rules := strings.Join(fe.Rules, ", ")
						if rules == "" {
							rules = "—"
						}
						fmt.Fprintf(w, "  %s:%s  rules: %s  (next rule: %s)\n", m.Module, fe.Feature, rules, dashIfEmpty(fe.NextRule))
					}
				}
				if check {
					if len(dto.Drift) == 0 {
						fmt.Fprintln(w, "\n✓ register ledger matches live nodes")
					} else {
						fmt.Fprintf(w, "\nDrift (%d):\n", len(dto.Drift))
						for _, d := range dto.Drift {
							fmt.Fprintf(w, "  - %s\n", d)
						}
					}
				}
				return nil
			}); err != nil {
				return err
			}
			if check && len(dto.Drift) > 0 {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().BoolVar(&check, "check", false, "diff the register ledger against live nodes (exit 5 on drift)")
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}

// buildLedgerDTO groups the live citation locs into modules → features →
// rules and computes the next-free number at each level.
func buildLedgerDTO(memURN string, locs []string, ledger registerLedger) ledgerDTO {
	type modAgg struct {
		features map[int]bool
		rules    map[int]map[int]bool // feature number -> rule numbers
	}
	mods := map[string]*modAgg{}
	ensure := func(m string) *modAgg {
		if mods[m] == nil {
			mods[m] = &modAgg{features: map[int]bool{}, rules: map[int]map[int]bool{}}
		}
		return mods[m]
	}

	for _, loc := range locs {
		c, err := ParseCitation(loc)
		if err != nil {
			continue
		}
		ma := ensure(c.Module)
		if c.Feature == "" {
			continue
		}
		fn, _ := strconv.Atoi(c.Feature)
		ma.features[fn] = true
		if c.Rule != "" {
			if ma.rules[fn] == nil {
				ma.rules[fn] = map[int]bool{}
			}
			rn, _ := strconv.Atoi(c.Rule)
			ma.rules[fn][rn] = true
		}
	}
	for m := range ledger.modules {
		ensure(m)
	}

	var modNames []string
	for m := range mods {
		modNames = append(modNames, m)
	}
	sort.Strings(modNames)

	dto := ledgerDTO{Memory: memURN}
	for _, m := range modNames {
		ma := mods[m]
		featNums := sortedKeys(ma.features)
		nextFeat, _ := allocateChild(Citation{Module: m}, featNums, ledger.retired[m], 0)
		md := ledgerModuleDTO{Module: m, NextFeature: nextFeat.Feature}
		for _, fn := range featNums {
			feat := fmt.Sprintf("%03d", fn)
			ruleNums := sortedKeys(ma.rules[fn])
			ruleStrs := make([]string, 0, len(ruleNums))
			for _, rn := range ruleNums {
				ruleStrs = append(ruleStrs, fmt.Sprintf("%02d", rn))
			}
			nextRule, _ := allocateChild(Citation{Module: m, Feature: feat}, ruleNums, ledger.retired[m+":"+feat], 0)
			md.Features = append(md.Features, ledgerFeatureDTO{Feature: feat, Rules: ruleStrs, NextRule: nextRule.Rule})
		}
		dto.Modules = append(dto.Modules, md)
	}
	return dto
}

// computeDrift reports disagreements between the register's hand-written
// ledger and the live nodes (module-level + retired-but-live).
func computeDrift(locs []string, ledger registerLedger) []string {
	live := map[string]bool{}
	liveModules := map[string]bool{}
	for _, loc := range locs {
		live[loc] = true
		if c, err := ParseCitation(loc); err == nil {
			liveModules[c.Module] = true
		}
	}
	var drift []string
	if len(ledger.modules) > 0 {
		for m := range liveModules {
			if !ledger.modules[m] {
				drift = append(drift, fmt.Sprintf("module %q has live nodes but is not in the register code table", m))
			}
		}
		for m := range ledger.modules {
			if !liveModules[m] {
				drift = append(drift, fmt.Sprintf("module %q is in the register code table but has no live nodes", m))
			}
		}
	}
	for key, nums := range ledger.retired {
		for _, n := range nums {
			var loc string
			if strings.Contains(key, ":") {
				loc = fmt.Sprintf("%s:%02d", key, n)
			} else {
				loc = fmt.Sprintf("%s:%03d", key, n)
			}
			if live[loc] {
				drift = append(drift, fmt.Sprintf("ledger marks %s retired but it is still live", loc))
			}
		}
	}
	sort.Strings(drift)
	return drift
}

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
