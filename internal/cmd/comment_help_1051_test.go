package cmd

import (
	"github.com/spf13/pflag"
	"strings"
	"testing"
)

// cli#1051 (replace and comment rows): two pins from the re-read of the comment pages.
//   - comment delete named one permission two ways ("a memory manager" for a single
//     comment, "a memory admin" for --thread). The server gates both on the same
//     manage access (comment-nodes contract: requireMemoryManageAccess), so the page
//     says "memory manager" throughout, in the text and in the --thread flag.
//   - comment add never said it needs write access, which comment create and
//     comment reply both say.
func TestCommentHelpWordingFromTheReRead1051(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	find := func(name string) (long, flat string, flags map[string]string) {
		c, _, err := root.Find([]string{"comment", name})
		if err != nil || c.Name() != name {
			t.Fatalf("comment %s: %v", name, err)
		}
		flags = map[string]string{}
		c.Flags().VisitAll(func(fl *pflag.Flag) { flags[fl.Name] = fl.Usage })
		return c.Long, strings.Join(strings.Fields(c.Long), " "), flags
	}
	_, del, delFlags := find("delete")
	if strings.Contains(del, "memory admin") || strings.Contains(delFlags["thread"], "memory admin") {
		t.Errorf("comment delete calls the permission both a memory manager and a memory admin: %q / %q", del, delFlags["thread"])
	}
	if !strings.Contains(del, "as a memory manager:") || !strings.Contains(delFlags["thread"], "a memory manager") {
		t.Errorf("comment delete must name the --thread permission a memory manager: %q / %q", del, delFlags["thread"])
	}
	_, add, _ := find("add")
	if !strings.Contains(add, "Needs write access to the memory.") {
		t.Error("comment add does not say it needs write access to the memory")
	}
}
