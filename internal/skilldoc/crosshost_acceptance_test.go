package skilldoc

// hadron-cli#622 slice 2: the cross-host acceptance matrix, and the half of it
// this package can answer today.
//
// testdata/crosshost-acceptance.json states what each host should see for a
// set of declarations, lint inputs, collision sets and disk scenarios, each
// tied to the cor:agt:030 rule it follows from. It is written from those
// rules, not generated from any implementation, so it can disagree with one.
//
// Since #665 this package knows every host (Hosts), so this file checks BOTH
// columns of every live case, as hadron-server does from its vendored copy
// (src/lib/skilldoc/crosshost.acceptance.test.ts, under a sha256 pin).
// The `pending` cases need a writer or resolver that does not exist yet; they
// are skipped BY NAME, so `go test -v` lists what is still owed rather than
// reporting it as passed.

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	// Enable and EnableSet are pointers so an OMITTED field is an error rather
	// than a silent false (@codex on #664): false is the value these cases
	// exist to pin, so it must be written, not defaulted.
	Enable *bool `json:"enable"`
	// EnableSet separates "switched off" from "never switched on" (D05): the
	// two publish identically, and only this field shows the default at work.
	EnableSet *bool `json:"enableSet"`
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
		// Unknown is UnknownHostKeys: the exports keys no host owns, in UTF-8
		// byte order. Host-free, so it is not a per-host column (#676).
		Unknown []string `json:"unknown"`
	} `json:"declarations"`
	Lint []struct {
		ID         string              `json:"id"`
		Title      string              `json:"title"`
		Note       string              `json:"note"`
		Contracts  []string            `json:"contracts"`
		Properties map[string]any      `json:"properties"`
		Expect     map[string][]string `json:"expect"`
		// Unhosted is the findings that belong to NO host (an unknown exports
		// key, cor:agt:030:06): LintUnknownHostKeys, as rule:severity.
		Unhosted []string `json:"unhosted"`
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
		SameFile  *bool    `json:"sameFile"`
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

// decodeMatrix refuses an unknown field, a version other than 1, anything
// after the one JSON value, and an empty executable section. A matrix that
// loads nothing passes every check below and measures nothing, so emptiness
// is an error rather than a vacuous pass. `pending` is exempt: it asserts
// nothing, and it is MEANT to empty as the writer and resolver land.
func decodeMatrix(raw []byte) (*xhMatrix, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m xhMatrix
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode matrix: %w", err)
	}
	// Decode reads ONE value; a second one, or trailing garbage, would be
	// ignored while the matrix still passed (Copilot on #664).
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("matrix has content after its one JSON value: %v", err)
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("matrix version %d; this loader reads version 1", m.Version)
	}
	if err := requireKeys(raw); err != nil {
		return nil, err
	}
	for _, c := range m.Declarations {
		for host, d := range c.Expect {
			if d != nil && (d.Enable == nil || d.EnableSet == nil) {
				return nil, fmt.Errorf("%s/%s: enable and enableSet must both be written; an omitted bool would read as false", c.ID, host)
			}
		}
	}
	for _, r := range m.Rendering {
		if r.SameFile == nil {
			return nil, fmt.Errorf("%s: sameFile must be written; an omitted bool would read as false", r.ID)
		}
	}
	for _, s := range []struct {
		name string
		n    int
	}{
		{"declarations", len(m.Declarations)}, {"lint", len(m.Lint)},
		{"collisions", len(m.Collisions)}, {"rendering", len(m.Rendering)},
	} {
		if s.n == 0 {
			return nil, fmt.Errorf("matrix section %q is empty; a section that loads nothing asserts nothing", s.name)
		}
	}
	return &m, nil
}

// requiredKeys is every key each case object must WRITE. An omitted key
// decodes as its zero value (a nil map, an empty list, false), which is also
// what several negative rows expect, so an omission would pass as a real
// expectation (@codex on #664, three rounds, one field at a time). This
// checks presence on the raw JSON for every field at once instead.
var requiredKeys = map[string][]string{
	"declarations": {"id", "title", "contracts", "properties", "expect", "malformed", "unknown"},
	"lint":         {"id", "title", "contracts", "properties", "expect", "unhosted"},
	"collisions":   {"id", "title", "contracts", "nodes", "expect"},
	"rendering":    {"id", "title", "contracts", "case", "sameFile"},
	"pending":      {"id", "layer", "pendingOn", "contracts", "given", "expect"},
}

