package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#398: `spec new`'s two number-allocating modes ask the server
// (allocateSpecNode), then fill that same node once, guarded on the returned
// revision. Fake transport only: nothing here reaches a real provider.

type alloc398Reply struct {
	body string // GraphQL JSON body
	drop bool   // abort the connection: the answer is lost
}

type alloc398Fake struct {
	mu    sync.Mutex
	calls map[string]int
	vars  map[string][]json.RawMessage
	reply map[string]alloc398Reply
}

func newAlloc398Server(t *testing.T, reply map[string]alloc398Reply) (*httptest.Server, *alloc398Fake) {
	t.Helper()
	fk := &alloc398Fake{calls: map[string]int{}, vars: map[string][]json.RawMessage{}, reply: reply}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fk.mu.Lock()
		fk.calls[body.OperationName]++
		fk.vars[body.OperationName] = append(fk.vars[body.OperationName], body.Variables)
		rep, ok := fk.reply[body.OperationName]
		fk.mu.Unlock()
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			rep = alloc398Reply{body: `{"errors":[{"message":"unexpected","extensions":{"code":"BAD_USER_INPUT"}}]}`}
		}
		if rep.drop {
			panic(http.ErrAbortHandler)
		}
		w.Header().Set("Content-Type", "application/json")
		// The scan pages to exhaustion: later pages answer empty, and the
		// shared harness translates the flat node list into the hits shape.
		_, _ = w.Write([]byte(translateFindNodes(body.OperationName, pageBeyondFirst(body.OperationName, body.Variables, rep.body))))
	}))
	t.Cleanup(srv.Close)
	return srv, fk
}

func (fk *alloc398Fake) count(op string) int {
	fk.mu.Lock()
	defer fk.mu.Unlock()
	return fk.calls[op]
}

func (fk *alloc398Fake) first(t *testing.T, op string) map[string]any {
	t.Helper()
	fk.mu.Lock()
	defer fk.mu.Unlock()
	if len(fk.vars[op]) == 0 {
		t.Fatalf("%s was never sent", op)
	}
	var v map[string]any
	if err := json.Unmarshal(fk.vars[op][0], &v); err != nil {
		t.Fatal(err)
	}
	return v
}

const (
	alloc398Scan = `{"data":{"nodes":[`
	alloc398Node = `{"data":{"allocateSpecNode":{"id":"new1","memoryId":"mem1","loc":"%s","name":"Test","revision":3,"isPlaceholder":true}}}`
	alloc398Fill = `{"data":{"updateSpecNode":{"id":"new1","memoryId":"mem1","loc":"msg:010:07","name":"x","nodeType":"info","tags":["spec"],"updatedAt":"2026-11-21T00:00:00Z"}}}`
	alloc398Res  = `{"data":{"resolveUrn":{"id":"t1","kind":"node","memoryId":"mem1"}}}`
)

// alloc398ErrAt is a coded error raised BELOW the mutation field (resolving the
// returned object), i.e. after the write may have committed.
func alloc398ErrAt(code string, path ...string) string {
	ps, _ := json.Marshal(path)
	return `{"data":null,"errors":[{"message":"` + code + ` below","path":` + string(ps) + `,"extensions":{"code":"` + code + `"}}]}`
}

func alloc398Err(code string) string {
	return `{"errors":[{"message":"` + code + ` refused","extensions":{"code":"` + code + `"}}]}`
}

func alloc398Scanned() string {
	return alloc398Scan + specNodeList("msg", `["spec"]`) + `,` + specNodeList("msg:010", `["spec"]`) + `,` + specNodeList("msg:010:00", `["spec"]`) + `]}}`
}

