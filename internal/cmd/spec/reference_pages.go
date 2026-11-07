package spec

import (
	"context"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// A missing legacy report is tolerated by draft lint only after that exact
// compatibility operation was attempted. An unrelated validation error from
// the page request must remain a failed read, even if it names this field.
type legacyUnresolvedUnsupported struct{ error }

func (e *legacyUnresolvedUnsupported) Unwrap() error { return e.error }

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

// The caller selects an operation explicitly; an empty citation is not a mode.
type referenceOperation uint8

const (
	referenceBacklinks referenceOperation = iota
	referenceUnresolved
)

func invalidLegacyReferenceList(why string) error {
	return exitcode.Newf(exitcode.Error, "server returned an invalid legacy spec-reference list: %s", why)
}

// readReferences uses a page field normally. A field-specific first-request
// validation rejection alone selects the full legacy twin once; no restart
// after accepted pages, and no fallback on a populated data+errors answer.
func readReferences(ctx context.Context, client graphql.Client, memory string, operation referenceOperation, loc string) ([]referenceDTO, error) {
	ctx, shape := api.WithGraphQLErrorShape(ctx)
	var field string
	switch operation {
	case referenceBacklinks:
		field = "specBacklinksPage"
	case referenceUnresolved:
		field = "specUnresolvedReferencesPage"
	default:
		return nil, invalidReferencePage("unknown reference operation")
	}
	first := true
	legacy := false
	rows, err := drainReferences(func(limit, offset int) (*referencePage, error) {
		var p *referencePage
		var err error
		if operation == referenceBacklinks {
			r, e := gen.SpecBacklinksPage(ctx, client, memory, loc, limit, offset)
			err = e
			// Preserve a server error before interpreting any partial payload.
			// Its presence still forbids capability fallback.
			if err != nil {
				if r != nil && r.SpecBacklinksPage != nil {
					p = &referencePage{}
				}
			} else if r != nil && r.SpecBacklinksPage != nil {
				v := r.SpecBacklinksPage
				p = &referencePage{total: v.Total, more: v.HasMore}
				if v.Items != nil {
					p.items = []referenceDTO{}
					for _, it := range v.Items {
						if it == nil {
							return nil, invalidReferencePage("null item")
						}
						item, e := referenceFrom(it, nil, invalidReferencePage)
						if e != nil {
							return nil, e
						}
						p.items = append(p.items, item)
					}
				}
			}
		} else {
			r, e := gen.SpecUnresolvedReferencesPage(ctx, client, memory, limit, offset)
			err = e
			// Preserve a server error before interpreting any partial payload.
			// Its presence still forbids capability fallback.
			if err != nil {
				if r != nil && r.SpecUnresolvedReferencesPage != nil {
					p = &referencePage{}
				}
			} else if r != nil && r.SpecUnresolvedReferencesPage != nil {
				v := r.SpecUnresolvedReferencesPage
				p = &referencePage{total: v.Total, more: v.HasMore}
				if v.Items != nil {
					p.items = []referenceDTO{}
					for _, it := range v.Items {
						if it == nil {
							return nil, invalidReferencePage("null item")
						}
						item, e := referenceFrom(it, it.Reason, invalidReferencePage)
						if e != nil {
							return nil, e
						}
						p.items = append(p.items, item)
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
	if operation == referenceBacklinks {
		r, e := gen.SpecBacklinks(ctx, client, memory, loc)
		if shape.HadNullEntry() {
			return nil, invalidLegacyReferenceList("null error entry")
		}
		if e != nil {
			return nil, e
		}
		if r == nil || r.SpecBacklinks == nil {
			return nil, invalidLegacyReferenceList("missing legacy items")
		}
		for _, it := range r.SpecBacklinks {
			if it == nil {
				return nil, invalidLegacyReferenceList("null legacy item")
			}
			item, e := referenceFrom(it, nil, invalidLegacyReferenceList)
			if e != nil {
				return nil, e
			}
			out = append(out, item)
		}
	} else {
		r, e := gen.SpecUnresolvedReferences(ctx, client, memory)
		if shape.HadNullEntry() {
			return nil, invalidLegacyReferenceList("null error entry")
		}
		if e != nil {
			if (r == nil || r.SpecUnresolvedReferences == nil) && api.IsUnknownQueryField(e, "specUnresolvedReferences") {
				return nil, &legacyUnresolvedUnsupported{e}
			}
			return nil, e
		}
		if r == nil || r.SpecUnresolvedReferences == nil {
			return nil, invalidLegacyReferenceList("missing legacy items")
		}
		for _, it := range r.SpecUnresolvedReferences {
			if it == nil {
				return nil, invalidLegacyReferenceList("null legacy item")
			}
			item, e := referenceFrom(it, it.Reason, invalidLegacyReferenceList)
			if e != nil {
				return nil, e
			}
			out = append(out, item)
		}
	}
	return out, nil
}
