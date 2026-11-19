package cmd

import (
	"strings"
	"testing"
)

// cli#2593 (help-audit pointers from the #2579 round 1): three things a reader
// needs and the CLI already has. The pages point at the existing commands
// instead of leaving the reader stuck; none of this adds a command.
func flat2593(t *testing.T, path ...string) (long, usage string, flags map[string]string) {
	t.Helper()
	f, _ := testFactory(t)
	c, _, err := NewRootCmd(f).Find(path)
	if err != nil || c.Name() != path[len(path)-1] {
		t.Fatalf("%v: %v", path, err)
	}
	flags = map[string]string{}
	for _, name := range []string{"app-key"} {
		if fl := c.Flags().Lookup(name); fl != nil {
			flags[name] = fl.Usage
		}
	}
	return strings.Join(strings.Fields(c.Long), " "), c.UseLine(), flags
}

func TestMarketplacePointsAtTheListingFlags2593(t *testing.T) {
	long, _, _ := flat2593(t, "marketplace")
	for _, want := range []string{
		"hadron agent update --listed-on-marketplace",
		"hadron org update --marketplace",
		"hadron memory set --listed-on-marketplace",
		"=false to unlist",
		"not access",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("marketplace must say how to list or unlist your own (missing %q): %q", want, long)
		}
	}
}

func TestQuotaPagesSayWhatAnUnusedScopeShows2593(t *testing.T) {
	const sentence = "For a scope you have not used, the limit shown is the platform default."
	for _, path := range [][]string{{"quota"}, {"quota", "list"}} {
		long, _, _ := flat2593(t, path...)
		if !strings.Contains(long, sentence) {
			t.Errorf("%v must say %q: %q", path, sentence, long)
		}
	}
}

func TestUsageEventsAppKeyPointsAtTheKeyList2593(t *testing.T) {
	long, _, flags := flat2593(t, "usage", "events")
	if !strings.Contains(long, "the ID column of hadron app key list") {
		t.Errorf("usage events must say where the App key id comes from: %q", long)
	}
	if !strings.Contains(flags["app-key"], "hadron app key list") {
		t.Errorf("--app-key usage must point at hadron app key list: %q", flags["app-key"])
	}
}
