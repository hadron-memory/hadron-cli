package cmd

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmd/node"
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
	reads := []string{"GetWorkerProfile", "GetWorkerTerminal", "GetWorkerModelGroups", "GetWorkerRendered", "GetWorkerAvailability", "GetWorkerBootPromptServe"}
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

const gqlBoom1677 = `{"errors":[{"message":"boom"}]}`

// blockingProbe swaps node.SigningCapabilityProbe for one that announces it
// started and then blocks until its context is cancelled, reporting when it has
// actually returned.
func blockingProbe1677(t *testing.T) (started, returned chan struct{}) {
	t.Helper()
	started, returned = make(chan struct{}), make(chan struct{})
	prev := node.SigningCapabilityProbe
	node.SigningCapabilityProbe = func(ctx context.Context, _ string) (bool, error) {
		close(started)
		defer close(returned)
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(rendezvousTimeout):
			t.Errorf("the capability probe was never cancelled")
			return false, nil
		}
	}
	t.Cleanup(func() { node.SigningCapabilityProbe = prev })
	return started, returned
}

// A failed resolve or content read must not leave the capability request
// running: the command cancels and joins it on every exit path.
func TestNodeGetFailureCancelsCapabilityProbe1677(t *testing.T) {
	for _, failing := range []string{"ResolveUrn", "GetNode"} {
		t.Run(failing, func(t *testing.T) {
			started, returned := blockingProbe1677(t)
			stubs := nodeGetStubs(nodeGetJSON(testNodeURL))
			gql, _ := captureGraphQLFunc(t, func(op string) string {
				if op == failing {
					// The probe is in flight before the failure is answered.
					select {
					case <-started:
					case <-time.After(rendezvousTimeout):
						t.Errorf("the capability probe never started before %s was answered", op)
					}
					return gqlBoom1677
				}
				if strings.HasPrefix(op, "NodeLiveRevisions") {
					return liveRevisions(map[string]int{"n1": 12})
				}
				return stubs[op]
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"node", "get", testNodeURN, "--json", "--server", gql.URL})
			if err := root.Execute(); err == nil {
				t.Fatalf("expected %s failure", failing)
			}
			select {
			case <-returned:
			default:
				t.Fatalf("command returned while the capability probe was still running")
			}
		})
	}
}

// The capability read starts with the resolve, not after the content read.
func TestNodeGetCapabilityProbeOverlapsResolve1677(t *testing.T) {
	rv := newRendezvous(t, "ResolveUrn", "Capability")
	prev := node.SigningCapabilityProbe
	node.SigningCapabilityProbe = func(context.Context, string) (bool, error) {
		rv.arrive("Capability")
		return false, nil
	}
	t.Cleanup(func() { node.SigningCapabilityProbe = prev })
	stubs := nodeGetStubs(nodeGetJSON(testNodeURL))
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		if op == "ResolveUrn" {
			rv.arrive(op)
		}
		if strings.HasPrefix(op, "NodeLiveRevisions") {
			return liveRevisions(map[string]int{"n1": 12})
		}
		return stubs[op]
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
}

// Both advisories fail at once, the later-in-order one finishing first: the
// notes still print comments before lint, as they did when the reads ran in
// sequence.
func TestNodeGetAdvisoryNotesKeepOrder1677(t *testing.T) {
	withSigningProbe(t, false, nil)
	stubs := nodeGetStubs(nodeGetJSON(testNodeURL))
	lintServed := make(chan struct{})
	var lintOnce sync.Once
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "GetNodeLintWarnings":
			lintOnce.Do(func() { close(lintServed) })
			return gqlBoom1677
		case "NodeCommentSummaries":
			// Fail only after lint has already failed, so completion order is
			// the reverse of the documented order.
			select {
			case <-lintServed:
			case <-time.After(rendezvousTimeout):
				t.Errorf("lint read never ran beside the comment read")
			}
			time.Sleep(50 * time.Millisecond)
			return gqlBoom1677
		}
		if strings.HasPrefix(op, "NodeLiveRevisions") {
			return liveRevisions(map[string]int{"n1": 12})
		}
		return stubs[op]
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--lint-warnings", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	stderr := f.IOStreams.ErrOut.(*strings.Builder).String()
	commentAt := strings.Index(stderr, "feedback counts unavailable")
	lintAt := strings.Index(stderr, "lint warnings: unavailable")
	if commentAt < 0 || lintAt < 0 || commentAt > lintAt {
		t.Errorf("notes must print comments then lint, got:\n%s", stderr)
	}
}

// The first revision probe must finish before the content read starts: the
// bracket's "before" is only a before if nothing reads content meanwhile.
func TestNodeGetBracketBeforeProbeCompletesFirst1677(t *testing.T) {
	stubs := nodeGetStubs(nodeGetJSON(testNodeURL))
	revs := liveRevisions(map[string]int{"n1": 12})
	released := make(chan struct{})
	time.AfterFunc(300*time.Millisecond, func() { close(released) })
	var first sync.Once
	var early atomic.Bool
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		if strings.HasPrefix(op, "NodeLiveRevisions") {
			first.Do(func() { <-released })
			return revs
		}
		if op == "GetNode" {
			select {
			case <-released:
			default:
				early.Store(true)
			}
		}
		return stubs[op]
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if early.Load() {
		t.Errorf("the content read started before the first revision probe was answered")
	}
}
