package skilldoc

// hadron-cli#622 slice 2: the cross-host acceptance matrix, and the half of it
// this package can answer today.
//
// testdata/crosshost-acceptance.json states what each host should see for a
// set of declarations, lint inputs, collision sets and disk scenarios, each
// tied to the cor:agt:030 rule it follows from. It is written from those
// rules, not generated from any implementation, so it can disagree with one.
//
// This package knows ONE host. Declared, Lint and LintCollisions answer for
// claudeSkill only (the Codex projection comes from the server, through
// skillPlan), so this file checks the claudeSkill column of every live case.
// Both columns belong to hadron-server's host registry; server#1254 part 2 is
// to vendor this file and run them there.
// The `pending` cases need a writer or resolver that does not exist yet; they
// are skipped BY NAME, so `go test -v` lists what is still owed rather than
// reporting it as passed.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"regexp"
	"sort"
	"testing"
)

type xhDecl struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enable      bool   `json:"enable"`
	// EnableSet separates "switched off" from "never switched on" (D05): the
	// two publish identically, and only this field shows the default at work.
	EnableSet bool `json:"enableSet"`
}

type xhMatrix struct {
	Comment       string   `json:"$comment"`
	Version       int      `json:"version"`
	Hosts         []string `json:"hosts"`
	SharedPayload struct {
		Source     string         `json:"source"`
		Properties map[string]any `json:"properties"`
	} `json:"sharedPayload"`
	Declarations []struct {
		ID         string              `json:"id"`
		Title      string              `json:"title"`
		Contracts  []string            `json:"contracts"`
		Note       string              `json:"note"`
		Properties map[string]any      `json:"properties"`
		Expect     map[string]*xhDecl  `json:"expect"`
		Malformed  map[string][]string `json:"malformed"`
	} `json:"declarations"`
	Lint []struct {
		ID         string              `json:"id"`
		Title      string              `json:"title"`
		Note       string              `json:"note"`
		Contracts  []string            `json:"contracts"`
		Properties map[string]any      `json:"properties"`
		Expect     map[string][]string `json:"expect"`
	} `json:"lint"`
	Collisions []struct {
		ID        string   `json:"id"`
		Title     string   `json:"title"`
		Contracts []string `json:"contracts"`
		Nodes     []struct {
			URN        string         `json:"urn"`
			Properties map[string]any `json:"properties"`
		} `json:"nodes"`
		Expect map[string][]xhCollision `json:"expect"`
	} `json:"collisions"`
	Rendering []struct {
		ID        string   `json:"id"`
		Title     string   `json:"title"`
		Contracts []string `json:"contracts"`
		Case      string   `json:"case"`
		SameFile  bool     `json:"sameFile"`
	} `json:"rendering"`
	Pending []struct {
		ID        string   `json:"id"`
		Layer     string   `json:"layer"`
		PendingOn string   `json:"pendingOn"`
		Contracts []string `json:"contracts"`
		Given     string   `json:"given"`
		Expect    string   `json:"expect"`
	} `json:"pending"`
}

// loadMatrix fails on a missing file, an unknown field, or an empty section.
// A matrix that loads nothing passes every check below and measures nothing,
// so emptiness is a failure rather than a vacuous pass.
func loadMatrix(t *testing.T) *xhMatrix {
	t.Helper()
	raw, err := os.ReadFile("testdata/crosshost-acceptance.json")
	if err != nil {
		t.Fatalf("read matrix: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m xhMatrix
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("decode matrix: %v", err)
	}
	// Decode reads ONE value; a second one, or trailing garbage, would be
	// ignored while the matrix still passed (Copilot on #664).
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("matrix has content after its one JSON value: %v", err)
	}
	if m.Version != 1 {
		t.Fatalf("matrix version %d; this loader reads version 1", m.Version)
	}
	// `pending` is exempt: it asserts nothing, and it is MEANT to empty as the
	// writer and resolver land (Copilot on #664).
	for section, n := range map[string]int{
		"declarations": len(m.Declarations), "lint": len(m.Lint), "collisions": len(m.Collisions),
		"rendering": len(m.Rendering),
	} {
		if n == 0 {
			t.Fatalf("matrix section %q is empty; a section that loads nothing asserts nothing", section)
		}
	}
	return &m
}

// The literal every surface pins (hadron-portal#888). If this changes, the
// portal's checkbox test and the server's copy must change with it, which is
// the point: a rename on one side has to break a test on that side.
const sharedPayloadLiteral = `{"exports":{"claudeSkill":{"name":"hadron-demo","description":"Use when demoing.","enable":true},"codexSkill":{"name":"hadron-demo","description":"Use when demoing.","enable":true}}}`

