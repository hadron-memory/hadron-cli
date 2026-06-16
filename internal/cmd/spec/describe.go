package spec

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// describeCounts is the per-tier node tally in `spec describe` output.
type describeCounts struct {
	Products  int `json:"products"`
	Modules   int `json:"modules"`
	Features  int `json:"features"`
	Rules     int `json:"rules"`
	Flows     int `json:"flows"`
	Contracts int `json:"contracts"`
}

// describeContracts names the reserved general-provisions contract slot at
// each tier (Product is "" in a flat corpus).
type describeContracts struct {
	Product string `json:"product"`
	Module  string `json:"module"`
	Feature string `json:"feature"`
}

// describeDTO is the stable --json shape for `spec describe`.
type describeDTO struct {
	Memory    string            `json:"memory"`
	Scheme    string            `json:"scheme"` // flat | product | mixed | empty
	Source    string            `json:"source"` // derived (later: declared)
	Products  []string          `json:"products"`
	Modules   []string          `json:"modules"`
	Counts    describeCounts    `json:"counts"`
	Contracts describeContracts `json:"contracts"`
	Warnings  []string          `json:"warnings,omitempty"`
}

func newCmdDescribe(f *cmdutil.Factory) *cobra.Command {
	var memory string
	cmd := &cobra.Command{
		Use:     "describe",
		Aliases: []string{"desc"},
		Short:   "Report a memory's spec scheme (flat or product-rooted)",
		Long: `Report the spec scheme a memory uses — whether citations are flat
(<module>:<feature>:…) or product-rooted (<product>:<module>:…) — plus the
products/modules present, per-tier counts, and the general-provisions
contract code at each tier.

The scheme is derived from the live spec nodes. (Once memories carry a
declared scheme in their data, describe will report that and flag drift.)`,
		Example: `  hadron spec describe -m hadronmemory.com::platform-specs
  hadron spec describe -m micromentor.org::platform-specs --json`,
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
				if _, perr := ParseCitation(n.Loc); perr == nil {
					locs = append(locs, n.Loc)
				}
			}
			dto := describeScheme(memURN, locs)

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return renderDescribe(w, dto)
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}

// describeScheme derives the spec scheme and inventory from a memory's live
// citation locs. A top code is a product when some loc roots under it
// (code:<alpha>…); a bare such code is a product root, not a flat module.
func describeScheme(memURN string, locs []string) describeDTO {
	products := map[string]bool{}
	for _, loc := range locs {
		if c, err := ParseCitation(loc); err == nil && c.Product != "" {
			products[c.Product] = true
		}
	}

	modules := map[string]bool{}
	var counts describeCounts
	flatSeen, productSeen := false, false
	for _, loc := range locs {
		c, err := ParseCitation(loc)
		if err != nil {
			continue
		}
		// Scheme signal: any product-rooted citation ⇒ product; any flat
		// citation with a numeric child ⇒ flat. (A bare top code is ambiguous
		// and contributes to neither.)
		if c.Product != "" {
			productSeen = true
		} else if c.Feature != "" {
			flatSeen = true
		}
		// Contracts are tallied once — not also under their position tier.
		if c.IsContract() {
			counts.Contracts++
			continue
		}
		switch {
		case c.Product == "" && products[c.Module] && c.Feature == "":
			// a bare product root parsed as a flat module — counted via products
		case c.Level() == 1:
			modules[c.Format()] = true
		case c.Level() == 2:
			counts.Features++
		case c.Level() == 3:
			counts.Rules++
		case c.Level() == 4:
			counts.Flows++
		}
	}
	counts.Products = len(products)
	counts.Modules = len(modules)

	scheme := "empty"
	switch {
	case productSeen && flatSeen:
		scheme = "mixed"
	case productSeen:
		scheme = "product"
	case flatSeen:
		scheme = "flat"
	case len(modules) > 0 || len(products) > 0:
		scheme = "flat" // only bare roots present; default to flat
	}

	dto := describeDTO{
		Memory:    memURN,
		Scheme:    scheme,
		Source:    "derived",
		Products:  sortedStringKeys(products),
		Modules:   sortedStringKeys(modules),
		Counts:    counts,
		Contracts: describeContracts{Module: moduleContractFeature, Feature: "00"},
	}
	if scheme == "product" || scheme == "mixed" {
		dto.Contracts.Product = productContractCode
	}
	if scheme == "mixed" {
		dto.Warnings = append(dto.Warnings,
			"memory mixes flat and product-rooted citations — keep one arity per memory")
	}
	return dto
}

func renderDescribe(w io.Writer, d describeDTO) error {
	fmt.Fprintf(w, "Spec scheme — %s (derived from live nodes)\n", d.Memory)
	fmt.Fprintf(w, "  scheme:    %s\n", d.Scheme)
	if len(d.Products) > 0 {
		fmt.Fprintf(w, "  products:  %s\n", strings.Join(d.Products, ", "))
	}
	if len(d.Modules) > 0 {
		fmt.Fprintf(w, "  modules:   %s\n", strings.Join(d.Modules, ", "))
	}
	fmt.Fprintf(w, "  counts:    %s\n", describeCountsLine(d.Counts))
	fmt.Fprintf(w, "  contracts: %s\n", describeContractsLine(d.Contracts))
	if d.Scheme == "empty" {
		fmt.Fprintln(w, "  (no spec nodes yet — scaffold a product root with `spec new --new-product`, or a module with `spec new --new-module`)")
	}
	for _, warn := range d.Warnings {
		fmt.Fprintf(w, "  ⚠ %s\n", warn)
	}
	return nil
}

func describeCountsLine(c describeCounts) string {
	var parts []string
	if c.Products > 0 {
		parts = append(parts, fmt.Sprintf("%d products", c.Products))
	}
	parts = append(parts,
		fmt.Sprintf("%d modules", c.Modules),
		fmt.Sprintf("%d features", c.Features),
		fmt.Sprintf("%d rules", c.Rules),
		fmt.Sprintf("%d flows", c.Flows),
		fmt.Sprintf("%d contracts", c.Contracts),
	)
	return strings.Join(parts, ", ")
}

func describeContractsLine(c describeContracts) string {
	var parts []string
	if c.Product != "" {
		parts = append(parts, "product <p>:"+c.Product)
	}
	parts = append(parts,
		"module <m>:"+c.Module,
		"feature <m>:<f>:"+c.Feature,
	)
	return strings.Join(parts, " · ")
}
