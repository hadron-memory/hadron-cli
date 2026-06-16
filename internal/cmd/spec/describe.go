package spec

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
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
	Scheme    string            `json:"scheme"` // effective: declared if set, else derived
	Source    string            `json:"source"` // "declared" | "derived"
	Declared  string            `json:"declared,omitempty"`
	Derived   string            `json:"derived"` // flat | product | mixed | empty
	Products  []string          `json:"products"`
	Modules   []string          `json:"modules"`
	Counts    describeCounts    `json:"counts"`
	Contracts describeContracts `json:"contracts"`
	Warnings  []string          `json:"warnings,omitempty"`
}

func newCmdDescribe(f *cmdutil.Factory) *cobra.Command {
	var memory, declare string
	cmd := &cobra.Command{
		Use:     "describe",
		Aliases: []string{"desc"},
		Short:   "Report (or declare) a memory's spec scheme",
		Long: `Report the spec scheme a memory uses — whether citations are flat
(<module>:<feature>:…) or product-rooted (<product>:<module>:…) — plus the
products/modules present, per-tier counts, and the general-provisions
contract code at each tier.

The scheme can be declared in the memory's data (so an empty memory can
announce its intended arity); when declared it is authoritative and any
disagreement with the live nodes is flagged. --declare flat|product
writes that declaration.`,
		Example: `  hadron spec describe -m hadronmemory.com::platform-specs
  hadron spec describe -m hadronmemory.com::platform-specs --declare product
  hadron spec describe -m micromentor.org::platform-specs --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			memURN, err := memoryURNFromFlag(memory)
			if err != nil {
				return err
			}
			if declare != "" && declare != "flat" && declare != "product" {
				return exitcode.Newf(exitcode.Usage, "--declare must be \"flat\" or \"product\"")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			memID, err := resolveSpecMemoryID(cmd, client, memURN)
			if err != nil {
				return err
			}
			memResp, err := gen.GetMemory(cmd.Context(), client, memID)
			if err != nil {
				return api.MapError(err)
			}
			var curData *json.RawMessage
			if memResp.Memory != nil {
				curData = memResp.Memory.Data
			}

			if declare != "" {
				merged, merr := withScheme(curData, declare)
				if merr != nil {
					return merr
				}
				if _, uerr := gen.UpdateMemory(cmd.Context(), client, memID, nil, nil, nil, nil, nil, &merged); uerr != nil {
					return api.MapError(uerr)
				}
				curData = &merged
			}
			declared := schemeFromData(curData)

			all, err := scanAllNodes(cmd.Context(), client, &memURN, nil, nil, []string{"spec"})
			if err != nil {
				return err
			}
			var locs []string
			for _, n := range all {
				if n == nil {
					continue
				}
				if _, perr := ParseCitation(n.Loc); perr == nil {
					locs = append(locs, n.Loc)
				}
			}
			dto := describeScheme(memURN, locs)
			applyDeclared(&dto, declared)

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return renderDescribe(w, dto)
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().StringVar(&declare, "declare", "", "declare the scheme in the memory's data: flat | product")
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}

// resolveSpecMemoryID maps a spec memory ref to its ID. A memory's own URN
// uses a single colon between org and memory; the spec memURN uses the
// node-ref double colon — normalize, then match myMemories (Query.memory /
// updateMemory accept PK ids only today). This adds a round-trip to describe
// (myMemories → memory → nodes); collapse it once the server dispatches memory
// URNs on those resolvers (same TODO as the memory package's resolveMemoryID).
func resolveSpecMemoryID(cmd *cobra.Command, client graphql.Client, memURN string) (string, error) {
	want := strings.Replace(memURN, "::", ":", 1)
	includeAgentSystem := true
	resp, err := gen.MyMemories(cmd.Context(), client, &includeAgentSystem)
	if err != nil {
		return "", api.MapError(err)
	}
	for _, m := range resp.MyMemories {
		if m.Urn == want {
			return m.Id, nil
		}
	}
	return "", exitcode.Newf(exitcode.NotFound, "memory %q not found", memURN)
}

// schemeFromData extracts data.spec.scheme; "" if absent or unparseable. It is
// deliberately lenient on read (unlike withScheme, which rejects a malformed
// bag on write): a foreign or empty data bag degrades to "no declaration".
func schemeFromData(data *json.RawMessage) string {
	if data == nil || len(*data) == 0 {
		return ""
	}
	var d struct {
		Spec struct {
			Scheme string `json:"scheme"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(*data, &d); err != nil {
		return ""
	}
	return d.Spec.Scheme
}

// withScheme merges spec.scheme=scheme into the memory's data bag, preserving
// every other key.
func withScheme(data *json.RawMessage, scheme string) (json.RawMessage, error) {
	bag := map[string]json.RawMessage{}
	if data != nil && len(*data) > 0 {
		if err := json.Unmarshal(*data, &bag); err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "memory data is not a JSON object: %v", err)
		}
		if bag == nil { // a literal JSON null unmarshals into a nil map
			bag = map[string]json.RawMessage{}
		}
	}
	spec := map[string]json.RawMessage{}
	if raw, ok := bag["spec"]; ok {
		_ = json.Unmarshal(raw, &spec) // best-effort; the scheme key is overwritten
	}
	schemeRaw, _ := json.Marshal(scheme)
	spec["scheme"] = schemeRaw
	specRaw, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	bag["spec"] = specRaw
	return json.Marshal(bag)
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
		Derived:   scheme,
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

// applyDeclared overlays a declared scheme (from the memory's data) onto a
// derived DTO: the declaration wins, and any disagreement with the live nodes
// is flagged.
func applyDeclared(d *describeDTO, declared string) {
	if declared == "" {
		return
	}
	d.Declared = declared
	d.Scheme = declared
	d.Source = "declared"
	if declared == "product" || declared == "mixed" {
		d.Contracts.Product = productContractCode
	}
	if d.Derived != "empty" && d.Derived != declared {
		d.Warnings = append(d.Warnings,
			fmt.Sprintf("declared scheme %q but live nodes look %q", declared, d.Derived))
	}
}

func renderDescribe(w io.Writer, d describeDTO) error {
	fmt.Fprintf(w, "Spec scheme — %s\n", d.Memory)
	fmt.Fprintf(w, "  scheme:    %s  (%s)\n", d.Scheme, d.Source)
	if d.Declared != "" && d.Declared != d.Derived {
		fmt.Fprintf(w, "  derived:   %s  (from live nodes)\n", d.Derived)
	}
	if len(d.Products) > 0 {
		fmt.Fprintf(w, "  products:  %s\n", strings.Join(d.Products, ", "))
	}
	if len(d.Modules) > 0 {
		fmt.Fprintf(w, "  modules:   %s\n", strings.Join(d.Modules, ", "))
	}
	fmt.Fprintf(w, "  counts:    %s\n", describeCountsLine(d.Counts))
	fmt.Fprintf(w, "  contracts: %s\n", describeContractsLine(d.Contracts))
	if d.Scheme == "empty" {
		fmt.Fprintln(w, "  (no spec nodes yet — declare with --declare, or scaffold a root with `spec new --new-product`/`--new-module`)")
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
