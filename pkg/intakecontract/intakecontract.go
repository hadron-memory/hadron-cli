// Package intakecontract is the hadron-cli intake runner's rules for what an
// adapter may answer (hadron-cli#1977): the contract version, the bounds on
// stdout, a page, an id, a cursor and display text, and the validators the
// runner applies to `describe` and `list-untracked` answers.
//
// It exists so an adapter's conformance suite imports the rules at a pinned
// hadron-cli version instead of copying them. The runner (internal/intake)
// enforces exactly these rules through this package, so "published" and
// "enforced" cannot drift apart.
//
// This package is a convenience mirror of the runner's enforcement, not the
// contract itself. The contract (cor:agt:120:04) is the adapter's wire
// protocol: the calls, the opaque cursor, the external id passed through
// unchanged. Adapters may be written in any language; the documented contract
// stays the reference for those that cannot import Go.
//
// Only checks the runner runs live here. Rules a tracker or the server applies
// to an external id (no whitespace, no userinfo, and so on) are not the
// runner's and are not part of this package.
package intakecontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ContractVersion is the one contract version the runner speaks. An adapter
// that describes another is refused with a SkewError.
const ContractVersion = 1

// Bounds on what an adapter may hand back. An answer outside one is refused;
// nothing is truncated into a plausible answer.
const (
	MaxOutputBytes      = 1 << 20 // stdout of one call
	MaxStderrBytes      = 4096    // kept for the refusal message only
	MaxItemsPerPage     = 100
	MaxPages            = 50
	ServerExternalIDMax = 512  // the server's externalId cap, counted in code points
	MaxExternalIDLen    = 2048 // byte bound: 512 code points at up to 4 UTF-8 bytes each
	MaxCursorLen        = 4096
	MaxTextLen          = 1024 // adapter-supplied display text: name, title
)

// Item is one outside item the adapter says is not yet tracked.
type Item struct {
	// ExternalID is the item's canonical URL exactly as the adapter wrote it:
	// compared exactly by the server, so the CLI never normalises it.
	ExternalID string `json:"externalId"`
	Title      string `json:"title"`
}

// Describe is what `describe` answers.
type Describe struct {
	Name            string   `json:"name"`
	ContractVersion int      `json:"contractVersion"`
	Calls           []string `json:"calls"`
	Filters         []string `json:"filters"`
	Order           string   `json:"order"`
}

// Has reports whether the adapter says it answers call.
func (d Describe) Has(call string) bool {
	for _, c := range d.Calls {
		if c == call {
			return true
		}
	}
	return false
}

// ListAnswer is what `list-untracked` answers. NextCursor is nil (or absent)
// when the list is exhausted.
type ListAnswer struct {
	Items      []Item  `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// SkewError is a contract-version mismatch.
type SkewError struct{ Got int }

func (e *SkewError) Error() string {
	return fmt.Sprintf("the intake adapter speaks contract version %d; this CLI speaks version %d, so nothing was asked of it", e.Got, ContractVersion)
}

// DecodeAnswer applies the runner's check on any answer: raw is the whole of
// stdout, which must be at most MaxOutputBytes, valid UTF-8, and exactly one
// JSON object with nothing after it. It decodes into out.
func DecodeAnswer(stdout []byte, out any) error {
	if len(stdout) > MaxOutputBytes {
		return fmt.Errorf("more than %d bytes on stdout", MaxOutputBytes)
	}
	raw := bytes.TrimSpace(stdout)
	if len(raw) == 0 || raw[0] != '{' {
		return errors.New("stdout is not one JSON object")
	}
	if !utf8.Valid(raw) {
		return errors.New("stdout is not valid UTF-8")
	}
	// Unmarshal rejects text after the first value, so a second document or
	// trailing junk is refused here too.
	if err := json.Unmarshal(raw, out); err != nil {
		return errors.New("stdout is not one well-formed JSON object")
	}
	return nil
}

// CheckDescribe applies the runner's checks to a `describe` answer: the
// contract version (a *SkewError when it differs), then a non-blank name of
// at most MaxTextLen bytes.
func CheckDescribe(d Describe) error {
	if d.ContractVersion != ContractVersion {
		return &SkewError{Got: d.ContractVersion}
	}
	if strings.TrimSpace(d.Name) == "" {
		return errors.New("no adapter name")
	}
	return boundedText("name", d.Name, MaxTextLen)
}

// CheckPage applies the runner's checks to one `list-untracked` answer: at
// most MaxItemsPerPage items, each with a non-empty externalId within the
// byte and code-point bounds and a title within bound, and a nextCursor
// within bound.
func CheckPage(p ListAnswer) error {
	if len(p.Items) > MaxItemsPerPage {
		return fmt.Errorf("more than %d items in one page", MaxItemsPerPage)
	}
	for i, it := range p.Items {
		if it.ExternalID == "" {
			return fmt.Errorf("item %d has no externalId", i)
		}
		if err := boundedText("an externalId", it.ExternalID, MaxExternalIDLen); err != nil {
			return err
		}
		// The server refuses a whole externalIds batch if one entry exceeds
		// its 512-code-point rule, so it is enforced per item.
		if utf8.RuneCountInString(it.ExternalID) > ServerExternalIDMax {
			return fmt.Errorf("item %d externalId is longer than %d characters", i, ServerExternalIDMax)
		}
		if err := boundedText("a title", it.Title, MaxTextLen); err != nil {
			return err
		}
	}
	if p.NextCursor != nil {
		return boundedText("nextCursor", *p.NextCursor, MaxCursorLen)
	}
	return nil
}

func boundedText(field, s string, max int) error {
	if len(s) > max {
		return fmt.Errorf("%s is longer than %d bytes", field, max)
	}
	return nil
}
