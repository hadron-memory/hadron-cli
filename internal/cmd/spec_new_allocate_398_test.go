package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	if _, ok := reply["Memories"]; !ok {
		reply["Memories"] = alloc398Reply{body: `{"data":{"memories":{"total":1,"items":[{"id":"mem1","urn":"hrn:memory:micromentor.org::platform-specs","name":"Platform Specs","class":"knowledge","visibility":"PUBLIC","organizationId":"org1","isEncrypted":false,"updatedAt":"2026-06-14T00:00:00Z"}]}}}`}
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
	if in["ref"] != "new1" || in["expectedRevision"] != float64(3) {
		t.Errorf("fill must carry the allocated id and revision, got id=%v rev=%v", in["ref"], in["expectedRevision"])
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
		"refused":  {alloc398Reply{body: alloc398Err("BAD_USER_INPUT")}, "refused", exitcode.Error},
		"conflict": {alloc398Reply{body: alloc398Err("NODE_WRITE_CONFLICT")}, "refused", exitcode.Error},
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
				Citation   string `json:"citation"`
				Allocated  bool   `json:"allocated"`
				FillStatus string `json:"fillStatus"`
			}
			if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil || dto.Citation != "msg:010:07" || !dto.Allocated || dto.FillStatus != tc.wantFilled {
				t.Errorf("--json = %s (%v), want allocated:true fillStatus:%q at msg:010:07", out, jerr, tc.wantFilled)
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
	if exitCodeFor(err) == exitcode.Unavailable && strings.Contains(err.Error(), "outcome unknown") {
		t.Errorf("schema skew is a refusal, not an unknown allocation outcome: %v", err)
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

// A blank body would leave the placeholder unfilled while the command reports
// success: refused before any number is reserved.
func TestSpecNewBlankBodyReservesNothing398(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"empty": "", "whitespace": " \n\t\n"} {
		t.Run(name, func(t *testing.T) {
			file := dir + "/" + name + ".md"
			if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			fk, _, errText, err := alloc398Run(t, map[string]alloc398Reply{
				"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:010:07", 1)},
			}, "--module", "msg", "--feature", "010", "--title", "Test", "--content-file", file)
			if err == nil || exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("a blank body is a usage refusal, got %v", err)
			}
			if fk.count("AllocateSpecNode") != 0 {
				t.Errorf("a number was reserved for a blank body (%s)", errText)
			}
		})
	}
}

