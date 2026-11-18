package cmd

import (
	"github.com/spf13/pflag"
	"strings"
	"testing"
)

// cli#1051 (replace and comment rows): pins from the re-read of the comment pages (comment add is outside the ten rows and belongs to the newer-pages audit child).
//   - comment delete named one permission two ways ("a memory manager" for a single
//     comment, "a memory admin" for --thread). The server gates both on the same
//     manage access (comment-nodes contract: requireMemoryManageAccess), so the page
//     says "memory manager" throughout, in the text and in the --thread flag.
func TestCommentHelpWordingFromTheReRead1051(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	find := func(name string) (flat string, flags map[string]string) {
		c, _, err := root.Find([]string{"comment", name})
		if err != nil || c.Name() != name {
			t.Fatalf("comment %s: %v", name, err)
		}
		flags = map[string]string{}
		c.Flags().VisitAll(func(fl *pflag.Flag) { flags[fl.Name] = fl.Usage })
		return strings.Join(strings.Fields(c.Long), " "), flags
	}
	del, delFlags := find("delete")
	if strings.Contains(del, "memory admin") || strings.Contains(delFlags["thread"], "memory admin") {
		t.Errorf("comment delete calls the permission both a memory manager and a memory admin: %q / %q", del, delFlags["thread"])
	}
	if !strings.Contains(del, "as a memory manager:") || !strings.Contains(delFlags["thread"], "a memory manager") {
		t.Errorf("comment delete must name the --thread permission a memory manager: %q / %q", del, delFlags["thread"])
	}
	// Evaluator Gemma (AT0.8437), P3: the flag said "(required)" while the --thread
	// paragraph says no --expected-revision is needed. Only delete changes.
	if got := delFlags["expected-revision"]; !strings.Contains(got, "(required unless --thread)") {
		t.Errorf("comment delete --expected-revision usage must say it is not needed with --thread: %q", got)
	}
	for _, verb := range []string{"edit", "resolve"} {
		_, flags := find(verb)
		if got := flags["expected-revision"]; !strings.HasSuffix(got, "(required)") {
			t.Errorf("comment %s --expected-revision usage changed: %q", verb, got)
		}
	}
}
