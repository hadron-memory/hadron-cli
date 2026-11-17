package coveragematrix

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseReadsEveryDisposition(t *testing.T) {
	src := `# a comment
[Query] 4: header counts are not read
agents  command agent list
appNodes  gap #63
appRuns  gap #1180: run list --status
me  excluded not-a-capability: the same read as authContext
[Filters]
Query.agents(filter.ownedByMe)  flag agent list --owned-by-me
Query.scopes(filter.ownerRef)  flag scope list --owner-org --owner-app
Query.users(filter.query)  flag user search [query]
[Arguments]
Mutation.createApp(role)  UNCLASSIFIED
`
	got, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []Entry{
		{Section: "Query", Key: "agents", Kind: Command, Command: "agent list", Line: 3},
		{Section: "Query", Key: "appNodes", Kind: Gap, Issue: 63, Line: 4},
		{Section: "Query", Key: "appRuns", Kind: Gap, Issue: 1180, Reason: "run list --status", Line: 5},
		{Section: "Query", Key: "me", Kind: Excluded, Class: "not-a-capability", Reason: "the same read as authContext", Line: 6},
		{Section: "Filters", Key: "Query.agents(filter.ownedByMe)", Kind: Flag, Command: "agent list", Flags: []string{"--owned-by-me"}, Line: 8},
		{Section: "Filters", Key: "Query.scopes(filter.ownerRef)", Kind: Flag, Command: "scope list", Flags: []string{"--owner-org", "--owner-app"}, Line: 9},
		{Section: "Filters", Key: "Query.users(filter.query)", Kind: Flag, Command: "user search", Flags: []string{"[query]"}, Line: 10},
		{Section: "Arguments", Key: "Mutation.createApp(role)", Kind: Unclassified, Line: 12},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%+v\nwant\n%+v", got, want)
	}
	// Disposition is the inverse of the parse, so a regeneration round-trips.
	for _, e := range got {
		line := strings.Split(src, "\n")[e.Line-1]
		if back := e.Key + "  " + e.Disposition(); back != line {
			t.Errorf("round trip: %q, want %q", back, line)
		}
	}
}

// The grammar is strict so a typo cannot read as a disposition.
func TestParseRefusesMalformedLines(t *testing.T) {
	cases := map[string]string{
		"one space":                "[Query]\nagents command agent list\n",
		"unknown kind":             "[Query]\nagents  covered agent list\n",
		"gap without an issue":     "[Query]\nagents  gap later\n",
		"gap #0":                   "[Query]\nagents  gap #0\n",
		"unknown class":            "[Query]\nagents  excluded rarely-used: nobody asked\n",
		"exclusion without reason": "[Query]\nagents  excluded internal:  \n",
		"flag without a flag":      "[Filters]\nQuery.a(filter.b)  flag agent list\n",
		"flag without a command":   "[Filters]\nQuery.a(filter.b)  flag --mine\n",
		"command with a flag":      "[Query]\nagents  command agent list --mine\n",
		"entry before a section":   "agents  command agent list\n",
		"unknown section":          "[Types]\nAgent  command agent get\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(src); err == nil {
				t.Errorf("want a refusal for %q", src)
			}
		})
	}
}

// The exclusion classes are exactly the ones cor:api:240:02 leaves room for.
func TestExclusionClassesAreTheRuledOnes(t *testing.T) {
	want := []string{"deprecated", "graphical", "host-protocol", "internal", "not-a-capability"}
	var got []string
	for c := range ExclusionClasses {
		got = append(got, c)
	}
	if len(got) != len(want) {
		t.Fatalf("classes %v, want %v", got, want)
	}
	for _, c := range want {
		if _, ok := ExclusionClasses[c]; !ok {
			t.Errorf("missing class %q", c)
		}
		if !strings.Contains(classList(), c) {
			t.Errorf("the refusal message does not name %q", c)
		}
	}
}