func alloc398Run(t *testing.T, reply map[string]alloc398Reply, args ...string) (*alloc398Fake, string, string, error) {
	t.Helper()
	if _, ok := reply["FindNodes"]; !ok {
		reply["FindNodes"] = alloc398Reply{body: alloc398Scanned()}
	}
	if _, ok := reply["ResolveUrn"]; !ok {
		reply["ResolveUrn"] = alloc398Reply{body: alloc398Res}
	}
	srv, fk := newAlloc398Server(t, reply)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"spec", "new", "-m", specMem}, args...), "--server", srv.URL))
	err := root.Execute()
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	return fk, out.String(), errText, err
}

// The rule mode sends ONE allocation and ONE guarded fill on the returned id
// and revision; never a create, never a separate edge write.
func TestSpecNewAllocatesOnceThenFillsOnceGuarded398(t *testing.T) {
	fk, _, _, err := alloc398Run(t, map[string]alloc398Reply{
		"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:010:07", 1)},
		"UpdateSpecNode":   {body: alloc398Fill},
	}, "--module", "msg", "--feature", "010", "--title", "Test")
	if err != nil {
		t.Fatal(err)
	}
	if fk.count("AllocateSpecNode") != 1 || fk.count("UpdateSpecNode") != 1 {
		t.Fatalf("want one allocate and one fill, got %d and %d", fk.count("AllocateSpecNode"), fk.count("UpdateSpecNode"))
	}
	for _, op := range []string{"CreateSpecNode", "CreateEdge"} {
		if fk.count(op) != 0 {
			t.Errorf("%s must not be sent on the allocation path", op)
		}
	}
	a := fk.first(t, "AllocateSpecNode")
	if a["parentLoc"] != "msg:010" || a["memoryRef"] == nil {
		t.Errorf("allocate vars = %v, want parentLoc msg:010 and a memoryRef", a)
	}
	if _, has := a["after"]; has {
		t.Errorf("without --rule-after the floor must be OMITTED, got %v", a["after"])
	}
	in, _ := fk.first(t, "UpdateSpecNode")["input"].(map[string]any)
	if in["id"] != "new1" || in["expectedRevision"] != float64(3) {
		t.Errorf("fill must carry the allocated id and revision, got id=%v rev=%v", in["id"], in["expectedRevision"])
	}
	if in["name"] != "msg:010:07 — Test" {
		t.Errorf("fill name = %v, want the SERVER's loc composed with the title", in["name"])
	}
	if edges, _ := in["edges"].([]any); len(edges) != 2 {
		t.Errorf("ToC + inheritance edges must travel inline on the fill, got %v", in["edges"])
	}
}

// --rule-after N is the `after` floor; the other loc-explicit modes never ask
// the allocator for a number.
func TestSpecNewRuleAfterAndExplicitModes398(t *testing.T) {
	fk, _, _, err := alloc398Run(t, map[string]alloc398Reply{
		"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:010:08", 1)},
		"UpdateSpecNode":   {body: alloc398Fill},
	}, "--module", "msg", "--feature", "010", "--rule-after", "07", "--title", "Test")
	if err != nil {
		t.Fatal(err)
	}
	if got := fk.first(t, "AllocateSpecNode")["after"]; got != float64(7) {
		t.Errorf("--rule-after 07 must send after:7, got %v", got)
	}

	create := `{"data":{"createSpecNode":{"id":"n1","memoryId":"mem1","loc":"x","name":"x","nodeType":"info","tags":["spec"],"updatedAt":"2026-11-21T00:00:00Z"}}}`
	wantLoc := map[string]string{"--rule": "msg:010:09", "--contract": "msg:010:00", "positional": "msg:010:60"}
	for name, args := range map[string][]string{
		"--rule":     {"--module", "msg", "--feature", "010", "--rule", "09", "--title", "T"},
		"--contract": {"--module", "msg", "--feature", "010", "--contract", "--title", "T"},
		"positional": {"msg:010:60", "--no-edges", "--title", "T"},
	} {
		t.Run(name, func(t *testing.T) {
			reply := map[string]alloc398Reply{"CreateSpecNode": {body: create}}
			if name == "positional" { // the free-loc check resolves the loc: not found
				reply["ResolveUrn"] = alloc398Reply{body: `{"errors":[{"message":"not found","extensions":{"code":"NOT_FOUND"}}]}`}
			}
			fk, _, errText, err := alloc398Run(t, reply, args...)
			if err != nil {
				t.Fatalf("%s must succeed: %v (%s)", name, err, errText)
			}
			if fk.count("AllocateSpecNode") != 0 {
				t.Errorf("%s names its loc, so it must send no allocation", name)
			}
			if fk.count("CreateSpecNode") != 1 {
				t.Errorf("%s must send exactly one CreateSpecNode, got %d", name, fk.count("CreateSpecNode"))
			}
			if in, _ := fk.first(t, "CreateSpecNode")["input"].(map[string]any); in == nil || in["loc"] != wantLoc[name] {
				t.Errorf("%s must create at %q, got %v", name, wantLoc[name], in)
			}
		})
	}
}

