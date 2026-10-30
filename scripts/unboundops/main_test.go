package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gql "github.com/vektah/gqlparser/v2/ast"

	"github.com/hadron-memory/hadron-cli/internal/coveragematrix"
)

const schemaFixture = `
type Query {
  me: User
  agents(filter: AgentFilter, limit: Int): [Agent!]!
  "A doc comment must not be mistaken for a field."
  nodes(where: Where): [Agent!]!
  oldAgents: [Agent!]! @deprecated(reason: "Use agents.")
}
type Mutation {
  beginUpload(input: UploadInput!): Boolean
  createThing(input: ThingInput!): Boolean
  link(input: LinkInput!): Boolean
}
input AgentFilter { ownedByMe: Boolean type: String }
input UploadInput { memoryId: ID! filename: String requestId: String }
input ThingInput { name: String note: String }
input Where { and: [Where!] column: String value: String }
input LinkInput { edges: [EdgeIn!] meta: EdgeIn }
input EdgeIn { loc: String weight: Int }
type Agent { id: ID! }
type User { id: ID! }
`

const docFixture = `
fragment F on Agent { id }
query Me { me { id } }
query Agents($filter: AgentFilter, $limit: Int) {
  agents(filter: $filter, limit: $limit) { ...F }
}
query Nodes($where: Where) { nodes(where: $where) { id } }
mutation BeginUpload($memoryId: ID!, $filename: String) {
  beginUpload(input: { memoryId: $memoryId, filename: $filename })
}
mutation CreateThing($input: ThingInput!) { createThing(input: $input) }
`

func fixtureTypes(t *testing.T) map[string]*gql.Definition {
	t.Helper()
	types, err := parseSchema(schemaFixture)
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	return types
}

func fixtureOps(t *testing.T, doc string) []docOp {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ops.graphql")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	ops, err := parseDocuments([]string{path})
	if err != nil {
		t.Fatalf("parse documents: %v", err)
	}
	return ops
}

func calls(names ...string) *goUses {
	u := &goUses{funcs: map[string]bool{}, fields: map[string]bool{}, decoded: map[string]bool{}}
	for _, n := range names {
		u.funcs[n] = true
	}
	return u
}

func keysOf(items []item) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.key)
	}
	return out
}

func entries(t *testing.T, matrix string) []coveragematrix.Entry {
	t.Helper()
	e, err := coveragematrix.Parse(matrix)
	if err != nil {
		t.Fatalf("parse matrix: %v", err)
	}
	return e
}

