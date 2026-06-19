package nodedoc

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// RenderJSON emits the Document as a single pretty-printed JSON object — the
// canonical shape ParseJSON reads, so json↔json is trivially lossless and
// md↔json share the in-memory Document. HTML escaping is off so content with
// <, >, & stays literal. The output ends in a newline (json.Encoder framing).
func RenderJSON(doc *Document) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ParseJSON is the inverse of RenderJSON. Edges is normalized to a non-nil slice
// so a Document's shape is stable ([] not null) across the codecs.
func ParseJSON(data []byte) (*Document, error) {
	var doc Document
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parsing JSON node: %w", err)
	}
	if doc.Edges == nil {
		doc.Edges = []Edge{}
	}
	return &doc, nil
}