func TestCrossHostSharedPayloadIsTheLiteral(t *testing.T) {
	m := loadMatrix(t)
	var want map[string]any
	if err := json.Unmarshal([]byte(sharedPayloadLiteral), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.SharedPayload.Properties, want) {
		t.Fatalf("sharedPayload drifted from the portal#888 literal\n got: %v\nwant: %v", m.SharedPayload.Properties, want)
	}
	if !reflect.DeepEqual(m.Declarations[0].Properties, want) {
		t.Fatalf("D01 must be the shared payload, got %v", m.Declarations[0].Properties)
	}
	if !reflect.DeepEqual(m.Hosts, []string{HostClaudeSkill, "codexSkill"}) {
		t.Fatalf("hosts = %v", m.Hosts)
	}
}

func TestCrossHostDeclarationsClaudeColumn(t *testing.T) {
	m := loadMatrix(t)
	for _, c := range m.Declarations {
		t.Run(c.ID, func(t *testing.T) {
			want, ok := c.Expect[HostClaudeSkill]
			if !ok {
				t.Fatalf("%s states no claudeSkill expectation (null is a statement; absence is not)", c.ID)
			}
			d, declared := Declared(c.Properties)
			switch {
			case want == nil && declared:
				t.Fatalf("%s: want no Claude declaration, got one at %s", c.Title, d.Key)
			case want != nil && !declared:
				t.Fatalf("%s: want a Claude declaration at %s, got none", c.Title, want.Key)
			case want != nil:
				got := xhDecl{Key: d.Key, Name: d.Name, Description: d.Description, Enable: d.Enable, EnableSet: d.EnableSet}
				if got != *want {
					t.Fatalf("%s:\n got %+v\nwant %+v", c.Title, got, *want)
				}
			}
			gotBad := Malformed(c.Properties)
			if gotBad == nil {
				gotBad = []string{}
			}
			if !reflect.DeepEqual(gotBad, c.Malformed[HostClaudeSkill]) {
				t.Fatalf("%s: malformed = %v, want %v", c.Title, gotBad, c.Malformed[HostClaudeSkill])
			}
		})
	}
}

func TestCrossHostLintClaudeColumn(t *testing.T) {
	m := loadMatrix(t)
	for _, c := range m.Lint {
		t.Run(c.ID, func(t *testing.T) {
			n := Node{
				URN:       "hrn:node:example.com:demo:tasks:" + c.ID,
				MemoryURN: "hrn:mem:example.com:demo", IsRunnable: true, Content: "Do the demo.\n", Properties: c.Properties,
			}
			rules := []string{}
			for _, f := range Lint(n) {
				rules = append(rules, f.Rule+":"+f.Severity)
			}
			sort.Strings(rules)
			if !reflect.DeepEqual(rules, c.Expect[HostClaudeSkill]) {
				t.Fatalf("rules = %v, want %v", rules, c.Expect[HostClaudeSkill])
			}
		})
	}
}

var collisionName = regexp.MustCompile(`stores skill name "([^"]*)"`)

type xhCollision struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

// Every member of a colliding group must be told, at error severity, exactly
// once. Reducing findings to the set of names would pass a host that flagged
// only one member, or flagged it as a warning (@codex on #664).
func TestCrossHostCollisionsClaudeColumn(t *testing.T) {
	m := loadMatrix(t)
	for _, c := range m.Collisions {
		t.Run(c.ID, func(t *testing.T) {
			nodes := make([]Node, 0, len(c.Nodes))
			for _, n := range c.Nodes {
				nodes = append(nodes, Node{URN: n.URN, Properties: n.Properties})
			}
			byName := map[string][]string{}
			for _, f := range LintCollisions(nodes) {
				mm := collisionName.FindStringSubmatch(f.Message)
				if f.Rule != "skill-name-collision" || f.Severity != SevError || mm == nil {
					t.Fatalf("unexpected finding %+v", f)
				}
				byName[mm[1]] = append(byName[mm[1]], f.URN)
			}
			got := []xhCollision{}
			for name, urns := range byName {
				sort.Strings(urns)
				got = append(got, xhCollision{Name: name, Members: urns})
			}
			sort.Slice(got, func(i, j int) bool { return got[i].Name < got[j].Name })
			if !reflect.DeepEqual(got, c.Expect[HostClaudeSkill]) {
				t.Fatalf("collisions = %+v, want %+v", got, c.Expect[HostClaudeSkill])
			}
		})
	}
}