func hasProblem(problems []string, want string) bool {
	for _, p := range problems {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}

// A document the CLI never calls binds a field without reaching it: the
// field is not reached, so a `command` line for it is refused.
func TestDocumentNobodyCallsIsNotReached(t *testing.T) {
	types, ops := fixtureTypes(t), fixtureOps(t, docFixture)
	f := deriveFacts(types, ops, calls("Agents"))
	if f.roots["Query.me"] {
		t.Fatal("me is selected by an operation nobody calls; it must not count as reached")
	}
	if !f.roots["Query.agents"] {
		t.Fatal("agents is selected by a called operation; it must count as reached")
	}
	_, problems := build(f, entries(t, "[Query]\nme  command auth whoami\n"))
	if !hasProblem(problems, "[Query] me: no production code calls") {
		t.Errorf("a command on an unreached field must be refused, got %v", problems)
	}
	f = deriveFacts(types, ops, calls("Agents", "Me"))
	_, problems = build(f, entries(t, "[Query]\nme  command auth whoami\n"))
	if hasProblem(problems, "[Query] me:") {
		t.Errorf("me is reached once Me is called, got %v", problems)
	}
}

// An alias selects the same server field again: what is reached is the
// field's own name, with the union of the arguments.
func TestAliasCountsTheFieldAndUnionsArguments(t *testing.T) {
	ops := fixtureOps(t, `query Two($f: AgentFilter, $l: Int) { a: agents(filter: $f) { id } b: agents(limit: $l) { id } }`)
	u := calls("Two")
	u.fields["AgentFilter.ownedByMe"] = true
	u.fields["AgentFilter.type"] = true
	f := deriveFacts(fixtureTypes(t), ops, u)
	if !f.roots["Query.agents"] || f.roots["Query.a"] || f.roots["Query.b"] {
		t.Fatalf("an alias must reach the field, not the alias name: %v", f.roots)
	}
	args, filters := f.argumentItems()
	if len(args) != 0 {
		t.Errorf("both aliases' arguments are passed, yet Arguments lists %v", keysOf(args))
	}
	for _, it := range filters {
		if !it.reached {
			t.Errorf("%s is set through the aliased filter, yet reads as not reached", it.key)
		}
	}
}

// A field the server adds to the filter of a query the CLI already reaches
// is a new line nobody has decided: the check refuses it.
func TestNewFilterFieldOnAReachedQueryIsUnclassified(t *testing.T) {
	u := calls("Agents")
	u.fields["AgentFilter.ownedByMe"] = true
	f := deriveFacts(fixtureTypes(t), fixtureOps(t, docFixture), u)
	out, problems := build(f, entries(t, "[Query]\nagents  command agent list\n[Filters]\nQuery.agents(filter.ownedByMe)  flag agent list --owned-by-me\n"))
	if !hasProblem(problems, "[Filters] Query.agents(filter.type): UNCLASSIFIED") {
		t.Errorf("a filter field nobody decided must be refused, got %v", problems)
	}
	if !strings.Contains(out, "Query.agents(filter.type)  UNCLASSIFIED\n") {
		t.Errorf("the regenerated matrix must carry the new line:\n%s", out)
	}
	if hasProblem(problems, "filter.ownedByMe") {
		t.Errorf("a set filter field with a flag must stand, got %v", problems)
	}
}

func TestDispositionsThatContradictTheFacts(t *testing.T) {
	f := deriveFacts(fixtureTypes(t), fixtureOps(t, docFixture), calls("Agents", "BeginUpload"))
	cases := []struct {
		name, matrix, want string
	}{
		{"gap on a reached root", "[Query]\nagents  gap #12\n", "[Query] agents: production code now reaches it"},
		{"command on an unreached root", "[Mutation]\ncreateThing  command thing create\n", "[Mutation] createThing: no production code calls"},
		{"flag on a root", "[Query]\nagents  flag agent list --mine\n", "a root field is reached by a `command`"},
		{"flag on an unset filter field", "[Filters]\nQuery.agents(filter.type)  flag agent list --type\n", "production code never sets it"},
		{"command in Arguments", "[Arguments]\nMutation.beginUpload(input.requestId)  command asset upload\n", "the Arguments section lists only"},
		{"unclassified root", "[Query]\nnodes  UNCLASSIFIED\n", "[Query] nodes: UNCLASSIFIED"},
		{"duplicate", "[Query]\nagents  command agent list\nagents  command agent get\n", "[Query] agents: listed twice"},
		{"orphan", "[Query]\nretiredField  gap #3\n", "[Query] retiredField: orphan"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, problems := build(f, entries(t, c.matrix))
			if !hasProblem(problems, c.want) {
				t.Errorf("want a problem containing %q, got %v", c.want, problems)
			}
		})
	}
}

// An orphan is reported AND dropped: the regenerated file never carries a
// line for a field the schema no longer has.
func TestOrphanIsDroppedFromTheRegeneratedMatrix(t *testing.T) {
	f := deriveFacts(fixtureTypes(t), nil, calls())
	out, _ := build(f, entries(t, "[Query]\nretiredField  gap #3\n"))
	if strings.Contains(out, "retiredField") {
		t.Errorf("an orphan must leave the regenerated matrix:\n%s", out)
	}
}

// An unreached field the server deprecated is excluded with the server's own
// reason, without anyone writing a line; a line written by hand stands.
func TestUnreachedDeprecatedFieldIsExcludedWithTheServerReason(t *testing.T) {
	f := deriveFacts(fixtureTypes(t), nil, calls())
	out, problems := build(f, nil)
	if !strings.Contains(out, "oldAgents  excluded deprecated: @deprecated: Use agents.\n") {
		t.Errorf("want the derived exclusion, got:\n%s", out)
	}
	if hasProblem(problems, "oldAgents") {
		t.Errorf("a derived exclusion is decided, got %v", problems)
	}
	out, _ = build(f, entries(t, "[Query]\noldAgents  gap #9\n"))
	if !strings.Contains(out, "oldAgents  gap #9\n") {
		t.Errorf("a hand-written line must survive regeneration:\n%s", out)
	}
}

// An object literal in the document passes exactly the fields it spells out;
// the others are gaps, at their own path.
func TestObjectLiteralPassesOnlyTheFieldsItSpells(t *testing.T) {
	args, _ := deriveFacts(fixtureTypes(t), fixtureOps(t, docFixture), calls("BeginUpload")).argumentItems()
	if got := strings.Join(keysOf(args), " "); got != "Mutation.beginUpload(input.requestId)" {
		t.Errorf("want only the unspelled field as a gap, got %q", got)
	}
}

