package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// #308: `memory` is an upsert (`set`), but every other noun spells create
// `create`, so a user who learned `node/org/agent create` first dead-ends.
// The aliases make the guess land.
func TestMemorySetHasCreateAliases(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	var mem *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "memory" {
			mem = c
			break
		}
	}
	if mem == nil {
		t.Fatal("memory command group not found")
	}
	for _, alias := range []string{"create", "new", "add"} {
		got, _, err := mem.Find([]string{alias})
		if err != nil || got == nil || got.Name() != "set" {
			t.Errorf("`memory %s` must resolve to `memory set`, got %v (err %v)", alias, got, err)
		}
	}
}