// One renderer serves both hosts (cli#622 item 2): Render takes no host, so
// what may differ per host is only the declaration it is given. The Claude
// side renders what Declared ACTUALLY returns, not the expectation, so a
// reader regression shows up here too. The Codex side has to use the
// expectation, because this package has no Codex reader; the server checks
// that the expectation is what its registry reads.
func TestCrossHostRendering(t *testing.T) {
	m := loadMatrix(t)
	props := map[string]map[string]any{}
	expects := map[string]map[string]*xhDecl{}
	for _, c := range m.Declarations {
		props[c.ID], expects[c.ID] = c.Properties, c.Expect
	}
	const id, source, body = "01a0000000000000000000000000000r", "hrn:node:example.com:demo:tasks:demo", "Do the demo.\n"
	for _, r := range m.Rendering {
		t.Run(r.ID, func(t *testing.T) {
			cl, ok := Declared(props[r.Case])
			cx := expects[r.Case]["codexSkill"]
			if !ok || cx == nil {
				t.Fatalf("%s must name a case declared for both hosts, got %q", r.ID, r.Case)
			}
			fc, err := Render(id, cl.Name, source, cl.Description, body)
			if err != nil {
				t.Fatal(err)
			}
			fx, err := Render(id, cx.Name, source, cx.Description, body)
			if err != nil {
				t.Fatal(err)
			}
			if (fc == fx) != r.SameFile {
				t.Fatalf("files identical = %v, want %v", fc == fx, r.SameFile)
			}
			hc, hx := Hash(id, source, cl.Name, cl.Description, body), Hash(id, source, cx.Name, cx.Description, body)
			if (hc == hx) != r.SameFile {
				t.Fatalf("hashes identical = %v, want %v", hc == hx, r.SameFile)
			}
		})
	}
}

var citation = regexp.MustCompile(`^cor:[a-z]{3}:\d{3}(:\d{2})*$`)

// Every case names the rule it follows from, and every pending case names what
// it waits on. An expectation nobody can trace to a contract is an opinion.
func TestCrossHostMatrixIsTraceable(t *testing.T) {
	m := loadMatrix(t)
	ids := map[string]bool{}
	// An id names its section (D, L, C, R, P + two digits), so a case filed
	// under the wrong section, or pasted twice, is caught here.
	var section string
	check := func(id string, contracts []string) {
		if !regexp.MustCompile(`^` + section + `\d{2}$`).MatchString(id) {
			t.Errorf("case id %q does not match its section (%s + two digits)", id, section)
		}
		if ids[id] {
			t.Errorf("duplicate case id %s", id)
		}
		ids[id] = true
		if len(contracts) == 0 {
			t.Errorf("%s cites no contract", id)
		}
		for _, c := range contracts {
			if !citation.MatchString(c) {
				t.Errorf("%s: %q is not a spec citation", id, c)
			}
		}
	}
	section = "D"
	// A key that is not a host (a typo, `codex`) would be read by nobody and
	// the case would pass without saying anything about it (Copilot on #664).
	known := map[string]bool{}
	for _, h := range m.Hosts {
		known[h] = true
	}
	onlyHosts := func(id, what string, keys []string) {
		for _, k := range keys {
			if !known[k] {
				t.Errorf("%s: %s keyed by %q, which is not a host in the matrix", id, what, k)
			}
		}
	}
	for _, c := range m.Declarations {
		onlyHosts(c.ID, "expect", keysOf(c.Expect))
		onlyHosts(c.ID, "malformed", keysOf(c.Malformed))
	}
	for _, c := range m.Lint {
		onlyHosts(c.ID, "expect", keysOf(c.Expect))
	}
	for _, c := range m.Collisions {
		onlyHosts(c.ID, "expect", keysOf(c.Expect))
	}
	for _, c := range m.Declarations {
		check(c.ID, c.Contracts)
		for _, h := range m.Hosts {
			if _, ok := c.Expect[h]; !ok {
				t.Errorf("%s states no expectation for %s", c.ID, h)
			}
			if _, ok := c.Malformed[h]; !ok {
				t.Errorf("%s states no malformed list for %s", c.ID, h)
			}
		}
	}
	section = "L"
	for _, c := range m.Lint {
		check(c.ID, c.Contracts)
		for _, h := range m.Hosts {
			if _, ok := c.Expect[h]; !ok {
				t.Errorf("%s states no expectation for %s", c.ID, h)
			}
		}
	}
	section = "C"
	for _, c := range m.Collisions {
		check(c.ID, c.Contracts)
		for _, h := range m.Hosts {
			if _, ok := c.Expect[h]; !ok {
				t.Errorf("%s states no expectation for %s", c.ID, h)
			}
		}
	}
	section = "R"
	for _, r := range m.Rendering {
		check(r.ID, r.Contracts)
	}
	section = "P"
	for _, p := range m.Pending {
		check(p.ID, p.Contracts)
		if p.PendingOn == "" || p.Given == "" || p.Expect == "" || p.Layer == "" {
			t.Errorf("%s: a pending case needs layer, pendingOn, given and expect", p.ID)
		}
	}
}

// Pending cases are listed, not passed. When the writer or resolver lands,
// the case moves into an executable section and this skip goes with it.
func TestCrossHostPending(t *testing.T) {
	m := loadMatrix(t)
	for _, p := range m.Pending {
		t.Run(p.ID, func(t *testing.T) {
			t.Skipf("PENDING on %s (%s): given %s, expect %s", p.PendingOn, p.Layer, p.Given, p.Expect)
		})
	}
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