// Below a variable the Go code decides: a field the code never names is a
// gap, unless JSON is decoded straight into the input.
func TestVariableInputFieldsFollowTheGoCode(t *testing.T) {
	types, ops := fixtureTypes(t), fixtureOps(t, docFixture)
	u := calls("CreateThing")
	u.fields["ThingInput.name"] = true
	args, _ := deriveFacts(types, ops, u).argumentItems()
	if got := strings.Join(keysOf(args), " "); got != "Mutation.createThing(input.note)" {
		t.Errorf("want the unnamed field as the only gap, got %q", got)
	}
	u.decoded["ThingInput"] = true
	args, _ = deriveFacts(types, ops, u).argumentItems()
	if len(args) != 0 {
		t.Errorf("decoding JSON into the input reaches every field, got %v", keysOf(args))
	}
}

// A list of object literals passes each element's fields at the list's own
// path; a variable element hands that path to the Go code.
func TestListOfObjectLiteralsPassesTheirFields(t *testing.T) {
	ops := fixtureOps(t, `mutation Link($loc: String) { link(input: { edges: [{ loc: $loc }] }) }`)
	args, _ := deriveFacts(fixtureTypes(t), ops, calls("Link")).argumentItems()
	if got := strings.Join(keysOf(args), " "); got != "Mutation.link(input.edges.weight) Mutation.link(input.meta)" {
		t.Errorf("want the element's unspelled field and the unpassed one as gaps, got %q", got)
	}
	ops = fixtureOps(t, `mutation Link($e: EdgeIn!) { link(input: { edges: [{ loc: "x" }, $e] }) }`)
	u := calls("Link")
	u.fields["EdgeIn.weight"] = true
	args, _ = deriveFacts(fixtureTypes(t), ops, u).argumentItems()
	if got := strings.Join(keysOf(args), " "); got != "Mutation.link(input.meta)" {
		t.Errorf("a variable element lets the Go code reach the rest, got %q", got)
	}
}

// Decoding JSON into an input reaches its nested inputs too, through objects
// and lists alike.
func TestDecodedInputReachesNestedInputs(t *testing.T) {
	ops := fixtureOps(t, `mutation Link($input: LinkInput!) { link(input: $input) }`)
	u := calls("Link")
	u.decoded["LinkInput"] = true
	args, _ := deriveFacts(fixtureTypes(t), ops, u).argumentItems()
	if len(args) != 0 {
		t.Errorf("decoding JSON into LinkInput reaches every nested field, got %v", keysOf(args))
	}
}

// A recursive input is walked once per type per path, so the walk ends, and
// a field the code never sets is reported at the shallowest path.
func TestRecursiveInputEnds(t *testing.T) {
	u := calls("Nodes")
	u.fields["Where.and"] = true
	u.fields["Where.column"] = true
	args, _ := deriveFacts(fixtureTypes(t), fixtureOps(t, docFixture), u).argumentItems()
	if got := strings.Join(keysOf(args), " "); got != "Query.nodes(where.value)" {
		t.Errorf("want one gap at the shallowest path, got %q", got)
	}
}

// An argument a reached operation never passes is a gap; an unpassed filter
// lists each of its fields instead, under Filters.
func TestUnpassedArgumentsAndFilters(t *testing.T) {
	ops := fixtureOps(t, `query OnlyLimit($l: Int) { agents(limit: $l) { id } }`)
	args, filters := deriveFacts(fixtureTypes(t), ops, calls("OnlyLimit")).argumentItems()
	if len(args) != 0 {
		t.Errorf("an unpassed filter belongs under Filters, got Arguments %v", keysOf(args))
	}
	if got := strings.Join(keysOf(filters), " "); got != "Query.agents(filter.ownedByMe) Query.agents(filter.type)" {
		t.Errorf("want each filter field, got %q", got)
	}
	for _, it := range filters {
		if it.reached {
			t.Errorf("%s is not passed, so it is not reached", it.key)
		}
	}
}

// The loader reads real type-checked Go: a call from production code reaches
// an operation, a call from a test does not, a keyed literal sets a field and
// json.Unmarshal into an input sets them all.
func TestLoadGoUsesReadsProductionCodeOnly(t *testing.T) {
	inputs := map[string]bool{"AgentFilter": true, "ThingInput": true}
	u, err := loadGoUses(filepath.Join("testdata", "fixture"), []string{"./..."}, inputs, nil)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	if !u.funcs["Agents"] {
		t.Error("Agents is called from production code")
	}
	if u.funcs["Me"] {
		t.Error("Me is called only from a test; tests never count")
	}
	if !u.fields["AgentFilter.ownedByMe"] || u.fields["AgentFilter.type"] {
		t.Errorf("only the keyed field is set, got %v", u.fields)
	}
	if !u.decoded["ThingInput"] {
		t.Error("json.Unmarshal into *gen.ThingInput reaches the whole input")
	}
}
