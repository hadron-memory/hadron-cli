package api

import (
	"errors"
)

// slugFixedCode is hadron-server#5235's refusal of updateMemory(urn:) on an App personal or private memory.
const slugFixedCode = "APP_MEMORY_SLUG_FIXED"

// slugFixedNote is what the server's own sentence ("A personal or private memory in an App keeps its slug. Its
// display name can be changed.") leaves out, and a script that passed --name beside --slug needs: the refusal is of
// the whole call, so the name was not saved either (cli#3242). The CLI states it; it decides nothing by class.
const slugFixedNote = " Nothing in this call was applied (a --name passed with --slug was not saved): the memory keeps its address; run `memory set <ref> --name <name>` alone to change its display name."

// slugFixedClasses are the two classes the server names in the refusal. Any other value is not echoed: the field is
// a fixed set, never free server text.
var slugFixedClasses = map[string]bool{"personal": true, "private": true}

// slugFixedError is the mapped APP_MEMORY_SLUG_FIXED refusal: the mapped error's exit class and message, plus the
// nothing-applied note, the server's code and (when it is one of the two known) the memory class for the --json
// envelope.
type slugFixedError struct {
	error
	class string
}

func (e *slugFixedError) Unwrap() error       { return e.error }
func (e *slugFixedError) Error() string       { return e.error.Error() + slugFixedNote }
func (e *slugFixedError) ServerCode() string  { return slugFixedCode }
func (e *slugFixedError) MemoryClass() string { return e.class }

// WithSlugFixed adds the nothing-applied note, the server code and the memory class to mapped when raw is an
// APP_MEMORY_SLUG_FIXED refusal, and returns mapped untouched otherwise.
func WithSlugFixed(mapped, raw error) error {
	if mapped == nil {
		return nil
	}
	var found bool
	var class string
	for _, e := range graphQLErrors(raw) {
		if extensionCode(e) != slugFixedCode {
			continue
		}
		found = true
		if c, ok := e.Extensions["memoryClass"].(string); ok && slugFixedClasses[c] {
			class = c
		}
	}
	if !found {
		return mapped
	}
	return &slugFixedError{error: mapped, class: class}
}

// SlugFixedMemoryClass is the memory class a refusal made by WithSlugFixed carries, or "".
func SlugFixedMemoryClass(err error) string {
	var s *slugFixedError
	if errors.As(err, &s) {
		return s.class
	}
	return ""
}
