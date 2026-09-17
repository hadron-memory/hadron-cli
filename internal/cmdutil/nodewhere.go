package cmdutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gqltypes"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// WhereFlagUsage and SortPropertyFlagUsage are the shared `--where` /
// `--sort-property` help strings, defined once so the two commands offering
// them cannot drift (#603).
//
// Both name the DEFAULT COLUMN and show "field" in the example, which the
// previous wording did not. It read "structured predicate over properties/data",
// which a reader reasonably takes to mean the predicate searches BOTH — and the
// example it gave omitted "field" entirely, so copying it aimed the predicate at
// `properties` without ever mentioning the choice. A node's free-form envelope
// lives in `data`, so that spelling returns a silent zero for the commonest
// thing anyone wants to ask about.
const (
	WhereFlagUsage = `structured predicate over one JSONB column — "properties" unless a leaf sets "field":"data"` +
		` (e.g. '{"field":"data","path":["authorName"],"exists":true}')`
	SortPropertyFlagUsage = `order by a JSON path in one JSONB column — same "field" key and "properties" default as --where` +
		` (e.g. '{"path":["rank"],"as":"number","direction":"desc"}')`
)

// rejectTrailing fails if anything other than whitespace follows the value the
// decoder just consumed. json.Decoder.More() can't be used for this — it reports
// whether the decoder is mid-array/object, not whether the stream is exhausted —
// so concatenated JSON like `{...} {}` would otherwise slip through. A second
// Decode returns io.EOF on a clean stream and a value/parse error on trailing
// content.
func rejectTrailing(dec *json.Decoder) bool {
	var rest json.RawMessage
	return !errors.Is(dec.Decode(&rest), io.EOF)
}

// ParseNodeWhere parses the raw-JSON `--where` predicate (grammar parity with
// the server's NodeWhereInput, #719) into the bound gqltypes struct. The JSON
// keys are the GraphQL field names verbatim (and/or/not, path, field, as, and
// one of eq|ne|in|lt|lte|gt|gte|between|exists|contains), so a user's predicate
// unmarshals straight through — the struct's omitempty tags then omit every
// field they left unset, which the server's "exactly one operator" leaf check
// requires (it counts any operator key that is not undefined). Deep validation
// (depth, leaf-arity, path shape) is the server's job and surfaces as
// BAD_USER_INPUT; this only enforces well-formed JSON and rejects unknown keys
// so a typo like "equals" fails loudly instead of being silently dropped.
func ParseNodeWhere(raw string) (*gqltypes.NodeWhereInput, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	var w gqltypes.NodeWhereInput
	if err := dec.Decode(&w); err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "invalid --where JSON: %v", err)
	}
	if rejectTrailing(dec) {
		return nil, exitcode.Newf(exitcode.Usage, "invalid --where JSON: trailing data after the predicate object")
	}
	return &w, nil
}

// ParseNodePropertySort parses the raw-JSON `--sort-property` value (server
// NodePropertySort, #719) into the bound gqltypes struct. Same grammar-parity
// contract as ParseNodeWhere: keys are path (required), field, as, direction.
func ParseNodePropertySort(raw string) (*gqltypes.NodePropertySort, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	var s gqltypes.NodePropertySort
	if err := dec.Decode(&s); err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "invalid --sort-property JSON: %v", err)
	}
	if rejectTrailing(dec) {
		return nil, exitcode.Newf(exitcode.Usage, "invalid --sort-property JSON: trailing data after the sort object")
	}
	// path is required (server `[String!]!`); an omitted or empty path would
	// serialize as "path":null / [] and fail server-side GraphQL validation, so
	// reject it here as a client-side usage error instead.
	if len(s.Path) == 0 {
		return nil, exitcode.Newf(exitcode.Usage, "invalid --sort-property JSON: \"path\" is required and must be non-empty")
	}
	return &s, nil
}

// whereNamesColumn reports whether ANY leaf in the predicate sets "field" —
// i.e. whether the author knows the key exists. It is deliberately not "every
// leaf": a predicate mixing an explicit "field":"data" leaf with a bare one is
// written by someone who has met the key, and warning them would be noise.
func whereNamesColumn(w *gqltypes.NodeWhereInput) bool {
	if w == nil {
		return false
	}
	if w.Field != nil {
		return true
	}
	for _, child := range w.And {
		if whereNamesColumn(child) {
			return true
		}
	}
	for _, child := range w.Or {
		if whereNamesColumn(child) {
			return true
		}
	}
	return whereNamesColumn(w.Not)
}

// WhereDefaultColumnNote returns the stderr note for the silent-zero case
// (#603), or "" when there is nothing to say.
//
// `--where` reads the `properties` JSONB column unless a leaf names the other
// one, and a node's free-form envelope lives in `data`. A predicate aimed at
// `data` that omits "field" therefore matches nothing and returns a plain `0` —
// not an error, not an empty-with-caveat, just a zero, which reads as evidence
// that the corpus has none. Two people on this team published a conclusion off
// one within an hour of each other.
//
// The note fires on exactly that shape: a predicate was given, NO leaf named a
// column, and the result was empty. It costs nothing when there are hits, and
// nothing for an author who already passes "field" — so it targets the silent
// zero without nagging a correct caller.
func WhereDefaultColumnNote(where *gqltypes.NodeWhereInput, hits int) string {
	if where == nil || hits > 0 || whereNamesColumn(where) {
		return ""
	}
	return `--where searched the "properties" column (the default) and matched nothing; ` +
		`a node's free-form envelope lives in "data" — add "field":"data" to a leaf to search that instead`
}
