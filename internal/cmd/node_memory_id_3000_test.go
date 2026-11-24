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

// cli#3000 (Xabat's ruling: resolve-ID): a bare memory ID on -m resolves through the same
// memory read a URN would pass, then composes the bare loc. An ID that resolves to nothing the
// caller can read keeps today's plain refusal, and no request is sent for the node.

const memID3000 = "01a1d9c2453a782189ceeafb18c37d61"

type req3000 struct {
	op   string
	vars map[string]json.RawMessage
}

// serve3000 answers GetMemory with memoryBody and every other operation from stubs, recording the
// requests in order. An unstubbed operation answers an error (the commands under test are checked
// on what they SEND, so the tail of a run may fail).
func serve3000(t *testing.T, memoryBody string, stubs map[string]string) (url string, calls func() []req3000) {
	t.Helper()
	var mu sync.Mutex
	var got []req3000
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var q struct {
			OperationName string                     `json:"operationName"`
			Variables     map[string]json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		mu.Lock()
		got = append(got, req3000{q.OperationName, q.Variables})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if q.OperationName == "GetMemory" {
			if strings.HasPrefix(memoryBody, "HTTP ") {
				http.Error(w, "upstream", http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(memoryBody))
			return
		}
		if body, ok := stubs[q.OperationName]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(`{"errors":[{"message":"unstubbed in this test"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []req3000 {
		mu.Lock()
		defer mu.Unlock()
		return append([]req3000(nil), got...)
	}
}

const memFound3000 = `{"data":{"memory":{"id":"` + memID3000 + `","urn":"hrn:mem:acme.com:kb","name":"KB","isEncrypted":false}}}`
const memNull3000 = `{"data":{"memory":null}}`
const resolveNode3000 = `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"` + memID3000 + `"}}}`

func ops3000(c []req3000) []string {
	var o []string
	for _, r := range c {
		o = append(o, r.op)
	}
	return o
}

func str3000(r req3000, key string) string {
	var s string
	_ = json.Unmarshal(r.vars[key], &s)
	return s
}

func find3000(c []req3000, op string) []req3000 {
	var out []req3000
	for _, r := range c {
		if r.op == op {
			out = append(out, r)
		}
	}
	return out
}

func run3000(t *testing.T, url string, args ...string) error {
	t.Helper()
	withSigningProbe(t, false, nil)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", url))
	return root.Execute()
}

// A memory ID + a bare loc reads the memory, composes the canonical node URN and resolves it.
func TestNodeGetMemoryIDResolves3000(t *testing.T) {
	url, calls := serve3000(t, memFound3000, map[string]string{"ResolveUrn": resolveNode3000, "GetNode": nodeGetJSON(testNodeURL)})
	// The run's tail (the revision bracket) is not stubbed: what matters is what was sent.
	err := run3000(t, url, "node", "get", "-m", memID3000, "sheds:rolling-checkpoint")
	c := calls()
	if err != nil && strings.Contains(err.Error(), "not a fully-qualified node URN") {
		t.Fatalf("a memory ID on -m was refused: %v", err)
	}
	mem := find3000(c, "GetMemory")
	if len(mem) != 1 || str3000(mem[0], "ref") != memID3000 {
		t.Fatalf("want one GetMemory for the id, got %v", ops3000(c))
	}
	res := find3000(c, "ResolveUrn")
	if len(res) != 1 || str3000(res[0], "urn") != "hrn:node:acme.com:kb:sheds:rolling-checkpoint" {
		t.Fatalf("the node was not resolved by the composed URN: %v", c)
	}
}

// An ID that resolves to nothing readable (absent, foreign or forbidden answer the same null) keeps
// today's plain refusal: a usage error that discloses nothing, and the node is never asked for.
func TestNodeGetMemoryIDUnresolvableKeepsThePlainRefusal3000(t *testing.T) {
	var msgs []string
	for _, id := range []string{memID3000, "01a1d9c2453a782189ceeafb18c37d62"} {
		url, calls := serve3000(t, memNull3000, map[string]string{"ResolveUrn": resolveNode3000})
		err := run3000(t, url, "node", "get", "-m", id, "sheds:x")
		wantExit(t, err, exitcode.Usage)
		if got := find3000(calls(), "ResolveUrn"); len(got) != 0 {
			t.Fatalf("an unresolvable memory must not reach the node resolve: %v", ops3000(calls()))
		}
		if !strings.Contains(err.Error(), "not a fully-qualified node URN") {
			t.Fatalf("the refusal changed: %v", err)
		}
		msgs = append(msgs, strings.ReplaceAll(err.Error(), id, "<id>"))
	}
	if msgs[0] != msgs[1] {
		t.Fatalf("two unresolvable ids must be refused identically:\n%q\n%q", msgs[0], msgs[1])
	}
}

// A server fault on the memory read is the fault's own class, never the usage refusal.
func TestNodeGetMemoryIDServerFaultIsNotARefusal3000(t *testing.T) {
	url, _ := serve3000(t, "HTTP 502", nil)
	err := run3000(t, url, "node", "get", "-m", memID3000, "sheds:x")
	wantExit(t, err, exitcode.Unavailable)
}

// Every other -m spelling is untouched: no extra memory read.
func TestNodeGetMemoryURNFormsSendNoMemoryRead3000(t *testing.T) {
	for _, m := range []string{"hrn:mem:acme.com:kb", "acme.com:kb", "acme.com::kb"} {
		url, calls := serve3000(t, memNull3000, map[string]string{"ResolveUrn": resolveNode3000, "GetNode": nodeGetJSON(testNodeURL)})
		err := run3000(t, url, "node", "get", "-m", m, "start-here")
		if err != nil && strings.Contains(err.Error(), "not a fully-qualified node URN") {
			t.Fatalf("-m %s was refused: %v", m, err)
		}
		if got := find3000(calls(), "GetMemory"); len(got) != 0 {
			t.Fatalf("-m %s sent a memory read: %v", m, ops3000(calls()))
		}
		if got := find3000(calls(), "ResolveUrn"); len(got) != 1 || str3000(got[0], "urn") != "hrn:node:acme.com:kb:start-here" {
			t.Fatalf("-m %s did not resolve the composed URN: %v", m, ops3000(calls()))
		}
	}
}

// The batch read: one memory read for the whole set, every ref composed against the URN.
func TestNodeGetBatchMemoryID3000(t *testing.T) {
	url, calls := serve3000(t, memFound3000, map[string]string{"NodeBatch": nodeBatchResult([]string{}, "")})
	_ = run3000(t, url, "node", "get", "-m", memID3000, "a:one", "b:two")
	c := calls()
	if got := find3000(c, "GetMemory"); len(got) != 1 {
		t.Fatalf("want exactly one memory read for the batch, got %v", ops3000(c))
	}
	// The first read of the set carries the refs (the revision bracket, then the batch itself).
	var nb []req3000
	for _, r := range c {
		if _, ok := r.vars["refs"]; ok {
			nb = append(nb, r)
		}
	}
	if len(nb) == 0 {
		t.Fatalf("no read carried the refs: %v", ops3000(c))
	}
	var refs []string
	_ = json.Unmarshal(nb[0].vars["refs"], &refs)
	if len(refs) != 2 || refs[0] != "hrn:node:acme.com:kb:a:one" || refs[1] != "hrn:node:acme.com:kb:b:two" {
		t.Fatalf("batch refs = %v", refs)
	}
}

// The edge commands that take a bare loc use the same resolution.
func TestEdgeMemoryID3000(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		urns []string
	}{
		{"edge ls", []string{"edge", "ls", "-m", memID3000, "sheds:x"}, []string{"hrn:node:acme.com:kb:sheds:x"}},
		{"edge add", []string{"edge", "add", "-m", memID3000, "--from", "a:one", "--to", "b:two", "--name", "routes-to"}, []string{"hrn:node:acme.com:kb:a:one", "hrn:node:acme.com:kb:b:two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url, calls := serve3000(t, memFound3000, map[string]string{"ResolveUrn": resolveNode3000})
			_ = run3000(t, url, tc.args...)
			c := calls()
			if got := find3000(c, "GetMemory"); len(got) != 1 {
				t.Fatalf("want exactly one memory read for %s, got %v", tc.name, ops3000(c))
			}
			res := find3000(c, "ResolveUrn")
			if len(res) < len(tc.urns) {
				t.Fatalf("want %d resolves, got %v", len(tc.urns), ops3000(c))
			}
			for i, want := range tc.urns {
				if got := str3000(res[i], "urn"); got != want {
					t.Fatalf("resolve %d = %q, want %q", i, got, want)
				}
			}
		})
	}
}

// The help says -m takes a memory ID as well as a URN, on the three commands that compose a bare loc.
func TestMemoryFlagHelpNamesTheID3000(t *testing.T) {
	for _, args := range [][]string{{"node", "get", "--help"}, {"edge", "add", "--help"}, {"edge", "ls", "--help"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		flat := strings.Join(strings.Fields(out.String()), " ")
		if !strings.Contains(flat, "a memory ID or URN") {
			t.Errorf("%v must say -m accepts a memory ID or URN:\n%s", args, out.String())
		}
	}
}

// Every other command that composes a bare loc through ResolveNodeRef takes the ID the same way:
// the docs say so for node update, move, clone, rm and export, so each is pinned here (cli#3000).
func TestOtherNodeCommandsTakeAMemoryID3000(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"node update", []string{"node", "update", "-m", memID3000, "sheds:x", "--name", "N"}},
		{"node move", []string{"node", "move", "-m", memID3000, "sheds:x", "--to-urn", "hrn:node:acme.com:kb:sheds:y"}},
		{"node clone", []string{"node", "clone", "-m", memID3000, "sheds:x", "--to-urn", "hrn:node:acme.com:kb:sheds:y"}},
		{"node export", []string{"node", "export", "-m", memID3000, "sheds:x"}},
		{"node delete", []string{"node", "delete", "-m", memID3000, "sheds:x", "--yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url, calls := serve3000(t, memFound3000, map[string]string{"ResolveUrn": resolveNode3000})
			err := run3000(t, url, tc.args...)
			c := calls()
			if err != nil && strings.Contains(err.Error(), "not a fully-qualified node URN") {
				t.Fatalf("%s refused a memory ID: %v", tc.name, err)
			}
			if got := find3000(c, "GetMemory"); len(got) != 1 {
				t.Fatalf("want one memory read, got %v", ops3000(c))
			}
			res := find3000(c, "ResolveUrn")
			if len(res) == 0 || str3000(res[0], "urn") != "hrn:node:acme.com:kb:sheds:x" {
				t.Fatalf("the source was not resolved by the composed URN: %v", ops3000(c))
			}
		})
	}
}

// The contract (SDL, Query.memory): a missing and an unreadable memory both answer null with no
// error, and a malformed ref or a fault is an error. If a server ever answered 200 with a
// FORBIDDEN error instead, that is not the contract: it surfaces as its own error class, never
// silently as the plain refusal, so a deviation is visible rather than hidden.
func TestNodeGetMemoryIDErrorAnswerIsNotTheRefusal3000(t *testing.T) {
	url, calls := serve3000(t, `{"errors":[{"message":"nope","extensions":{"code":"FORBIDDEN"}}],"data":null}`, nil)
	err := run3000(t, url, "node", "get", "-m", memID3000, "sheds:x")
	wantExit(t, err, exitcode.Forbidden)
	if got := find3000(calls(), "ResolveUrn"); len(got) != 0 {
		t.Fatalf("no node resolve after a failed memory read: %v", ops3000(calls()))
	}
}

// --prefix names the memory to the server as given, and the server resolves an ID itself, so
// --prefix -m <id> is unchanged by this: no memory read, the ID goes through as typed.
func TestNodeGetPrefixWithMemoryIDIsUnchanged3000(t *testing.T) {
	url, calls := serve3000(t, memFound3000, map[string]string{"NodeBatch": nodeBatchResult([]string{}, "")})
	_ = run3000(t, url, "node", "get", "--prefix", "findings:", "-m", memID3000)
	if got := find3000(calls(), "GetMemory"); len(got) != 0 {
		t.Fatalf("--prefix must not read the memory itself: %v", ops3000(calls()))
	}
}

// Only a 32 lowercase hex token is an ID: anything else is composed (or refused) as before, with no
// memory read.
func TestMemoryIDShapeBoundaries3000(t *testing.T) {
	for _, m := range []string{
		strings.ToUpper(memID3000),         // uppercase hex is not an ID
		memID3000[:31],                     // 31 characters
		memID3000 + "0",                    // 33 characters
		"01a1d9c2453a782189ceeafb18c37d6g", // not hex
	} {
		url, calls := serve3000(t, memFound3000, map[string]string{"ResolveUrn": resolveNode3000})
		err := run3000(t, url, "node", "get", "-m", m, "sheds:x")
		if got := find3000(calls(), "GetMemory"); len(got) != 0 {
			t.Fatalf("-m %q sent a memory read", m)
		}
		wantExit(t, err, exitcode.Usage)
	}
}

// A batch with an unresolvable ID is refused as a whole, plainly, before any node read.
func TestNodeGetBatchUnresolvableMemoryID3000(t *testing.T) {
	url, calls := serve3000(t, memNull3000, map[string]string{"NodeBatch": nodeBatchResult([]string{}, "")})
	err := run3000(t, url, "node", "get", "-m", memID3000, "a:one", "b:two")
	wantExit(t, err, exitcode.Usage)
	for _, r := range calls() {
		if _, ok := r.vars["refs"]; ok {
			t.Fatalf("a node read was sent for an unresolvable memory: %s", r.op)
		}
	}
}

// Two URN endpoints name their own nodes, so -m is not used for them: a memory ID on -m must not
// cost a memory read, and a fault reading it must not fail an add that does not need it (an
// add with URN endpoints and an ignored -m passed before cli#3000 and still does).
func TestEdgeAddURNEndpointsNeverReadTheMemoryID3000(t *testing.T) {
	url, calls := serve3000(t, "HTTP 502", map[string]string{"ResolveUrn": resolveNode3000})
	err := run3000(t, url, "edge", "add", "-m", memID3000,
		"--from", "hrn:node:acme.com:kb:a:one", "--to", "hrn:node:acme.com:kb:b:two", "--name", "routes-to")
	// The run's tail (the create, unstubbed here) may fail, but never as the 502 the memory
	// read would have been: that read is not sent at all.
	if err != nil && exitCodeFor(err) == exitcode.Unavailable {
		t.Fatalf("a URN-endpoint add failed as the memory read's 502: %v", err)
	}
	c := calls()
	if got := find3000(c, "GetMemory"); len(got) != 0 {
		t.Fatalf("URN endpoints must not read the -m memory: %v", ops3000(c))
	}
	if got := find3000(c, "ResolveUrn"); len(got) != 2 {
		t.Fatalf("both endpoints should resolve by their URN: %v", ops3000(c))
	}
}

// One bare endpoint is enough to need the memory (either one), and it is still read exactly once.
func TestEdgeAddOneBareEndpointReadsTheMemoryOnce3000(t *testing.T) {
	for _, tc := range []struct{ name, from, to string }{
		{"bare source", "a:one", "hrn:node:acme.com:kb:b:two"},
		{"bare target", "hrn:node:acme.com:kb:a:one", "b:two"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url, calls := serve3000(t, memFound3000, map[string]string{"ResolveUrn": resolveNode3000})
			_ = run3000(t, url, "edge", "add", "-m", memID3000, "--from", tc.from, "--to", tc.to, "--name", "routes-to")
			c := calls()
			if got := find3000(c, "GetMemory"); len(got) != 1 {
				t.Fatalf("want one memory read, got %v", ops3000(c))
			}
		})
	}
}

// node move and clone compose -m with the SOURCE loc only: the destination is the explicit
// --to-urn or --to-memory and goes to the server as typed. A memory ID given as the destination
// memory is not read (and not resolved) by the CLI.
func TestMoveAndCloneDestinationIsNeverComposedWithMemoryID3000(t *testing.T) {
	const dest = "01a1d9c2453a782189ceeafb18c37d62"
	for _, cmdName := range []string{"move", "clone"} {
		t.Run(cmdName, func(t *testing.T) {
			url, calls := serve3000(t, memFound3000, map[string]string{"ResolveUrn": resolveNode3000})
			_ = run3000(t, url, "node", cmdName, "-m", memID3000, "sheds:x", "--to-memory", dest)
			c := calls()
			mem := find3000(c, "GetMemory")
			if len(mem) != 1 || str3000(mem[0], "ref") != memID3000 {
				t.Fatalf("only the -m source memory is read, got %v", ops3000(c))
			}
			// The destination memory reaches the mutation exactly as typed, not as a URN.
			op := map[string]string{"move": "MoveNode", "clone": "CloneNode"}[cmdName]
			sent := find3000(c, op)
			if len(sent) != 1 || str3000(sent[0], "targetMemoryRef") != dest {
				t.Fatalf("%s must carry the destination memory ID as typed, got %v", op, ops3000(c))
			}
		})
	}
}
