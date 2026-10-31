package cmdutil

import (
	"sort"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Shared by node merge and memory merge (cli#1340): both send the
// NodeMergeField strategy.
// ParseMergeFields maps repeated --field values (case-insensitive) to the
// NodeMergeField enum. An empty selection returns nil so the server merges
// every mergeable field. An unknown value is a usage error naming the valid set.
func ParseMergeFields(fields []string) ([]gen.NodeMergeField, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	valid := map[string]gen.NodeMergeField{
		"ABSTRACT":    gen.NodeMergeFieldAbstract,
		"CONTENT":     gen.NodeMergeFieldContent,
		"DATA":        gen.NodeMergeFieldData,
		"DESCRIPTION": gen.NodeMergeFieldDescription,
		"EDGES":       gen.NodeMergeFieldEdges,
		"PROPERTIES":  gen.NodeMergeFieldProperties,
		"TAGS":        gen.NodeMergeFieldTags,
	}
	seen := map[gen.NodeMergeField]bool{}
	out := []gen.NodeMergeField{}
	for _, f := range fields {
		key := strings.ToUpper(strings.TrimSpace(f))
		mf, ok := valid[key]
		if !ok {
			return nil, exitcode.Newf(exitcode.Usage,
				"invalid --field %q (want ABSTRACT, CONTENT, DATA, DESCRIPTION, EDGES, PROPERTIES, or TAGS)", f)
		}
		if !seen[mf] {
			seen[mf] = true
			out = append(out, mf)
		}
	}
	// Deterministic order regardless of flag order — the set is what matters.
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
