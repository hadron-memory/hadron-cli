package cmd

import (
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// cli#1677: worker get, node get and chat read overlap requests that used to
// run one after another. These tests PIN THE OVERLAP, not just the request set:
// each fake holds a request until its peers have arrived, so a command that
// went back to asking one at a time never lets the barrier open and the test
// goes red (after rendezvousTimeout) instead of passing on a loose count.

const rendezvousTimeout = 3 * time.Second

// rendezvous releases every waiter once all the named operations have arrived.
type rendezvous struct {
	t    *testing.T
	need map[string]bool
	mu   sync.Mutex
	seen map[string]bool
	open chan struct{}
	once sync.Once
}

func newRendezvous(t *testing.T, ops ...string) *rendezvous {
	r := &rendezvous{t: t, need: map[string]bool{}, seen: map[string]bool{}, open: make(chan struct{})}
	for _, op := range ops {
		r.need[op] = true
	}
	return r
}

// arrive records op (when it is one of the awaited) and, when op is a
// participant, blocks until all have arrived.
func (r *rendezvous) arrive(op string) {
	if !r.need[op] {
		return
	}
	r.mu.Lock()
	r.seen[op] = true
	all := len(r.seen) == len(r.need)
	r.mu.Unlock()
	if all {
		r.once.Do(func() { close(r.open) })
	}
	select {
	case <-r.open:
	case <-time.After(rendezvousTimeout):
		r.t.Errorf("%s was never accompanied by its peers %v: the reads are running one after another again", op, sortedKeys1677(r.need))
	}
}

func sortedKeys1677(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestWorkerGetReadsGoOutTogether1677(t *testing.T) {
	teamGitDir(t)
	reads := []string{"GetWorkerProfile", "GetWorkerTerminal", "GetWorkerModelGroups", "GetWorkerRendered", "GetWorkerBootPromptServe"}
	rv := newRendezvous(t, reads...)
	log := &profileOpLog{}
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		log.add(op)
		rv.arrive(op)
		switch op {
		case "GetWorker":
			return `{"data":{"worker":` + profileWorkerJSON + `}}`
		case "GetWorkerProfile":
			return profileNames
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "hrn:worker:acme.com:eng-team:iris", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, op := range reads {
		if n := log.count(op); n != 1 {
			t.Errorf("%s sent %d times, want exactly once", op, n)
		}
	}
}

func TestNodeGetBracketProbeOverlapsResolve1677(t *testing.T) {
	stubs := nodeGetStubs(nodeGetJSON(testNodeURL))
	revs := liveRevisions(map[string]int{"n1": 12})
	// The first revision read and the URN resolve must be in flight together.
	// Both revision reads share one name per projection, so the barrier waits
	// for ResolveUrn plus any one revision projection.
	rv := newRendezvous(t, "ResolveUrn", "NodeLiveRevisionsMinted")
	log := &profileOpLog{}
	var firstRev sync.Once
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		log.add(op)
		if op == "ResolveUrn" {
			rv.arrive(op)
		}
		if strings.HasPrefix(op, "NodeLiveRevisions") {
			firstRev.Do(func() { rv.arrive("NodeLiveRevisionsMinted") })
			return revs
		}
		return stubs[op]
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	// The bracket itself is the consistency guarantee and stays: one probe
	// before the content read, one after, and the content read between.
	revReads := 0
	getAt, lastRevAt := -1, -1
	log.mu.Lock()
	for i, op := range log.ops {
		switch {
		case strings.HasPrefix(op, "NodeLiveRevisions"):
			revReads++
			lastRevAt = i
		case op == "GetNode":
			getAt = i
		}
	}
	log.mu.Unlock()
	if revReads != 2 {
		t.Errorf("revision reads = %d, want 2 (before and after the content read)", revReads)
	}
	if getAt < 0 || lastRevAt < getAt {
		t.Errorf("the after-probe must follow the content read (GetNode at %d, last revision read at %d)", getAt, lastRevAt)
	}
	if n := log.count("ResolveUrn"); n != 1 {
		t.Errorf("ResolveUrn sent %d times, want 1", n)
	}
}

func TestChatReadMetadataOverlapsPage1677(t *testing.T) {
	// The metadata read runs beside the head and page reads: the page is held
	// until the metadata request has arrived. The HEAD probe still precedes the
	// page (its staleness claim is about messages seen before the page was
	// asked for), which assertChatOpsEconomy1677 checks on the order.
	rv := newRendezvous(t, "TeamChatMessages", "TeamChatReadMetadata")
	writeTeamBinding(t)
	srv, calls := attnServerHook(t, chatReadResponses(), func(op string) {
		if op == "TeamChatMessages" || op == "TeamChatReadMetadata" {
			rv.arrive(op)
		}
	})
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "chat", "read", "--app", "acme.com:eng-team", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	ops := opsOf(*calls)
	headAt, pageAt := -1, -1
	for i, op := range ops {
		switch op {
		case "TeamChatReadHead":
			headAt = i
		case "TeamChatMessages":
			pageAt = i
		}
	}
	if headAt < 0 || pageAt < 0 || headAt > pageAt {
		t.Errorf("the head probe must complete before the page is requested: %v", ops)
	}
}