// An unusable allocation answer is an unknown outcome and no fill is sent on it.
func TestSpecNewMalformedAllocationSendsNoFill398(t *testing.T) {
	node := func(id, loc string, rev int, ph bool) string {
		return fmt.Sprintf(`{"data":{"allocateSpecNode":{"id":%q,"memoryId":"mem1","loc":%q,"name":"Test","revision":%d,"isPlaceholder":%t}}}`, id, loc, rev, ph)
	}
	for name, body := range map[string]string{
		"other parent":    node("n1", "msg:020:07", 3, true),
		"missing id":      node("", "msg:010:07", 3, true),
		"zero revision":   node("n1", "msg:010:07", 0, true),
		"not placeholder": node("n1", "msg:010:07", 3, false),
		"wrong tier":      node("n1", "msg:010", 3, true),
		"unparseable":     node("n1", "not a loc", 3, true),
		"other memory":    strings.Replace(node("n1", "msg:010:07", 3, true), `"memoryId":"mem1"`, `"memoryId":"other"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			fk, _, errText, err := alloc398Run(t, map[string]alloc398Reply{
				"AllocateSpecNode": {body: body},
			}, "--module", "msg", "--feature", "010", "--title", "Test")
			if err == nil || exitCodeFor(err) != exitcode.Unavailable {
				t.Fatalf("an unusable answer exits %d, got %v", exitcode.Unavailable, err)
			}
			for _, op := range []string{"UpdateSpecNode", "CreateSpecNode", "CreateEdge"} {
				if fk.count(op) != 0 {
					t.Errorf("%s was sent on an unvalidated allocation", op)
				}
			}
			if !strings.Contains(errText, "outcome unknown") || !strings.Contains(errText, "spec ls -m") {
				t.Errorf("missing unknown-outcome guidance: %s", errText)
			}
		})
	}
}

// A refused fill's recovery restores everything the fill carried, and a failed
// feature fill also names the contract that was never attempted.
func TestSpecNewRecoveryRestoresFilledFields398(t *testing.T) {
	scan := alloc398Scan + specNodeList("msg", `["spec"]`) + `,` + specNodeList("msg:000", `["spec"]`) + `]}}`
	_, out, errText, err := alloc398Run(t, map[string]alloc398Reply{
		"FindNodes":        {body: scan},
		"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:020", 1)},
		"UpdateSpecNode":   {body: alloc398Err("BAD_USER_INPUT")},
	}, "--new-feature", "--module", "msg", "--title", "Palette", "--json")
	if err == nil {
		t.Fatal("a refused fill must fail")
	}
	var dto struct {
		FillStatus string   `json:"fillStatus"`
		Recovery   []string `json:"recovery"`
	}
	if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil || dto.FillStatus != "refused" {
		t.Fatalf("--json = %s (%v)", out, jerr)
	}
	all := strings.Join(dto.Recovery, "\n")
	for _, s := range []string{"spec get msg:020", "spec edit msg:020", "--title Palette", "--abstract-file", "node update msg:020", "--tag spec", "--role spec", "--seq 20", "edge add", "--from msg:020", "spec new -m", "msg:020:00"} {
		if !strings.Contains(all, s) {
			t.Errorf("recovery missing %q:\n%s", s, all)
		}
	}
	if strings.Contains(all, "--new-feature") {
		t.Errorf("recovery must never allocate again:\n%s", all)
	}
	_ = errText
}

// The feature is complete when only its contract fails: a structured partial
// result and a recovery that inspects the contract loc, never a rerun.
func TestSpecNewContractFailureIsStructured398(t *testing.T) {
	for name, tc := range map[string]struct {
		reply      alloc398Reply
		wantStatus string
		wantExit   int
	}{
		"refused": {alloc398Reply{body: alloc398Err("BAD_USER_INPUT")}, "refused", exitcode.Error},
		"lost":    {alloc398Reply{drop: true}, "unknown", exitcode.Unavailable},
	} {
		t.Run(name, func(t *testing.T) {
			scan := alloc398Scan + specNodeList("msg", `["spec"]`) + `,` + specNodeList("msg:000", `["spec"]`) + `]}}`
			fk, out, errText, err := alloc398Run(t, map[string]alloc398Reply{
				"FindNodes":        {body: scan},
				"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:020", 1)},
				"UpdateSpecNode":   {body: alloc398Fill},
				"CreateSpecNode":   tc.reply,
			}, "--new-feature", "--module", "msg", "--title", "Palette", "--json")
			if err == nil || exitCodeFor(err) != tc.wantExit {
				t.Fatalf("exit = %d, want %d (%v)", exitCodeFor(err), tc.wantExit, err)
			}
			if fk.count("AllocateSpecNode") != 1 {
				t.Errorf("no second feature: %d allocations", fk.count("AllocateSpecNode"))
			}
			var dto struct {
				Citation, ContractCitation, ContractStatus, FillStatus string
				Allocated                                              bool
				Recovery                                               []string
			}
			dec := map[string]any{}
			if jerr := json.Unmarshal([]byte(out), &dec); jerr != nil {
				t.Fatalf("--json = %s (%v)", out, jerr)
			}
			b, _ := json.Marshal(dec)
			_ = json.Unmarshal(b, &dto)
			if dec["citation"] != "msg:020" || dec["contractCitation"] != "msg:020:00" || dec["contractStatus"] != tc.wantStatus || dec["fillStatus"] != "filled" || dec["allocated"] != true {
				t.Errorf("partial result = %s", out)
			}
			for _, s := range []string{"msg:020:00", "do NOT rerun the original", "spec get msg:020:00"} {
				if !strings.Contains(errText, s) {
					t.Errorf("message missing %q: %s", s, errText)
				}
			}
		})
	}
}

// The dry-run preview carries every field that does not depend on the number.
func TestSpecNewDryRunPreviewsNumberIndependentFields398(t *testing.T) {
	scan := alloc398Scan + specNodeList("msg", `["spec"]`) + `,` + specNodeList("msg:000", `["spec"]`) + `]}}`
	fk, out, _, err := alloc398Run(t, map[string]alloc398Reply{
		"FindNodes":       {body: scan},
		"AuthContext":     {body: authUserJSON},
		"EffectiveAccess": {body: effectiveAccessJSON(true)},
	}, "--new-feature", "--module", "msg", "--title", "Palette", "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Name           string `json:"name"`
		Abstract       string `json:"abstract"`
		Citation       string `json:"citation"`
		DryRun         bool   `json:"dryRun"`
		AllocatesUnder string `json:"allocatesUnder"`
		Also           []struct {
			Citation string `json:"citation"`
			Name     string `json:"name"`
		} `json:"also"`
	}
	if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil {
		t.Fatal(jerr, out)
	}
	if dto.Citation != "" || !dto.DryRun || dto.AllocatesUnder != "msg" {
		t.Errorf("dry run must show an empty citation, dryRun:true and allocatesUnder msg: %s", out)
	}
	if len(dto.Also) == 1 && dto.Also[0].Citation != "msg:NNN:00" {
		t.Errorf("the planned contract must use the symbolic citation, got %q", dto.Also[0].Citation)
	}
	if dto.Name == "" || dto.Abstract == "" || len(dto.Also) != 1 || dto.Also[0].Name == "" {
		t.Errorf("preview must carry name, abstract and the planned contract: %s", out)
	}
	for _, op := range []string{"AllocateSpecNode", "UpdateSpecNode", "CreateSpecNode"} {
		if fk.count(op) != 0 {
			t.Errorf("--dry-run sent %s", op)
		}
	}
}

// A rule at or below the requested --rule-after floor is not what was asked for.
func TestSpecNewAllocationBelowFloorSendsNoFill398(t *testing.T) {
	fk, _, errText, err := alloc398Run(t, map[string]alloc398Reply{
		"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:010:05", 1)},
	}, "--module", "msg", "--feature", "010", "--rule-after", "07", "--title", "Test")
	if err == nil || exitCodeFor(err) != exitcode.Unavailable {
		t.Fatalf("a rule below the floor exits %d, got %v (%s)", exitcode.Unavailable, err, errText)
	}
	if fk.count("UpdateSpecNode") != 0 {
		t.Error("a fill was sent on a node below the requested floor")
	}
}

// An explicit empty --content is a blank body, not an omitted flag.
func TestSpecNewExplicitEmptyContentReservesNothing398(t *testing.T) {
	for _, args := range [][]string{{"--content", ""}, {"--content=", "--title", "Test2"}} {
		fk, _, _, err := alloc398Run(t, map[string]alloc398Reply{
			"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:010:07", 1)},
		}, append([]string{"--module", "msg", "--feature", "010", "--title", "Test"}, args...)...)
		if err == nil || exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("%v: an explicit blank body is a usage refusal, got %v", args, err)
		}
		if fk.count("AllocateSpecNode") != 0 {
			t.Errorf("%v: a number was reserved for a blank body", args)
		}
	}
}

// Recovery commands are shell-safe: a hostile title or label round-trips as one
// literal word and never as an expansion.
func TestSpecNewRecoveryIsShellSafe398(t *testing.T) {
	hostile := "a\"$(touch /tmp/x)`id`'b"
	_, out, _, err := alloc398Run(t, map[string]alloc398Reply{
		"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:010:07", 1)},
		"UpdateSpecNode":   {body: alloc398Err("BAD_USER_INPUT")},
	}, "--module", "msg", "--feature", "010", "--title", hostile, "--tag", "x y;z", "--json")
	if err == nil {
		t.Fatal("a refused fill must fail")
	}
	var dto struct {
		Recovery []string `json:"recovery"`
	}
	if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil {
		t.Fatal(jerr, out)
	}
	all := strings.Join(dto.Recovery, "\n")
	quoted := "'" + strings.ReplaceAll(hostile, "'", `'\''`) + "'"
	if !strings.Contains(all, "--title "+quoted) {
		t.Errorf("title is not one single-quoted word:\n%s", all)
	}
	if !strings.Contains(all, "--tag 'x y;z'") {
		t.Errorf("tag is not one single-quoted word:\n%s", all)
	}
	if strings.Contains(all, `"a\"$(`) || strings.Contains(all, ` "$(`) {
		t.Errorf("a double-quoted expansion leaked into recovery:\n%s", all)
	}
}

// A lost fill may have committed: the message says to inspect first and, if the
// content is already there, to create only what is missing.
func TestSpecNewLostFillSaysInspectFirst398(t *testing.T) {
	scan := alloc398Scan + specNodeList("msg", `["spec"]`) + `,` + specNodeList("msg:000", `["spec"]`) + `]}}`
	_, _, errText, err := alloc398Run(t, map[string]alloc398Reply{
		"FindNodes":        {body: scan},
		"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:020", 1)},
		"UpdateSpecNode":   {drop: true},
	}, "--new-feature", "--module", "msg", "--title", "Palette")
	if err == nil || exitCodeFor(err) != exitcode.Unavailable {
		t.Fatalf("a lost fill exits %d, got %v", exitcode.Unavailable, err)
	}
	for _, s := range []string{"already holds your content", "the fill committed", "msg:020:00 was never attempted", "create only that"} {
		if !strings.Contains(errText, s) {
			t.Errorf("message missing %q: %s", s, errText)
		}
	}
}

// Contract recovery keeps the caller's tags and --no-edges, on both paths.
func TestSpecNewContractRecoveryKeepsOptions398(t *testing.T) {
	scan := alloc398Scan + specNodeList("msg", `["spec"]`) + `,` + specNodeList("msg:000", `["spec"]`) + `]}}`
	args := []string{"--new-feature", "--module", "msg", "--title", "Palette", "--tag", "custom", "--no-edges", "--json"}
	for name, reply := range map[string]map[string]alloc398Reply{
		"fill refused": {"UpdateSpecNode": {body: alloc398Err("BAD_USER_INPUT")}},
		"contract refused": {
			"UpdateSpecNode": {body: alloc398Fill},
			"CreateSpecNode": {body: alloc398Err("BAD_USER_INPUT")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			reply["FindNodes"] = alloc398Reply{body: scan}
			reply["AllocateSpecNode"] = alloc398Reply{body: strings.Replace(alloc398Node, "%s", "msg:020", 1)}
			_, out, _, err := alloc398Run(t, reply, args...)
			if err == nil {
				t.Fatal("must fail")
			}
			var dto struct {
				Recovery []string `json:"recovery"`
			}
			if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil {
				t.Fatal(jerr, out)
			}
			var contract string
			for _, c := range dto.Recovery {
				if strings.Contains(c, "msg:020:00") && strings.Contains(c, "spec new") {
					contract = c
				}
				if strings.Contains(c, "edge add") {
					t.Errorf("--no-edges was disabled, yet recovery adds an edge: %s", c)
				}
			}
			if contract == "" || !strings.Contains(contract, "--tag custom") || !strings.Contains(contract, "--no-edges") {
				t.Errorf("contract recovery must keep the tags and --no-edges, got %q", contract)
			}
		})
	}
}

// Dara P2 (a141a2a6): recovery from a failed fill must be a pasteable command
// whose files hold what the failed fill carried (body from a file or the
// scaffold default), without printing authored text in the diagnostic.
func TestSpecNewRecoveryPreservesFillText398(t *testing.T) {
	dir := t.TempDir()
	bodyFile := dir + "/body.md"
	secret := "SECRET-BODY-TEXT-398"
	if err := os.WriteFile(bodyFile, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		args     []string
		wantBody string
		wantAbs  string
	}{
		"supplied":  {[]string{"--content-file", bodyFile, "--abstract", "SECRET-ABSTRACT-398"}, secret, "SECRET-ABSTRACT-398"},
		"scaffolds": {nil, "msg:010:07", "msg:010:07"},
	} {
		t.Run(name, func(t *testing.T) {
			args := append([]string{"--module", "msg", "--feature", "010", "--title", "Test", "--json"}, tc.args...)
			_, out, errText, err := alloc398Run(t, map[string]alloc398Reply{
				"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:010:07", 1)},
				"UpdateSpecNode":   {body: alloc398Err("BAD_USER_INPUT")},
			}, args...)
			if err == nil {
				t.Fatal("a refused fill must fail")
			}
			var dto struct {
				Recovery []string `json:"recovery"`
			}
			if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil {
				t.Fatal(jerr, out)
			}
			all := strings.Join(dto.Recovery, "\n")
			if strings.Contains(all, "<file>") || strings.ContainsAny(all, "<>") {
				t.Errorf("recovery has an unfilled placeholder / shell redirection:\n%s", all)
			}
			var edit string
			for _, c := range dto.Recovery {
				if strings.HasPrefix(c, "hadron spec edit") {
					edit = c
				}
			}
			read := func(flag string) string {
				fields := strings.Fields(edit)
				path := ""
				for i, w := range fields {
					if w == flag && i+1 < len(fields) {
						path = strings.Trim(fields[i+1], "'")
					}
				}
				if path == "" {
					t.Fatalf("edit command lacks %s: %s", flag, edit)
				}
				b, rerr := os.ReadFile(path)
				if rerr != nil {
					t.Fatalf("recovery names %s but it cannot be read: %v", path, rerr)
				}
				t.Cleanup(func() { _ = os.Remove(path) })
				return string(b)
			}
			if got := read("--content-file"); !strings.Contains(got, tc.wantBody) {
				t.Errorf("preserved body = %q, want it to hold %q", got, tc.wantBody)
			}
			if got := read("--abstract-file"); !strings.Contains(got, tc.wantAbs) {
				t.Errorf("preserved abstract = %q, want it to hold %q", got, tc.wantAbs)
			}
			if strings.Contains(errText+out, "SECRET-") {
				t.Errorf("authored text leaked into the diagnostic:\n%s\n%s", errText, out)
			}
		})
	}
}

// Dara P2: the provider's numbering contract is enforced, not just "a rule".
func TestSpecNewOutOfContractNumberSendsNoFill398(t *testing.T) {
	for name, tc := range map[string]struct {
		loc  string
		args []string
	}{
		"rule 00 is the contract":  {"msg:010:00", []string{"--module", "msg", "--feature", "010"}},
		"feature off the step":     {"msg:015", []string{"--new-feature", "--module", "msg"}},
		"feature 000":              {"msg:000", []string{"--new-feature", "--module", "msg"}},
		"feature past the ceiling": {"msg:1000", []string{"--new-feature", "--module", "msg"}},
	} {
		t.Run(name, func(t *testing.T) {
			fk, _, errText, err := alloc398Run(t, map[string]alloc398Reply{
				"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", tc.loc, 1)},
			}, append(tc.args, "--title", "Test")...)
			if err == nil || exitCodeFor(err) != exitcode.Unavailable {
				t.Fatalf("exit %d expected, got %v (%s)", exitcode.Unavailable, err, errText)
			}
			if fk.count("UpdateSpecNode") != 0 {
				t.Error("a fill was sent on an out-of-contract number")
			}
		})
	}
}

// Dara P3: the expected memory id is resolved from the ref, so a scan whose
// nodes carry no memory id still reaches the allocator.
func TestSpecNewMemoryIDIsNotReadOffScan398(t *testing.T) {
	scan := strings.ReplaceAll(alloc398Scanned(), `"memoryId":"mem1"`, `"memoryId":""`)
	fk, _, errText, err := alloc398Run(t, map[string]alloc398Reply{
		"FindNodes":        {body: scan},
		"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:010:07", 1)},
		"UpdateSpecNode":   {body: alloc398Fill},
	}, "--module", "msg", "--feature", "010", "--title", "Test")
	if err != nil {
		t.Fatalf("%v (%s)", err, errText)
	}
	if fk.count("AllocateSpecNode") != 1 {
		t.Error("the allocator must be reached when the scan carries no memory id")
	}
}

// Rodolfo P3-1: every recovery command, on both the fill and contract paths,
// survives a real shell split: a hostile title/tag/label arrives as ONE literal
// word and nothing runs.
func TestSpecNewRecoveryShellSplitsEveryCommand398(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	canary := t.TempDir() + "/canary"
	hostile := "a\"$(touch " + canary + ")`id`'b"
	scan := alloc398Scan + specNodeList("msg", `["spec"]`) + `,` + specNodeList("msg:000", `["spec"]`) + `]}}`
	for name, reply := range map[string]map[string]alloc398Reply{
		"fill refused": {
			"FindNodes":        {body: scan},
			"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:020", 1)},
			"UpdateSpecNode":   {body: alloc398Err("BAD_USER_INPUT")},
		},
		"contract refused": {
			"FindNodes":        {body: scan},
			"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:020", 1)},
			"UpdateSpecNode":   {body: alloc398Fill},
			"CreateSpecNode":   {body: alloc398Err("BAD_USER_INPUT")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, out, _, err := alloc398Run(t, reply, "--new-feature", "--module", "msg", "--title", hostile, "--tag", "x y;z", "--json")
			if err == nil {
				t.Fatal("expected a failure")
			}
			var dto struct {
				Recovery []string `json:"recovery"`
			}
			if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil || len(dto.Recovery) == 0 {
				t.Fatal(jerr, out)
			}
			for _, c := range dto.Recovery {
				script := "hadron() { printf '%s\\n' \"$@\"; }\n" + c
				got, rerr := exec.Command("bash", "-c", script).Output()
				if rerr != nil {
					t.Fatalf("bash rejected %q: %v", c, rerr)
				}
				for _, w := range strings.Split(strings.TrimSuffix(string(got), "\n"), "\n") {
					if strings.Contains(w, "touch") && w != hostile && w != hostile+" general provisions" && !strings.HasPrefix(w, "--") {
						// a word holding the canary must be a whole title or label, never a fragment
						if !strings.Contains(w, hostile) {
							t.Errorf("hostile text was split across words in %q: word %q", c, w)
						}
					}
				}
				if strings.Contains(c, "--tag") && !strings.Contains(string(got), "x y;z\n") {
					t.Errorf("tag was not one literal word in %q:\n%s", c, got)
				}
			}
			if _, serr := os.Stat(canary); serr == nil {
				t.Fatal("a recovery command executed the hostile title")
			}
		})
	}
}

// Rodolfo P3-2: a rule allocated exactly AT the requested floor is refused
// (the floor is exclusive).
func TestSpecNewAllocationAtFloorSendsNoFill398(t *testing.T) {
	fk, _, errText, err := alloc398Run(t, map[string]alloc398Reply{
		"AllocateSpecNode": {body: strings.Replace(alloc398Node, "%s", "msg:010:07", 1)},
	}, "--module", "msg", "--feature", "010", "--rule-after", "07", "--title", "Test")
	if err == nil || exitCodeFor(err) != exitcode.Unavailable {
		t.Fatalf("a rule at the floor exits %d, got %v (%s)", exitcode.Unavailable, err, errText)
	}
	if fk.count("UpdateSpecNode") != 0 {
		t.Error("a fill was sent on a node at the requested floor")
	}
}