// The four real refusals (and nothing else) map through api.MapError with zero
// further writes: no fill, no create, no retry.
func TestSpecNewAllocationRefusalsWriteNothing398(t *testing.T) {
	for _, code := range []string{"SPEC_PARENT_NOT_FOUND", "SPEC_PARENT_NOT_NUMBERED", "SPEC_NUMBER_SPACE_EXHAUSTED", "BAD_USER_INPUT"} {
		t.Run(code, func(t *testing.T) {
			fk, _, errText, err := alloc398Run(t, map[string]alloc398Reply{
				"AllocateSpecNode": {body: alloc398Err(code)},
			}, "--module", "msg", "--feature", "010", "--title", "Test")
			if err == nil {
				t.Fatal("a refused allocation must fail")
			}
			if fk.count("AllocateSpecNode") != 1 {
				t.Errorf("a refusal is final: %d allocation calls", fk.count("AllocateSpecNode"))
			}
			for _, op := range []string{"UpdateSpecNode", "CreateSpecNode", "CreateEdge"} {
				if fk.count(op) != 0 {
					t.Errorf("%s was sent after the allocation was refused (%s)", op, errText)
				}
			}
			// A definite refusal is a refusal (exit 1/usage-class), never the
			// unknown-outcome path (exit 7 with "outcome unknown").
			if got := exitCodeFor(err); got == exitcode.Unavailable {
				t.Errorf("refusal %s exited %d (unknown-outcome path)", code, got)
			}
			if strings.Contains(errText, "outcome unknown") {
				t.Errorf("refusal %s reported as unknown outcome: %s", code, errText)
			}
		})
	}
}