var requiredDeclKeys = []string{"key", "name", "description", "enable", "enableSet"}

func requireKeys(raw []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return err
	}
	doc := map[string][]map[string]json.RawMessage{}
	for section := range requiredKeys {
		var cases []map[string]json.RawMessage
		if err := json.Unmarshal(top[section], &cases); err != nil {
			return fmt.Errorf("section %q: %w", section, err)
		}
		doc[section] = cases
	}
	has := func(obj map[string]json.RawMessage, where string, keys []string) error {
		for _, k := range keys {
			if _, ok := obj[k]; !ok {
				return fmt.Errorf("%s omits %q; an omitted key decodes as a zero value that can pass as an expectation", where, k)
			}
		}
		return nil
	}
	for section, keys := range requiredKeys {
		for i, c := range doc[section] {
			where := fmt.Sprintf("%s[%d] %s", section, i, c["id"])
			if err := has(c, where, keys); err != nil {
				return err
			}
			switch section {
			case "declarations":
				var exp map[string]map[string]json.RawMessage
				if err := json.Unmarshal(c["expect"], &exp); err != nil {
					return fmt.Errorf("%s expect: %w", where, err)
				}
				for host, d := range exp {
					if d != nil {
						if err := has(d, where+" expect."+host, requiredDeclKeys); err != nil {
							return err
						}
					}
				}
			case "collisions":
				var nodes []map[string]json.RawMessage
				if err := json.Unmarshal(c["nodes"], &nodes); err != nil {
					return fmt.Errorf("%s nodes: %w", where, err)
				}
				for j, n := range nodes {
					if err := has(n, fmt.Sprintf("%s nodes[%d]", where, j), []string{"urn", "properties"}); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func loadMatrix(t *testing.T) *xhMatrix {
	t.Helper()
	raw, err := os.ReadFile("testdata/crosshost-acceptance.json")
	if err != nil {
		t.Fatalf("read matrix: %v", err)
	}
	m, err := decodeMatrix(raw)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// The loader's refusals are what keep the matrix from going quietly
// malformed or vacuous, so each is exercised against a variant of the real
// fixture, not only by hand (Copilot on #664). The unmodified fixture is the
// positive control: if it failed, every red below would mean nothing.
func TestCrossHostLoaderRefuses(t *testing.T) {
	raw, err := os.ReadFile("testdata/crosshost-acceptance.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeMatrix(raw); err != nil {
		t.Fatalf("positive control: the checked-in fixture must load: %v", err)
	}
	edit := func(f func(map[string]any)) []byte {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		f(doc)
		out, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for name, in := range map[string][]byte{
		"unknown field":           edit(func(d map[string]any) { d["surprise"] = true }),
		"version 2":               edit(func(d map[string]any) { d["version"] = 2 }),
		"trailing value":          append(append([]byte{}, raw...), []byte("{}")...),
		"trailing garbage":        append(append([]byte{}, raw...), []byte("xx")...),
		"empty declarations":      edit(func(d map[string]any) { d["declarations"] = []any{} }),
		"empty lint":              edit(func(d map[string]any) { d["lint"] = []any{} }),
		"empty collisions":        edit(func(d map[string]any) { d["collisions"] = []any{} }),
		"empty rendering":         edit(func(d map[string]any) { d["rendering"] = []any{} }),
		"unknown field in case":   edit(func(d map[string]any) { d["lint"].([]any)[0].(map[string]any)["surprise"] = 1 }),
		"omitted lint properties": edit(func(d map[string]any) { delete(d["lint"].([]any)[4].(map[string]any), "properties") }),
		"omitted declaration malformed": edit(func(d map[string]any) {
			delete(d["declarations"].([]any)[0].(map[string]any), "malformed")
		}),
		"omitted collision node properties": edit(func(d map[string]any) {
			delete(d["collisions"].([]any)[0].(map[string]any)["nodes"].([]any)[0].(map[string]any), "properties")
		}),
		"omitted pending given": edit(func(d map[string]any) { delete(d["pending"].([]any)[0].(map[string]any), "given") }),
		"omitted expected name": edit(func(d map[string]any) {
			delete(d["declarations"].([]any)[0].(map[string]any)["expect"].(map[string]any)["claudeSkill"].(map[string]any), "name")
		}),
		"omitted sameFile": edit(func(d map[string]any) { delete(d["rendering"].([]any)[1].(map[string]any), "sameFile") }),
		"omitted enable": edit(func(d map[string]any) {
			delete(d["declarations"].([]any)[4].(map[string]any)["expect"].(map[string]any)["codexSkill"].(map[string]any), "enable")
		}),
		"omitted enableSet": edit(func(d map[string]any) {
			delete(d["declarations"].([]any)[4].(map[string]any)["expect"].(map[string]any)["codexSkill"].(map[string]any), "enableSet")
		}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeMatrix(in); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
	t.Run("empty pending is allowed", func(t *testing.T) {
		if _, err := decodeMatrix(edit(func(d map[string]any) { d["pending"] = []any{} })); err != nil {
			t.Fatalf("pending must be allowed to drain: %v", err)
		}
	})
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

// matrixHost resolves a matrix host key to this package's row, failing the
// test for a key the host table does not carry.
func matrixHost(t *testing.T, key string) Host {
	t.Helper()
	h, ok := HostFor(key)
	if !ok {
		t.Fatalf("matrix host %q has no row in Hosts", key)
	}
	return h
}

func TestCrossHostDeclarations(t *testing.T) {
	m := loadMatrix(t)
	for _, key := range m.Hosts {
		h := matrixHost(t, key)
		for _, c := range m.Declarations {
			t.Run(key+"/"+c.ID, func(t *testing.T) {
				want, ok := c.Expect[key]
				if !ok {
					t.Fatalf("%s states no %s expectation (null is a statement; absence is not)", c.ID, key)
				}
				d, declared := DeclaredFor(c.Properties, h)
				switch {
				case want == nil && declared:
					t.Fatalf("%s: want no %s declaration, got one at %s", c.Title, key, d.Key)
				case want != nil && !declared:
					t.Fatalf("%s: want a %s declaration at %s, got none", c.Title, key, want.Key)
				case want != nil:
					if d.Key != want.Key || d.Name != want.Name || d.Description != want.Description ||
						d.Enable != *want.Enable || d.EnableSet != *want.EnableSet {
						t.Fatalf("%s:\n got {%s %q %q enable=%v enableSet=%v}\nwant {%s %q %q enable=%v enableSet=%v}", c.Title,
							d.Key, d.Name, d.Description, d.Enable, d.EnableSet,
							want.Key, want.Name, want.Description, *want.Enable, *want.EnableSet)
					}
				}
				gotBad := MalformedFor(c.Properties, h)
				if gotBad == nil {
					gotBad = []string{}
				}
				if !reflect.DeepEqual(gotBad, c.Malformed[key]) {
					t.Fatalf("%s: malformed = %v, want %v", c.Title, gotBad, c.Malformed[key])
				}
				gotUnknown := UnknownHostKeys(c.Properties)
				if gotUnknown == nil {
					gotUnknown = []string{}
				}
				if !reflect.DeepEqual(gotUnknown, c.Unknown) {
					t.Fatalf("%s: unknown host keys = %q, want %q", c.Title, gotUnknown, c.Unknown)
				}
			})
		}
	}
}

func TestCrossHostLint(t *testing.T) {
	m := loadMatrix(t)
	for _, key := range m.Hosts {
		h := matrixHost(t, key)
		for _, c := range m.Lint {
			t.Run(key+"/"+c.ID, func(t *testing.T) {
				n := Node{
					URN:       "hrn:node:example.com:demo:tasks:" + c.ID,
					MemoryURN: "hrn:mem:example.com:demo", IsRunnable: true, Content: "Do the demo.\n", Properties: c.Properties,
				}
				rules := []string{}
				for _, f := range LintFor(n, h) {
					rules = append(rules, f.Rule+":"+f.Severity)
				}
				sort.Strings(rules)
				if !reflect.DeepEqual(rules, c.Expect[key]) {
					t.Fatalf("rules = %v, want %v", rules, c.Expect[key])
				}
			})
		}
	}
}

// The host-free column: an unknown exports key is judged ONCE, by no host, so
// it is checked outside the per-host loop above rather than inside it.
func TestCrossHostUnhosted(t *testing.T) {
	m := loadMatrix(t)
	for _, c := range m.Lint {
		t.Run(c.ID, func(t *testing.T) {
			n := Node{URN: "hrn:node:example.com:demo:tasks:" + c.ID, MemoryURN: "hrn:mem:example.com:demo",
				IsRunnable: true, Content: "Do the demo.\n", Properties: c.Properties}
			rules := []string{}
			for _, f := range LintUnknownHostKeys(n) {
				rules = append(rules, f.Rule+":"+f.Severity)
			}
			if !reflect.DeepEqual(rules, c.Unhosted) {
				t.Fatalf("unhosted = %v, want %v", rules, c.Unhosted)
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
func TestCrossHostCollisions(t *testing.T) {
	m := loadMatrix(t)
	for _, key := range m.Hosts {
		h := matrixHost(t, key)
		for _, c := range m.Collisions {
			t.Run(key+"/"+c.ID, func(t *testing.T) {
				nodes := make([]Node, 0, len(c.Nodes))
				for _, n := range c.Nodes {
					nodes = append(nodes, Node{URN: n.URN, Properties: n.Properties})
				}
				byName := map[string][]string{}
				for _, f := range LintCollisionsFor(nodes, h) {
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
				if !reflect.DeepEqual(got, c.Expect[key]) {
					t.Fatalf("collisions = %+v, want %+v", got, c.Expect[key])
				}
			})
		}
	}
}

// One renderer serves both hosts (cli#622 item 2): Render takes no host, so
// what may differ per host is only the declaration it is given. Both sides
// render what DeclaredFor ACTUALLY returns, not the expectation, so a reader
// regression shows up here too.
func TestCrossHostRendering(t *testing.T) {
	m := loadMatrix(t)
	props := map[string]map[string]any{}
	for _, c := range m.Declarations {
		props[c.ID] = c.Properties
	}
	codex := matrixHost(t, HostCodexSkill)
	const id, source, body = "01a0000000000000000000000000000r", "hrn:node:example.com:demo:tasks:demo", "Do the demo.\n"
	for _, r := range m.Rendering {
		t.Run(r.ID, func(t *testing.T) {
			cl, ok := Declared(props[r.Case])
			cx, okx := DeclaredFor(props[r.Case], codex)
			if !ok || !okx {
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
			// The host reads the frontmatter, so assert what landed there. File
			// inequality alone is not enough: the provenance line carries a hash
			// of both fields, so a serializer that dropped a changed name would
			// still produce two different files (@codex on #664).
			for _, side := range []struct {
				host, file, name, description string
			}{
				{HostClaudeSkill, fc, cl.Name, cl.Description},
				{"codexSkill", fx, cx.Name, cx.Description},
			} {
				pf, err := ParseFile([]byte(side.file))
				if err != nil {
					t.Fatalf("%s file does not parse: %v", side.host, err)
				}
				if pf.Name != side.name || pf.Description != NormalizeDescription(side.description) {
					t.Fatalf("%s frontmatter = {name %q, description %q}, want {%q, %q}", side.host, pf.Name, pf.Description, side.name, NormalizeDescription(side.description))
				}
			}
			if (fc == fx) != *r.SameFile {
				t.Fatalf("files identical = %v, want %v", fc == fx, *r.SameFile)
			}
			hc, hx := Hash(id, source, cl.Name, cl.Description, body), Hash(id, source, cx.Name, cx.Description, body)
			if (hc == hx) != *r.SameFile {
				t.Fatalf("hashes identical = %v, want %v", hc == hx, *r.SameFile)
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
