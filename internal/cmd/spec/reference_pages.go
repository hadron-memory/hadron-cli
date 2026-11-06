package spec

import (
	"context"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

type referencePage struct {
	items []referenceDTO
	total *int
	more  *bool
}

// drainReferences preserves order and multiplicity: repeated rows are legal.
// No output escapes until hasMore and total agree on a complete traversal.
func drainReferences(fetch func(limit, offset int) (*referencePage, error)) ([]referenceDTO, error) {
	out := []referenceDTO{}
	total := -1
	for {
		page, err := fetch(20, len(out))
		if err != nil {
			return nil, err
		}
		if page == nil || page.items == nil || page.total == nil || page.more == nil {
			return nil, invalidReferencePage("missing page, items or metadata")
		}
		if *page.total < 0 || (total >= 0 && *page.total != total) {
			return nil, invalidReferencePage("negative or changing total")
		}
		total = *page.total
		// Subtraction avoids overflow in the consumed count.
		if len(out) > total || len(page.items) > total-len(out) {
			return nil, invalidReferencePage("items exceed total")
		}
		out = append(out, page.items...)
		if *page.more {
			if len(page.items) == 0 || len(out) >= total {
				return nil, invalidReferencePage("hasMore without remaining progress")
			}
		} else {
			if len(out) != total {
				return nil, invalidReferencePage("terminal count disagrees with total")
			}
			return out, nil
		}
	}
}

func invalidReferencePage(why string) error {
	return exitcode.Newf(exitcode.Error, "server returned an invalid spec-reference page: %s", why)
}

// readReferences uses a page field normally. A field-specific first-request
// validation rejection alone selects the full legacy twin once; no restart
// after accepted pages, and no fallback on a populated data+errors answer.
func readReferences(ctx context.Context, client graphql.Client, memory, loc string) ([]referenceDTO, error) {
	ctx, shape := api.WithGraphQLErrorShape(ctx)
	field := "specUnresolvedReferencesPage"
	if loc != "" {
		field = "specBacklinksPage"
	}
	first := true
	legacy := false
	rows, err := drainReferences(func(limit, offset int) (*referencePage, error) {
		var p *referencePage
		var err error
		if loc != "" {
			r, e := gen.SpecBacklinksPage(ctx, client, memory, loc, limit, offset)
			err = e
			if r != nil && r.SpecBacklinksPage != nil {
				v := r.SpecBacklinksPage
				p = &referencePage{total: v.Total, more: v.HasMore}
				if v.Items != nil {
					p.items = []referenceDTO{}
					for _, it := range v.Items {
						if it == nil {
							return nil, invalidReferencePage("null item")
						}
						p.items = append(p.items, referenceFrom(it, nil))
					}
				}
			}
		} else {
			r, e := gen.SpecUnresolvedReferencesPage(ctx, client, memory, limit, offset)
			err = e
			if r != nil && r.SpecUnresolvedReferencesPage != nil {
				v := r.SpecUnresolvedReferencesPage
				p = &referencePage{total: v.Total, more: v.HasMore}
				if v.Items != nil {
					p.items = []referenceDTO{}
					for _, it := range v.Items {
						if it == nil {
							return nil, invalidReferencePage("null item")
						}
						p.items = append(p.items, referenceFrom(it, it.Reason))
					}
				}
			}
		}
		if shape.HadNullEntry() {
			return nil, invalidReferencePage("null error entry")
		}
		if first && p == nil && api.IsUnknownQueryField(err, field) {
			legacy = true
		}
		first = false
		return p, err
	})
	if !legacy {
		return rows, err
	}
	out := []referenceDTO{}
	if loc != "" {
		r, e := gen.SpecBacklinks(ctx, client, memory, loc)
		if shape.HadNullEntry() {
			return nil, invalidReferencePage("null error entry")
		}
		if e != nil {
			if r != nil && r.SpecBacklinks != nil {
				return nil, invalidReferencePage("legacy data beside errors")
			}
			return nil, e
		}
		if r == nil || r.SpecBacklinks == nil {
			return nil, invalidReferencePage("missing legacy items")
		}
		for _, it := range r.SpecBacklinks {
			if it == nil {
				return nil, invalidReferencePage("null legacy item")
			}
			out = append(out, referenceFrom(it, nil))
		}
	} else {
		r, e := gen.SpecUnresolvedReferences(ctx, client, memory)
		if shape.HadNullEntry() {
			return nil, invalidReferencePage("null error entry")
		}
		if e != nil {
			if r != nil && r.SpecUnresolvedReferences != nil {
				return nil, invalidReferencePage("legacy data beside errors")
			}
			return nil, e
		}
		if r == nil || r.SpecUnresolvedReferences == nil {
			return nil, invalidReferencePage("missing legacy items")
		}
		for _, it := range r.SpecUnresolvedReferences {
			if it == nil {
				return nil, invalidReferencePage("null legacy item")
			}
			out = append(out, referenceFrom(it, it.Reason))
		}
	}
	return out, nil
}
