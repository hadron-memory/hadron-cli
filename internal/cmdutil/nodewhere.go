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
