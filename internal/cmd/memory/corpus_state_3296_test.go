package memory

import (
	"reflect"
	"testing"
)

// hadron-cli#3296: the memory DTO carries no corpus state. Memory.corpusState is
// memory-wide and derived from nodes (DRAFT while none is minted), so an ordinary
// memory with no minted node reads DRAFT too (checked live: a working memory with
// 0 specs answers DRAFT). A `memory get` line built on it would print on every
// such memory, and nothing in the answer tells a spec corpus from any other. The
// state belongs to `spec describe`, which reads it for a memory the caller names
// as a corpus. Nothing populates the field since the --draft-corpus read-back went
// (cli#3295), so the field and its renderer are removed rather than left dead.
func TestMemoryDTOCarriesNoCorpusState3296(t *testing.T) {
	for name, typ := range map[string]reflect.Type{
		"memoryDTO":       reflect.TypeOf(memoryDTO{}),
		"memoryDetailDTO": reflect.TypeOf(memoryDetailDTO{}),
	} {
		if _, found := typ.FieldByName("CorpusState"); found {
			t.Errorf("%s has a CorpusState field: nothing populates it (cli#3296)", name)
		}
	}
}
