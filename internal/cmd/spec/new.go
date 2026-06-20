package spec

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type plannedEdgeDTO struct {
	Label  string `json:"label"`
	Target string `json:"target"`
}

type newResultDTO struct {
	Citation string           `json:"citation"`
	MemoryID string           `json:"memoryId"`
	Name     string           `json:"name"`
	Tags     []string         `json:"tags"`
	Abstract string           `json:"abstract"`
	Edges    []plannedEdgeDTO `json:"edges"`
	DryRun   bool             `json:"dryRun"`
	Content  string           `json:"content,omitempty"`
	// Also carries nodes scaffolded alongside the primary in the same call —
	// today, the tier's general-provisions contract co-created with a root.
	Also []newResultDTO `json:"also,omitempty"`
}

func newCmdNew(f *cmdutil.Factory) *cobra.Command {
	var (
		memory, product, module, title  string
		feature, rule, ruleAfter, flow  string
		inherit, abstract, abstractFile string
		content, contentFile            string
		tags                            []string
		newFeature, newModule           bool
		newProduct, contract            bool
		newPath                         bool
		noEdges, noContract, dryRun     bool
	)
	cmd := &cobra.Command{
		Use:     "new",
		Aliases: []string{"scaffold"},
		Short:   "Allocate the next citation and scaffold a spec node",
		Long: `Allocate the next free citation number and create a spec node
pre-filled with the rubric (abstract + the four mandatory sections) and
wired with table-of-contents and inheritance edges.

In a product-rooted corpus, pass --product <ppp> to qualify the module;
omit it for a flat corpus.

Target (deepest wins):
  --new-product                      create a product root (needs --product)
  --new-module                       create a module root (under --product, if given)
  --new-feature                      allocate a new feature under the module
  --feature <fff>                    allocate the next rule under that feature
  --feature <fff> --rule <rr>        create that exact rule
  --feature <fff> --rule <rr> --flow <uu>   create that exact flow
  --contract                         the reserved general-provisions contract at
                                     the deepest specified tier (product :gen,
                                     module :000, feature :00)

Features are numbered in tens (010, 020, …); rules and flows by one. Use
--dry-run to preview without writing.

--new-path <citation> scaffolds a whole chain at once: it creates the given
citation and every missing ancestor (each with its tier template and, for the
roots, their general-provisions contract), so a fresh module + feature + rule
is one call instead of four.`,
		Example: `  hadron spec new -m micromentor.org::platform-specs --module msg --feature 010 --title "W4 — 7d check-in"
  hadron spec new -m hadronmemory.com::platform-specs --new-product --product cli --title "Hadron CLI"
  hadron spec new -m hadronmemory.com::platform-specs --product cli --new-module --module cha --title "chat command group"
  hadron spec new -m hadronmemory.com::platform-specs cli:cha:010:01 --new-path --title "send a message"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			memURN, err := memoryURNFromFlag(memory)
			if err != nil {
				return err
			}
			if product != "" && !reModule.MatchString(product) {
				return exitcode.Newf(exitcode.Usage, "--product %q must be 3 lowercase letters", product)
			}
			if module != "" && !reModule.MatchString(module) {
				return exitcode.Newf(exitcode.Usage, "--module %q must be 3 lowercase letters", module)
			}
			if product != "" && module == productContractCode {
				return exitcode.Newf(exitcode.Usage, "module %q is reserved for the product contract — use --contract", productContractCode)
			}
			if title == "" {
				return exitcode.Newf(exitcode.Usage, "--title is required")
			}
			if newFeature && feature != "" {
				return exitcode.Newf(exitcode.Usage, "--new-feature and --feature are mutually exclusive")
			}
			if rule != "" && ruleAfter != "" {
				return exitcode.Newf(exitcode.Usage, "--rule and --rule-after are mutually exclusive")
			}
			// Body and abstract can each read stdin via "-", but stdin is
			// consumable only once.
			if content == "-" && abstract == "-" {
				return exitcode.Newf(exitcode.Usage, "--content - and --abstract - cannot both read stdin")
			}
			// --abstract and --abstract-file are mutually exclusive — guard on
			// Changed() so an explicit empty --abstract is caught too, not just
			// the value-based check inside ResolveTextInput.
			if cmd.Flags().Changed("abstract") && cmd.Flags().Changed("abstract-file") {
				return exitcode.Newf(exitcode.Usage, "--abstract and --abstract-file are mutually exclusive")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			if newPath {
				if len(args) != 1 {
					return exitcode.Newf(exitcode.Usage, "--new-path needs a <citation> argument (e.g. cli:cha:010:01)")
				}
				if product != "" || module != "" || feature != "" || rule != "" || ruleAfter != "" ||
					flow != "" || inherit != "" || newFeature || newModule || newProduct || contract {
					return exitcode.Newf(exitcode.Usage, "--new-path takes a <citation> argument — don't combine it with --product/--module/--feature/--rule/--flow/--inherit/--new-*/--contract")
				}
				target, perr := ParseCitation(args[0])
				if perr != nil {
					return perr
				}
				body, berr := resolveBody(content, contentFile, f.IOStreams.In, target, title)
				if berr != nil {
					return berr
				}
				abs, aerr := cmdutil.ResolveTextInput("abstract", abstract, abstractFile, f.IOStreams.In)
				if aerr != nil {
					return aerr
				}
				if abs == "" {
					abs = tierAbstract(target, title)
				}
				return runNewPath(cmd, f, client, memURN, target, title, body, abs, specTags(tags), noContract, noEdges, dryRun)
			}
			if len(args) != 0 {
				return exitcode.Newf(exitcode.Usage, "a positional <citation> is only for --new-path; otherwise select the tier with flags")
			}

			// One scan of the whole product/module subtree: existence + allocation.
			// Paged to exhaustion — a truncated scan here would make the
			// allocator reuse a live number on a subtree past one page (#23).
			prefix := module
			if product != "" {
				prefix = product
			}
			if prefix == "" {
				return exitcode.Newf(exitcode.Usage, "pass --product and/or --module")
			}
			all, err := scanAllNodes(cmd.Context(), client, &memURN, &prefix, nil)
			if err != nil {
				return err
			}
			locs := map[string]bool{}
			var allLocs []string
			for _, n := range all {
				if n == nil {
					continue
				}
				if n.Loc != prefix && !strings.HasPrefix(n.Loc, prefix+":") {
					continue // keep the scan scoped to the requested subtree
				}
				if _, perr := ParseCitation(n.Loc); perr != nil {
					continue
				}
				locs[n.Loc] = true
				allLocs = append(allLocs, n.Loc)
			}

			target, parentLoc, inheritLoc, err := planTarget(planInput{
				product: product, module: module, feature: feature, rule: rule, ruleAfter: ruleAfter, flow: flow,
				inherit: inherit, newFeature: newFeature, newModule: newModule, newProduct: newProduct,
				contract: contract, locs: locs, allLocs: allLocs,
			})
			if err != nil {
				return err
			}

			body, err := resolveBody(content, contentFile, f.IOStreams.In, target, title)
			if err != nil {
				return err
			}
			abs, err := cmdutil.ResolveTextInput("abstract", abstract, abstractFile, f.IOStreams.In)
			if err != nil {
				return err
			}
			if abs == "" {
				abs = tierAbstract(target, title)
			}
			name := specName(target, title)
			tagSet := specTags(tags)

			// Co-scaffold the tier's general-provisions contract when creating a
			// root (unless --no-contract): a product's :gen, a module's :000, or a
			// feature's :00 is what the root's children inherit, so creating it up
			// front removes the separate --contract step and the missing-
			// inheritance-target gap (#69).
			var coContract *plannedContract
			if !noContract && (newProduct || newModule || newFeature) {
				if cc, ok := target.ChildContract(); ok && !locs[cc.Format()] {
					ctitle := title + " general provisions"
					coContract = &plannedContract{
						cit:      cc,
						title:    ctitle,
						name:     specName(cc, ctitle),
						abstract: tierAbstract(cc, ctitle),
						body:     tierBody(cc, ctitle),
					}
				}
			}

			result := newResultDTO{
				Citation: target.Format(),
				MemoryID: memURN,
				Name:     name,
				Tags:     tagSet,
				Abstract: abs,
				DryRun:   dryRun,
			}
			if !noEdges {
				if parentLoc != "" {
					result.Edges = append(result.Edges, plannedEdgeDTO{Label: title, Target: parentLoc})
				}
				if inheritLoc != "" {
					result.Edges = append(result.Edges, plannedEdgeDTO{Label: inheritEdgeLabel, Target: inheritLoc})
				}
			}
			if coContract != nil {
				cr := newResultDTO{
					Citation: coContract.cit.Format(),
					MemoryID: memURN,
					Name:     coContract.name,
					Tags:     tagSet,
					Abstract: coContract.abstract,
					DryRun:   dryRun,
				}
				if dryRun {
					cr.Content = coContract.body
				}
				if !noEdges {
					cr.Edges = append(cr.Edges, plannedEdgeDTO{Label: coContract.title, Target: target.Format()})
				}
				result.Also = append(result.Also, cr)
			}

			if dryRun {
				result.Content = body
				return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
					return renderNewResult(w, result)
				})
			}

			createOnly := true
			nodeType := "info"
			input := gen.NodeInput{
				MemoryId:   memURN,
				Loc:        target.Format(),
				Name:       name,
				CreateOnly: &createOnly,
				Tags:       tagSet,
				NodeType:   &nodeType,
				Abstract:   &abs,
				Content:    &body,
				Data:       specDataRaw(),
			}
			up, err := gen.UpsertNode(cmd.Context(), client, &input)
			if err != nil {
				return api.MapError(err)
			}
			newID := up.UpsertNode.Id

			if !noEdges {
				for _, e := range result.Edges {
					targetID, rerr := resolveSpecNode(cmd, client, memURN, e.Target)
					if rerr != nil {
						fmt.Fprintf(f.IOStreams.ErrOut, "warning: skipped edge %q → %s: %v\n", e.Label, e.Target, rerr)
						continue
					}
					if _, cerr := gen.CreateEdge(cmd.Context(), client, newID, targetID, e.Label, nil, nil, nil); cerr != nil {
						fmt.Fprintf(f.IOStreams.ErrOut, "warning: edge %q → %s failed: %v\n", e.Label, e.Target, api.MapError(cerr))
					}
				}
			}

			// Co-created contract: body/abstract come from the tier templates, and
			// its sole ToC edge points at the root we just made — wired by id,
			// since resolveUrn can lag a fresh node by ~a minute.
			if coContract != nil {
				cInput := gen.NodeInput{
					MemoryId:   memURN,
					Loc:        coContract.cit.Format(),
					Name:       coContract.name,
					CreateOnly: &createOnly,
					Tags:       tagSet,
					NodeType:   &nodeType,
					Abstract:   &coContract.abstract,
					Content:    &coContract.body,
					Data:       specDataRaw(),
				}
				cUp, cErr := gen.UpsertNode(cmd.Context(), client, &cInput)
				if cErr != nil {
					return fmt.Errorf("created %s but its contract %s failed: %w", target.Format(), coContract.cit.Format(), api.MapError(cErr))
				}
				if !noEdges {
					if _, eErr := gen.CreateEdge(cmd.Context(), client, cUp.UpsertNode.Id, newID, coContract.title, nil, nil, nil); eErr != nil {
						fmt.Fprintf(f.IOStreams.ErrOut, "warning: edge %q → %s failed: %v\n", coContract.title, target.Format(), api.MapError(eErr))
					}
				}
			}

			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				return renderNewResult(w, result)
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().StringVar(&product, "product", "", "3-letter product code (product-rooted corpora)")
	cmd.Flags().StringVar(&module, "module", "", "3-letter module code")
	cmd.Flags().StringVar(&title, "title", "", "human title for the spec (required)")
	cmd.Flags().StringVar(&feature, "feature", "", "existing feature to create a rule under (3 digits)")
	cmd.Flags().BoolVar(&newFeature, "new-feature", false, "allocate a new feature under the module")
	cmd.Flags().StringVar(&rule, "rule", "", "create this exact rule number (2 digits)")
	cmd.Flags().StringVar(&ruleAfter, "rule-after", "", "allocate the next rule strictly after this number")
	cmd.Flags().StringVar(&flow, "flow", "", "create this exact flow number (2 digits)")
	cmd.Flags().BoolVar(&newModule, "new-module", false, "create a new (frozen) module root")
	cmd.Flags().BoolVar(&newProduct, "new-product", false, "create a new (frozen) product root (needs --product)")
	cmd.Flags().BoolVar(&contract, "contract", false, "scaffold the general-provisions contract at the deepest specified tier")
	cmd.Flags().BoolVar(&newPath, "new-path", false, "create the positional <citation> and every missing ancestor in one call")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "extra semantic tag (repeatable)")
	cmd.Flags().StringVar(&abstract, "abstract", "", `the spec's abstract ("-" reads stdin; default: a placeholder lint flags)`)
	cmd.Flags().StringVar(&abstractFile, "abstract-file", "", "read the abstract from a file")
	cmd.Flags().StringVarP(&content, "content", "c", "", `body content ("-" reads stdin; default: the rubric template)`)
	cmd.Flags().StringVar(&contentFile, "content-file", "", "read body content from a file")
	cmd.Flags().StringVar(&inherit, "inherit", "", "inheritance-edge target citation (default: the tier's contract)")
	cmd.Flags().BoolVar(&noEdges, "no-edges", false, "do not create table-of-contents / inheritance edges")
	cmd.Flags().BoolVar(&noContract, "no-contract", false, "when creating a root, do not also scaffold its general-provisions contract")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the planned spec without writing anything")
	_ = cmd.MarkFlagRequired("memory")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

const inheritEdgeLabel = "inherits the shared contract (general provisions)"

// plannedContract is a general-provisions contract co-scaffolded with a root.
type plannedContract struct {
	cit      Citation
	title    string // contract title, also the ToC edge label to the root
	name     string
	abstract string
	body     string
}

type planInput struct {
	product, module                             string
	feature, rule, ruleAfter, flow, inherit     string
	newFeature, newModule, newProduct, contract bool
	locs                                        map[string]bool
	allLocs                                     []string
}

// planTarget resolves the target citation plus the ToC parent and the
// inheritance target, enforcing the frozen-code / parent-exists rules.
func planTarget(in planInput) (target Citation, parentLoc, inheritLoc string, err error) {
	productRoot := Citation{Product: in.product}
	productExists := in.product != "" && in.locs[productRoot.Format()]
	moduleCit := Citation{Product: in.product, Module: in.module}
	moduleExists := in.module != "" && in.locs[moduleCit.Format()]

	switch {
	case in.newProduct:
		if in.module != "" || in.newModule || in.newFeature || in.contract || in.feature != "" || in.rule != "" || in.flow != "" {
			return Citation{}, "", "", exitcode.Newf(exitcode.Usage, "--new-product takes only --product <ppp> (no module/feature/rule/flow)")
		}
		if in.product == "" {
			return Citation{}, "", "", exitcode.Newf(exitcode.Usage, "--new-product requires --product <ppp>")
		}
		if productExists {
			return Citation{}, "", "", exitcode.Newf(exitcode.Conflict, "product %q already exists (product codes are frozen)", in.product)
		}
		return productRoot, "", "", nil

	case in.newModule:
		if in.module == "" {
			return Citation{}, "", "", exitcode.Newf(exitcode.Usage, "--new-module requires --module <mmm>")
		}
		if in.newFeature || in.contract || in.feature != "" || in.rule != "" || in.flow != "" {
			return Citation{}, "", "", exitcode.Newf(exitcode.Usage, "--new-module cannot be combined with feature/rule/flow/contract flags")
		}
		if in.product != "" && !productExists {
			return Citation{}, "", "", exitcode.Newf(exitcode.NotFound, "product %q does not exist — create it first with --new-product", in.product)
		}
		if moduleExists {
			return Citation{}, "", "", exitcode.Newf(exitcode.Conflict, "module %q already exists (module codes are frozen)", moduleCit.Format())
		}
		// Parent is the product root (product mode) or none (flat); a module
		// under a product inherits the product's :gen contract.
		parent := ""
		if in.product != "" {
			parent = productRoot.Format()
		}
		inh := ""
		if cl, ok := moduleCit.InheritedContractLoc(); ok && in.locs[cl.Format()] {
			inh = cl.Format()
		}
		return moduleCit, parent, inh, nil

	case in.contract:
		if in.newFeature || in.rule != "" || in.flow != "" {
			return Citation{}, "", "", exitcode.Newf(exitcode.Usage, "--contract cannot be combined with --rule/--flow/--new-feature")
		}
		if in.product != "" && !productExists {
			return Citation{}, "", "", exitcode.Newf(exitcode.NotFound, "product %q does not exist — create it first with --new-product", in.product)
		}
		return planContract(in, productRoot, moduleCit)
	}

	// Creating a feature, rule, or flow under an existing module.
	if in.module == "" {
		return Citation{}, "", "", exitcode.Newf(exitcode.Usage, "pass --module <mmm> (or --new-product/--new-module/--contract)")
	}
	if in.product != "" && !productExists {
		return Citation{}, "", "", exitcode.Newf(exitcode.NotFound, "product %q does not exist — create it first with --new-product", in.product)
	}
	if !moduleExists {
		return Citation{}, "", "", exitcode.Newf(exitcode.NotFound, "module %q does not exist — create it first with --new-module", moduleCit.Format())
	}

	if in.newFeature {
		if in.rule != "" || in.flow != "" {
			return Citation{}, "", "", exitcode.Newf(exitcode.Usage, "--new-feature cannot be combined with --rule/--flow")
		}
		t, aerr := allocateChild(moduleCit, childNumbersAt(moduleCit, in.allLocs), nil, 0)
		if aerr != nil {
			return Citation{}, "", "", aerr
		}
		// A new feature inherits the module's :000 contract.
		inh := ""
		if cl, ok := t.InheritedContractLoc(); ok && in.locs[cl.Format()] {
			inh = cl.Format()
		}
		return t, moduleCit.Format(), inh, nil
	}

	if in.feature == "" {
		return Citation{}, "", "", exitcode.Newf(exitcode.Usage, "pass --feature <fff> (or --new-feature / --new-module / --contract)")
	}
	featureCit := Citation{Product: in.product, Module: in.module, Feature: in.feature}
	if _, perr := ParseCitation(featureCit.Format()); perr != nil {
		return Citation{}, "", "", perr
	}
	if !in.locs[featureCit.Format()] {
		return Citation{}, "", "", exitcode.Newf(exitcode.NotFound, "feature %q does not exist — create it with --new-feature", featureCit.Format())
	}

	// Explicit flow.
	if in.flow != "" {
		if in.rule == "" {
			return Citation{}, "", "", exitcode.Newf(exitcode.Usage, "--flow requires --rule")
		}
		ruleCit := Citation{Product: in.product, Module: in.module, Feature: in.feature, Rule: in.rule}
		if !in.locs[ruleCit.Format()] {
			return Citation{}, "", "", exitcode.Newf(exitcode.NotFound, "rule %q does not exist", ruleCit.Format())
		}
		t := Citation{Product: in.product, Module: in.module, Feature: in.feature, Rule: in.rule, Flow: in.flow}
		if _, perr := ParseCitation(t.Format()); perr != nil {
			return Citation{}, "", "", perr
		}
		return t, ruleCit.Format(), "", nil
	}

	// Explicit rule number (e.g. 00 contract), else allocate the next rule.
	var t Citation
	if in.rule != "" {
		t = Citation{Product: in.product, Module: in.module, Feature: in.feature, Rule: in.rule}
		if _, perr := ParseCitation(t.Format()); perr != nil {
			return Citation{}, "", "", perr
		}
	} else {
		after := 0
		if in.ruleAfter != "" {
			n, cerr := strconv.Atoi(in.ruleAfter)
			if cerr != nil {
				return Citation{}, "", "", exitcode.Newf(exitcode.Usage, "--rule-after must be a number")
			}
			after = n
		}
		alloc, aerr := allocateChild(featureCit, childNumbersAt(featureCit, in.allLocs), nil, after)
		if aerr != nil {
			return Citation{}, "", "", aerr
		}
		t = alloc
	}

	// Inheritance: a non-contract rule inherits its feature's :00 contract.
	inheritLoc = ""
	if !t.IsContract() {
		if in.inherit != "" {
			ic, perr := ParseCitation(in.inherit)
			if perr != nil {
				return Citation{}, "", "", perr
			}
			inheritLoc = ic.Format()
		} else if cl, ok := t.InheritedContractLoc(); ok && in.locs[cl.Format()] {
			inheritLoc = cl.Format()
		}
	}
	return t, featureCit.Format(), inheritLoc, nil
}

// planContract resolves the reserved general-provisions contract at the
// deepest specified tier: --feature → the feature's :00, --module → the
// module's :000, --product (no module) → the product's :gen. Contracts get a
// ToC edge to their parent but no inheritance edge (they are inheritance
// sources, not sinks).
func planContract(in planInput, productRoot, moduleCit Citation) (Citation, string, string, error) {
	switch {
	case in.feature != "":
		featureCit := Citation{Product: in.product, Module: in.module, Feature: in.feature}
		if _, perr := ParseCitation(featureCit.Format()); perr != nil {
			return Citation{}, "", "", perr
		}
		if !in.locs[featureCit.Format()] {
			return Citation{}, "", "", exitcode.Newf(exitcode.NotFound, "feature %q does not exist", featureCit.Format())
		}
		t := Citation{Product: in.product, Module: in.module, Feature: in.feature, Rule: "00"}
		return t, featureCit.Format(), "", nil
	case in.module != "":
		if !in.locs[moduleCit.Format()] {
			return Citation{}, "", "", exitcode.Newf(exitcode.NotFound, "module %q does not exist — create it first with --new-module", moduleCit.Format())
		}
		t := Citation{Product: in.product, Module: in.module, Feature: moduleContractFeature}
		return t, moduleCit.Format(), "", nil
	case in.product != "":
		if !in.locs[productRoot.Format()] {
			return Citation{}, "", "", exitcode.Newf(exitcode.NotFound, "product %q does not exist — create it first with --new-product", in.product)
		}
		t := Citation{Product: in.product, Module: productContractCode}
		return t, productRoot.Format(), "", nil
	default:
		return Citation{}, "", "", exitcode.Newf(exitcode.Usage, "--contract needs --product, --module, or --feature to choose the tier")
	}
}

func resolveBody(content, contentFile string, stdin io.Reader, c Citation, title string) (string, error) {
	if content != "" && contentFile != "" {
		return "", exitcode.Newf(exitcode.Usage, "--content and --content-file are mutually exclusive")
	}
	if contentFile != "" {
		data, err := os.ReadFile(contentFile)
		if err != nil {
			return "", exitcode.Newf(exitcode.Usage, "reading --content-file: %v", err)
		}
		return string(data), nil
	}
	if content == "-" {
		if stdin == nil {
			return "", exitcode.Newf(exitcode.Usage, "stdin is not available")
		}
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	if content != "" {
		return content, nil
	}
	return tierBody(c, title), nil
}

func renderNewResult(w io.Writer, r newResultDTO) error {
	verb := "✓ created"
	if r.DryRun {
		verb = "would create"
	}
	fmt.Fprintf(w, "%s %s — %s\n", verb, r.Citation, r.Name)
	fmt.Fprintf(w, "  tags: %v\n", r.Tags)
	for _, e := range r.Edges {
		fmt.Fprintf(w, "  edge: %s → %s\n", e.Label, e.Target)
	}
	if r.DryRun && r.Content != "" {
		fmt.Fprintf(w, "\n%s\n", r.Content)
	}
	for _, also := range r.Also {
		fmt.Fprintln(w)
		if err := renderNewResult(w, also); err != nil {
			return err
		}
	}
	return nil
}

// planChain returns the citations from the top tier down to target that need
// creating — the target (which must not already exist) plus any missing
// ancestor — in top-down order.
func planChain(target Citation, existing map[string]bool) ([]Citation, error) {
	if existing[target.Format()] {
		return nil, exitcode.Newf(exitcode.Conflict, "%s already exists", target.Format())
	}
	var chain []Citation
	for c := target; ; {
		chain = append(chain, c)
		p, ok := c.Parent()
		if !ok {
			break
		}
		c = p
	}
	todo := make([]Citation, 0, len(chain))
	for i := len(chain) - 1; i >= 0; i-- { // deepest-first → top-down
		if !existing[chain[i].Format()] {
			todo = append(todo, chain[i])
		}
	}
	return todo, nil
}

// pathNode is one node scaffolded by runNewPath: the root tier or its
// co-created contract, with the edges it should carry.
type pathNode struct {
	cit      Citation
	name     string
	abstract string
	body     string
	edges    []plannedEdgeDTO
}

// runNewPath scaffolds target and every missing ancestor in one call. Each node
// gets its tier template (the target uses the caller's body/abstract); each
// created root also gets its general-provisions contract unless noContract.
// Edges resolve by id for nodes made this run (resolveUrn lags a fresh node ~a
// minute) and by loc for pre-existing ancestors; an unresolvable target warns,
// never aborts.
func runNewPath(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, memURN string, target Citation, title, body, abs string, tagSet []string, noContract, noEdges, dryRun bool) error {
	prefix := target.Module
	if target.Product != "" {
		prefix = target.Product
	}
	all, err := scanAllNodes(cmd.Context(), client, &memURN, &prefix, nil)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for _, n := range all {
		if n != nil {
			if _, perr := ParseCitation(n.Loc); perr == nil {
				existing[n.Loc] = true
			}
		}
	}

	todo, err := planChain(target, existing)
	if err != nil {
		return err
	}

	// Locs present after the run, so an edge is only planned to a target that
	// will exist (pre-existing, or about to be created).
	willExist := map[string]bool{}
	for loc := range existing {
		willExist[loc] = true
	}
	for _, cit := range todo {
		willExist[cit.Format()] = true
		if !noContract {
			if con, ok := cit.ChildContract(); ok {
				willExist[con.Format()] = true
			}
		}
	}

	// Ordered plan: each root, immediately followed by its contract, so every
	// edge target precedes the node that references it.
	var plan []pathNode
	for _, cit := range todo {
		t := cit.Leaf()
		nb, na := tierBody(cit, t), tierAbstract(cit, t)
		if cit == target {
			t, nb, na = title, body, abs
		}
		pn := pathNode{cit: cit, name: specName(cit, t), abstract: na, body: nb}
		if !noEdges {
			if p, ok := cit.Parent(); ok && willExist[p.Format()] {
				pn.edges = append(pn.edges, plannedEdgeDTO{Label: t, Target: p.Format()})
			}
			if ic, ok := cit.InheritedContractLoc(); ok && willExist[ic.Format()] {
				pn.edges = append(pn.edges, plannedEdgeDTO{Label: inheritEdgeLabel, Target: ic.Format()})
			}
		}
		plan = append(plan, pn)

		if !noContract {
			if con, ok := cit.ChildContract(); ok && !existing[con.Format()] {
				ct := t + " general provisions"
				cn := pathNode{cit: con, name: specName(con, ct), abstract: tierAbstract(con, ct), body: tierBody(con, ct)}
				if !noEdges {
					cn.edges = append(cn.edges, plannedEdgeDTO{Label: ct, Target: cit.Format()})
				}
				plan = append(plan, cn)
			}
		}
	}

	// The target is the primary result; the ancestors and contracts hang off it
	// under `also`, in creation order.
	var result newResultDTO
	var also []newResultDTO
	for _, pn := range plan {
		dto := newResultDTO{
			Citation: pn.cit.Format(), MemoryID: memURN, Name: pn.name,
			Tags: tagSet, Abstract: pn.abstract, Edges: pn.edges, DryRun: dryRun,
		}
		if dryRun {
			dto.Content = pn.body
		}
		if pn.cit == target {
			result = dto
		} else {
			also = append(also, dto)
		}
	}
	result.Also = also

	render := func() error {
		return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
			return renderNewResult(w, result)
		})
	}
	if dryRun {
		return render()
	}

	createOnly := true
	nodeType := "info"
	created := map[string]string{}
	for _, pn := range plan {
		ab, bd := pn.abstract, pn.body
		input := gen.NodeInput{
			MemoryId: memURN, Loc: pn.cit.Format(), Name: pn.name,
			CreateOnly: &createOnly, Tags: tagSet, NodeType: &nodeType,
			Abstract: &ab, Content: &bd, Data: specDataRaw(),
		}
		up, uerr := gen.UpsertNode(cmd.Context(), client, &input)
		if uerr != nil {
			return fmt.Errorf("scaffolding %s: %w", pn.cit.Format(), api.MapError(uerr))
		}
		srcID := up.UpsertNode.Id
		created[pn.cit.Format()] = srcID
		if noEdges {
			continue
		}
		for _, e := range pn.edges {
			tid, ok := created[e.Target]
			if !ok {
				rid, rerr := resolveSpecNode(cmd, client, memURN, e.Target)
				if rerr != nil {
					fmt.Fprintf(f.IOStreams.ErrOut, "warning: skipped edge %q → %s: %v\n", e.Label, e.Target, rerr)
					continue
				}
				tid = rid
			}
			if _, cerr := gen.CreateEdge(cmd.Context(), client, srcID, tid, e.Label, nil, nil, nil); cerr != nil {
				fmt.Fprintf(f.IOStreams.ErrOut, "warning: edge %q → %s failed: %v\n", e.Label, e.Target, api.MapError(cerr))
			}
		}
	}
	return render()
}
