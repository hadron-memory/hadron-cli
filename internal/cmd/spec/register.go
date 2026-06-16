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
// rules and computes the next-free number at each level. Modules are keyed by
// their full root loc, so a product corpus keys "cli:cha" (not "cha") and two
// products can reuse a module code without colliding. A bare product root has
// no numeric ledger of its own and is skipped.
func buildLedgerDTO(memURN string, locs []string, ledger registerLedger) ledgerDTO {
	type modAgg struct {
		cit      Citation // the module-root citation (carries the product)
		features map[int]bool
		rules    map[int]map[int]bool // feature number -> rule numbers
	}
	mods := map[string]*modAgg{} // key: module-root loc ("msg" or "cli:cha")
	ensure := func(c Citation) *modAgg {
		key := c.Format()
		if mods[key] == nil {
			mods[key] = &modAgg{cit: c, features: map[int]bool{}, rules: map[int]map[int]bool{}}
		}
		return mods[key]
	}

	// A top-level code is a product if any loc roots under it (code:<alpha>…);
	// such a bare code is a product root, not a flat module.
	productCodes := map[string]bool{}
	for _, loc := range locs {
		if c, err := ParseCitation(loc); err == nil && c.Product != "" {
			productCodes[c.Product] = true
		}
	}

	for _, loc := range locs {
		c, err := ParseCitation(loc)
		if err != nil || c.Module == "" {
			continue
		}
		if c.Product == "" && productCodes[c.Module] {
			continue // a bare product root parsed as a flat module — skip
		}
		if c.Product != "" && c.Module == productContractCode {
			continue // the product contract (<product>:gen) is not a module
		}
		ma := ensure(Citation{Product: c.Product, Module: c.Module})
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
		ensure(Citation{Module: m})
	}

	var modKeys []string
	for k := range mods {
		modKeys = append(modKeys, k)
	}
	sort.Strings(modKeys)

	dto := ledgerDTO{Memory: memURN}
	for _, key := range modKeys {
		ma := mods[key]
		featNums := sortedKeys(ma.features)
		nextFeat, _ := allocateChild(ma.cit, featNums, ledger.retired[key], 0)
		md := ledgerModuleDTO{Module: key, NextFeature: nextFeat.Feature}
		for _, fn := range featNums {
			feat := fmt.Sprintf("%03d", fn)
			ruleNums := sortedKeys(ma.rules[fn])
			ruleStrs := make([]string, 0, len(ruleNums))
			for _, rn := range ruleNums {
				ruleStrs = append(ruleStrs, fmt.Sprintf("%02d", rn))
			}
			featCit := ma.cit
			featCit.Feature = feat
			nextRule, _ := allocateChild(featCit, ruleNums, ledger.retired[key+":"+feat], 0)
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
