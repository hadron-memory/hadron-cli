package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// cli#2579: the evaluator's round 1 (Celine, AT0.8563.1) failed five of the 24
// residual rows. The pins below are the revisions' contract; each page is read
// the way a user reads it, so the checks are on the words the evaluator could
// not find (where the ID comes from; what preview and re-adopt are for and how
// they differ; no example that promises behaviour the server does not have).
func page2579(t *testing.T, path ...string) *cobra.Command {
	t.Helper()
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	c, _, err := root.Find(path)
	if err != nil || c.Name() != path[len(path)-1] {
		t.Fatalf("%v: %v", path, err)
	}
	return c
}

// pageText2579 is a page's Long text on one line, and its Example.
func pageText2579(t *testing.T, path ...string) (long, example string) {
	t.Helper()
	c := page2579(t, path...)
	return strings.Join(strings.Fields(c.Long), " "), c.Example
}

func TestGrantSetPagesSayWhereTheTriggerIDComesFrom2579(t *testing.T) {
	for _, path := range [][]string{{"automation", "grant", "set"}, {"automation", "team-snapshot-grant", "set"}} {
		long, _ := pageText2579(t, path...)
		usage := page2579(t, path...).Flags().Lookup("trigger-id").Usage
		text := long + " " + usage
		for _, want := range []string{"hadron schedule list", "hadron webhook list", "ID column"} {
			if !strings.Contains(text, want) {
				t.Errorf("%v must say where --trigger-id comes from (missing %q): %q", path, want, text)
			}
		}
	}
}

func TestPreviewAndReAdoptSayWhatTheyAreFor2579(t *testing.T) {
	previewLong, _ := pageText2579(t, "automation", "preview")
	adoptLong, _ := pageText2579(t, "automation", "re-adopt")
	for name, long := range map[string]string{"preview": previewLong, "re-adopt": adoptLong} {
		// Both open with their purpose, not mid-thought with the argument.
		if strings.HasPrefix(long, "Use a trigger row id") {
			t.Errorf("%s still opens with the argument instead of what the command is for: %q", name, long)
		}
		for _, want := range []string{"hadron schedule list", "hadron webhook list", "automation approval", "ID column"} {
			if !strings.Contains(long, want) {
				t.Errorf("%s must say how to get the ID and the name-based path (missing %q): %q", name, want, long)
			}
		}
	}
	if !strings.Contains(previewLong, "without changing anything") {
		t.Errorf("preview must say it changes nothing: %q", previewLong)
	}
	if strings.Contains(previewLong, "--expected-hash") {
		t.Errorf("preview carries re-adopt's flag paragraph: %q", previewLong)
	}
	if !strings.Contains(adoptLong, "automation preview") || !strings.Contains(adoptLong, "--expected-hash") {
		t.Errorf("re-adopt must send the reader to preview first and name --expected-hash: %q", adoptLong)
	}
	if strings.Contains(adoptLong, "graph:<server JSON>") {
		t.Errorf("re-adopt carries preview's --json shape paragraph: %q", adoptLong)
	}
}

func TestBillingPricesSetExamplesPromiseNoTier2Behaviour2579(t *testing.T) {
	_, example := pageText2579(t, "billing", "prices", "set")
	if strings.Contains(example, "--tier") {
		t.Errorf("an example sets --tier, which the page cannot show is applied: %s", example)
	}
	long, _ := pageText2579(t, "billing", "prices", "set")
	if !strings.Contains(long, "nothing reads them yet") || !strings.Contains(long, "leave --tier off") {
		t.Errorf("the page must say tiers 2 and 3 are stored but not read yet and to leave --tier off: %q", long)
	}
}