// A fill the server refused, one whose answer was lost, and a fill that hit
// NODE_WRITE_CONFLICT each leave a REAL reserved placeholder: reported with its
// loc and the spec-edit remedy, never a second allocation.
func TestSpecNewFillFailuresReportReservation398(t *testing.T) {
	for name, tc := range map[string]struct {
		fill       alloc398Reply
		wantFilled string
		wantExit   int
	}{
		"refused":  {alloc398Reply{body: alloc398Err("BAD_USER_INPUT")}, "false", exitcode.Error},
		"conflict": {alloc398Reply{body: alloc398Err("NODE_WRITE_CONFLICT")}, "false", exitcode.Error},
		"lost":     {alloc398Reply{drop: true}, "unknown", exitcode.Unavailable},
		"readback": {alloc398Reply{body: alloc398ErrAt("FORBIDDEN", "updateSpecNode", "id")}, "unknown", exitcode.Unavailable},
	} {
		t.Run(name, func(t *testing.T) {
			fk, out, errText, err := alloc398Run(t, map[string]alloc398Reply{
				"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:010:07", 1)},
				"UpdateSpecNode":   tc.fill,
			}, "--module", "msg", "--feature", "010", "--title", "Test", "--json")
			if err == nil {
				t.Fatal("a failed fill must exit non-zero")
			}
			if got := exitCodeFor(err); got != tc.wantExit {
				t.Errorf("exit = %d, want %d (%s)", got, tc.wantExit, errText)
			}
			if fk.count("AllocateSpecNode") != 1 {
				t.Errorf("a failed fill must never allocate again: %d allocations", fk.count("AllocateSpecNode"))
			}
			if fk.count("CreateSpecNode") != 0 {
				t.Error("a failed fill must not fall back to a create")
			}
			for _, s := range []string{"msg:010:07", "spec edit msg:010:07", "do NOT rerun `spec new`", "cor:dmo:120:07", "spec get msg:010:07"} {
				if !strings.Contains(errText, s) {
					t.Errorf("message missing %q: %s", s, errText)
				}
			}
			if strings.Contains(errText, "is an empty placeholder") {
				t.Errorf("a failed fill must not claim the node is blank (another writer may have filled it): %s", errText)
			}
			var dto struct {
				Citation  string `json:"citation"`
				Allocated bool   `json:"allocated"`
				Filled    string `json:"filled"`
			}
			if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil || dto.Citation != "msg:010:07" || !dto.Allocated || dto.Filled != tc.wantFilled {
				t.Errorf("--json = %s (%v), want allocated:true filled:%q at msg:010:07", out, jerr, tc.wantFilled)
			}
		})
	}
}

// An error below allocateSpecNode (resolving the returned node) follows the
// commit: unknown outcome, the recovery guidance, no fill and no retry.
func TestSpecNewAllocationReadbackErrorIsUnknown398(t *testing.T) {
	fk, _, errText, err := alloc398Run(t, map[string]alloc398Reply{
		"AllocateSpecNode": {body: alloc398ErrAt("FORBIDDEN", "allocateSpecNode", "revision")},
	}, "--module", "msg", "--feature", "010", "--title", "Test")
	if err == nil || exitCodeFor(err) != exitcode.Unavailable {
		t.Fatalf("a readback error after allocation exits %d, got %v", exitcode.Unavailable, err)
	}
	if fk.count("AllocateSpecNode") != 1 || fk.count("UpdateSpecNode") != 0 {
		t.Errorf("no retry, no fill: allocate=%d update=%d", fk.count("AllocateSpecNode"), fk.count("UpdateSpecNode"))
	}
	for _, s := range []string{"outcome unknown", "spec ls -m", "rerunning allocates another number"} {
		if !strings.Contains(errText, s) {
			t.Errorf("message missing %q: %s", s, errText)
		}
	}
}

// Unreadable input is refused BEFORE any number is reserved.
func TestSpecNewUnreadableInputReservesNothing398(t *testing.T) {
	for name, extra := range map[string][]string{
		"abstract-file": {"--abstract-file", "/nonexistent/abs.txt"},
		"content-file":  {"--content-file", "/nonexistent/body.md"},
	} {
		t.Run(name, func(t *testing.T) {
			fk, _, _, err := alloc398Run(t, map[string]alloc398Reply{
				"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:010:07", 1)},
			}, append([]string{"--module", "msg", "--feature", "010", "--title", "Test"}, extra...)...)
			if err == nil {
				t.Fatal("an unreadable input file must fail")
			}
			if fk.count("AllocateSpecNode") != 0 {
				t.Errorf("a number was reserved for unreadable input: %d allocations", fk.count("AllocateSpecNode"))
			}
		})
	}
}

