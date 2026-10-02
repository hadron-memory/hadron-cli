// Package approvalhash implements the server's versioned approval hash for a
// node's reader-visible title, abstract, and content (server #1591).
package approvalhash

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

const Version = "hadron-approval-v1"

// Fields are the three values covered by an approval. Title is Node.name.
// A nil pointer is distinct from a pointer to the empty string.
type Fields struct {
	Title    *string
	Abstract *string
	Content  *string
}

// Canonical returns the exact v1 bytes hashed by hadron-server. The field
// order and length-prefixed format are a wire contract; a change needs a new
// version rather than an edit to this serializer.
func Canonical(fields Fields) []byte {
	var out bytes.Buffer
	out.WriteString(Version)
	out.WriteByte('\n')
	writeField(&out, "title", fields.Title)
	writeField(&out, "abstract", fields.Abstract)
	writeField(&out, "content", fields.Content)
	return out.Bytes()
}

// Hash is the full lowercase hexadecimal SHA-256 of Canonical(fields).
func Hash(fields Fields) string {
	sum := sha256.Sum256(Canonical(fields))
	return hex.EncodeToString(sum[:])
}

func writeField(out *bytes.Buffer, name string, value *string) {
	out.WriteString(name)
	out.WriteByte(' ')
	if value == nil {
		out.WriteString("null\n")
		return
	}
	// The server normalizes CRLF and lone CR to LF before counting UTF-8
	// bytes. Do not trim or normalize Unicode.
	normalized := strings.ReplaceAll(strings.ReplaceAll(*value, "\r\n", "\n"), "\r", "\n")
	out.WriteString(strconv.Itoa(len(normalized)))
	out.WriteByte('\n')
	out.WriteString(normalized)
	out.WriteByte('\n')
}