// A lost allocation answer: a placeholder MAY exist. No fill, no second
// allocation, exit 7, and the message says what to check.
func TestSpecNewLostAllocationAnswer398(t *testing.T) {
	fk, _, errText, err := alloc398Run(t, map[string]alloc398Reply{
		"AllocateSpecNode": {drop: true},
	}, "--module", "msg", "--feature", "010", "--title", "Test")
	if err == nil || exitCodeFor(err) != exitcode.Unavailable {
		t.Fatalf("a lost allocation answer exits %d, got %v", exitcode.Unavailable, err)
	}
	if fk.count("AllocateSpecNode") != 1 {
		t.Errorf("no retry: %d allocation calls", fk.count("AllocateSpecNode"))
	}
	for _, op := range []string{"UpdateSpecNode", "CreateSpecNode"} {
		if fk.count(op) != 0 {
			t.Errorf("%s was sent although nothing was allocated for certain", op)
		}
	}
	for _, s := range []string{"outcome unknown", "spec ls -m", "--prefix msg:010", `"Test"`, "rerunning allocates another number"} {
		if !strings.Contains(errText, s) {
			t.Errorf("message missing %q: %s", s, errText)
		}
	}
}

// An older server without the operation refuses as schema skew; there is no
// silent fallback to client-side numbering.
func TestSpecNewNoClientFallbackOnSchemaSkew398(t *testing.T) {
	fk, _, _, err := alloc398Run(t, map[string]alloc398Reply{
		"AllocateSpecNode": {body: `{"errors":[{"message":"Cannot query field \"allocateSpecNode\" on type \"Mutation\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`},
	}, "--module", "msg", "--feature", "010", "--title", "Test")
	if err == nil {
		t.Fatal("a server without allocateSpecNode must refuse")
	}
	for _, op := range []string{"UpdateSpecNode", "CreateSpecNode"} {
		if fk.count(op) != 0 {
			t.Errorf("fell back to a client write (%s)", op)
		}
	}
}

// A new feature still co-creates its :00 contract at the EXPLICIT loc, which the
// allocator never hands out; the allocation is for the feature alone.
func TestSpecNewFeatureContractStaysExplicit398(t *testing.T) {
	scan := alloc398Scan + specNodeList("msg", `["spec"]`) + `,` + specNodeList("msg:000", `["spec"]`) + `]}}`
	fk, _, _, err := alloc398Run(t, map[string]alloc398Reply{
		"FindNodes":        {body: scan},
		"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:020", 1)},
		"UpdateSpecNode":   {body: alloc398Fill},
		"CreateSpecNode":   {body: `{"data":{"createSpecNode":{"id":"c1","memoryId":"mem1","loc":"msg:020:00","name":"x","nodeType":"info","tags":["spec"],"updatedAt":"2026-11-21T00:00:00Z"}}}`},
	}, "--new-feature", "--module", "msg", "--title", "Palette")
	if err != nil {
		t.Fatal(err)
	}
	if fk.count("AllocateSpecNode") != 1 || fk.count("CreateSpecNode") != 1 {
		t.Fatalf("want one allocation (the feature) and one explicit create (its contract), got %d and %d", fk.count("AllocateSpecNode"), fk.count("CreateSpecNode"))
	}
	if a := fk.first(t, "AllocateSpecNode"); a["parentLoc"] != "msg" {
		t.Errorf("a new feature allocates under the module, got %v", a["parentLoc"])
	}
	in, _ := fk.first(t, "CreateSpecNode")["input"].(map[string]any)
	if in["loc"] != "msg:020:00" {
		t.Errorf("contract loc = %v, want msg:020:00 (the allocated feature's :00)", in["loc"])
	}
}

// A copied "<parent>:NN — " title prefix is refused BEFORE any write, so no
// number is reserved for a title that was never going to be accepted.
func TestSpecNewCopiedPrefixRefusedBeforeAllocation398(t *testing.T) {
	fk, _, _, err := alloc398Run(t, map[string]alloc398Reply{},
		"--module", "msg", "--feature", "010", "--title", "msg:010:07 — Test")
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("a copied prefix is a usage refusal, got %v", err)
	}
	if fk.count("AllocateSpecNode") != 0 {
		t.Error("the prefix refusal must happen before anything is reserved")
	}
}
